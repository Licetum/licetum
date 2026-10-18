package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/klauspost/compress/zstd"
)

// job is one statement: a single row, or an unlogged batch of rows of one partition. With qs set,
// row i is bound to qs[i] instead of q.
type job struct {
	q    string
	qs   []string
	rows [][]any
}

func (j job) stmt(i int) string {
	if j.qs != nil {
		return j.qs[i]
	}
	return j.q
}

type runResult struct {
	Rows, Stmts, Retries int
	Took                 time.Duration
	Lat                  summary
}

// runJobs executes what produce emits with conc statements in flight.
func runJobs(s *gocql.Session, conc int, produce func(emit func(job) bool)) (runResult, error) {
	ch := make(chan job, conc*4)
	var lat latencies
	var rows, stmts, retries atomic.Int64
	var firstErr error
	var errOnce sync.Once
	var failed atomic.Bool
	var wg sync.WaitGroup
	start := time.Now()
	for range conc {
		wg.Go(func() {
			local := make([]time.Duration, 0, 4096)
			for j := range ch {
				if failed.Load() {
					continue
				}
				var err error
				for attempt := range 8 {
					t0 := time.Now()
					if len(j.rows) == 1 {
						err = s.Query(j.stmt(0), j.rows[0]...).Exec()
					} else {
						b := s.Batch(gocql.UnloggedBatch)
						for i, r := range j.rows {
							b.Query(j.stmt(i), r...)
						}
						err = b.Exec()
					}
					if err == nil {
						local = append(local, time.Since(t0))
						break
					}
					retries.Add(1)
					time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
				}
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					failed.Store(true)
					continue
				}
				rows.Add(int64(len(j.rows)))
				stmts.Add(1)
			}
			lat.add(local)
		})
	}
	produce(func(j job) bool {
		if failed.Load() {
			return false
		}
		ch <- j
		return true
	})
	close(ch)
	wg.Wait()
	return runResult{Rows: int(rows.Load()), Stmts: int(stmts.Load()), Retries: int(retries.Load()), Took: time.Since(start), Lat: lat.summary()}, firstErr
}

// batcher groups rows of one partition into batches of at most maxBatch rows.
func emitBatches(emit func(job) bool, q string, rows [][]any, maxBatch int) bool {
	for len(rows) > 0 {
		n := min(len(rows), maxBatch)
		if !emit(job{q: q, rows: rows[:n:n]}) {
			return false
		}
		rows = rows[n:]
	}
	return true
}

const (
	insResource   = `INSERT INTO resource_v (type_id, ext, ver, active, name, meta, lineage, unresolved_up, committed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`
	insDescendant = `INSERT INTO descendant_v (anchor_type, anchor_ext, desc_type, bucket, desc_ext, ver, mask, name, gone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`
	insTypeMember = `INSERT INTO type_member_v (type_id, bucket, ext, ver, name, gone) VALUES (?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`
	insAttr       = `INSERT INTO attr_v (type_id, attr, value, bucket, ext, ver, gone) VALUES (?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`
)

func tid(t int8) gocql.UUID { return gocql.UUID(types[t].ID) }

// producers of the derived rows of the first synchronization at position pos. maxRows > 0 stops
// a table after that many rows.
func (d *Dataset) produceResources(pos int64, maxRows int) func(func(job) bool) {
	return func(emit func(job) bool) {
		now := time.Now()
		var lin []LineageEntry
		for i := range d.Res {
			if maxRows > 0 && i >= maxRows {
				return
			}
			r := &d.Res[i]
			lin = d.Lineage(int32(i), lin)
			row := []any{tid(r.Type), r.Ext, pos, true, r.Name, r.MetaJSON(), d.lineageBlob(lin), gocql.UnsetValue, now, ts(pos, 1)}
			if !emit(job{q: insResource, rows: [][]any{row}}) {
				return
			}
		}
	}
}

func (d *Dataset) produceDescendants(pos int64, maxRows, maxBatch int) func(func(job) bool) {
	return func(emit func(job) bool) {
		type item struct {
			x    int32
			mask []string
			all  bool
		}
		var stack []item
		var groups [4][typeBuckets][][]any
		n := 0
		for a := range d.Res {
			ar := &d.Res[a]
			stack = append(stack[:0], item{x: int32(a), all: true})
			for len(stack) > 0 {
				it := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				r := &d.Res[it.x]
				var m []string
				if !it.all {
					m = it.mask
				}
				b := bucketOf(r.Ext, typeBuckets)
				groups[r.Type][b] = append(groups[r.Type][b], []any{tid(ar.Type), ar.Ext, tid(r.Type), b, r.Ext, pos, maskValue(m), r.Name, false, ts(pos, 1)})
				for _, k := range d.Kids[d.KidStart[it.x]:d.KidStart[it.x+1]] {
					next := item{x: k, mask: it.mask, all: it.all}
					if inh := types[d.Res[k].Type].Inherit; inh != nil {
						if it.all {
							next.mask, next.all = inh, false
						} else {
							next.mask = intersect(it.mask, inh)
						}
					}
					stack = append(stack, next)
				}
			}
			for t := range groups {
				for b := range groups[t] {
					if len(groups[t][b]) == 0 {
						continue
					}
					if !emitBatches(emit, insDescendant, groups[t][b], maxBatch) {
						return
					}
					n += len(groups[t][b])
					groups[t][b] = nil
				}
			}
			if maxRows > 0 && n >= maxRows {
				return
			}
		}
	}
}

func (d *Dataset) produceTypeMembers(pos int64, maxRows, maxBatch int) func(func(job) bool) {
	return func(emit func(job) bool) {
		n := 0
		for ti := range types {
			var groups [typeBuckets][][]any
			for i := d.TypeOff[ti]; i < d.TypeOff[ti+1]; i++ {
				r := &d.Res[i]
				b := bucketOf(r.Ext, typeBuckets)
				groups[b] = append(groups[b], []any{tid(r.Type), b, r.Ext, pos, r.Name, false, ts(pos, 1)})
				if len(groups[b]) == maxBatch {
					if !emitBatches(emit, insTypeMember, groups[b], maxBatch) {
						return
					}
					n += len(groups[b])
					groups[b] = nil
					if maxRows > 0 && n >= maxRows {
						return
					}
				}
			}
			for b := range groups {
				if !emitBatches(emit, insTypeMember, groups[b], maxBatch) {
					return
				}
				n += len(groups[b])
			}
		}
	}
}

func (d *Dataset) produceAttrs(pos int64, maxRows, maxBatch int) func(func(job) bool) {
	return func(emit func(job) bool) {
		n := 0
		type key struct {
			attr, value string
			b           int16
		}
		for ti := range types {
			groups := map[key][][]any{}
			var order []key
			for i := d.TypeOff[ti]; i < d.TypeOff[ti+1]; i++ {
				r := &d.Res[i]
				b := bucketOf(r.Ext, attrBuckets)
				for _, kv := range r.Attrs() {
					k := key{kv[0], kv[1], b}
					if _, ok := groups[k]; !ok {
						order = append(order, k)
					}
					groups[k] = append(groups[k], []any{tid(r.Type), kv[0], kv[1], b, r.Ext, pos, false, ts(pos, 1)})
				}
			}
			for _, k := range order {
				if !emitBatches(emit, insAttr, groups[k], maxBatch) {
					return
				}
				n += len(groups[k])
				delete(groups, k)
				if maxRows > 0 && n >= maxRows {
					return
				}
			}
		}
	}
}

// writeLog writes the log of the first synchronization: one log_chunk row per chunk and one
// log_entry naming the streams.
func (d *Dataset) writeLog(s *gocql.Session, pos int64, conc int) (runResult, int, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return runResult{}, 0, err
	}
	defer func() { _ = enc.Close() }()
	var streams []*Stream
	for ti := range types {
		streams = append(streams, d.encodeStream(ti, "binary", enc))
	}
	bytes := 0
	res, err := runJobs(s, conc, func(emit func(job) bool) {
		for _, st := range streams {
			for n, c := range st.Chunks {
				bytes += len(c)
				if !emit(job{q: `INSERT INTO licetum_log.log_chunk (epoch, position, stream, n, body) VALUES (?, ?, ?, ?, ?) USING TIMESTAMP ?`,
					rows: [][]any{{1, pos, st.Name, n, c, ts(pos, 1)}}}) {
					return
				}
			}
		}
	})
	if err != nil {
		return res, bytes, err
	}
	m := map[string][]any{}
	for _, st := range streams {
		m[st.Name] = []any{int64(0), len(st.Chunks), st.Digest[:]}
	}
	svc := []gocql.UUID{gocql.UUID(types[0].ID)}
	err = s.Query(`INSERT INTO licetum_log.log_entry (epoch, bucket, position, kind, committed_at, services, streams, base_position, format) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`,
		1, pos>>16, pos, "sync", time.Now(), svc, m, int64(0), int16(1), ts(pos, 1)).Exec()
	return res, bytes, err
}

// cmdLoad applies the first synchronization to a generation keyspace.
func cmdLoad(hosts string, gen int, seed uint64, conc, maxBatch, maxRows int, tables string) error {
	d := Generate(seed)
	s, dc, err := connect(hosts, "")
	if err != nil {
		return err
	}
	defer s.Close()
	if err := createSchema(s, dc, gen); err != nil {
		return err
	}
	ks := regionKeyspace(dc, gen)
	s.Close()
	s, _, err = connect(hosts, ks)
	if err != nil {
		return err
	}
	const pos = int64(1_000)
	fmt.Printf("\n### first synchronization into %s: %d in flight, batches <= %d rows, max rows per table %d\n\n", ks, conc, maxBatch, maxRows)
	fmt.Println("| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |")
	fmt.Println("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	var total runResult
	for _, t := range strings.Split(tables, ",") {
		var p func(func(job) bool)
		switch t {
		case "resource_v":
			p = d.produceResources(pos, maxRows)
		case "descendant_v":
			p = d.produceDescendants(pos, maxRows, maxBatch)
		case "type_member_v":
			p = d.produceTypeMembers(pos, maxRows, maxBatch)
		case "attr_v":
			p = d.produceAttrs(pos, maxRows, maxBatch)
		case "log":
			r, bytes, err := d.writeLog(s, pos, conc)
			if err != nil {
				return err
			}
			fmt.Printf("| log_chunk | %d (%.1f MiB) | %d | %d | %.1f | %.1f MiB/s | %s | %s | %s | %s |\n", r.Rows, float64(bytes)/(1<<20), r.Stmts, r.Retries,
				r.Took.Seconds(), float64(bytes)/(1<<20)/r.Took.Seconds(), ms(r.Lat.P50), ms(r.Lat.P95), ms(r.Lat.P99), ms(r.Lat.Max))
			continue
		default:
			return errors.New("unknown table " + t)
		}
		r, err := runJobs(s, conc, p)
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		fmt.Printf("| %s | %d | %d | %d | %.1f | %.0f | %s | %s | %s | %s |\n", t, r.Rows, r.Stmts, r.Retries, r.Took.Seconds(),
			float64(r.Rows)/r.Took.Seconds(), ms(r.Lat.P50), ms(r.Lat.P95), ms(r.Lat.P99), ms(r.Lat.Max))
		total.Rows += r.Rows
		total.Stmts += r.Stmts
		total.Took += r.Took
	}
	if total.Rows > 0 {
		fmt.Printf("| region tables | %d | %d | | %.1f | %.0f | | | | |\n", total.Rows, total.Stmts, total.Took.Seconds(), float64(total.Rows)/total.Took.Seconds())
	}
	return nil
}

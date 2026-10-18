package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// The MVCC experiment: hot keys written version after version into a table shaped like
// resource_v, with the garbage collection of the applier: when version r of a key is written and
// the GC fires, the versions older than the previous version v1 are deleted with one clustering
// range tombstone at ts(r, 0). Every previous version counts as older than the retention, so the
// GC fires on every k-th write. Version j of key k is at position mvccBase + j*keys + k.

const mvccBase = int64(1_000_000)

// mvccType is the type of the hot keys (types get their ids in init, after package variables).
func mvccType() gocql.UUID { return gocql.UUID(types[3].ID) }

func mvccKey(k int) string       { return fmt.Sprintf("hot-%04d", k) }
func mvccTable(every int) string { return fmt.Sprintf("licetum_mvcc.rv_k%d", every) }

type mvccModel struct{ keys, versions, every int }

func (m mvccModel) pos(j, k int) int64 { return mvccBase + int64(j)*int64(m.keys) + int64(k) }

// lastGC is the last write that ran the GC; the versions from lastGC-1 on are retained.
func (m mvccModel) lastGC() int { return (m.versions - 1) / m.every * m.every }

// expect is the version a read at P must return for key k under the contract: the newest retained
// version <= P, or none (-1) when P is below v1 (garbage collected, or before the first version).
func (m mvccModel) expect(k int, p int64) int64 {
	j := p - mvccBase - int64(k)
	if j < 0 {
		return -1
	}
	j /= int64(m.keys)
	j = min(j, int64(m.versions-1))
	if int(j) < m.lastGC()-1 {
		return -1
	}
	return m.pos(int(j), k)
}

type mvccOptions struct {
	keys, versions   int
	every, stage     string
	label            string
	conc, flushEvery int
	container        string
	bypass           bool
}

func cmdMVCC(hosts string, o mvccOptions) error {
	keys, versions := o.keys, o.versions
	var cadences []int
	for _, e := range strings.Split(o.every, ",") {
		k, err := strconv.Atoi(e)
		if err != nil || k < 1 {
			return fmt.Errorf("bad cadence %q", e)
		}
		cadences = append(cadences, k)
	}
	s, dc, err := connect(hosts, "")
	if err != nil {
		return err
	}
	defer s.Close()
	switch o.stage {
	case "write":
		return mvccWrite(s, dc, o, cadences)
	case "measure":
		return mvccMeasure(s, keys, versions, cadences, o.label, o.bypass)
	case "reinsert":
		return mvccReinsert(s, keys, versions, cadences)
	case "describe":
		return mvccDescribe(s, cadences)
	}
	return errors.New("unknown stage " + o.stage)
}

func mvccWrite(s *gocql.Session, dc string, o mvccOptions, cadences []int) error {
	keys, versions, conc := o.keys, o.versions, o.conc
	if err := s.Query(fmt.Sprintf(`CREATE KEYSPACE IF NOT EXISTS licetum_mvcc WITH replication = {'class': 'NetworkTopologyStrategy', '%s': 1}`, dc)).Exec(); err != nil {
		return err
	}
	fmt.Printf("\n### MVCC writes: %d keys x %d versions, %d keys in flight, flush every %d versions\n\n", keys, versions, conc, o.flushEvery)
	fmt.Println("| table | GC every | inserts | range deletes | time s | batches/s | p50 ms | p99 ms |")
	fmt.Println("|---|---:|---:|---:|---:|---:|---:|---:|")
	for _, every := range cadences {
		t := mvccTable(every)
		if err := s.Query(`DROP TABLE IF EXISTS ` + t).Exec(); err != nil {
			return err
		}
		if err := s.Query(`CREATE TABLE ` + t + ` (
			type_id uuid, ext text, ver bigint,
			active boolean, name text, meta text, lineage blob, committed_at timestamp,
			PRIMARY KEY ((type_id, ext), ver)
		) WITH CLUSTERING ORDER BY (ver DESC) AND compaction = {'class': 'LeveledCompactionStrategy'}`).Exec(); err != nil {
			return err
		}
		if o.flushEvery > 0 {
			// Keep the flushed sstables apart until the major compaction of the measurement.
			// #nosec G204 -- the container is a flag of a local measurement tool
			if out, err := exec.Command("docker", "exec", o.container, "nodetool", "disableautocompaction", "licetum_mvcc",
				strings.TrimPrefix(t, "licetum_mvcc.")).CombinedOutput(); err != nil {
				return fmt.Errorf("disableautocompaction: %w %s", err, out)
			}
		}
		m := mvccModel{keys, versions, every}
		ins := `INSERT INTO ` + t + ` (type_id, ext, ver, active, name, meta, lineage, committed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ?`
		del := `DELETE FROM ` + t + ` USING TIMESTAMP ? WHERE type_id = ? AND ext = ? AND ver < ?`
		lineage := make([]byte, 120)
		meta := `{"region":"region-07","status":"active","tags":["promo","tea"]}`
		deletes := 0
		res, err := runJobs(s, conc, func(emit func(job) bool) {
			// Version-major: the versions of a key are written in order, the keys in parallel.
			for j := range versions {
				if o.flushEvery > 0 && j > 0 && j%o.flushEvery == 0 {
					// As on a node that flushes on its own: the older sstables keep versions
					// that the range tombstones of later writes shadow.
					// #nosec G204 -- the container is a flag of a local measurement tool
					if out, err := exec.Command("docker", "exec", o.container, "nodetool", "flush", "licetum_mvcc").CombinedOutput(); err != nil {
						fmt.Printf("flush: %v %s\n", err, out)
					}
				}
				for k := range keys {
					p := m.pos(j, k)
					row := []any{mvccType(), mvccKey(k), p, true, fmt.Sprintf("name %d", j), meta, lineage, time.Now(), ts(p, 1)}
					jb := job{q: ins, rows: [][]any{row}}
					if j >= 1 && j%every == 0 {
						// The GC and the new version share the partition: one unlogged batch.
						jb = job{qs: []string{del, ins}, rows: [][]any{{ts(p, 0), mvccType(), mvccKey(k), m.pos(j-1, k)}, row}}
						deletes++
					}
					if !emit(jb) {
						return
					}
				}
			}
		})
		if err != nil {
			return err
		}
		fmt.Printf("| %s | %d | %d | %d | %.1f | %.0f | %s | %s |\n", t, every, keys*versions, deletes, res.Took.Seconds(),
			float64(res.Stmts)/res.Took.Seconds(), ms(res.Lat.P50), ms(res.Lat.P99))
	}
	return nil
}

// mvccPoints are the positions read for key k, by class.
func (m mvccModel) points(k int, rng *rand.Rand) []struct {
	class string
	p     int64
} {
	jg := m.lastGC()
	between := m.pos(jg-1, k) + rng.Int64N(m.pos(m.versions-1, k)-m.pos(jg-1, k)+1)
	return []struct {
		class string
		p     int64
	}{
		{"newest P", m.pos(m.versions-1, k)},
		{"P = v1 (oldest retained)", m.pos(jg-1, k)},
		{"v1 <= P <= newest", between},
		{"old P (collected, middle)", m.pos(m.versions/2, k)},
		{"old P (collected, first)", m.pos(0, k)},
	}
}

func mvccMeasure(s *gocql.Session, keys, versions int, cadences []int, label string, bypass bool) error {
	suffix := ""
	if bypass {
		suffix = " BYPASS CACHE"
		label += ", BYPASS CACHE"
	}
	fmt.Printf("\n### MVCC reads `ver <= P LIMIT 1` (%s), sequential, %d keys\n\n", label, keys)
	fmt.Println("| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|---:|---:|")
	type traced struct {
		table, class string
		id           []byte
	}
	var traces []traced
	for _, every := range cadences {
		t := mvccTable(every)
		m := mvccModel{keys, versions, every}
		q := `SELECT ver FROM ` + t + ` WHERE type_id = ? AND ext = ? AND ver <= ? LIMIT 1` + suffix
		rng := rand.New(rand.NewPCG(uint64(every), 3))
		classes := m.points(0, rng)
		lat := make([][]time.Duration, len(classes))
		wrong := make([]int, len(classes))
		visible := make([]int, len(classes))
		for k := range keys {
			for ci, pt := range m.points(k, rng) {
				var ver int64
				t0 := time.Now()
				err := s.Query(q, mvccType(), mvccKey(k), pt.p).Scan(&ver)
				lat[ci] = append(lat[ci], time.Since(t0))
				got := ver
				if errors.Is(err, gocql.ErrNotFound) {
					got = -1
				} else if err != nil {
					return err
				}
				if got >= 0 {
					visible[ci]++
				}
				if got != m.expect(k, pt.p) {
					wrong[ci]++
				}
			}
		}
		for ci, c := range classes {
			fmt.Printf("| %s | %s | %s | %d | %d |\n", t, c.class, summarize(lat[ci]).row(), wrong[ci], visible[ci])
		}
		// Traced reads of a few keys.
		for _, k := range []int{1, keys / 2, keys - 2} {
			for _, pt := range m.points(k, rng) {
				var tr traceIDs
				var ver int64
				_ = s.Query(q, mvccType(), mvccKey(k), pt.p).Trace(&tr).Scan(&ver)
				for _, id := range tr.ids {
					traces = append(traces, traced{t, pt.class, id})
				}
			}
		}
	}
	time.Sleep(3 * time.Second) // trace events are written asynchronously
	// One line per table and class: the server-side durations of the traced reads and the distinct
	// trace lines about rows, tombstones and sstables (partition keys and slices stripped).
	fmt.Printf("\n#### traces (%s)\n\n", label)
	type group struct {
		durs  []int
		lines []string
	}
	groups := map[string]*group{}
	var order []string
	for _, tr := range traces {
		lines, dur, err := traceEvents(s, tr.id)
		if err != nil {
			return err
		}
		k := tr.table + ", " + tr.class
		g := groups[k]
		if g == nil {
			g = &group{}
			groups[k] = g
			order = append(order, k)
		}
		g.durs = append(g.durs, dur)
		for _, l := range lines {
			if !slices.Contains(g.lines, l) {
				g.lines = append(g.lines, l)
			}
		}
	}
	for _, k := range order {
		g := groups[k]
		fmt.Printf("- %s: server %v us; %s\n", k, g.durs, strings.Join(g.lines, "; "))
	}
	return nil
}

// traceIDs collects the trace ids of traced statements.
type traceIDs struct {
	mu  sync.Mutex
	ids [][]byte
}

func (t *traceIDs) Trace(id []byte) {
	t.mu.Lock()
	t.ids = append(t.ids, id)
	t.mu.Unlock()
}

var (
	interesting = regexp.MustCompile(`(?i)tombstone|live rows|page stats|sstables|merged`)
	keyDetail   = regexp.MustCompile(`\{\{.*\}\}|\[\[.*\)\]|\[\[.*\]\]|pk\{[0-9a-f]+\}|ckp\{[0-9a-f]*\}`)
)

// traceEvents returns the trace lines about what the read touched, and its duration.
func traceEvents(s *gocql.Session, id []byte) ([]string, int, error) {
	var dur int
	q := s.Query(`SELECT duration FROM system_traces.sessions WHERE session_id = ?`, id).Consistency(gocql.One)
	if err := q.Scan(&dur); err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return nil, 0, err
	}
	q = s.Query(`SELECT activity FROM system_traces.events WHERE session_id = ?`, id).Consistency(gocql.One)
	it := q.Iter()
	var a string
	var out []string
	for it.Scan(&a) {
		if interesting.MatchString(a) && !strings.HasPrefix(a, "Querying cache") {
			out = append(out, keyDetail.ReplaceAllString(strings.TrimSpace(a), "…"))
		}
	}
	return out, dur, it.Close()
}

// mvccReinsert plays a lagging applier: it writes versions 0 and versions/2 again with their
// original timestamps ts(old, 1), below the range tombstones of the GC.
func mvccReinsert(s *gocql.Session, keys, versions int, cadences []int) error {
	for _, every := range cadences {
		t := mvccTable(every)
		m := mvccModel{keys, versions, every}
		for k := range keys {
			for _, j := range []int{0, versions / 2} {
				p := m.pos(j, k)
				if err := s.Query(`INSERT INTO `+t+` (type_id, ext, ver, active, name) VALUES (?, ?, ?, ?, ?) USING TIMESTAMP ?`,
					mvccType(), mvccKey(k), p, true, "late", ts(p, 1)).Exec(); err != nil {
					return err
				}
			}
		}
		fmt.Printf("re-inserted versions 0 and %d of %d keys into %s with their original timestamps\n", versions/2, keys, t)
	}
	return nil
}

// mvccDescribe prints the table options that decide when tombstones go.
func mvccDescribe(s *gocql.Session, cadences []int) error {
	for _, every := range cadences {
		name := strings.TrimPrefix(mvccTable(every), "licetum_mvcc.")
		row := map[string]any{}
		if err := s.Query(`SELECT * FROM system_schema.tables WHERE keyspace_name = 'licetum_mvcc' AND table_name = ?`, name).MapScan(row); err != nil {
			return err
		}
		ext := ""
		if e, ok := row["extensions"].(map[string][]byte); ok {
			for k, v := range e {
				ext += fmt.Sprintf(" %s=%q", k, v)
			}
		}
		fmt.Printf("%s: gc_grace_seconds=%v compaction=%v extensions:%s\n", name, row["gc_grace_seconds"], row["compaction"], ext)
	}
	return nil
}

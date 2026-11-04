package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const readAt = int64(2_000) // the position of the reads, after the first synchronization (1,000)

// readOp is one read; it returns the rows it got.
type readOp func(s *gocql.Session) (int, error)

// runReads runs the operations with conc in flight and returns their latencies and rows.
func runReads(s *gocql.Session, conc int, ops []readOp) (summary, int, error) {
	fs := make([]func() (int, error), len(ops))
	for i, op := range ops {
		fs[i] = func() (int, error) { return op(s) }
	}
	return runFuncs(conc, fs)
}

// runFuncs runs the operations with conc in flight and returns their latencies and rows.
func runFuncs(conc int, ops []func() (int, error)) (summary, int, error) {
	var lat latencies
	var rows atomic.Int64
	var next atomic.Int64
	var firstErr error
	var once sync.Once
	var wg sync.WaitGroup
	start := time.Now()
	for range conc {
		wg.Go(func() {
			var local []time.Duration
			for {
				i := int(next.Add(1)) - 1
				if i >= len(ops) {
					break
				}
				t0 := time.Now()
				n, err := ops[i]()
				if err != nil {
					once.Do(func() { firstErr = err })
					continue
				}
				local = append(local, time.Since(t0))
				rows.Add(int64(n))
			}
			lat.add(local)
		})
	}
	wg.Wait()
	sum := lat.summary()
	sum.Took = time.Since(start)
	return sum, int(rows.Load()), firstErr
}

func pointRead(t int8, ext string) readOp {
	return func(s *gocql.Session) (int, error) {
		var ver int64
		var active bool
		var name, meta string
		var lineage, unresolved []byte
		err := s.Query(`SELECT ver, active, name, meta, lineage, unresolved_up FROM resource_v WHERE type_id = ? AND ext = ? AND ver <= ? LIMIT 1`,
			tid(t), ext, readAt).Scan(&ver, &active, &name, &meta, &lineage, &unresolved)
		if errors.Is(err, gocql.ErrNotFound) {
			return 0, nil
		}
		return 1, err
	}
}

func pageRead(q string, args ...any) readOp {
	return func(s *gocql.Session) (int, error) {
		it := s.Query(q, args...).PageSize(1_000).Iter()
		n := 0
		for it.Scanner().Next() {
			n++
		}
		return n, it.Close()
	}
}

// cmdRead measures point reads and pages; the first pass after a restart is the cold one.
func cmdRead(hosts string, gen int, seed, keySeed uint64, conc, n, pages, passes int) error {
	d := Generate(seed)
	s, dc, err := connect(hosts, "")
	if err != nil {
		return err
	}
	ks := regionKeyspace(dc, gen)
	s.Close()
	s, _, err = connect(hosts, ks)
	if err != nil {
		return err
	}
	defer s.Close()
	rng := rand.New(rand.NewPCG(keySeed, 7))

	points := make([]readOp, n)
	for i := range points {
		r := &d.Res[rng.IntN(len(d.Res))]
		points[i] = pointRead(r.Type, r.Ext)
	}
	// descendant_v: the partitions (division, item, bucket) hold about 2,800 rows each.
	var descPages []readOp
	for range pages {
		div := &d.Res[d.TypeOff[0]+rng.IntN(types[0].Count)]
		descPages = append(descPages, pageRead(`SELECT desc_ext, ver, mask, name, gone FROM descendant_v WHERE anchor_type = ? AND anchor_ext = ? AND desc_type = ? AND bucket = ? LIMIT 1000`,
			tid(0), div.Ext, tid(3), int16(rng.IntN(typeBuckets))))
	}
	// type_member_v: a page of items after a random item of the same bucket.
	var tmPages []readOp
	for range pages {
		it := &d.Res[d.TypeOff[3]+rng.IntN(types[3].Count/2)]
		tmPages = append(tmPages, pageRead(`SELECT ext, ver, name, gone FROM type_member_v WHERE type_id = ? AND bucket = ? AND ext > ? LIMIT 1000`,
			tid(3), bucketOf(it.Ext, typeBuckets), it.Ext))
	}

	fmt.Printf("\n### reads at LOCAL_QUORUM, %d in flight, key seed %d\n\n", conc, keySeed)
	fmt.Println("| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|---:|---:|")
	for p := 1; p <= passes; p++ {
		for _, set := range []struct {
			name string
			ops  []readOp
		}{
			{"resource_v ver <= P LIMIT 1", points},
			{"descendant_v page of 1,000", descPages},
			{"type_member_v page of 1,000", tmPages},
		} {
			if len(set.ops) == 0 {
				continue
			}
			sum, rows, err := runReads(s, conc, set.ops)
			if err != nil {
				return fmt.Errorf("%s: %w", set.name, err)
			}
			fmt.Printf("| %d | %s | %s | %.2f | %s |\n", p, set.name, sum.row(), float64(rows)/float64(sum.N), sum.rate())
		}
	}
	return nil
}

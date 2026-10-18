package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The PostgreSQL baseline: resource_cache and the closure resource_ancestor of
// core/db/migrations/00001_init.sql (same columns, keys and indexes; without type_resource and
// resource_edge, so without the foreign key to type_resource).

const pgDatabase = "p05spike"

func pgURI(uri, db string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	u.Path = "/" + db
	return u.String(), nil
}

// rowID is the deterministic id of resource i.
func rowID(i int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("licetum-spike-resource:%d", i)))
}

func cmdPG(uri, stage string, seed, keySeed uint64, conc, n, passes int) error {
	ctx := context.Background()
	switch stage {
	case "load":
		return pgLoad(ctx, uri, seed)
	case "read":
		return pgRead(ctx, uri, seed, keySeed, conc, n, passes)
	case "drop":
		c, err := pgx.Connect(ctx, uri)
		if err != nil {
			return err
		}
		defer func() { _ = c.Close(ctx) }()
		_, err = c.Exec(ctx, `DROP DATABASE IF EXISTS `+pgDatabase)
		return err
	}
	return errors.New("unknown stage " + stage)
}

func pgLoad(ctx context.Context, uri string, seed uint64) error {
	admin, err := pgx.Connect(ctx, uri)
	if err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+pgDatabase); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgDatabase); err != nil {
		return err
	}
	var version string
	_ = admin.QueryRow(ctx, `SHOW server_version`).Scan(&version)
	var sb string
	_ = admin.QueryRow(ctx, `SHOW shared_buffers`).Scan(&sb)
	_ = admin.Close(ctx)
	dbURI, err := pgURI(uri, pgDatabase)
	if err != nil {
		return err
	}
	c, err := pgx.Connect(ctx, dbURI)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close(ctx) }()
	for _, q := range []string{
		`CREATE TABLE resource_cache (
			id uuid DEFAULT gen_random_uuid() NOT NULL,
			type_resource_id uuid NOT NULL,
			external_id character varying NOT NULL,
			name character varying NOT NULL,
			synced_at timestamp without time zone DEFAULT now() NOT NULL,
			active boolean DEFAULT true NOT NULL,
			meta jsonb DEFAULT '{}'::jsonb NOT NULL)`,
		`CREATE TABLE resource_ancestor (
			ancestor_id uuid NOT NULL,
			descendant_id uuid NOT NULL,
			inherit character varying[],
			descendant_type_id uuid NOT NULL,
			descendant_external_id character varying NOT NULL,
			descendant_name character varying NOT NULL)`,
	} {
		if _, err := c.Exec(ctx, q); err != nil {
			return err
		}
	}
	d := Generate(seed)
	fmt.Printf("\n### PostgreSQL %s (shared_buffers %s): bulk load of the 1M dataset\n\n", version, sb)
	fmt.Println("| step | rows | time s |")
	fmt.Println("|---|---:|---:|")

	t0 := time.Now()
	i := 0
	nres, err := c.CopyFrom(ctx, pgx.Identifier{"resource_cache"}, []string{"id", "type_resource_id", "external_id", "name", "active", "meta"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if i >= len(d.Res) {
				return nil, nil
			}
			r := &d.Res[i]
			row := []any{rowID(i), types[r.Type].ID, r.Ext, r.Name, true, r.MetaJSON()}
			i++
			return row, nil
		}))
	if err != nil {
		return err
	}
	fmt.Printf("| COPY resource_cache | %d | %.1f |\n", nres, time.Since(t0).Seconds())

	// The closure in the order the engine writes it (by ancestor), the resource itself included.
	t0 = time.Now()
	type frame struct {
		x    int32
		mask []string
		all  bool
	}
	var stack []frame
	var pending [][]any
	a := 0
	nanc, err := c.CopyFrom(ctx, pgx.Identifier{"resource_ancestor"},
		[]string{"ancestor_id", "descendant_id", "inherit", "descendant_type_id", "descendant_external_id", "descendant_name"},
		pgx.CopyFromFunc(func() ([]any, error) {
			for len(pending) == 0 {
				if a >= len(d.Res) {
					return nil, nil
				}
				aid := rowID(a)
				stack = append(stack[:0], frame{x: int32(a), all: true})
				for len(stack) > 0 {
					f := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					r := &d.Res[f.x]
					var inherit []string
					if !f.all {
						inherit = f.mask
					}
					pending = append(pending, []any{aid, rowID(int(f.x)), inherit, types[r.Type].ID, r.Ext, r.Name})
					for _, k := range d.Kids[d.KidStart[f.x]:d.KidStart[f.x+1]] {
						next := frame{x: k, mask: f.mask, all: f.all}
						if inh := types[d.Res[k].Type].Inherit; inh != nil {
							if f.all {
								next.mask, next.all = inh, false
							} else {
								next.mask = intersect(f.mask, inh)
							}
						}
						stack = append(stack, next)
					}
				}
				a++
			}
			row := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			return row, nil
		}))
	if err != nil {
		return err
	}
	fmt.Printf("| COPY resource_ancestor | %d | %.1f |\n", nanc, time.Since(t0).Seconds())

	for _, q := range []string{
		`ALTER TABLE ONLY resource_cache ADD CONSTRAINT resource_cache_pkey PRIMARY KEY (id)`,
		`ALTER TABLE ONLY resource_cache ADD CONSTRAINT uq_resource_cache_type_resource_external_id UNIQUE (type_resource_id, external_id)`,
		`CREATE INDEX ix_resource_cache_meta ON resource_cache USING gin (meta jsonb_path_ops)`,
		`CREATE INDEX ix_resource_cache_type_name ON resource_cache USING btree (type_resource_id, name)`,
		`CREATE INDEX ix_resource_cache_type_resource_id ON resource_cache USING btree (type_resource_id)`,
		`ALTER TABLE ONLY resource_ancestor ADD CONSTRAINT resource_ancestor_pkey PRIMARY KEY (ancestor_id, descendant_id)`,
		`CREATE INDEX ix_resource_ancestor_descendant ON resource_ancestor USING btree (descendant_id, ancestor_id)`,
		`ALTER TABLE ONLY resource_ancestor ADD CONSTRAINT resource_ancestor_ancestor_fkey FOREIGN KEY (ancestor_id) REFERENCES resource_cache(id) ON DELETE CASCADE`,
		`ALTER TABLE ONLY resource_ancestor ADD CONSTRAINT resource_ancestor_descendant_fkey FOREIGN KEY (descendant_id) REFERENCES resource_cache(id) ON DELETE CASCADE`,
		`VACUUM ANALYZE resource_cache`,
		`VACUUM ANALYZE resource_ancestor`,
	} {
		t0 = time.Now()
		if _, err := c.Exec(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
		fmt.Printf("| %s | | %.1f |\n", strings.Join(strings.Fields(q), " "), time.Since(t0).Seconds())
	}

	fmt.Println("\n| relation | heap MB | indexes MB | total MB |")
	fmt.Println("|---|---:|---:|---:|")
	for _, t := range []string{"resource_cache", "resource_ancestor"} {
		var heap, idx, total int64
		if err := c.QueryRow(ctx, `SELECT pg_relation_size($1), pg_indexes_size($1), pg_total_relation_size($1)`, t).Scan(&heap, &idx, &total); err != nil {
			return err
		}
		fmt.Printf("| %s | %.1f | %.1f | %.1f |\n", t, float64(heap)/1e6, float64(idx)/1e6, float64(total)/1e6)
	}
	return nil
}

const (
	pgPointSQL = `SELECT r.id, r.name, r.meta, r.active, a.ancestor_id, a.inherit
		FROM resource_cache r JOIN resource_ancestor a ON a.descendant_id = r.id
		WHERE r.type_resource_id = $1 AND r.external_id = $2`
	pgPageSQL = `SELECT descendant_external_id, descendant_name, inherit FROM resource_ancestor
		WHERE ancestor_id = $1 AND descendant_type_id = $2 ORDER BY ancestor_id, descendant_id LIMIT 1000`
)

func pgRead(ctx context.Context, uri string, seed, keySeed uint64, conc, n, passes int) error {
	dbURI, err := pgURI(uri, pgDatabase)
	if err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(dbURI)
	if err != nil {
		return err
	}
	cfg.MaxConns = int32(conc)
	cfg.MinConns = int32(conc)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	d := Generate(seed)
	rng := rand.New(rand.NewPCG(keySeed, 7))
	type op struct {
		sql  string
		args []any
	}
	points := make([]op, n)
	for i := range points {
		r := &d.Res[rng.IntN(len(d.Res))]
		points[i] = op{pgPointSQL, []any{types[r.Type].ID, r.Ext}}
	}
	var pages []op
	for range 200 {
		pages = append(pages, op{pgPageSQL, []any{rowID(d.TypeOff[0] + rng.IntN(types[0].Count)), types[3].ID}})
	}
	// The plans, with buffers, of one read of each kind (before the measured passes).
	for _, o := range []op{points[len(points)-1], pages[len(pages)-1]} {
		rows, err := pool.Query(ctx, `EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) `+o.sql, o.args...)
		if err != nil {
			return err
		}
		fmt.Println("\n```")
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				return err
			}
			fmt.Println(line)
		}
		rows.Close()
		fmt.Println("```")
	}
	run := func(ops []op) (summary, int, error) {
		readOps := make([]func() (int, error), len(ops))
		for i, o := range ops {
			readOps[i] = func() (int, error) {
				rows, err := pool.Query(ctx, o.sql, o.args...)
				if err != nil {
					return 0, err
				}
				k := 0
				for rows.Next() {
					k++
				}
				rows.Close()
				return k, rows.Err()
			}
		}
		return runFuncs(conc, readOps)
	}
	fmt.Printf("\n### PostgreSQL reads, %d in flight, key seed %d\n\n", conc, keySeed)
	fmt.Println("| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|---:|---:|")
	for p := 1; p <= passes; p++ {
		for _, set := range []struct {
			name string
			ops  []op
		}{{"resource row + closure (point decision)", points}, {"closure page of 1,000 (division -> items)", pages}} {
			sum, rows, err := run(set.ops)
			if err != nil {
				return err
			}
			fmt.Printf("| %d | %s | %s | %.2f | %s |\n", p, set.name, sum.row(), float64(rows)/float64(sum.N), sum.rate())
		}
	}
	return nil
}

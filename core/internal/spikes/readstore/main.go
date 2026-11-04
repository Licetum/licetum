// Command readstore is the measurement tool of the P0.5 spike (issue #51): it generates a
// deterministic dataset of 1,000,000 resources and measures the planned CQL read model on ScyllaDB
// and Apache Cassandra, and the PostgreSQL closure as the baseline. It is a throwaway tool, not
// product code; docs/adr/0002-read-store.md has the results.
package main

import (
	"flag"
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: readstore <command> [flags]

commands:
  gen    payload of the first synchronization and the region rows it derives (no store)
  load   apply the first synchronization (region tables and the log) to a CQL store
  read   point reads of resource_v and pages of descendant_v / type_member_v
  mvcc   range tombstones of the MVCC garbage collection
  pg     the PostgreSQL baseline (resource_cache + resource_ancestor)

run "readstore <command> -h" for the flags of a command`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	seed := fs.Uint64("seed", 51, "seed of the dataset")
	hosts := fs.String("hosts", "127.0.0.1", "CQL contact points, comma-separated")
	gen := fs.Int("gen", 1, "generation of the region keyspace")
	conc := fs.Int("conc", 64, "statements in flight")
	var err error
	switch cmd {
	case "gen":
		_ = fs.Parse(args)
		err = cmdGen(*seed)
	case "load":
		batch := fs.Int("batch", 50, "rows per unlogged single-partition batch")
		maxRows := fs.Int("max-rows", 0, "stop each table after this many rows (0: all)")
		tables := fs.String("tables", "resource_v,descendant_v,type_member_v,attr_v,log", "what to write, comma-separated")
		_ = fs.Parse(args)
		err = cmdLoad(*hosts, *gen, *seed, *conc, *batch, *maxRows, *tables)
	case "read":
		n := fs.Int("n", 10_000, "point reads per pass")
		pages := fs.Int("pages", 200, "pages of 1,000 rows per pass and table")
		passes := fs.Int("passes", 2, "passes over the same keys (the first is the cold one)")
		keySeed := fs.Uint64("key-seed", 1, "seed of the keys read")
		_ = fs.Parse(args)
		err = cmdRead(*hosts, *gen, *seed, *keySeed, *conc, *n, *pages, *passes)
	case "mvcc":
		keys := fs.Int("keys", 1_000, "hot keys")
		versions := fs.Int("versions", 1_000, "versions per key")
		every := fs.String("every", "1,10", "GC cadences to measure: the GC fires on every k-th write")
		stage := fs.String("stage", "write", "write | measure | reinsert | describe")
		label := fs.String("label", "", "label of the measurement")
		flushEvery := fs.Int("flush-every", 0, "write: flush the memtables every n versions (0: never), through docker exec")
		container := fs.String("container", "", "write: the container to flush")
		bypass := fs.Bool("bypass-cache", false, "measure: read with BYPASS CACHE (ScyllaDB only)")
		_ = fs.Parse(args)
		err = cmdMVCC(*hosts, mvccOptions{keys: *keys, versions: *versions, every: *every, stage: *stage, label: *label, conc: *conc,
			flushEvery: *flushEvery, container: *container, bypass: *bypass})
	case "pg":
		uri := fs.String("uri", "postgres://postgres@127.0.0.1:5433/postgres?sslmode=disable", "PostgreSQL server (a database p05spike is created on it)")
		stage := fs.String("stage", "load", "load | read | drop")
		n := fs.Int("n", 10_000, "point reads per pass")
		passes := fs.Int("passes", 2, "passes over the same keys")
		keySeed := fs.Uint64("key-seed", 1, "seed of the keys read")
		_ = fs.Parse(args)
		err = cmdPG(*uri, *stage, *seed, *keySeed, *conc, *n, *passes)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

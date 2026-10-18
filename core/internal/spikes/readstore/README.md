# Read store spike (P0.5, issue #51)

A throwaway measurement tool, not product code: nothing in Licetum imports it, it has no tests and it
is kept only so that the numbers of `docs/adr/0002-read-store.md` can be reproduced (raw output in
`docs/adr/0002/results.md`). It is a nested Go module, so the `core` module and its `go.mod` do not
see it. MIT, like the rest of `core/`.

It measures the CQL read model planned for the regional PDP (the log keyspace `licetum_log` and a
generation keyspace `licetum_r_<dc>_g<gen>` with `resource_v`, `descendant_v`, `type_member_v`,
`attr_v`; `cql.go`) on ScyllaDB and Apache Cassandra, and the PostgreSQL closure of the current
engine as the baseline. Driver: `github.com/apache/cassandra-gocql-driver/v2` for both CQL stores,
`pgx/v5` for PostgreSQL.

## Dataset

`Generate(51)` (`dataset.go`): 1,000,000 resources, deterministic, in four levels: 20 `division` →
2,000 `rrs` → 100,000 `branch` → 897,980 `item` (an item hangs under a random branch). The link
`rrs → branch` passes only `read` and `write`, the others pass all levels. Every resource has a name
and three attributes: `region` and `status` (scalars) and `tags` (a list of 2 or 3 strings). Ids are
short codes, items have UUID ids. The lineage of a resource is itself and all its ancestors.

## Commands

```
go build -o /var/tmp/p05spike/readstore .
B=/var/tmp/p05spike/readstore
```

| command | what it does |
|---|---|
| `$B gen` | encodes the `data:<type>` streams of the first synchronization (binary and JSON lines, zstd default and better), cuts them into chunks of at most 1 MiB, and counts the region rows the applier derives |
| `$B load -hosts H -gen G -conc C [-batch 50] [-max-rows N] [-tables ...]` | creates the schema and writes the derived rows into `licetum_r_<dc>_g<G>` at LOCAL_QUORUM, every row with `USING TIMESTAMP ts(P, 1)`, rows of one partition in unlogged batches of at most 50, C statements in flight; then the log (`log_chunk`, `log_entry`) |
| `$B read -hosts H -gen G -conc C -key-seed S [-passes 2]` | 10,000 random point reads `resource_v ... ver <= P LIMIT 1`, 200 pages of 1,000 rows of `descendant_v` (division → items, one bucket) and of `type_member_v`; the passes go over the same keys |
| `$B mvcc -hosts H -stage write\|measure\|reinsert\|describe` | 1,000 hot keys × 1,000 versions with the GC range delete on every k-th write (`-every 1,10`); reads at the newest P, at v1, between them and below v1, with the correctness checks and query traces |
| `$B pg -stage load\|read\|drop [-uri U] [-conc C]` | the baseline in a database `p05spike`: COPY of `resource_cache` and `resource_ancestor` (columns, keys and indexes of `core/db/migrations/00001_init.sql`, without `type_resource`), then point decisions and closure pages |

`scripts/` has the shell steps: `du.sh` (on-disk bytes per table), `restart.sh` (restart a node, wait for
CQL, drop the OS page cache) and `mvcc-stages.sh` (the MVCC stages).

## Stores

One at a time, on a 4-vCPU host; the client runs on the same host. Data directories are bind mounts,
so `du.sh` can measure them.

```
# ScyllaDB 2025.1 (developer mode, 2 shards, 4 GB; Seastar needs the container limit above 4 GB)
docker run -d --name p05-scylla --cpus 2 --memory 6g -v /var/tmp/p05spike/scylla:/var/lib/scylla \
  scylladb/scylla:2025.1 --smp 2 --memory 4G --overprovisioned 1 --developer-mode 1

# Apache Cassandra 5.0 (3 GB heap)
docker run -d --name p05-cassandra --cpus 2 --memory 5g -e MAX_HEAP_SIZE=3G -e HEAP_NEWSIZE=600M \
  -v /var/tmp/p05spike/cassandra:/var/lib/cassandra mirror.gcr.io/library/cassandra:5.0
```

`H` is the container address (`docker inspect`), `C` the container, `DATA` its data directory
(`/var/tmp/p05spike/scylla/data` or `/var/tmp/p05spike/cassandra/data`), `KS` the generation keyspace
(`licetum_r_datacenter1_g1`). The sequence per store:

```
$B load -hosts H -gen 1 -conc 64                                 # first synchronization and the log
scripts/du.sh DATA licetum
docker exec C nodetool flush;          scripts/du.sh DATA licetum
docker exec C nodetool compact KS;     docker exec C nodetool compact licetum_log;  scripts/du.sh DATA licetum
scripts/restart.sh C;  $B read -hosts H -gen 1 -conc 1 -key-seed 1   # pass 1 cold, pass 2 warm
scripts/restart.sh C;  $B read -hosts H -gen 1 -conc 32 -key-seed 2 -passes 3
for g in 2:16 3:64 4:256; do                                     # concurrency, first 300,000 rows per table
  $B load -hosts H -gen ${g%:*} -conc ${g#*:} -max-rows 300000 -tables resource_v,descendant_v,type_member_v,attr_v
done                                                             # then DROP KEYSPACE and nodetool clearsnapshot
$B mvcc -hosts H -stage write > mvcc.txt
scripts/mvcc-stages.sh C H DATA mvcc.txt a                       # memtables, re-insert, flush, compaction
scripts/mvcc-stages.sh C H DATA mvcc.txt b                       # gc_grace_seconds 0, purge, re-insert
scripts/mvcc-stages.sh C H DATA mvcc-sstables.txt c [-bypass-cache]   # sstables; -bypass-cache on ScyllaDB
```

PostgreSQL (any server; the database `p05spike` is created and dropped):

```
$B pg -stage load
sync; echo 3 > /proc/sys/vm/drop_caches;  $B pg -stage read -conc 1 -key-seed 1
sync; echo 3 > /proc/sys/vm/drop_caches;  $B pg -stage read -conc 32 -key-seed 2 -passes 3
$B pg -stage drop
```

## Formats

The stream `data:<type>` is a sequence of length-prefixed records (`payload.go`): ext, name, active,
parents (type uuid, ext), canonical meta JSON, lineage (type uuid, ext, mask; no mask = all levels;
the resource itself included). JSON lines with the same content are measured for comparison. Chunks
hold whole records and are at most 1 MiB after zstd. `resource_v.lineage` uses the same encoding.
An absent mask or `unresolved_up` is bound as unset, never `NULL` (a `NULL` writes a cell tombstone).

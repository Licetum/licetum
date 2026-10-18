# ADR 0002: the read store of the regional PDP

- Status: proposed
- Issue: [#51](https://github.com/Licetum/licetum/issues/51) (phase P0.5 of [#49](https://github.com/Licetum/licetum/issues/49))
- Measurements: `core/internal/spikes/readstore` (how to reproduce), raw output in [`0002/results.md`](0002/results.md)

## Context

The refactor of #49 moves decisions to a regional AuthZEN PDP. PostgreSQL stays the source of truth. A writer
publishes an immutable change log; a regional applier projects it into region tables; the PDP reads them "at
position P": for every key, the newest version at or below P. The read-model port is pluggable: `postgres` (one
region, or a regional PostgreSQL streaming replica) or a CQL store. This record decides which CQL store, or none.

What a CQL read store has to give:

- reads at a position P: `SELECT … FROM resource_v WHERE type_id = ? AND ext = ? AND ver <= ? LIMIT 1`, one to three
  such single-partition reads per point decision, and pages of `descendant_v` / `type_member_v` for listings;
- LOCAL_QUORUM reads and writes in every data center, RF 3 per data center, so a region answers on its own when the
  others are gone;
- no LWT, no counters, no materialized views, no secondary indexes; only unlogged single-partition batches; every
  write with an explicit `USING TIMESTAMP ts(position, phase) = position*2 + phase` (phase 0 deletes, 1 upserts);
- MVCC garbage collection by the applier: when version r of a key is written and the previous version v1 is older
  than `MVCC__RETENTION`, `DELETE … USING TIMESTAMP ts(r, 0) WHERE … AND ver < v1` (one clustering range tombstone).
  The newest version is never deleted, and a late re-insert of an old version (`ts(old, 1) < ts(r, 0)`) has to stay
  invisible;
- a license under which a self-hosted enterprise installation can run the product.

The model measured is the one of the plan: the log keyspace `licetum_log` (`log_entry`, `log_chunk` with bodies of at
most 1 MiB of zstd, TTL 14 days) and a generation keyspace `licetum_r_<dc>_g<gen>` with `resource_v` (LCS,
`ver DESC`), `descendant_v`, `type_member_v` (16 buckets per type) and `attr_v` (8 buckets). The table definitions are
in `core/internal/spikes/readstore/cql.go`.

## Options

| | ScyllaDB 2025.1 | Apache Cassandra 5.0 | PostgreSQL streaming replicas only |
|---|---|---|---|
| License | ScyllaDB Software License Agreement (source-available) | Apache License 2.0 | PostgreSQL License |
| Use by a self-hosted customer | free within the usage limit and the eligibility terms below; a commercial agreement above them | no conditions | no conditions |
| Regional reads | LOCAL_QUORUM, RF 3 per DC | LOCAL_QUORUM, RF 3 per DC | a replica per region; reads see a consistent snapshot of the primary |
| Writes in a region | the regional applier | the regional applier | none: all writes go to the primary; a region without the primary keeps reading its replica |
| Driver | `apache/cassandra-gocql-driver/v2` (Apache-2.0), or the shard-aware fork `scylladb/gocql` (Apache-2.0) | `apache/cassandra-gocql-driver/v2` | `pgx/v5` (already used) |

### The ScyllaDB license, as verified

From 2025.1 on, ScyllaDB ships under a source-available license (its FAQ names 6.2 as the last AGPL release). The
license text is in the repository:

- the version shipped with 2025.1: <https://github.com/scylladb/scylladb/blob/branch-2025.1/LICENSE-ScyllaDB-Source-Available.md>
  (version 1.0, last updated December 18, 2024);
- the current one: <https://github.com/scylladb/scylladb/blob/master/LICENSE-ScyllaDB-Source-Available.md>
  (version 1.1, last updated April 12, 2026).

What it says (quoted from version 1.1; version 1.0 has the same usage limit and restrictions, without the
eligibility clause):

- The usage limit: "Licensee's total overall available storage across all deployments and clusters of the Software
  and the Licensed Work under this License shall not exceed 10TB and/or an upper limit of 50 VCPUs (hyper threads)."
  The licensee is the legal entity on whose behalf the software is used, so the limit counts every cluster of the
  organization, not one installation.
- Above the limit: "If Licensee's Use of the Software does not comply with the above license grant or the terms of
  this section (including exceeding the Usage Limit set forth below), Licensee must: (i) refrain from any Use of the
  Software; and (ii) unless Licensee is a Never Customer, purchase a commercial paid license".
- Eligibility (new in 1.1): "The license granted under this Agreement is strictly limited to Never Customers", an
  entity that "does not have, and has not had within the previous twelve (12) months, a paid commercial subscription,
  professional services agreement, or any other commercial relationship with Licensor". A commercial customer may use
  the free license "exclusively for non-production purposes", within the usage limit.
- Restrictions: "Licensee may not offer the Software as a software-as-a-service (SaaS) or commercial
  database-as-as-service (dBaaS) offering." "Licensor reserves the right to modify this Agreement at any time."
  The software may send telemetry and may require a license key.
- The grant lists "copying, distributing, evaluating (…) and improving the Software" and creating modified versions.
  ScyllaDB's FAQ (<https://www.scylladb.com/source-available-faq>; not reachable from the measurement host, read
  through a search engine summary) describes the free tier as the full product, free in production up to 50 vCPUs and
  10 TB. We have no legal review of it.

For Licetum this means: a customer may run ScyllaDB under this license without a commercial agreement only while all
of its ScyllaDB clusters together stay within 50 vCPUs and 10 TB and it has had no commercial relationship with
ScyllaDB in the last twelve months. A regional deployment of the plan (RF 3 per data center) passes 50 vCPUs early:
three data centers of three 8-vCPU nodes are 72 vCPUs. Above the limit the customer needs a commercial license.
Licetum cannot be offered as a service on the free license. Cassandra and PostgreSQL have no such conditions.

## Measurements

### Setup

- Host: a 4-vCPU VM (Intel Xeon @ 2.10 GHz), 15 GB RAM, a virtio disk, Docker 29.8. The client runs on the same
  host. Other agents ran PostgreSQL tests on the host during the runs (load average 4 to 5).
- ScyllaDB 2025.1.16, single node, `--smp 2 --memory 4G --overprovisioned 1 --developer-mode 1`, container `--cpus 2
  --memory 6g`. Cassandra 5.0.9 (official image, default `cassandra.yaml`: `storage_compatibility_mode: CASSANDRA_4`,
  skip-list memtables), `MAX_HEAP_SIZE=3G HEAP_NEWSIZE=600M`, `--cpus 2 --memory 5g`. One store at a time.
- Keyspaces `NetworkTopologyStrategy` with RF 1 in the single data center, so LOCAL_QUORUM is one replica: the
  numbers have no replication or coordination cost.
- PostgreSQL 16.15, a shared development server on the same host (`shared_buffers` 128 MB, all four vCPUs, not
  restarted).
- Driver `github.com/apache/cassandra-gocql-driver/v2` v2.1.2, `pgx/v5` v5.11.0, Go 1.26.
- Dataset: 1,000,000 resources, deterministic: 20 divisions → 2,000 RRS → 100,000 branches → 897,980 items. Each has
  a name and three attributes (`region`, `status`, `tags` with 2 or 3 elements). Items have UUID ids, the others short
  codes. The lineage of a resource is itself and its ancestors (4 entries for an item).

Absolute numbers from one node in a container on a shared 4-vCPU host are indicative. The ratios between the stores,
measured with the same client on the same host, are what matters.

### Payload of the first synchronization

The `data:<type>` streams of the 1M resources with their full lineage, in length-prefixed binary records (ext, name,
active, parents, canonical meta JSON, lineage with type uuid, ext and mask), zstd, cut at record boundaries into
chunks of at most 1 MiB:

| format, zstd level | raw bytes | zstd bytes | ratio | chunks | raw B/resource | zstd B/resource | encode + compress (1 core) |
|---|---:|---:|---:|---:|---:|---:|---:|
| binary, default | 302,444,535 | 54,038,492 | 5.6 | 57 | 302 | 54.0 | 8.2 s |
| binary, better | 302,444,535 | 51,186,433 | 5.9 | 54 | 302 | 51.2 | 6.8 s |
| JSON lines, default | 510,214,135 | 54,484,659 | 9.4 | 58 | 510 | 54.5 | 12.9 s |
| JSON lines, better | 510,214,135 | 49,898,722 | 10.2 | 53 | 510 | 49.9 | 12.5 s |

The item stream is 95% of it (51.3 MB in 51 chunks with the binary format): random UUID ids do not compress. Chunks
are 0.95 MB on average.

Region rows the applier derives (`descendant_v` has one row per (ancestor, descendant) pair, the resource itself
included, as the PostgreSQL closure does):

| table | rows | partitions | largest partition (rows) |
|---|---:|---:|---:|
| `resource_v` | 1,000,000 | 1,000,000 | 1 |
| `descendant_v` | 3,895,940 | 1,752,488 | 2,971 |
| `type_member_v` | 1,000,000 | 60 | 56,464 |
| `attr_v` | 5,499,702 | 999,446 | 90,186 (`status = active` of items, one of 8 buckets) |
| total | 11,395,642 | | |

On-disk size of one replica (LZ4, the stores' default; all files of the table):

| table | ScyllaDB flushed | ScyllaDB major compaction | Cassandra flushed | Cassandra major compaction | PostgreSQL (heap + indexes) |
|---|---:|---:|---:|---:|---:|
| `resource_v` / `resource_cache` | 195.3 MB | 196.3 MB | 207.4 MB | 172.0 MB | 398.4 MB (219.9 + 178.4) |
| `descendant_v` / `resource_ancestor` | 393.4 MB | 393.4 MB | 364.0 MB | 369.1 MB | 994.1 MB (606.6 + 387.3) |
| `type_member_v` | 67.9 MB | 51.1 MB | 58.5 MB | 47.3 MB | |
| `attr_v` | 279.3 MB | 280.4 MB | 265.9 MB | 267.8 MB | (GIN on `meta`, included above) |
| region total | 935.9 MB | 921.3 MB | 895.8 MB | 856.1 MB | 1,392.5 MB |
| `log_chunk` | 54.5 MB | 54.5 MB | 54.3 MB | 54.3 MB | |

About 80 bytes per region row and 0.9 KB per resource on a CQL replica; with RF 3, about 2.7 GB per million resources
and data center, plus 0.16 GB of log per first synchronization.

### Applier throughput

The full first synchronization at LOCAL_QUORUM, 64 statements in flight, rows of one partition in unlogged batches of
at most 50 rows, one table after the other:

| table | statements | ScyllaDB time | ScyllaDB rows/s | ScyllaDB stmt p50 / p99 ms | Cassandra time | Cassandra rows/s | Cassandra stmt p50 / p99 ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| `resource_v` (single rows) | 1,000,000 | 34.6 s | 28,915 | 1.91 / 7.00 | 57.9 s | 17,275 | 2.23 / 32.02 |
| `descendant_v` | 1,772,211 | 87.6 s | 44,474 | 2.06 / 28.81 | 88.3 s | 44,113 | 1.54 / 36.77 |
| `type_member_v` | 20,036 | 10.5 s | 94,943 | 30.89 / 74.78 | 8.0 s | 124,557 | 13.06 / 163.07 |
| `attr_v` | 1,088,273 | 90.3 s | 60,887 | 2.15 / 43.96 | 70.7 s | 77,753 | 1.41 / 47.66 |
| region tables | 3,880,520 | **223.0 s** | **51,091** | | **225.0 s** | **50,655** | |
| log (57 chunks, 51.5 MiB) | 57 | 0.2 s | 220 MiB/s | 160 / 234 | 0.5 s | 106 MiB/s | 405 / 488 |

The log time excludes the 8.2 s of encoding. No retries, no batch or large-partition warnings on either store.

The cost is the number of statements, not of rows: both stores ran about 17,000 statements per second, and most
statements are partitions of one or a few rows: every `resource_v` row, the 1,000,000 self rows of `descendant_v` (in
this dataset nothing has a descendant of its own type), the nine or so items of a branch spread over 16 buckets, and
nearly every `attr_v` row of `name` (names are almost unique).

Concurrency, on the first 300,000 rows of each table (1,250,954 rows, 322,369 statements; the batched tables start
with the largest partitions, so their rates are higher than in the full run):

| in flight | ScyllaDB rows/s | ScyllaDB `resource_v` rows/s (p50 / p99 ms) | Cassandra rows/s | Cassandra `resource_v` rows/s (p50 / p99 ms) |
|---:|---:|---:|---:|---:|
| 16 | 49,212 | 17,546 (0.72 / 3.61) | 34,233 | 12,036 (0.83 / 7.91) |
| 64 | 94,245 | 43,508 (1.29 / 4.89) | 69,785 | 26,755 (1.57 / 22.05) |
| 256 | 100,436 | 49,901 (4.28 / 18.28) | 91,252 | 39,217 (4.51 / 36.51) |

The client used about one core at 256 in flight (15.9 s of CPU in 15.2 s). Both stores saturate their two vCPUs
between 64 and 256 in flight; ScyllaDB gives 1.3 to 1.7 times the single-row write rate of Cassandra, batched rows are
on par.

### Cold and warm reads at LOCAL_QUORUM

After `nodetool flush`, a major compaction and a restart of the node, and after `echo 3 > /proc/sys/vm/drop_caches`
(permitted on this host). Pass 1 is the first touch of 10,000 random keys (cold), pass 2 the same keys again (warm).
ScyllaDB reads sstables with O_DIRECT, so the OS page cache does not serve it; its row cache is empty after the
restart. Cassandra has no row cache by default and reads through the OS page cache, which the drop emptied. The VM's
disk may still be cached by the hypervisor. Pages are 1,000 rows of one partition: `descendant_v` (division → items,
one bucket, about 2,800 rows) and `type_member_v` (items after a random item). Milliseconds, p50 / p95 / p99 / max:

| read | in flight | pass | ScyllaDB | Cassandra | PostgreSQL |
|---|---:|---|---|---|---|
| point (`resource_v ver <= P LIMIT 1`; PG: row + closure) | 1 | cold | 1.88 / 2.84 / 4.68 / 45.3 | 2.27 / 3.40 / 4.67 / 33.9 | 0.47 / 0.82 / 1.20 / 50.3 |
| | 1 | warm | 1.55 / 1.94 / 3.32 / 13.0 | 1.82 / 2.14 / 3.15 / 50.7 | 0.13 / 0.20 / 0.32 / 4.7 |
| | 32 | cold | 2.09 / 6.40 / 10.31 / 21.1 | 11.65 / 63.28 / 72.28 / 163.3 | 3.50 / 8.46 / 12.21 / 52.8 |
| | 32 | warm | 1.28 / 4.46 / 8.29 / 29.4 (18,694 reads/s) | 3.60 / 34.14 / 42.44 / 54.3 (5,256 reads/s, 6th pass) | 0.43 / 6.43 / 10.01 / 20.8 (20,744 reads/s) |
| page of 1,000 `descendant_v` (PG: closure by ancestor and type) | 1 | cold | 5.14 / 7.77 / 10.31 / 10.6 | 5.31 / 7.47 / 11.61 / 25.9 | 19.20 / 35.80 / 45.15 / 45.4 |
| | 1 | warm | 2.98 / 5.08 / 8.80 / 11.5 | 3.93 / 5.27 / 5.79 / 5.9 | 17.74 / 22.37 / 28.14 / 28.3 |
| | 32 | cold | 63.4 / 144.2 / 170.1 / 179.4 | 482.9 / 794.5 / 1011.9 / 1020.2 | 54.2 / 283.2 / 343.3 / 348.2 |
| | 32 | warm | 35.7 / 73.1 / 77.3 / 80.1 (790 pages/s) | 35.3 / 84.6 / 97.9 / 104.8 (761 pages/s, 6th pass) | 211.0 / 343.2 / 414.8 / 428.9 (140 pages/s) |
| page of 1,000 `type_member_v` | 1 | cold | 4.56 / 6.27 / 8.05 / 8.7 | 4.71 / 6.76 / 16.27 / 20.4 | |
| | 1 | warm | 2.72 / 3.95 / 6.44 / 7.8 | 3.97 / 5.25 / 7.04 / 8.1 | |
| | 32 | warm | 33.8 / 47.4 / 62.9 / 64.0 (935 pages/s) | 32.3 / 83.8 / 90.1 / 105.8 (788 pages/s) | |

- At one request in flight both CQL stores answer a point read in about 2 ms, most of it the round trip through the
  driver and the container network (ScyllaDB's traces of the MVCC reads put the server side at 0.1 to 0.8 ms). Cold costs 0.3 to 0.5 ms
  more. A point decision of one to three reads costs 2 to 6 ms read one after the other, about 2 ms read in parallel.
- Under 32 in flight ScyllaDB serves 3.6 times the point reads of Cassandra on the same two vCPUs, with a p99 of 8 ms
  against 42 ms. Cassandra warmed up over several passes after the restart (pass 2: 3,346 reads/s, pass 6: 5,256),
  as a JVM does.
- PostgreSQL is not comparable as a store (four vCPUs, shared, no replication, `shared_buffers` not emptied: the cold
  plan read 11 of 13 buffers from the OS) but gives the scale: the point decision of today's engine (row plus closure
  by one index range scan) costs 0.13 ms warm at one in flight. Its closure page touches about 1,140 buffers, one per row:
  the primary key orders the rows of an ancestor by random descendant ids, the heap does not. The CQL page is one
  contiguous slice of a partition.

### Range tombstones of the MVCC garbage collection

1,000 hot keys, 1,000 versions each (version j of key k at position 1,000,000 + 1,000·j + k), in a table shaped like
`resource_v` (LCS, `ver DESC`). The GC fires on every write (`rv_k1`, 999,000 range deletes) or on every 10th
(`rv_k10`, 99,000), each previous version counting as older than the retention; the delete and the new version go in
one unlogged batch. Reads `ver <= P LIMIT 1` of every key at five positions: the newest, v1 (the oldest retained
version), a random P between them, and two positions below v1 (garbage collected).

Writes, 64 keys in flight:

| | ScyllaDB `rv_k1` | ScyllaDB `rv_k10` | Cassandra `rv_k1` | Cassandra `rv_k10` |
|---|---:|---:|---:|---:|
| 1M versions, in memtables | 40.9 s | 33.4 s | 31.9 s | 23.6 s |
| 1M versions, flushed every 100 versions | 31.1 s | 25.6 s | 36.5 s | 33.2 s |

Read latency, p50 in ms over 1,000 keys, one request in flight, the same for every P class unless shown:

| state | ScyllaDB | Cassandra |
|---|---|---|
| in memtables | 1.55–1.63 (row cache) | 1.75–1.86 |
| flushed once | 1.55–1.63 (row cache) | 1.71–1.77 |
| 10 flushes, autocompaction off (20 sstables on ScyllaDB's 2 shards, 10 on Cassandra) | 3.16–3.32 `rv_k1`, 2.84–3.09 `rv_k10` (`BYPASS CACHE`) | 1.84–1.89 at P ≥ v1, 1.95–2.12 below v1 |
| major compaction, `gc_grace_seconds` 864000 | 2.02–2.09 (`BYPASS CACHE`), 1.53–1.58 (cache) | 1.73–1.81 |
| major compaction, `gc_grace_seconds` 0 | 1.94–2.11 (`BYPASS CACHE`) | 1.65–1.91 |

What a read touches, from query tracing (three keys per class and stage):

- P ≥ v1, every state, both stores: one live row and no tombstone. ScyllaDB: "1 clustering row(s) (1 live, 0 dead),
  0 range tombstone(s)"; Cassandra: "Read 1 live rows and 0 tombstone cells". The slice starts at P and stops at the
  first live row, so it never reaches the deleted range. With ten sstables Cassandra merged one of them at the newest P
  ("Merged data from memtables and 1 sstables").
- P below v1: no row, the two bounds of one range tombstone ("2 range tombstone(s)"; "Read 0 live rows and 2 tombstone
  cells"), with ten sstables merged on Cassandra. The deleted rows did not reach the sstables: both stores drop the rows
  a range tombstone shadows already in the memtable (one million versions flushed to 0.15 MB on ScyllaDB, ten flushes
  to 1.0 MB on Cassandra).
- After the purge (`gc_grace_seconds` 0 and a major compaction): no tombstone left ("0 range tombstone(s)", "Skipped
  1/1 non-slice-intersecting sstables").
- No tombstone warning in the Cassandra log (`tombstone_warn_threshold` 1000).

The correctness claims, checked on every key at every stage on both stores (no deviation between them):

1. The newest version is never deleted: every read at the newest P returned it (0 wrong in 11 stages × 2 cadences).
2. For P ≥ v1 the read returns the newest version at or below P: 0 wrong, in every stage, also after the purge and
   after late re-inserts.
3. A late re-insert of a collected version with its original timestamp `ts(old, 1)` stays invisible while the range
   tombstone exists: 0 visible in memtables, in sstables and after a major compaction with the default
   `gc_grace_seconds`. Once the tombstone is purged, a re-insert written after the purge is visible at positions below
   v1 (1,000 of 1,000 on both stores). Reads at P ≥ v1 are not affected, since the slice meets v1 first.

## Decision

The plan's default is confirmed:

1. The read-model port stays pluggable. `postgres` is the default and first-class: one region on the primary, several
   regions on regional PostgreSQL streaming replicas, which give consistent snapshots without a CQL store.
2. The CQL adapter is written for what Apache Cassandra 5 and ScyllaDB have in common, and its store tests run on both.
3. Apache Cassandra is the documented default CQL store. ScyllaDB is supported for operators who accept its license.

Why:

- License. Cassandra (Apache-2.0) and PostgreSQL (PostgreSQL License) can run at any customer at any size. ScyllaDB's
  free use stops at 50 vCPUs and 10 TB across all clusters of the customer, a regional deployment with RF 3 passes the
  vCPU limit at about three data centers, the 1.1 terms exclude from it any organization that had a commercial
  relationship with ScyllaDB in the last year, and the terms may change at any time. A product that customers install themselves
  cannot make it the default.
- The model works the same on both stores. The MVCC reads, the range tombstones, the late re-inserts and the purge
  behaved identically; no schema change was needed between them. Nothing in the measurements calls for a
  ScyllaDB-only feature.
- Performance does not override the license. ScyllaDB is ahead where a node is CPU-bound: 3.6 times the point reads
  under 32 in flight, a p99 of 8 ms against 42 ms, 1.3 to 1.7 times the single-row writes. At one request in flight
  the two are within 0.4 ms, the full first synchronization took the same time (223 s and 225 s), the batched writes
  and the pages are on par, and Cassandra's data is 7% smaller. An operator who needs the higher read rate per vCPU
  can choose ScyllaDB and its license.
- PostgreSQL as the default: the closure of today's engine answers a point decision in 0.13 ms warm, is already
  operated by every installation, and a streaming replica keeps a region reading when the primary is gone. A CQL store
  is for installations that need regional writes of the read model to be independent of one primary, or more read
  capacity than replicas give.

The measurements found no design problem in the read path: the range tombstones of the GC are never read at P ≥ v1.
They found three things to change in the model, listed under P6.

## Consequences

### P3 (the outbox and payload format, frozen)

- Records are length-prefixed binary as measured (`payload.go`): 302 bytes per resource raw, 54 compressed. JSON
  lines compress to the same size but are 1.7 times larger raw and 1.6 times slower to encode; they stay a debugging
  format.
- zstd at the default level; the better level saves 5% (and was not slower here) and can come later without a format
  change.
- A chunk is at most 1 MiB of zstd (`chunkLimit`) and holds whole records. ScyllaDB records cells above 1 MB as large
  cells (`compaction_large_cell_warning_threshold_mb: 1`); `system.large_cells` stayed empty. 1M resources make 57
  chunks.
- The digest of a stream is sha256 of the raw stream, as in `log_entry.streams`.
- Encoding takes 8 s per million resources on one core: the writer encodes the streams of a synchronization in
  parallel.

### P6 (the CQL adapter)

- Driver: `github.com/apache/cassandra-gocql-driver/v2` for both stores (measured, v2.1.2). The shard-aware fork
  `github.com/scylladb/gocql` (Apache-2.0) is an option for ScyllaDB behind the same port; it was not measured.
- The schema states every option that differs between the stores, with values both accept: LCS for `resource_v`, an
  explicit strategy for the other region tables (ScyllaDB 2025.1 defaults to its own `IncrementalCompactionStrategy`,
  Cassandra to STCS), `TimeWindowCompactionStrategy` for the log tables with their TTL. Store-specific options
  (ScyllaDB `tombstone_gc`, Cassandra `memtable` or UCS) belong to a per-store profile, not to the shared schema.
  ScyllaDB 2025.1 creates keyspaces with tablets enabled; the model uses nothing tablets forbid.
- `gc_grace_seconds`: a purged range tombstone only lets old versions reappear below v1, where no correct read goes
  (claim 3 above). The region tables can therefore use a short value, at least the hinted-handoff window (3 hours by
  default), instead of the 10-day default, so tombstones do not accumulate. On ScyllaDB set `tombstone_gc = {'mode':
  'timeout'}` explicitly (the 2025.1 default here). This holds only with the two rules below.
- Two rules follow from claim 3 and need enforcing: the PDP never reads at a position older than the GC horizon
  (regional position minus `MVCC__RETENTION`), and an applier never writes a version older than the horizon of its key
  (a lagging applier stops and rebuilds the generation instead).
- Changes to the model:
  - No `NULL` bind values: an absent mask or an empty `unresolved_up` is left unset (`gocql.UnsetValue`). A `NULL` in an
    INSERT writes a cell tombstone per row.
  - Drop the self rows of `descendant_v`. They are 1,000,000 of its 3.9 million rows and 1,000,000 of its 1.8 million
    statements (each its own partition): the PDP already reads the anchor itself from `resource_v`. This takes a
    quarter of the statements of a first synchronization.
  - Size the buckets by type size: at 8 buckets the largest `attr_v` partition is 90,186 rows (4.9 MB) for 0.9 million
    items; at 10 million resources a type would reach a million rows per partition. Choose `buckets(type)` so that a
    partition stays under 100,000 rows (ScyllaDB's `compaction_rows_count_warning_threshold`), and record it in the
    state of the generation. The other way round, 16 buckets split the nine or so items of a branch into about seven
    partitions: a listing of them reads 16 partitions and the first synchronization writes about seven statements. The buckets of
    `descendant_v` should depend on the pair (anchor type, descendant type), from the average number of descendants
    (one bucket for branch → item, 16 for division → item).
  - To measure in P6: bucketing `attr_v` rows of `name` by a hash of the value instead of the ext. Names are nearly
    unique, so today each is a single-row partition (1 million statements of the first synchronization) and a lookup
    by name reads 8 partitions; bucketed by value a lookup reads one and the writes batch. Values shared by many
    resources (`status`) keep the ext buckets.
- A first synchronization of 1M resources takes about 4 minutes on two vCPUs at 64 in flight on either store; it is
  bounded by statements per second, which grow with the nodes of the region.
- Not measured here, for P6: RF 3 with three nodes per data center and two data centers (LOCAL_QUORUM with real
  replicas), Cassandra with `cassandra_latest.yaml` (BTI sstables, trie memtables, UCS), pages of `descendant_v` with
  versions and the GC (a page reads the versions and range tombstones of all its rows, unlike a point read), and a regional
  PostgreSQL replica (expected to read like the primary).

### Contracts (proposed, `contracts.yaml` is not changed here)

- ST07: add that the write helper never binds `NULL` (a cell tombstone); unset instead.
- ST08: add that a late re-insert below the newest GC range tombstone stays invisible, and that reads at P ≥ v1 are
  unchanged after the tombstone is purged and a collected version is written again.
- New (cql, P6): the applier refuses a write older than the GC horizon of its key, and the PDP refuses a read
  position older than the GC horizon.
- New (cql, P6, nightly for the second store): the CQL schema and the store tests pass on Apache Cassandra 5 and on
  ScyllaDB.
- New (unit, P3): every `log_chunk.body` is at most 1 MiB of zstd and holds whole records.
- ST02: if the self rows leave `descendant_v`, its oracle (the PostgreSQL closure) excludes the anchor itself.

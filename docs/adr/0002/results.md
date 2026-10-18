# ADR 0002: raw results

The output of `core/internal/spikes/readstore` for [ADR 0002](../0002-read-store.md), as printed, on 2026-10-18.
Lines of the form `<keyspace> <table> <bytes> <data files>` are the on-disk sizes of a table directory
(`du.sh` of the spike's README). Latencies are in milliseconds unless shown otherwise.


## Payload and derived rows (`gen`)

dataset: 1000000 resources generated in 914ms

#### payload: format binary, zstd default, chunks <= 1048576 bytes

| stream | records | raw bytes | zstd bytes | ratio | chunks | avg chunk | zstd B/resource | encode+compress |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| data:division | 20 | 2457 | 697 | 3.53 | 1 | 697 | 34.9 | 6ms |
| data:rrs | 2000 | 343875 | 44350 | 7.75 | 1 | 44350 | 22.2 | 32ms |
| data:branch | 100000 | 23883336 | 2736574 | 8.73 | 4 | 684143 | 27.4 | 1.249s |
| data:item | 897980 | 278214867 | 51256871 | 5.43 | 51 | 1005036 | 57.1 | 6.939s |
| total | 1000000 | 302444535 | 54038492 | 5.60 | 57 | 948043 | 54.0 | 8.225s |
raw bytes per resource: 302.4

#### payload: format json, zstd default, chunks <= 1048576 bytes

| stream | records | raw bytes | zstd bytes | ratio | chunks | avg chunk | zstd B/resource | encode+compress |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| data:division | 20 | 4277 | 671 | 6.37 | 1 | 671 | 33.5 | 0s |
| data:rrs | 2000 | 637875 | 40376 | 15.80 | 1 | 40376 | 20.2 | 6ms |
| data:branch | 100000 | 41883336 | 2414306 | 17.35 | 4 | 603576 | 24.1 | 884ms |
| data:item | 897980 | 467688647 | 52029306 | 8.99 | 52 | 1000563 | 57.9 | 11.966s |
| total | 1000000 | 510214135 | 54484659 | 9.36 | 58 | 939390 | 54.5 | 12.857s |
raw bytes per resource: 510.2

#### payload: format binary, zstd better, chunks <= 1048576 bytes

| stream | records | raw bytes | zstd bytes | ratio | chunks | avg chunk | zstd B/resource | encode+compress |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| data:division | 20 | 2457 | 698 | 3.52 | 1 | 698 | 34.9 | 11ms |
| data:rrs | 2000 | 343875 | 40809 | 8.43 | 1 | 40809 | 20.4 | 30ms |
| data:branch | 100000 | 23883336 | 2427855 | 9.84 | 3 | 809285 | 24.3 | 1.312s |
| data:item | 897980 | 278214867 | 48717071 | 5.71 | 49 | 994225 | 54.3 | 5.439s |
| total | 1000000 | 302444535 | 51186433 | 5.91 | 54 | 947896 | 51.2 | 6.792s |
raw bytes per resource: 302.4

#### payload: format json, zstd better, chunks <= 1048576 bytes

| stream | records | raw bytes | zstd bytes | ratio | chunks | avg chunk | zstd B/resource | encode+compress |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| data:division | 20 | 4277 | 666 | 6.42 | 1 | 666 | 33.3 | 0s |
| data:rrs | 2000 | 637875 | 36074 | 17.68 | 1 | 36074 | 18.0 | 9ms |
| data:branch | 100000 | 41883336 | 2112943 | 19.82 | 3 | 704314 | 21.1 | 634ms |
| data:item | 897980 | 467688647 | 47749039 | 9.79 | 48 | 994771 | 53.2 | 11.831s |
| total | 1000000 | 510214135 | 49898722 | 10.22 | 53 | 941485 | 49.9 | 12.474s |
raw bytes per resource: 510.2

#### region rows derived by the applier

| table | rows | partitions | max rows per partition |
|---|---:|---:|---:|
| resource_v | 1000000 | 1000000 | 1 |
| descendant_v | 3895940 | 1752488 | 2971 |
| type_member_v | 1000000 | 60 | 56464 |
| attr_v | 5499702 | 999446 | 90186 |
| total | 11395642 | | |


## ScyllaDB: first synchronization, 64 in flight

#### first synchronization into licetum_r_datacenter1_g1: 64 in flight, batches <= 50 rows, max rows per table 0

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 1000000 | 1000000 | 0 | 34.6 | 28915 | 1.91 | 4.57 | 7.00 | 46.10 |
| descendant_v | 3895940 | 1772211 | 0 | 87.6 | 44474 | 2.06 | 7.67 | 28.81 | 115.14 |
| type_member_v | 1000000 | 20036 | 0 | 10.5 | 94943 | 30.89 | 59.09 | 74.78 | 133.74 |
| attr_v | 5499702 | 1088273 | 0 | 90.3 | 60887 | 2.15 | 25.42 | 43.96 | 172.26 |
| log_chunk | 57 (51.5 MiB) | 57 | 0 | 0.2 | 220.0 MiB/s | 160.20 | 233.89 | 233.97 | 233.97 |
| region tables | 11395642 | 3880520 | | 223.0 | 51091 | | | | |


## ScyllaDB: sizes before flush, after flush, after major compaction


Before `nodetool flush`:  
`licetum_log log_chunk 0 0`  
`licetum_log log_entry 0 0`  
`licetum_r_datacenter1_g1 attr_v 258565208 3`  
`licetum_r_datacenter1_g1 descendant_v 393419092 2`  
`licetum_r_datacenter1_g1 resource_v 179425689 6`  
`licetum_r_datacenter1_g1 type_member_v 46382883 2`


After `nodetool flush`:  
`licetum_log log_chunk 54500776 2`  
`licetum_log log_entry 6324 1`  
`licetum_r_datacenter1_g1 attr_v 279252358 5`  
`licetum_r_datacenter1_g1 descendant_v 393419092 2`  
`licetum_r_datacenter1_g1 resource_v 195319534 8`  
`licetum_r_datacenter1_g1 type_member_v 67910051 4`


After a major compaction:  
`licetum_log log_chunk 54500780 2`  
`licetum_log log_entry 6326 1`  
`licetum_r_datacenter1_g1 attr_v 280442420 2`  
`licetum_r_datacenter1_g1 descendant_v 393418772 2`  
`licetum_r_datacenter1_g1 resource_v 196348684 2`  
`licetum_r_datacenter1_g1 type_member_v 51053916 2`


## ScyllaDB: concurrency sweep

#### first synchronization into licetum_r_datacenter1_g2: 16 in flight, batches <= 50 rows, max rows per table 300000

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 300000 | 300000 | 0 | 17.1 | 17546 | 0.72 | 2.01 | 3.61 | 23.29 |
| descendant_v | 350021 | 7250 | 0 | 3.3 | 104784 | 6.16 | 12.70 | 18.75 | 34.49 |
| type_member_v | 300020 | 6028 | 0 | 2.5 | 120038 | 5.02 | 16.83 | 29.74 | 62.56 |
| attr_v | 300913 | 9091 | 0 | 2.5 | 121247 | 3.08 | 8.21 | 12.57 | 42.87 |
| region tables | 1250954 | 322369 | | 25.4 | 49212 | | | | |

#### first synchronization into licetum_r_datacenter1_g3: 64 in flight, batches <= 50 rows, max rows per table 300000

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 300000 | 300000 | 0 | 6.9 | 43508 | 1.29 | 2.94 | 4.89 | 26.39 |
| descendant_v | 350021 | 7250 | 0 | 2.8 | 126627 | 19.51 | 51.13 | 106.89 | 168.45 |
| type_member_v | 300020 | 6028 | 0 | 1.8 | 171218 | 16.83 | 38.25 | 57.08 | 83.62 |
| attr_v | 300913 | 9091 | 0 | 1.9 | 161630 | 9.14 | 26.09 | 36.44 | 55.46 |
| region tables | 1250954 | 322369 | | 13.3 | 94245 | | | | |

#### first synchronization into licetum_r_datacenter1_g4: 256 in flight, batches <= 50 rows, max rows per table 300000

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 300000 | 300000 | 0 | 6.0 | 49901 | 4.28 | 11.54 | 18.28 | 46.53 |
| descendant_v | 350021 | 7250 | 0 | 2.7 | 128075 | 84.50 | 179.58 | 205.16 | 267.73 |
| type_member_v | 300020 | 6028 | 0 | 1.7 | 173746 | 66.86 | 128.94 | 147.01 | 169.14 |
| attr_v | 300913 | 9091 | 0 | 2.0 | 151696 | 37.72 | 99.10 | 121.99 | 180.78 |
| region tables | 1250954 | 322369 | | 12.5 | 100436 | | | | |


## ScyllaDB: reads, 1 in flight (after restart and page-cache drop)

#### reads at LOCAL_QUORUM, 1 in flight, key seed 1

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read |
|---|---|---:|---:|---:|---:|---:|---:|
| 1 | resource_v ver <= P LIMIT 1 | 10000 | 1.88 | 2.84 | 4.68 | 45.27 | 1.00 |
| 1 | descendant_v page of 1,000 | 200 | 5.14 | 7.77 | 10.31 | 10.61 | 1000.00 |
| 1 | type_member_v page of 1,000 | 200 | 4.56 | 6.27 | 8.05 | 8.65 | 996.95 |
| 2 | resource_v ver <= P LIMIT 1 | 10000 | 1.55 | 1.94 | 3.32 | 12.96 | 1.00 |
| 2 | descendant_v page of 1,000 | 200 | 2.98 | 5.08 | 8.80 | 11.50 | 1000.00 |
| 2 | type_member_v page of 1,000 | 200 | 2.72 | 3.95 | 6.44 | 7.80 | 996.95 |


## ScyllaDB: reads, 32 in flight (after restart and page-cache drop), then a third pass (printed as pass 1)

#### reads at LOCAL_QUORUM, 32 in flight, key seed 2

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read |
|---|---|---:|---:|---:|---:|---:|---:|
| 1 | resource_v ver <= P LIMIT 1 | 10000 | 2.09 | 6.40 | 10.31 | 21.07 | 1.00 |
| 1 | descendant_v page of 1,000 | 200 | 63.35 | 144.19 | 170.11 | 179.44 | 1000.00 |
| 1 | type_member_v page of 1,000 | 200 | 76.21 | 144.85 | 164.97 | 173.72 | 994.89 |
| 2 | resource_v ver <= P LIMIT 1 | 10000 | 1.05 | 4.28 | 9.20 | 22.45 | 1.00 |
| 2 | descendant_v page of 1,000 | 200 | 31.99 | 49.38 | 55.60 | 57.10 | 1000.00 |
| 2 | type_member_v page of 1,000 | 200 | 39.86 | 68.55 | 91.16 | 92.56 | 994.89 |

#### reads at LOCAL_QUORUM, 32 in flight, key seed 2

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | resource_v ver <= P LIMIT 1 | 10000 | 1.28 | 4.46 | 8.29 | 29.42 | 1.00 | 18694 |
| 1 | descendant_v page of 1,000 | 200 | 35.65 | 73.07 | 77.30 | 80.10 | 1000.00 | 790 |
| 1 | type_member_v page of 1,000 | 200 | 33.84 | 47.42 | 62.87 | 64.00 | 994.89 | 935 |


## ScyllaDB: MVCC, memtables, re-insert, flush, compaction, purge

#### MVCC writes: 1000 keys x 1000 versions, 64 keys in flight

| table | GC every | inserts | range deletes | time s | batches/s | p50 ms | p99 ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | 1 | 1000000 | 999000 | 40.9 | 24475 | 2.16 | 9.75 |
| licetum_mvcc.rv_k10 | 10 | 1000000 | 99000 | 33.4 | 29969 | 1.75 | 7.96 |
`rv_k1: gc_grace_seconds=864000 compaction=map[class:LeveledCompactionStrategy] tombstone_gc = {mode: timeout, propagation_delay_in_seconds: 3600}`  

#### MVCC reads `ver <= P LIMIT 1` (memtable), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.59 | 1.98 | 3.63 | 6.27 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.60 | 2.04 | 3.54 | 9.23 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.58 | 1.96 | 3.51 | 7.91 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.57 | 1.96 | 2.67 | 7.24 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.57 | 2.00 | 3.12 | 5.15 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.63 | 1.96 | 3.15 | 5.64 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.60 | 1.91 | 2.53 | 6.66 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.63 | 1.93 | 3.00 | 10.72 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.59 | 1.89 | 3.39 | 33.17 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.60 | 1.88 | 2.90 | 36.63 | 0 | 0 |

##### traces (memtable)

- licetum_mvcc.rv_k1, newest P: server [185 525 110] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [531 157 524] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [175 481 94] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [399 86 409] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [76 436 97] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [652 128 462] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [119 501 135] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [532 121 365] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [120 482 204] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [511 116 586] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k1 with their original timestamps  
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k10 with their original timestamps  

#### MVCC reads `ver <= P LIMIT 1` (memtable, after a late re-insert), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.57 | 2.07 | 3.26 | 8.63 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.59 | 2.17 | 4.34 | 7.26 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.58 | 2.07 | 3.25 | 5.67 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.58 | 1.99 | 3.24 | 15.16 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.56 | 2.05 | 3.89 | 10.51 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.61 | 1.93 | 2.81 | 6.99 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.57 | 1.94 | 3.15 | 8.21 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.59 | 1.94 | 2.55 | 15.44 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.56 | 1.94 | 3.45 | 19.35 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.58 | 1.90 | 2.93 | 4.97 | 0 | 0 |

##### traces (memtable, after a late re-insert)

- licetum_mvcc.rv_k1, newest P: server [161 299 140] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [430 99 1466] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [105 710 127] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [572 91 469] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 2 clustering row(s) (0 live, 2 dead), 2 range tombstone(s) and 4 cell(s) (0 live, 4 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [94 360 125] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
- licetum_mvcc.rv_k10, newest P: server [752 150 478] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [126 384 159] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [412 82 470] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [121 496 157] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 2 clustering row(s) (0 live, 2 dead), 2 range tombstone(s) and 4 cell(s) (0 live, 4 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [528 148 423] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
`licetum_mvcc rv_k1 151888 2`  
`licetum_mvcc rv_k10 405228 2`  

#### MVCC reads `ver <= P LIMIT 1` (flushed), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.59 | 1.93 | 3.34 | 4.62 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.58 | 1.96 | 3.25 | 8.89 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.57 | 1.97 | 3.56 | 5.75 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.57 | 1.89 | 2.72 | 8.56 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.56 | 1.94 | 4.29 | 8.92 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.61 | 2.19 | 3.79 | 5.64 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.58 | 2.12 | 3.51 | 7.73 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.58 | 2.15 | 3.66 | 6.02 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.56 | 2.13 | 3.69 | 8.91 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.55 | 2.21 | 4.14 | 6.37 | 0 | 0 |

##### traces (flushed)

- licetum_mvcc.rv_k1, newest P: server [98 105 96] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [113 99 70] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [94 113 129] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [79 84 107] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 2 clustering row(s) (0 live, 2 dead), 2 range tombstone(s) and 4 cell(s) (0 live, 4 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [90 85 88] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
- licetum_mvcc.rv_k10, newest P: server [141 71 78] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [122 63 133] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [101 107 181] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [65 95 78] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 2 clustering row(s) (0 live, 2 dead), 2 range tombstone(s) and 4 cell(s) (0 live, 4 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [57 134 93] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
`licetum_mvcc rv_k1 124475 2`  
`licetum_mvcc rv_k10 372477 2`  

#### MVCC reads `ver <= P LIMIT 1` (major compaction, gc_grace_seconds 864000), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.55 | 1.92 | 3.29 | 8.98 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.55 | 1.96 | 4.36 | 7.07 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.55 | 1.97 | 3.83 | 7.08 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.54 | 1.91 | 3.47 | 11.02 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.53 | 1.88 | 3.07 | 6.19 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.58 | 1.99 | 4.07 | 8.93 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.56 | 1.90 | 3.59 | 21.84 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.57 | 1.94 | 3.30 | 9.44 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.55 | 1.92 | 3.45 | 8.58 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.55 | 1.88 | 2.69 | 7.36 | 0 | 0 |

##### traces (major compaction, gc_grace_seconds 864000)

- licetum_mvcc.rv_k1, newest P: server [133 435 114] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [539 85 440] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [92 370 87] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [382 88 328] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 2 clustering row(s) (0 live, 2 dead), 2 range tombstone(s) and 4 cell(s) (0 live, 4 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [171 420 113] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
- licetum_mvcc.rv_k10, newest P: server [845 115 604] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [103 504 146] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [347 110 406] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [85 435 112] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 2 clustering row(s) (0 live, 2 dead), 2 range tombstone(s) and 4 cell(s) (0 live, 4 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [426 147 666] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
`rv_k1: gc_grace_seconds=0 compaction=map[class:LeveledCompactionStrategy] tombstone_gc = {mode: timeout, propagation_delay_in_seconds: 3600}`  
`rv_k10: gc_grace_seconds=0 compaction=map[class:LeveledCompactionStrategy] tombstone_gc = {mode: timeout, propagation_delay_in_seconds: 3600}`  
`licetum_mvcc rv_k1 106323 2`  
`licetum_mvcc rv_k10 348183 2`  

#### MVCC reads `ver <= P LIMIT 1` (major compaction, gc_grace_seconds 0), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.62 | 1.97 | 3.14 | 5.29 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.59 | 1.94 | 3.12 | 16.86 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.89 | 2.32 | 4.71 | 10.17 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.57 | 1.90 | 2.68 | 6.00 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.55 | 1.88 | 2.97 | 4.02 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.67 | 2.06 | 2.75 | 8.01 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.59 | 1.99 | 2.90 | 6.54 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.92 | 2.36 | 3.34 | 21.93 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.57 | 1.91 | 2.96 | 5.51 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.57 | 1.89 | 2.38 | 4.21 | 0 | 0 |

##### traces (major compaction, gc_grace_seconds 0)

- licetum_mvcc.rv_k1, newest P: server [115 293 131] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [513 89 298] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [97 371 90] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [344 107 400] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 0 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [71 404 93] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 0 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [612 166 459] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [113 366 106] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [351 143 789] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [93 397 87] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 0 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [420 150 985] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 0 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k1 with their original timestamps  
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k10 with their original timestamps  

#### MVCC reads `ver <= P LIMIT 1` (late re-insert after the purge (memtable)), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.63 | 1.92 | 2.71 | 6.26 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.63 | 1.92 | 2.30 | 4.78 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.63 | 1.91 | 2.37 | 3.96 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.62 | 1.92 | 2.20 | 3.08 | 1000 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.61 | 1.88 | 2.61 | 5.99 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.66 | 1.94 | 2.42 | 4.39 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.63 | 1.90 | 2.15 | 4.57 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.64 | 1.92 | 2.20 | 4.16 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.63 | 1.91 | 2.15 | 8.17 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.63 | 1.92 | 2.51 | 8.74 | 1000 | 1000 |

##### traces (late re-insert after the purge (memtable))

- licetum_mvcc.rv_k1, newest P: server [129 387 132] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [465 103 356] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [152 439 109] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [362 95 388] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [124 592 92] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [541 116 675] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [129 403 135] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [379 119 331] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [154 402 120] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [332 106 426] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)

#### MVCC reads `ver <= P LIMIT 1` (late re-insert after the purge, compacted), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.63 | 1.92 | 2.42 | 8.06 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.61 | 1.96 | 2.34 | 4.71 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.61 | 1.91 | 2.14 | 3.67 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.61 | 1.94 | 2.86 | 18.82 | 1000 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.61 | 1.94 | 2.59 | 3.43 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.64 | 1.96 | 2.21 | 8.37 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.60 | 1.91 | 3.00 | 8.12 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.61 | 1.94 | 2.70 | 6.87 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.60 | 1.90 | 2.33 | 3.77 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.60 | 1.91 | 2.55 | 6.12 | 1000 | 1000 |

##### traces (late re-insert after the purge, compacted)

- licetum_mvcc.rv_k1, newest P: server [128 127 89] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [76 159 97] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [115 115 74] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [77 120 87] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [87 91 111] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [133 139 77] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [96 110 106] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [107 93 96] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [87 122 88] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [76 85 102] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 2 cell(s) (2 live, 0 dead)


## ScyllaDB: MVCC, sstables (flush every 100 versions, autocompaction off), BYPASS CACHE

#### MVCC writes: 1000 keys x 1000 versions, 64 keys in flight, flush every 100 versions

| table | GC every | inserts | range deletes | time s | batches/s | p50 ms | p99 ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | 1 | 1000000 | 999000 | 31.1 | 32172 | 1.68 | 4.25 |
| licetum_mvcc.rv_k10 | 10 | 1000000 | 99000 | 25.6 | 39008 | 1.30 | 3.67 |
`licetum_mvcc rv_k1 1262601 20`  
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k10 3795721 20`  
`licetum_mvcc rv_k10 0 0`  
`licetum_mvcc rv_k10 0 0`  

#### MVCC reads `ver <= P LIMIT 1` (sstables, flushed every 100 versions, BYPASS CACHE), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 3.32 | 4.22 | 5.67 | 24.30 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 3.22 | 4.03 | 5.36 | 14.71 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 3.20 | 3.95 | 4.50 | 7.43 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 3.17 | 3.96 | 4.85 | 11.57 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 3.16 | 3.93 | 5.28 | 49.44 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 3.09 | 3.88 | 5.51 | 63.81 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 3.00 | 3.60 | 4.25 | 15.57 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 2.96 | 3.68 | 5.32 | 10.87 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 2.92 | 3.63 | 4.85 | 14.73 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 2.84 | 3.43 | 4.07 | 8.85 | 0 | 0 |

##### traces (sstables, flushed every 100 versions, BYPASS CACHE)

- licetum_mvcc.rv_k1, newest P: server [3267 2005 1678] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [2842 1909 1499] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [3147 2091 1862] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [2995 2823 1507] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 10 clustering row(s) (0 live, 10 dead), 2 range tombstone(s) and 50 cell(s) (0 live, 50 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [3612 3015 1444] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [2828 1518 2273] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [2739 1756 1638] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [2782 1677 1247] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [2938 1680 1370] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 55 clustering row(s) (0 live, 55 dead), 2 range tombstone(s) and 275 cell(s) (0 live, 275 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [2010 1796 1333] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k1 with their original timestamps  
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k10 with their original timestamps  

#### MVCC reads `ver <= P LIMIT 1` (sstables, after a late re-insert, BYPASS CACHE), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 3.46 | 4.31 | 5.56 | 11.11 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 3.34 | 4.12 | 5.33 | 7.09 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 3.33 | 4.15 | 5.81 | 7.14 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 3.28 | 4.08 | 5.44 | 10.20 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 3.27 | 4.07 | 5.74 | 10.14 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 3.24 | 4.14 | 5.34 | 8.83 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 3.16 | 3.99 | 5.07 | 9.49 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 3.13 | 3.84 | 4.96 | 25.32 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 3.10 | 3.76 | 5.40 | 71.82 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 2.97 | 3.62 | 4.63 | 7.85 | 0 | 0 |

##### traces (sstables, after a late re-insert, BYPASS CACHE)

- licetum_mvcc.rv_k1, newest P: server [3509 2628 1852] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [2182 2517 2426] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [3054 2480 2409] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [3344 2461 2216] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 12 clustering row(s) (0 live, 12 dead), 2 range tombstone(s) and 54 cell(s) (0 live, 54 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [2971 2350 2642] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
- licetum_mvcc.rv_k10, newest P: server [2968 1559 2566] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [2079 2230 1424] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [2133 1855 1520] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [1726 1885 2374] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 57 clustering row(s) (0 live, 57 dead), 2 range tombstone(s) and 279 cell(s) (0 live, 279 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [2095 1717 1527] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (0 live, 1 dead), 2 range tombstone(s) and 2 cell(s) (0 live, 2 dead)
`licetum_mvcc rv_k1 125748 2`  
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k10 377981 2`  
`licetum_mvcc rv_k10 0 0`  
`licetum_mvcc rv_k10 0 0`  

#### MVCC reads `ver <= P LIMIT 1` (sstables, major compaction, gc_grace_seconds 864000, BYPASS CACHE), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 2.09 | 2.50 | 3.32 | 5.86 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 2.07 | 2.48 | 3.16 | 8.05 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 2.07 | 2.50 | 3.35 | 5.44 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 2.07 | 2.47 | 3.34 | 8.11 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 2.06 | 2.47 | 3.00 | 6.28 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 2.08 | 2.54 | 3.17 | 4.73 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 2.07 | 2.57 | 3.74 | 8.49 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 2.07 | 2.53 | 3.36 | 5.19 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 2.05 | 2.51 | 3.37 | 6.60 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 2.04 | 2.50 | 3.49 | 5.69 | 0 | 0 |

##### traces (sstables, major compaction, gc_grace_seconds 864000, BYPASS CACHE)

- licetum_mvcc.rv_k1, newest P: server [1226 418 712] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [417 851 685] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [689 539 831] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [451 971 774] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [833 395 495] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [542 870 796] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [754 436 601] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [509 780 816] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [933 522 481] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [413 855 966] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
`licetum_mvcc rv_k1 124638 2`  
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k10 377855 2`  
`licetum_mvcc rv_k10 0 0`  
`licetum_mvcc rv_k10 0 0`  

#### MVCC reads `ver <= P LIMIT 1` (sstables, major compaction, gc_grace_seconds 0, BYPASS CACHE), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 2.11 | 2.54 | 4.02 | 10.06 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 2.10 | 2.53 | 3.51 | 5.92 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 2.09 | 2.51 | 3.12 | 8.30 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 2.06 | 2.53 | 3.29 | 6.22 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 2.07 | 2.50 | 4.00 | 11.30 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 2.01 | 2.38 | 2.92 | 5.27 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.98 | 2.37 | 3.24 | 5.16 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.96 | 2.36 | 2.69 | 5.31 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.94 | 2.34 | 2.98 | 5.95 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.94 | 2.35 | 2.67 | 3.57 | 0 | 0 |

##### traces (sstables, major compaction, gc_grace_seconds 0, BYPASS CACHE)

- licetum_mvcc.rv_k1, newest P: server [1093 721 455] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [889 657 469] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [1051 669 451] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, middle): server [798 762 673] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k1, old P (collected, first): server [814 684 491] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, newest P: server [932 664 704] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [880 629 719] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [1173 584 866] us; Page stats: 1 partition(s), 0 static row(s) (0 live, 0 dead), 1 clustering row(s) (1 live, 0 dead), 0 range tombstone(s) and 5 cell(s) (5 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, middle): server [730 986 543] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)
- licetum_mvcc.rv_k10, old P (collected, first): server [618 768 673] us; Page stats: 0 partition(s), 0 static row(s) (0 live, 0 dead), 0 clustering row(s) (0 live, 0 dead), 2 range tombstone(s) and 0 cell(s) (0 live, 0 dead)


## Cassandra: first synchronization, 64 in flight

#### first synchronization into licetum_r_datacenter1_g1: 64 in flight, batches <= 50 rows, max rows per table 0

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 1000000 | 1000000 | 0 | 57.9 | 17275 | 2.23 | 9.45 | 32.02 | 318.40 |
| descendant_v | 3895940 | 1772211 | 0 | 88.3 | 44113 | 1.54 | 7.37 | 36.77 | 561.00 |
| type_member_v | 1000000 | 20036 | 0 | 8.0 | 124557 | 13.06 | 77.12 | 163.07 | 511.78 |
| attr_v | 5499702 | 1088273 | 0 | 70.7 | 77753 | 1.41 | 13.61 | 47.66 | 450.30 |
| log_chunk | 57 (51.5 MiB) | 57 | 0 | 0.5 | 105.6 MiB/s | 405.02 | 486.11 | 487.85 | 487.85 |
| region tables | 11395642 | 3880520 | | 225.0 | 50655 | | | | |


## Cassandra: sizes before flush, after flush, after major compaction


Before `nodetool flush`:  
`licetum_log log_chunk 0 0`  
`licetum_log log_entry 0 0`  
`licetum_r_datacenter1_g1 attr_v 246329680 4`  
`licetum_r_datacenter1_g1 descendant_v 364029322 3`  
`licetum_r_datacenter1_g1 resource_v 170797540 1`  
`licetum_r_datacenter1_g1 type_member_v 41408602 3`


After `nodetool flush`:  
`licetum_log log_chunk 54316915 1`  
`licetum_log log_entry 6088 1`  
`licetum_r_datacenter1_g1 attr_v 265878639 5`  
`licetum_r_datacenter1_g1 descendant_v 364029322 3`  
`licetum_r_datacenter1_g1 resource_v 207352500 3`  
`licetum_r_datacenter1_g1 type_member_v 58531595 5`


After a major compaction:  
`licetum_log log_chunk 54316932 1`  
`licetum_log log_entry 6105 1`  
`licetum_r_datacenter1_g1 attr_v 267756682 1`  
`licetum_r_datacenter1_g1 descendant_v 369109073 1`  
`licetum_r_datacenter1_g1 resource_v 171963827 1`  
`licetum_r_datacenter1_g1 type_member_v 47318417 1`


## Cassandra: concurrency sweep

#### first synchronization into licetum_r_datacenter1_g2: 16 in flight, batches <= 50 rows, max rows per table 300000

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 300000 | 300000 | 0 | 24.9 | 12036 | 0.83 | 2.71 | 7.91 | 170.52 |
| descendant_v | 350021 | 7250 | 0 | 5.4 | 64268 | 6.84 | 43.87 | 57.27 | 245.96 |
| type_member_v | 300020 | 6028 | 0 | 3.3 | 91580 | 4.34 | 39.70 | 52.40 | 293.99 |
| attr_v | 300913 | 9091 | 0 | 2.9 | 103971 | 2.32 | 10.42 | 38.55 | 187.24 |
| region tables | 1250954 | 322369 | | 36.5 | 34233 | | | | |

#### first synchronization into licetum_r_datacenter1_g3: 64 in flight, batches <= 50 rows, max rows per table 300000

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 300000 | 300000 | 0 | 11.2 | 26755 | 1.57 | 4.81 | 22.05 | 281.79 |
| descendant_v | 350021 | 7250 | 0 | 2.7 | 128652 | 14.23 | 67.23 | 122.02 | 264.92 |
| type_member_v | 300020 | 6028 | 0 | 2.1 | 142463 | 10.76 | 70.13 | 181.92 | 255.83 |
| attr_v | 300913 | 9091 | 0 | 1.9 | 159522 | 6.06 | 34.54 | 51.14 | 208.14 |
| region tables | 1250954 | 322369 | | 17.9 | 69785 | | | | |

#### first synchronization into licetum_r_datacenter1_g4: 256 in flight, batches <= 50 rows, max rows per table 300000

| table | rows | statements | retries | time s | rows/s | stmt p50 ms | stmt p95 ms | stmt p99 ms | stmt max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| resource_v | 300000 | 300000 | 0 | 7.6 | 39217 | 4.51 | 18.41 | 36.51 | 264.82 |
| descendant_v | 350021 | 7250 | 0 | 2.5 | 138316 | 64.75 | 244.63 | 394.85 | 488.54 |
| type_member_v | 300020 | 6028 | 0 | 1.6 | 186136 | 48.64 | 172.99 | 320.08 | 592.11 |
| attr_v | 300913 | 9091 | 0 | 1.9 | 157001 | 28.77 | 117.65 | 147.81 | 328.06 |
| region tables | 1250954 | 322369 | | 13.7 | 91252 | | | | |


## Cassandra: reads, 1 in flight (after restart and page-cache drop)

#### reads at LOCAL_QUORUM, 1 in flight, key seed 1

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | resource_v ver <= P LIMIT 1 | 10000 | 2.27 | 3.40 | 4.67 | 33.90 | 1.00 | 417 |
| 1 | descendant_v page of 1,000 | 200 | 5.31 | 7.47 | 11.61 | 25.91 | 1000.00 | 180 |
| 1 | type_member_v page of 1,000 | 200 | 4.71 | 6.76 | 16.27 | 20.41 | 996.95 | 202 |
| 2 | resource_v ver <= P LIMIT 1 | 10000 | 1.82 | 2.14 | 3.15 | 50.73 | 1.00 | 537 |
| 2 | descendant_v page of 1,000 | 200 | 3.93 | 5.27 | 5.79 | 5.92 | 1000.00 | 251 |
| 2 | type_member_v page of 1,000 | 200 | 3.97 | 5.25 | 7.04 | 8.13 | 996.95 | 247 |


## Cassandra: reads, 32 in flight (after restart and page-cache drop), then three more passes (printed as passes 1 to 3: they are passes 4 to 6)

#### reads at LOCAL_QUORUM, 32 in flight, key seed 2

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | resource_v ver <= P LIMIT 1 | 10000 | 11.65 | 63.28 | 72.28 | 163.28 | 1.00 | 1561 |
| 1 | descendant_v page of 1,000 | 200 | 482.92 | 794.52 | 1011.91 | 1020.20 | 1000.00 | 63 |
| 1 | type_member_v page of 1,000 | 200 | 383.72 | 711.43 | 805.78 | 881.63 | 994.89 | 79 |
| 2 | resource_v ver <= P LIMIT 1 | 10000 | 5.14 | 46.01 | 54.23 | 81.86 | 1.00 | 3346 |
| 2 | descendant_v page of 1,000 | 200 | 328.97 | 513.49 | 617.26 | 632.04 | 1000.00 | 91 |
| 2 | type_member_v page of 1,000 | 200 | 289.93 | 499.72 | 575.37 | 586.97 | 994.89 | 105 |
| 3 | resource_v ver <= P LIMIT 1 | 10000 | 4.98 | 44.26 | 51.33 | 82.95 | 1.00 | 3480 |
| 3 | descendant_v page of 1,000 | 200 | 182.78 | 315.42 | 390.54 | 400.16 | 1000.00 | 176 |
| 3 | type_member_v page of 1,000 | 200 | 144.31 | 291.59 | 376.11 | 377.53 | 994.89 | 205 |

#### reads at LOCAL_QUORUM, 32 in flight, key seed 2

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | resource_v ver <= P LIMIT 1 | 10000 | 4.29 | 37.50 | 47.79 | 72.97 | 1.00 | 4283 |
| 1 | descendant_v page of 1,000 | 200 | 48.22 | 89.25 | 104.54 | 106.10 | 1000.00 | 577 |
| 1 | type_member_v page of 1,000 | 200 | 32.29 | 83.82 | 90.13 | 105.76 | 994.89 | 788 |
| 2 | resource_v ver <= P LIMIT 1 | 10000 | 3.95 | 36.08 | 43.96 | 62.26 | 1.00 | 4767 |
| 2 | descendant_v page of 1,000 | 200 | 76.78 | 118.89 | 142.95 | 160.49 | 1000.00 | 420 |
| 2 | type_member_v page of 1,000 | 200 | 39.68 | 76.89 | 98.35 | 118.49 | 994.89 | 720 |
| 3 | resource_v ver <= P LIMIT 1 | 10000 | 3.60 | 34.14 | 42.44 | 54.25 | 1.00 | 5256 |
| 3 | descendant_v page of 1,000 | 200 | 35.33 | 84.58 | 97.89 | 104.76 | 1000.00 | 761 |
| 3 | type_member_v page of 1,000 | 200 | 42.81 | 96.86 | 134.04 | 135.61 | 994.89 | 607 |


## Cassandra: MVCC, memtables, re-insert, flush, compaction, purge

#### MVCC writes: 1000 keys x 1000 versions, 64 keys in flight, flush every 0 versions

| table | GC every | inserts | range deletes | time s | batches/s | p50 ms | p99 ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | 1 | 1000000 | 999000 | 31.9 | 31381 | 1.41 | 13.41 |
| licetum_mvcc.rv_k10 | 10 | 1000000 | 99000 | 23.6 | 42294 | 1.19 | 5.49 |
`rv_k1: gc_grace_seconds=864000 compaction=map[class:org.apache.cassandra.db.compaction.LeveledCompactionStrategy max_threshold:32 min_threshold:4] extensions:`  
`rv_k10: gc_grace_seconds=864000 compaction=map[class:org.apache.cassandra.db.compaction.LeveledCompactionStrategy max_threshold:32 min_threshold:4] extensions:`  

#### MVCC reads `ver <= P LIMIT 1` (memtable), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.77 | 2.10 | 3.91 | 9.89 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.75 | 2.08 | 3.45 | 6.59 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.75 | 2.15 | 3.63 | 7.10 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.86 | 2.25 | 4.84 | 7.86 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.81 | 2.21 | 4.05 | 8.40 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.77 | 2.09 | 2.92 | 7.17 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.76 | 2.06 | 2.84 | 6.03 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.75 | 2.07 | 2.75 | 4.44 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.83 | 2.20 | 3.40 | 5.39 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.80 | 2.13 | 3.24 | 6.37 | 0 | 0 |

##### traces (memtable)

- licetum_mvcc.rv_k1, newest P: server [5198 1037 661] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [1013 762 949] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [814 929 628] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [915 953 584] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [975 550 992] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [1662 922 788] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [813 1264 1107] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [949 1445 1621] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [946 2950 4339] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [1032 882 1889] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k1 with their original timestamps  
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k10 with their original timestamps  

#### MVCC reads `ver <= P LIMIT 1` (memtable, after a late re-insert), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.75 | 2.00 | 2.45 | 6.20 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.74 | 2.03 | 2.51 | 5.50 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.73 | 2.01 | 2.53 | 6.09 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.78 | 2.07 | 2.71 | 7.53 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.78 | 2.06 | 2.59 | 3.90 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.78 | 2.09 | 3.54 | 20.41 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.76 | 2.08 | 3.09 | 7.16 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.76 | 2.09 | 2.98 | 8.34 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.81 | 2.13 | 2.94 | 10.01 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.81 | 2.13 | 2.78 | 5.71 | 0 | 0 |

##### traces (memtable, after a late re-insert)

- licetum_mvcc.rv_k1, newest P: server [1593 802 851] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [627 738 632] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [782 692 803] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [721 1225 675] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [712 763 724] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [1512 726 1204] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [552 1263 935] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [769 592 869] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [815 542 854] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [906 796 1341] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
`licetum_mvcc rv_k1 297145 3`  
`licetum_mvcc rv_k10 282921 1`  

#### MVCC reads `ver <= P LIMIT 1` (flushed), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.77 | 2.08 | 3.29 | 8.63 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.76 | 2.04 | 2.82 | 7.87 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.76 | 2.01 | 2.47 | 5.35 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.74 | 2.02 | 2.74 | 8.40 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.75 | 2.01 | 2.61 | 7.73 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.73 | 2.01 | 2.60 | 38.65 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.74 | 2.00 | 2.62 | 4.28 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.73 | 2.00 | 2.23 | 6.40 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.71 | 1.97 | 2.30 | 4.64 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.71 | 1.96 | 2.35 | 4.30 | 0 | 0 |

##### traces (flushed)

- licetum_mvcc.rv_k1, newest P: server [1153 618 749] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [718 683 648] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [743 499 638] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [709 591 590] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [696 630 4195] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [1128 495 512] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [868 557 528] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [621 341 463] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [588 681 580] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [565 523 390] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
`licetum_mvcc rv_k1 100744 1`  
`licetum_mvcc rv_k10 282625 1`  

#### MVCC reads `ver <= P LIMIT 1` (major compaction, gc_grace_seconds 864000), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.79 | 2.08 | 2.83 | 5.70 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.79 | 2.08 | 3.31 | 7.18 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.78 | 2.07 | 2.66 | 5.57 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.77 | 2.04 | 2.33 | 3.52 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.77 | 2.06 | 2.70 | 5.23 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.76 | 2.06 | 3.05 | 5.82 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.75 | 2.08 | 3.12 | 4.65 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.74 | 2.10 | 3.14 | 5.96 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.73 | 2.04 | 2.80 | 7.01 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.73 | 2.05 | 2.64 | 5.42 | 0 | 0 |

##### traces (major compaction, gc_grace_seconds 864000)

- licetum_mvcc.rv_k1, newest P: server [1155 627 546] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [540 621 571] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [649 691 662] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [475 695 570] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [667 607 540] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [1835 1298 560] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [1221 991 542] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [817 558 573] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [758 1068 845] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [888 662 673] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
`rv_k1: gc_grace_seconds=0 compaction=map[class:org.apache.cassandra.db.compaction.LeveledCompactionStrategy max_threshold:32 min_threshold:4] extensions:`  
`rv_k10: gc_grace_seconds=0 compaction=map[class:org.apache.cassandra.db.compaction.LeveledCompactionStrategy max_threshold:32 min_threshold:4] extensions:`  
`licetum_mvcc rv_k1 85986 1`  
`licetum_mvcc rv_k10 265945 1`  

#### MVCC reads `ver <= P LIMIT 1` (major compaction, gc_grace_seconds 0), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.79 | 2.08 | 3.09 | 6.48 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.79 | 2.11 | 2.64 | 5.24 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.79 | 2.08 | 2.63 | 5.60 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.66 | 1.96 | 3.12 | 4.89 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.65 | 1.92 | 2.59 | 7.25 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.79 | 2.13 | 3.00 | 5.33 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.79 | 2.08 | 2.84 | 8.86 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.78 | 2.06 | 2.81 | 4.42 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.65 | 1.96 | 2.86 | 9.76 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.65 | 1.95 | 3.18 | 11.40 | 0 | 0 |

##### traces (major compaction, gc_grace_seconds 0)

- licetum_mvcc.rv_k1, newest P: server [953 639 531] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [627 646 453] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [542 550 482] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [403 511 382] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [415 470 1015] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [915 590 788] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [924 630 911] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [611 685 582] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [665 569 418] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [443 557 392] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k1 with their original timestamps  
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k10 with their original timestamps  

#### MVCC reads `ver <= P LIMIT 1` (late re-insert after the purge (memtable)), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.82 | 2.10 | 2.65 | 5.99 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.81 | 2.10 | 2.33 | 7.58 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.81 | 2.07 | 2.78 | 8.63 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.73 | 1.97 | 2.19 | 5.38 | 1000 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.73 | 1.98 | 2.43 | 5.67 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.90 | 2.19 | 2.88 | 52.24 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.90 | 2.16 | 2.60 | 12.91 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.91 | 2.21 | 2.67 | 15.78 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.81 | 2.08 | 2.77 | 4.66 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.81 | 2.09 | 2.76 | 25.47 | 1000 | 1000 |

##### traces (late re-insert after the purge (memtable))

- licetum_mvcc.rv_k1, newest P: server [900 489 932] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [669 411 698] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [584 452 507] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [483 835 374] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [518 696 630] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [1157 1473 731] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [793 734 796] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [780 778 739] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [573 491 621] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [642 639 656] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 0 sstables; Read 1 live rows and 0 tombstone cells

#### MVCC reads `ver <= P LIMIT 1` (late re-insert after the purge, compacted), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.79 | 2.08 | 2.68 | 24.11 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.79 | 2.07 | 2.48 | 32.99 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.79 | 2.08 | 2.58 | 6.15 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.80 | 2.08 | 2.69 | 23.06 | 1000 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.80 | 2.07 | 2.50 | 38.96 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.82 | 2.08 | 2.52 | 36.87 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.81 | 2.11 | 2.36 | 5.47 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.82 | 2.11 | 2.63 | 3.94 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.82 | 2.11 | 2.92 | 5.64 | 1000 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.81 | 2.09 | 2.38 | 3.65 | 1000 | 1000 |

##### traces (late re-insert after the purge, compacted)

- licetum_mvcc.rv_k1, newest P: server [959 650 568] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [429 539 482] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [512 650 717] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [637 567 699] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [704 694 648] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [762 541 478] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [636 670 581] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [607 522 578] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [656 462 523] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [486 423 776] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells


## Cassandra: MVCC, sstables (flush every 100 versions, autocompaction off)

#### MVCC writes: 1000 keys x 1000 versions, 64 keys in flight, flush every 100 versions

| table | GC every | inserts | range deletes | time s | batches/s | p50 ms | p99 ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | 1 | 1000000 | 999000 | 36.5 | 27435 | 1.35 | 6.36 |
| licetum_mvcc.rv_k10 | 10 | 1000000 | 99000 | 33.2 | 30142 | 1.18 | 5.07 |
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k1 1022821 10`  
`licetum_mvcc rv_k10 0 0`  
`licetum_mvcc rv_k10 2827092 10`  

#### MVCC reads `ver <= P LIMIT 1` (sstables, flushed every 100 versions), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.89 | 2.20 | 2.72 | 4.57 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.85 | 2.16 | 2.66 | 10.91 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.85 | 2.13 | 2.64 | 4.53 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 2.07 | 2.40 | 3.32 | 5.30 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.95 | 2.27 | 3.03 | 6.59 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.87 | 2.18 | 2.67 | 5.31 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.85 | 2.15 | 2.49 | 3.89 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.84 | 2.15 | 2.62 | 5.52 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 2.12 | 2.53 | 3.02 | 13.64 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.95 | 2.26 | 2.47 | 5.94 | 0 | 0 |

##### traces (sstables, flushed every 100 versions)

- licetum_mvcc.rv_k1, newest P: server [954 602 537] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [832 571 749] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [712 818 614] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [1212 906 751] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 10 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [813 1241 721] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 10 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [1428 680 724] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [1020 957 678] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [693 752 507] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [1327 907 982] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 10 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [1169 1007 891] us; Skipped 0/10 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 10 sstables; Read 0 live rows and 2 tombstone cells
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k1 with their original timestamps  
re-inserted versions 0 and 500 of 1000 keys into licetum_mvcc.rv_k10 with their original timestamps  

#### MVCC reads `ver <= P LIMIT 1` (sstables, after a late re-insert), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.90 | 2.23 | 2.75 | 6.88 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.89 | 2.21 | 2.92 | 5.00 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.88 | 2.21 | 2.49 | 4.93 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 2.05 | 2.41 | 2.64 | 3.15 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.99 | 2.36 | 2.88 | 5.62 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.91 | 2.18 | 2.72 | 6.16 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.90 | 2.16 | 2.93 | 7.26 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.89 | 2.17 | 2.50 | 6.61 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 2.15 | 2.49 | 3.28 | 8.00 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 2.02 | 2.30 | 2.75 | 5.74 | 0 | 0 |

##### traces (sstables, after a late re-insert)

- licetum_mvcc.rv_k1, newest P: server [1090 628 547] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [710 599 600] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [722 806 488] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [734 865 729] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 11 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [650 766 779] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 11 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [977 581 609] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [753 543 588] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [850 683 584] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [1041 1038 941] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 11 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [743 778 854] us; Skipped 0/11 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 11 sstables; Read 0 live rows and 2 tombstone cells
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k1 101868 1`  
`licetum_mvcc rv_k10 0 0`  
`licetum_mvcc rv_k10 281725 1`  

#### MVCC reads `ver <= P LIMIT 1` (sstables, major compaction, gc_grace_seconds 864000), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.81 | 2.10 | 3.02 | 6.31 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.80 | 2.13 | 3.22 | 5.75 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.79 | 2.09 | 2.94 | 7.73 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.78 | 2.06 | 2.56 | 4.41 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.79 | 2.05 | 2.93 | 5.51 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.78 | 2.07 | 2.42 | 3.87 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.78 | 2.06 | 2.56 | 6.30 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.78 | 2.09 | 2.55 | 6.32 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.75 | 2.06 | 3.06 | 5.81 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.76 | 2.05 | 2.62 | 3.88 | 0 | 0 |

##### traces (sstables, major compaction, gc_grace_seconds 864000)

- licetum_mvcc.rv_k1, newest P: server [809 537 582] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [646 761 956] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [750 582 533] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [619 720 374] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [535 740 547] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [944 570 656] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [615 511 768] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [673 566 615] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [638 611 522] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [552 574 538] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 0 live rows and 2 tombstone cells
`licetum_mvcc rv_k1 0 0`  
`licetum_mvcc rv_k1 85875 1`  
`licetum_mvcc rv_k10 0 0`  
`licetum_mvcc rv_k10 264556 1`  

#### MVCC reads `ver <= P LIMIT 1` (sstables, major compaction, gc_grace_seconds 0), sequential, 1000 keys

| table | P | n | p50 ms | p95 ms | p99 ms | max ms | wrong | visible |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| licetum_mvcc.rv_k1 | newest P | 1000 | 1.90 | 2.36 | 3.66 | 12.30 | 0 | 1000 |
| licetum_mvcc.rv_k1 | P = v1 (oldest retained) | 1000 | 1.91 | 2.33 | 3.25 | 6.14 | 0 | 1000 |
| licetum_mvcc.rv_k1 | v1 <= P <= newest | 1000 | 1.90 | 2.33 | 2.92 | 8.85 | 0 | 1000 |
| licetum_mvcc.rv_k1 | old P (collected, middle) | 1000 | 1.75 | 2.10 | 2.75 | 7.10 | 0 | 0 |
| licetum_mvcc.rv_k1 | old P (collected, first) | 1000 | 1.75 | 2.12 | 3.01 | 7.02 | 0 | 0 |
| licetum_mvcc.rv_k10 | newest P | 1000 | 1.82 | 2.10 | 2.94 | 6.39 | 0 | 1000 |
| licetum_mvcc.rv_k10 | P = v1 (oldest retained) | 1000 | 1.81 | 2.10 | 3.09 | 6.05 | 0 | 1000 |
| licetum_mvcc.rv_k10 | v1 <= P <= newest | 1000 | 1.80 | 2.09 | 2.57 | 7.17 | 0 | 1000 |
| licetum_mvcc.rv_k10 | old P (collected, middle) | 1000 | 1.68 | 1.91 | 2.47 | 4.28 | 0 | 0 |
| licetum_mvcc.rv_k10 | old P (collected, first) | 1000 | 1.66 | 1.93 | 2.59 | 13.48 | 0 | 0 |

##### traces (sstables, major compaction, gc_grace_seconds 0)

- licetum_mvcc.rv_k1, newest P: server [1026 639 539] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, P = v1 (oldest retained): server [710 641 541] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, v1 <= P <= newest: server [760 902 500] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, middle): server [571 467 639] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
- licetum_mvcc.rv_k1, old P (collected, first): server [432 402 597] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, newest P: server [875 987 523] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, P = v1 (oldest retained): server [742 510 387] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, v1 <= P <= newest: server [909 497 687] us; Skipped 0/1 non-slice-intersecting sstables, included 0 due to tombstones; Merged data from memtables and 1 sstables; Read 1 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, middle): server [808 633 363] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells
- licetum_mvcc.rv_k10, old P (collected, first): server [646 470 343] us; Skipped 1/1 non-slice-intersecting sstables, included 0 due to tombstones; Read 0 live rows and 0 tombstone cells


## PostgreSQL: bulk load

#### PostgreSQL 16.15 (Ubuntu 16.15-0ubuntu0.24.04.1) (shared_buffers 128MB): bulk load of the 1M dataset

| step | rows | time s |
|---|---:|---:|
| COPY resource_cache | 1000000 | 4.9 |
| COPY resource_ancestor | 3895940 | 27.7 |
| ALTER TABLE ONLY resource_cache ADD CONSTRAINT resource_cache_pkey PRIMARY KEY (id) | | 0.4 |
| ALTER TABLE ONLY resource_cache ADD CONSTRAINT uq_resource_cache_type_resource_external_id UNIQUE (type_resource_id, external_id) | | 0.8 |
| CREATE INDEX ix_resource_cache_meta ON resource_cache USING gin (meta jsonb_path_ops) | | 0.8 |
| CREATE INDEX ix_resource_cache_type_name ON resource_cache USING btree (type_resource_id, name) | | 0.8 |
| CREATE INDEX ix_resource_cache_type_resource_id ON resource_cache USING btree (type_resource_id) | | 0.3 |
| ALTER TABLE ONLY resource_ancestor ADD CONSTRAINT resource_ancestor_pkey PRIMARY KEY (ancestor_id, descendant_id) | | 1.7 |
| CREATE INDEX ix_resource_ancestor_descendant ON resource_ancestor USING btree (descendant_id, ancestor_id) | | 1.9 |
| ALTER TABLE ONLY resource_ancestor ADD CONSTRAINT resource_ancestor_ancestor_fkey FOREIGN KEY (ancestor_id) REFERENCES resource_cache(id) ON DELETE CASCADE | | 3.5 |
| ALTER TABLE ONLY resource_ancestor ADD CONSTRAINT resource_ancestor_descendant_fkey FOREIGN KEY (descendant_id) REFERENCES resource_cache(id) ON DELETE CASCADE | | 2.3 |
| VACUUM ANALYZE resource_cache | | 0.7 |
| VACUUM ANALYZE resource_ancestor | | 0.7 |

| relation | heap MB | indexes MB | total MB |
|---|---:|---:|---:|
| resource_cache | 219.9 | 178.4 | 398.4 |
| resource_ancestor | 606.6 | 387.3 | 994.1 |


## PostgreSQL: reads, 1 in flight (after a page-cache drop)

```
Nested Loop (actual time=0.638..0.775 rows=4 loops=1)
  Buffers: shared hit=2 read=11
  ->  Index Scan using uq_resource_cache_type_resource_external_id on resource_cache r (actual time=0.454..0.455 rows=1 loops=1)
        Index Cond: ((type_resource_id = '896c2a1f-579f-5e0a-bdc6-d29829918bee'::uuid) AND ((external_id)::text = '7f577940-c127-4a47-b940-c2a3018fcf4f'::text))
        Buffers: shared read=5
  ->  Index Scan using ix_resource_ancestor_descendant on resource_ancestor a (actual time=0.177..0.311 rows=4 loops=1)
        Index Cond: (descendant_id = r.id)
        Buffers: shared hit=2 read=6
Planning:
  Buffers: shared hit=363 read=12
Planning Time: 2.667 ms
Execution Time: 1.005 ms
```

```
Limit (actual time=0.344..31.893 rows=1000 loops=1)
  Buffers: shared hit=440 read=700
  ->  Index Scan using resource_ancestor_pkey on resource_ancestor (actual time=0.343..31.752 rows=1000 loops=1)
        Index Cond: (ancestor_id = 'df318d48-7ef5-54dd-8fe9-131fa26d94df'::uuid)
        Filter: (descendant_type_id = '896c2a1f-579f-5e0a-bdc6-d29829918bee'::uuid)
        Rows Removed by Filter: 130
        Buffers: shared hit=440 read=700
Planning:
  Buffers: shared hit=11
Planning Time: 0.193 ms
Execution Time: 31.999 ms
```

#### PostgreSQL reads, 1 in flight, key seed 1

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | resource row + closure (point decision) | 10000 | 0.47 | 0.82 | 1.20 | 50.27 | 3.90 | 1954 |
| 1 | closure page of 1,000 (division -> items) | 200 | 19.20 | 35.80 | 45.15 | 45.36 | 1000.00 | 48 |
| 2 | resource row + closure (point decision) | 10000 | 0.13 | 0.20 | 0.32 | 4.68 | 3.90 | 7002 |
| 2 | closure page of 1,000 (division -> items) | 200 | 17.74 | 22.37 | 28.14 | 28.30 | 1000.00 | 55 |


## PostgreSQL: reads, 32 in flight (after a page-cache drop)

```
Nested Loop (actual time=0.768..0.929 rows=4 loops=1)
  Buffers: shared hit=2 read=11
  ->  Index Scan using uq_resource_cache_type_resource_external_id on resource_cache r (actual time=0.464..0.465 rows=1 loops=1)
        Index Cond: ((type_resource_id = '896c2a1f-579f-5e0a-bdc6-d29829918bee'::uuid) AND ((external_id)::text = '3737833f-0d28-4ec8-8c6c-29d3ad92f3bb'::text))
        Buffers: shared read=5
  ->  Index Scan using ix_resource_ancestor_descendant on resource_ancestor a (actual time=0.250..0.406 rows=4 loops=1)
        Index Cond: (descendant_id = r.id)
        Buffers: shared hit=2 read=6
Planning:
  Buffers: shared hit=312 read=63
Planning Time: 7.044 ms
Execution Time: 1.356 ms
```

```
Limit (actual time=0.055..3.306 rows=1000 loops=1)
  Buffers: shared hit=1140
  ->  Index Scan using resource_ancestor_pkey on resource_ancestor (actual time=0.054..3.180 rows=1000 loops=1)
        Index Cond: (ancestor_id = 'df318d48-7ef5-54dd-8fe9-131fa26d94df'::uuid)
        Filter: (descendant_type_id = '896c2a1f-579f-5e0a-bdc6-d29829918bee'::uuid)
        Rows Removed by Filter: 130
        Buffers: shared hit=1140
Planning:
  Buffers: shared hit=11
Planning Time: 0.321 ms
Execution Time: 3.386 ms
```

#### PostgreSQL reads, 32 in flight, key seed 2

| pass | read | n | p50 ms | p95 ms | p99 ms | max ms | rows/read | reads/s |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | resource row + closure (point decision) | 10000 | 3.50 | 8.46 | 12.21 | 52.83 | 3.89 | 7945 |
| 1 | closure page of 1,000 (division -> items) | 200 | 54.22 | 283.18 | 343.34 | 348.23 | 1000.00 | 317 |
| 2 | resource row + closure (point decision) | 10000 | 0.54 | 5.37 | 8.58 | 15.38 | 3.89 | 22420 |
| 2 | closure page of 1,000 (division -> items) | 200 | 210.18 | 387.37 | 435.02 | 454.14 | 1000.00 | 134 |
| 3 | resource row + closure (point decision) | 10000 | 0.43 | 6.43 | 10.01 | 20.75 | 3.89 | 20744 |
| 3 | closure page of 1,000 (division -> items) | 200 | 211.00 | 343.17 | 414.80 | 428.93 | 1000.00 | 140 |

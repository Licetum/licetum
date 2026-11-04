# Load test: medium, cold mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 16 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.54 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.56 |
| warm-up permission (first synchronization) | 43.04 |
| warm-up check1 | 0.20 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 2 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 36 | 437.1 | 428.6 | 467.9 | 496.2 | – | – | 564.2 |
| check1 | 46 | 432.1 | 431.8 | 454.7 | 458.2 | – | – | 476.2 |
| check10 | 25 | 424.5 | 428.1 | 462.2 | 468.8 | – | – | 471.8 |
| authzen | 37 | 430.3 | 433.0 | 463.3 | 471.7 | – | – | 489.9 |
| authzen_batch | 17 | 435.2 | 432.9 | 459.8 | 475.6 | – | – | 487.4 |
| on_behalf | 20 | 433.1 | 421.7 | 445.8 | 467.3 | – | – | 672.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 181, answered 2xx 181, other 0 (error rate 0.00%), answered 2.0 requests/s
- dropped iterations 0; allowed share of the decisions 82.0% over 523 decisions
- load generator CPU 0% of 4 cores; server CPU 1.4 cores; permission cache hit –, check cache hit 0.2%
- per request: 16.33 database queries, 10.49 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 4 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 67 | 313.7 | 222.3 | 457.8 | 467.2 | – | – | 484.6 |
| check1 | 92 | 330.9 | 426.5 | 459.3 | 467.1 | – | – | 490.3 |
| check10 | 43 | 363.7 | 438.8 | 468.5 | 472.6 | – | – | 500.3 |
| authzen | 79 | 330.6 | 433.4 | 458.7 | 465.6 | – | – | 491.1 |
| authzen_batch | 47 | 296.8 | 214.1 | 452.6 | 461.3 | – | – | 465.2 |
| on_behalf | 33 | 306.0 | 224.4 | 454.5 | 462.4 | – | – | 474.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 361, answered 2xx 361, other 0 (error rate 0.00%), answered 4.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.3% over 1104 decisions
- load generator CPU 0% of 4 cores; server CPU 1.5 cores; permission cache hit –, check cache hit 0.2%
- per request: 16.03 database queries, 7.62 Redis round trips (counted by the server)
- oracle: 453 answers checked (346 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 8 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 137 | 324.7 | 307.6 | 506.0 | 539.3 | – | – | 568.2 |
| check1 | 197 | 299.9 | 285.7 | 500.8 | 532.9 | – | – | 632.1 |
| check10 | 86 | 308.2 | 277.0 | 496.4 | 509.7 | – | – | 545.6 |
| authzen | 148 | 317.3 | 349.1 | 492.4 | 505.7 | – | – | 547.2 |
| authzen_batch | 82 | 321.8 | 348.9 | 493.7 | 503.9 | – | – | 532.6 |
| on_behalf | 70 | 296.7 | 308.9 | 497.0 | 503.5 | – | – | 534.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 720, answered 2xx 720, other 0 (error rate 0.00%), answered 8.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 2095 decisions
- load generator CPU 0% of 4 cores; server CPU 1.8 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.71 database queries, 6.54 Redis round trips (counted by the server)
- oracle: 450 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 16 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 285 | 411.3 | 422.4 | 664.0 | 690.4 | – | – | 935.1 |
| check1 | 412 | 391.6 | 385.3 | 632.5 | 673.9 | – | – | 747.7 |
| check10 | 154 | 371.3 | 355.5 | 604.4 | 635.5 | – | – | 721.1 |
| authzen | 277 | 400.9 | 409.5 | 646.0 | 672.4 | – | – | 746.0 |
| authzen_batch | 174 | 384.9 | 380.1 | 634.3 | 665.6 | – | – | 871.8 |
| on_behalf | 139 | 418.5 | 421.4 | 650.5 | 680.6 | – | – | 719.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1441, answered 2xx 1441, other 0 (error rate 0.00%), answered 16.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.5% over 4108 decisions
- load generator CPU 0% of 4 cores; server CPU 2.1 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.40 database queries, 5.54 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 32 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 565 | 1304.6 | 1301.9 | 2028.7 | 2147.9 | – | – | 2615.8 |
| check1 | 795 | 1311.1 | 1295.0 | 2080.8 | 2197.3 | – | – | 2711.3 |
| check10 | 310 | 1303.5 | 1281.4 | 2024.2 | 2197.8 | – | – | 2739.0 |
| authzen | 574 | 1310.7 | 1274.6 | 2049.7 | 2170.2 | – | – | 2561.5 |
| authzen_batch | 317 | 1324.2 | 1298.6 | 2072.1 | 2245.8 | – | – | 2710.6 |
| on_behalf | 286 | 1347.2 | 1347.6 | 2136.7 | 2227.1 | – | – | 2696.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2847, answered 2xx 2847, other 0 (error rate 0.00%), answered 31.6 requests/s
- dropped iterations 33; allowed share of the decisions 79.5% over 7925 decisions
- load generator CPU 1% of 4 cores; server CPU 2.4 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.12 database queries, 4.57 Redis round trips (counted by the server)
- oracle: 265 answers checked (203 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 33 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 64 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 687 | 5339.6 | 5716.1 | 7458.6 | 7885.1 | – | – | 8896.3 |
| check1 | 1004 | 5842.9 | 6455.4 | 7894.5 | 8226.6 | – | – | 9044.9 |
| check10 | 358 | 5679.4 | 6135.2 | 7736.9 | 8216.0 | – | – | 8909.3 |
| authzen | 667 | 5770.7 | 6251.9 | 7786.5 | 8103.4 | – | – | 9050.9 |
| authzen_batch | 364 | 5880.5 | 6461.9 | 7818.8 | 8141.3 | – | – | 9085.8 |
| on_behalf | 326 | 5856.3 | 6196.7 | 8112.0 | 8342.9 | – | – | 9010.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3406, answered 2xx 3406, other 0 (error rate 0.00%), answered 37.8 requests/s
- dropped iterations 2354; allowed share of the decisions 79.1% over 9217 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 2.9%
- per request: 14.98 database queries, 4.05 Redis round trips (counted by the server)
- oracle: 35 answers checked (23 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 2354 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 38 answers/s, under 95% of the 64 asked

## Window at 128 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 681 | 6119.7 | 6846.1 | 8380.3 | 8689.7 | – | – | 9162.7 |
| check1 | 1003 | 6203.9 | 5835.1 | 8458.7 | 8845.8 | – | – | 9481.8 |
| check10 | 358 | 6192.3 | 5813.8 | 8491.8 | 8872.5 | – | – | 9254.6 |
| authzen | 658 | 6218.5 | 5800.9 | 8568.0 | 8868.1 | – | – | 9453.7 |
| authzen_batch | 360 | 6273.7 | 6879.0 | 8541.6 | 8745.4 | – | – | 9414.0 |
| on_behalf | 319 | 6333.4 | 6264.0 | 8616.8 | 8969.8 | – | – | 9507.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3379, answered 2xx 3379, other 0 (error rate 0.00%), answered 37.5 requests/s
- dropped iterations 8142; allowed share of the decisions 79.2% over 9160 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.00 database queries, 3.37 Redis round trips (counted by the server)
- oracle: 31 answers checked (20 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 8142 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 38 answers/s, under 95% of the 128 asked

## Window at 256 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 699 | 6313.7 | 6282.9 | 8785.6 | 10508.7 | – | – | 11530.5 |
| check1 | 1005 | 6490.2 | 6432.9 | 8910.9 | 10024.0 | – | – | 11837.4 |
| check10 | 359 | 6299.5 | 6314.7 | 8832.5 | 10000.1 | – | – | 11872.5 |
| authzen | 667 | 6435.9 | 6477.4 | 8831.8 | 9288.8 | – | – | 11726.5 |
| authzen_batch | 361 | 6502.7 | 6458.5 | 8929.2 | 10604.0 | – | – | 11735.2 |
| on_behalf | 328 | 6521.9 | 6449.8 | 8807.6 | 9178.2 | – | – | 11790.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3419, answered 2xx 3419, other 0 (error rate 0.00%), answered 38.0 requests/s
- dropped iterations 19622; allowed share of the decisions 79.1% over 9200 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.99 database queries, 3.53 Redis round trips (counted by the server)
- oracle: 34 answers checked (22 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 19622 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 38 answers/s, under 95% of the 256 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 1435042 | 17906 | 80.14 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 288070 | 17906 | 16.09 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 66419 | 800 | 83.02 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 40713 | 17906 | 2.27 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 36582 | 3142 | 11.64 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 35767 | 32670 | 1.09 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 25974 | 800 | 32.47 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 3341 | 800 | 4.18 | `SELECT id, name, $1::text AS parent_id FROM lt_folder` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31686656 | 19308544 |
| resource_edge | 95000 | 25878528 | 11321344 |
| resource_ancestor | 195000 | 41975808 | 22208512 |
| assignment_rule | 10000 | 2383872 | 819200 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

# Load test: medium, cold mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 32 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.54 |
| service and types | 0.02 |
| groups | 0.11 |
| rules | 0.48 |
| warm-up permission (first synchronization) | 26.11 |
| warm-up check1 | 0.19 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 2 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 36 | 408.1 | 414.3 | 441.2 | 445.4 | – | – | 453.7 |
| check1 | 46 | 404.9 | 406.8 | 428.9 | 440.3 | – | – | 451.6 |
| check10 | 25 | 409.0 | 410.7 | 436.9 | 440.7 | – | – | 441.5 |
| authzen | 37 | 404.9 | 408.3 | 437.6 | 443.6 | – | – | 453.1 |
| authzen_batch | 17 | 440.5 | 411.9 | 539.2 | 660.6 | – | – | 662.1 |
| on_behalf | 20 | 404.0 | 418.3 | 429.8 | 435.2 | – | – | 442.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 181, answered 2xx 181, other 0 (error rate 0.00%), answered 2.0 requests/s
- dropped iterations 0; allowed share of the decisions 82.0% over 523 decisions
- load generator CPU 0% of 4 cores; server CPU 1.3 cores; permission cache hit –, check cache hit 0.2%
- per request: 16.33 database queries, 10.47 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 4 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 67 | 333.5 | 415.2 | 446.6 | 452.3 | – | – | 476.6 |
| check1 | 92 | 302.8 | 379.0 | 440.6 | 444.4 | – | – | 475.5 |
| check10 | 43 | 320.0 | 410.2 | 435.2 | 443.2 | – | – | 449.4 |
| authzen | 79 | 281.3 | 193.7 | 434.2 | 440.4 | – | – | 461.6 |
| authzen_batch | 47 | 260.4 | 185.6 | 427.6 | 431.1 | – | – | 444.5 |
| on_behalf | 33 | 290.1 | 197.4 | 429.7 | 432.7 | – | – | 446.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 361, answered 2xx 361, other 0 (error rate 0.00%), answered 4.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.3% over 1104 decisions
- load generator CPU 0% of 4 cores; server CPU 1.4 cores; permission cache hit –, check cache hit 0.2%
- per request: 16.03 database queries, 7.62 Redis round trips (counted by the server)
- oracle: 453 answers checked (346 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 8 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 140 | 274.1 | 245.4 | 467.5 | 480.5 | – | – | 736.0 |
| check1 | 197 | 293.9 | 319.0 | 470.0 | 476.8 | – | – | 504.3 |
| check10 | 86 | 256.5 | 222.1 | 459.6 | 467.3 | – | – | 471.9 |
| authzen | 147 | 276.5 | 316.0 | 465.5 | 479.8 | – | – | 509.5 |
| authzen_batch | 81 | 243.3 | 229.2 | 448.3 | 468.1 | – | – | 483.4 |
| on_behalf | 70 | 286.3 | 274.7 | 475.7 | 486.3 | – | – | 689.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 721, answered 2xx 721, other 0 (error rate 0.00%), answered 8.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 2084 decisions
- load generator CPU 0% of 4 cores; server CPU 1.6 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.71 database queries, 6.56 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 16 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 288 | 344.7 | 336.7 | 572.6 | 589.5 | – | – | 662.3 |
| check1 | 410 | 341.9 | 344.7 | 551.9 | 576.9 | – | – | 626.5 |
| check10 | 154 | 331.5 | 328.6 | 540.3 | 568.3 | – | – | 593.0 |
| authzen | 277 | 337.0 | 344.7 | 562.8 | 589.0 | – | – | 646.9 |
| authzen_batch | 173 | 311.5 | 280.9 | 531.5 | 566.6 | – | – | 678.8 |
| on_behalf | 139 | 333.7 | 357.2 | 549.3 | 580.0 | – | – | 642.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1441, answered 2xx 1441, other 0 (error rate 0.00%), answered 16.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.4% over 4096 decisions
- load generator CPU 0% of 4 cores; server CPU 2.0 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.42 database queries, 5.65 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 32 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 565 | 806.5 | 796.6 | 1270.4 | 1346.8 | – | – | 1550.0 |
| check1 | 847 | 801.5 | 802.7 | 1264.6 | 1337.2 | – | – | 1611.0 |
| check10 | 307 | 809.0 | 824.5 | 1247.3 | 1316.0 | – | – | 1473.5 |
| authzen | 571 | 784.5 | 764.0 | 1260.6 | 1341.8 | – | – | 1529.8 |
| authzen_batch | 311 | 787.2 | 764.0 | 1258.5 | 1341.2 | – | – | 1525.5 |
| on_behalf | 280 | 824.1 | 832.7 | 1259.0 | 1333.9 | – | – | 1469.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2881, answered 2xx 2881, other 0 (error rate 0.00%), answered 32.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 7878 decisions
- load generator CPU 1% of 4 cores; server CPU 2.4 cores; permission cache hit –, check cache hit 0.5%
- per request: 15.18 database queries, 4.77 Redis round trips (counted by the server)
- oracle: 433 answers checked (334 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 64 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 763 | 4844.6 | 5021.9 | 6719.6 | 7042.8 | – | – | 8281.7 |
| check1 | 1093 | 5131.8 | 5425.7 | 7057.7 | 7380.3 | – | – | 8309.1 |
| check10 | 393 | 5245.7 | 5506.2 | 7201.8 | 7558.2 | – | – | 8549.7 |
| authzen | 726 | 5159.9 | 5458.4 | 7034.0 | 7406.0 | – | – | 8261.0 |
| authzen_batch | 386 | 5282.4 | 5651.8 | 7138.0 | 7523.4 | – | – | 8192.4 |
| on_behalf | 347 | 5306.3 | 5654.6 | 7310.1 | 7578.5 | – | – | 8542.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3708, answered 2xx 3708, other 0 (error rate 0.00%), answered 41.2 requests/s
- dropped iterations 2053; allowed share of the decisions 79.3% over 9956 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 2.7%
- per request: 14.98 database queries, 4.12 Redis round trips (counted by the server)
- oracle: 37 answers checked (25 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 2053 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 41 answers/s, under 95% of the 64 asked

## Window at 128 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 776 | 5508.7 | 5821.7 | 7628.9 | 8006.8 | – | – | 8961.3 |
| check1 | 1135 | 5715.9 | 5957.7 | 7775.9 | 8059.5 | – | – | 9097.3 |
| check10 | 409 | 5665.4 | 5965.1 | 7616.5 | 7954.2 | – | – | 9243.2 |
| authzen | 762 | 5590.1 | 5830.0 | 7646.2 | 7946.4 | – | – | 9247.5 |
| authzen_batch | 408 | 5686.1 | 6006.0 | 7798.2 | 8124.8 | – | – | 9078.5 |
| on_behalf | 365 | 5768.9 | 6014.7 | 7845.7 | 8072.4 | – | – | 9259.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3855, answered 2xx 3855, other 0 (error rate 0.00%), answered 42.8 requests/s
- dropped iterations 7666; allowed share of the decisions 79.3% over 10432 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.99 database queries, 3.42 Redis round trips (counted by the server)
- oracle: 34 answers checked (22 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 7666 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 43 answers/s, under 95% of the 128 asked

## Window at 256 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 782 | 5678.7 | 6038.2 | 7858.9 | 8197.9 | – | – | 9776.1 |
| check1 | 1133 | 5827.9 | 6133.2 | 7900.2 | 8271.4 | – | – | 9935.0 |
| check10 | 406 | 5893.9 | 6194.9 | 8053.7 | 8350.8 | – | – | 9901.6 |
| authzen | 748 | 5857.5 | 6165.5 | 7925.1 | 8278.9 | – | – | 9874.3 |
| authzen_batch | 405 | 5608.2 | 5663.8 | 7923.4 | 8307.7 | – | – | 9823.8 |
| on_behalf | 361 | 5844.6 | 6253.1 | 7949.3 | 8094.4 | – | – | 9861.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3835, answered 2xx 3835, other 0 (error rate 0.00%), answered 42.6 requests/s
- dropped iterations 19206; allowed share of the decisions 79.3% over 10352 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.99 database queries, 3.42 Redis round trips (counted by the server)
- oracle: 31 answers checked (20 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 19206 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 43 answers/s, under 95% of the 256 asked

## Window at 512 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 774 | 5824.7 | 5792.3 | 7988.7 | 8717.8 | – | – | 9176.1 |
| check1 | 1111 | 6013.2 | 5893.2 | 8214.0 | 8867.0 | – | – | 9760.7 |
| check10 | 403 | 5861.3 | 5834.1 | 8399.1 | 8892.4 | – | – | 9615.3 |
| authzen | 742 | 5830.5 | 5866.1 | 8166.7 | 8817.2 | – | – | 9855.9 |
| authzen_batch | 400 | 6011.9 | 5866.5 | 8168.8 | 8820.8 | – | – | 9230.6 |
| on_behalf | 356 | 6115.0 | 5911.0 | 8543.4 | 9110.1 | – | – | 10110.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3786, answered 2xx 3786, other 0 (error rate 0.00%), answered 42.1 requests/s
- dropped iterations 42294; allowed share of the decisions 79.3% over 10239 decisions
- load generator CPU 2% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.99 database queries, 3.65 Redis round trips (counted by the server)
- oracle: 35 answers checked (23 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 42294 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 42 answers/s, under 95% of the 512 asked

## Window at 1024 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 790 | 5897.7 | 5883.7 | 8575.7 | 8795.8 | – | – | 9839.4 |
| check1 | 1147 | 6052.4 | 5981.2 | 8745.9 | 8909.6 | – | – | 11141.1 |
| check10 | 423 | 6129.0 | 6034.4 | 8736.5 | 8869.1 | – | – | 9982.3 |
| authzen | 759 | 6066.1 | 5993.3 | 8729.3 | 8928.1 | – | – | 11106.9 |
| authzen_batch | 404 | 6032.0 | 5971.2 | 7645.3 | 8856.3 | – | – | 11076.7 |
| on_behalf | 366 | 5956.5 | 6009.5 | 7595.5 | 8842.7 | – | – | 10986.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3889, answered 2xx 3889, other 0 (error rate 0.00%), answered 43.2 requests/s
- dropped iterations 88272; allowed share of the decisions 79.5% over 10542 decisions
- load generator CPU 2% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.99 database queries, 3.95 Redis round trips (counted by the server)
- oracle: 39 answers checked (26 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 88272 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 43 answers/s, under 95% of the 1024 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 2070919 | 27037 | 76.60 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 412259 | 27037 | 15.25 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 69395 | 901 | 77.02 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 57070 | 27037 | 2.11 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 52743 | 49124 | 1.07 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 52739 | 4950 | 10.65 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 28732 | 901 | 31.89 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 3913 | 27037 | 0.14 | `SELECT id, group_id, member_group_id, predicate, enabled, valid_from, expires_at FROM group_member WHERE enabled AND (valid_from IS NULL OR valid_from <= now())…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32555008 | 20029440 |
| resource_edge | 95000 | 16752640 | 8683520 |
| resource_ancestor | 195000 | 42000384 | 22233088 |
| assignment_rule | 10000 | 2433024 | 868352 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

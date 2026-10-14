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
| groups | 0.11 |
| rules | 0.50 |
| warm-up permission (first synchronization) | 26.66 |
| warm-up check1 | 0.18 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 2 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 36 | 422.3 | 420.2 | 452.5 | 460.8 | – | – | 467.5 |
| check1 | 46 | 416.2 | 421.3 | 440.9 | 453.7 | – | – | 463.9 |
| check10 | 25 | 413.1 | 411.2 | 437.6 | 438.7 | – | – | 460.8 |
| authzen | 36 | 414.1 | 413.8 | 444.9 | 448.0 | – | – | 465.1 |
| authzen_batch | 17 | 424.0 | 433.4 | 443.6 | 447.0 | – | – | 447.9 |
| on_behalf | 20 | 416.2 | 412.3 | 443.5 | 447.1 | – | – | 453.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 180, answered 2xx 180, other 0 (error rate 0.00%), answered 2.0 requests/s
- dropped iterations 0; allowed share of the decisions 82.0% over 522 decisions
- load generator CPU 0% of 4 cores; server CPU 1.3 cores; permission cache hit –, check cache hit 0.2%
- per request: 16.33 database queries, 10.49 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 4 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 67 | 315.9 | 249.4 | 461.7 | 471.3 | – | – | 499.1 |
| check1 | 91 | 346.8 | 420.0 | 453.9 | 472.7 | – | – | 499.7 |
| check10 | 43 | 318.3 | 407.5 | 440.9 | 444.5 | – | – | 488.2 |
| authzen | 79 | 303.3 | 215.7 | 453.9 | 475.9 | – | – | 670.1 |
| authzen_batch | 47 | 281.2 | 194.8 | 462.1 | 471.0 | – | – | 508.6 |
| on_behalf | 33 | 327.8 | 410.0 | 478.8 | 481.9 | – | – | 498.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 360, answered 2xx 360, other 0 (error rate 0.00%), answered 4.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 1103 decisions
- load generator CPU 0% of 4 cores; server CPU 1.5 cores; permission cache hit –, check cache hit 0.2%
- per request: 16.03 database queries, 7.61 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 8 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 138 | 285.9 | 255.7 | 494.5 | 503.7 | – | – | 537.6 |
| check1 | 197 | 277.7 | 253.2 | 475.6 | 493.3 | – | – | 743.7 |
| check10 | 86 | 305.8 | 332.0 | 473.2 | 485.2 | – | – | 543.6 |
| authzen | 148 | 292.6 | 322.1 | 477.7 | 489.6 | – | – | 541.8 |
| authzen_batch | 81 | 305.2 | 329.2 | 481.5 | 505.7 | – | – | 539.6 |
| on_behalf | 70 | 309.9 | 335.3 | 477.7 | 500.2 | – | – | 541.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 720, answered 2xx 720, other 0 (error rate 0.00%), answered 8.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 2085 decisions
- load generator CPU 0% of 4 cores; server CPU 1.7 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.72 database queries, 6.56 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 16 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 286 | 365.0 | 374.5 | 585.3 | 612.6 | – | – | 688.5 |
| check1 | 407 | 360.2 | 369.3 | 575.3 | 610.9 | – | – | 745.3 |
| check10 | 153 | 340.8 | 317.2 | 575.9 | 622.4 | – | – | 663.4 |
| authzen | 279 | 336.2 | 329.6 | 563.4 | 595.1 | – | – | 642.5 |
| authzen_batch | 177 | 356.9 | 371.8 | 570.6 | 599.1 | – | – | 666.4 |
| on_behalf | 139 | 334.7 | 321.9 | 576.1 | 599.1 | – | – | 672.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1441, answered 2xx 1441, other 0 (error rate 0.00%), answered 16.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.4% over 4125 decisions
- load generator CPU 1% of 4 cores; server CPU 2.0 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.41 database queries, 5.61 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 32 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 561 | 890.5 | 880.6 | 1395.6 | 1491.4 | – | – | 1761.8 |
| check1 | 834 | 905.8 | 903.7 | 1398.8 | 1476.5 | – | – | 1876.8 |
| check10 | 306 | 887.7 | 887.3 | 1387.1 | 1472.9 | – | – | 1716.7 |
| authzen | 571 | 889.0 | 879.5 | 1420.4 | 1483.4 | – | – | 1767.3 |
| authzen_batch | 312 | 823.2 | 814.4 | 1332.0 | 1422.6 | – | – | 1621.2 |
| on_behalf | 291 | 894.1 | 906.2 | 1398.0 | 1458.4 | – | – | 1657.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2875, answered 2xx 2875, other 0 (error rate 0.00%), answered 31.9 requests/s
- dropped iterations 6; allowed share of the decisions 79.9% over 7876 decisions
- load generator CPU 1% of 4 cores; server CPU 2.4 cores; permission cache hit –, check cache hit 0.2%
- per request: 15.18 database queries, 4.71 Redis round trips (counted by the server)
- oracle: 403 answers checked (309 expected granted), 0 mismatches
- validity: **invalid**: 6 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 64 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 742 | 4861.8 | 4866.5 | 6975.3 | 7388.1 | – | – | 8344.2 |
| check1 | 1064 | 5163.0 | 5260.9 | 7309.8 | 7663.1 | – | – | 8466.5 |
| check10 | 386 | 5115.4 | 5308.2 | 7207.9 | 7534.6 | – | – | 8474.6 |
| authzen | 716 | 5146.1 | 5237.3 | 7279.2 | 7648.3 | – | – | 8413.0 |
| authzen_batch | 380 | 5124.3 | 5222.6 | 7192.2 | 7601.0 | – | – | 8358.8 |
| on_behalf | 345 | 5357.5 | 5358.3 | 7534.2 | 7895.8 | – | – | 8493.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3633, answered 2xx 3633, other 0 (error rate 0.00%), answered 40.4 requests/s
- dropped iterations 2127; allowed share of the decisions 79.2% over 9785 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 2.6%
- per request: 14.99 database queries, 4.12 Redis round trips (counted by the server)
- oracle: 39 answers checked (26 expected granted), 0 mismatches
- validity: **invalid**: 2127 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 40 answers/s, under 95% of the 64 asked

## Window at 128 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 758 | 5497.1 | 5497.2 | 7679.6 | 7923.6 | – | – | 9824.1 |
| check1 | 1112 | 5702.9 | 5905.8 | 7842.8 | 8071.6 | – | – | 9781.4 |
| check10 | 396 | 5679.7 | 5881.0 | 7838.1 | 8191.7 | – | – | 9784.9 |
| authzen | 738 | 5811.0 | 6125.4 | 7848.8 | 8140.4 | – | – | 9909.5 |
| authzen_batch | 394 | 5662.1 | 5842.8 | 7920.0 | 8149.1 | – | – | 9796.1 |
| on_behalf | 359 | 5878.1 | 6340.5 | 8036.1 | 8166.1 | – | – | 8908.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3757, answered 2xx 3757, other 0 (error rate 0.00%), answered 41.7 requests/s
- dropped iterations 7764; allowed share of the decisions 79.3% over 10109 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.00 database queries, 3.41 Redis round trips (counted by the server)
- oracle: 32 answers checked (21 expected granted), 0 mismatches
- validity: **invalid**: 7764 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 42 answers/s, under 95% of the 128 asked

## Window at 256 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 753 | 5629.4 | 5743.9 | 7479.6 | 8047.8 | – | – | 9927.6 |
| check1 | 1097 | 5924.3 | 5824.6 | 7804.1 | 8248.9 | – | – | 10335.7 |
| check10 | 403 | 5859.1 | 5765.1 | 7791.2 | 8245.6 | – | – | 10242.8 |
| authzen | 721 | 5886.2 | 5880.6 | 7816.2 | 8144.8 | – | – | 10013.3 |
| authzen_batch | 397 | 5998.7 | 5866.8 | 7915.9 | 8403.3 | – | – | 10363.6 |
| on_behalf | 360 | 5958.5 | 5867.8 | 8043.9 | 8299.0 | – | – | 9849.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3731, answered 2xx 3731, other 0 (error rate 0.00%), answered 41.5 requests/s
- dropped iterations 19310; allowed share of the decisions 79.4% over 10178 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.99 database queries, 3.40 Redis round trips (counted by the server)
- oracle: 37 answers checked (25 expected granted), 0 mismatches
- validity: **invalid**: 19310 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 41 answers/s, under 95% of the 256 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 1279050 | 18991 | 67.35 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 250155 | 18991 | 13.17 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 58535 | 841 | 69.60 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 36574 | 18991 | 1.93 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 34650 | 3323 | 10.43 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 31376 | 34659 | 0.91 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 24753 | 841 | 29.43 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 2834 | 38006 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32038912 | 19513344 |
| resource_edge | 95000 | 16482304 | 8413184 |
| resource_ancestor | 195000 | 41934848 | 22167552 |
| assignment_rule | 10000 | 2433024 | 868352 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

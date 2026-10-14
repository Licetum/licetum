# Load test: medium, warm mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 80 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.53 |
| service and types | 0.02 |
| groups | 0.10 |
| rules | 0.44 |
| warm-up permission (first synchronization) | 27.32 |
| warm-up check1 | 0.39 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 168 | 24.0 | 5.4 | 51.9 | 54.1 | – | – | 106.1 |
| check1 | 250 | 13.3 | 1.9 | 44.9 | 46.5 | – | – | 80.4 |
| check10 | 96 | 27.6 | 39.8 | 47.2 | 48.0 | – | – | 51.9 |
| authzen | 193 | 10.3 | 1.9 | 42.4 | 45.4 | – | – | 49.9 |
| authzen_batch | 99 | 30.5 | 41.1 | 49.0 | 49.4 | – | – | 100.9 |
| on_behalf | 95 | 12.6 | 2.5 | 41.9 | 45.0 | – | – | 53.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.1% over 2488 decisions
- load generator CPU 0% of 4 cores; server CPU 0.5 cores; permission cache hit 57.7%, check cache hit 40.5%
- per request: 10.11 database queries, 5.68 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 336 | 20.1 | 4.0 | 57.2 | 61.8 | – | – | 96.7 |
| check1 | 530 | 8.8 | 1.6 | 40.1 | 46.7 | – | – | 453.5 |
| check10 | 197 | 40.4 | 42.5 | 54.8 | 58.4 | – | – | 139.2 |
| authzen | 361 | 6.8 | 1.7 | 38.3 | 45.7 | – | – | 63.9 |
| authzen_batch | 198 | 40.9 | 42.8 | 55.6 | 59.3 | – | – | 259.3 |
| on_behalf | 179 | 9.4 | 2.1 | 40.3 | 48.3 | – | – | 81.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.4% over 5020 decisions
- load generator CPU 1% of 4 cores; server CPU 0.7 cores; permission cache hit 66.4%, check cache hit 52.0%
- per request: 8.49 database queries, 4.39 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 704 | 16.0 | 4.2 | 57.0 | 73.7 | – | – | 242.6 |
| check1 | 1086 | 6.1 | 1.6 | 5.2 | 46.7 | – | – | 128.0 |
| check10 | 364 | 45.3 | 46.9 | 72.2 | 85.7 | – | – | 251.1 |
| authzen | 742 | 4.4 | 1.6 | 3.9 | 10.3 | – | – | 112.4 |
| authzen_batch | 342 | 47.0 | 49.1 | 74.0 | 79.7 | – | – | 315.7 |
| on_behalf | 363 | 5.4 | 2.1 | 4.9 | 38.0 | – | – | 80.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3601, answered 2xx 3601, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.2% over 9251 decisions
- load generator CPU 1% of 4 cores; server CPU 0.9 cores; permission cache hit 81.1%, check cache hit 65.5%
- per request: 6.54 database queries, 4.03 Redis round trips (counted by the server)
- oracle: 450 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1452 | 16.5 | 6.4 | 46.4 | 84.4 | – | – | 301.8 |
| check1 | 2154 | 7.6 | 2.2 | 14.7 | 29.1 | – | – | 245.9 |
| check10 | 732 | 83.8 | 71.1 | 128.2 | 175.4 | – | – | 376.4 |
| authzen | 1451 | 7.4 | 2.3 | 13.1 | 24.5 | – | – | 180.7 |
| authzen_batch | 679 | 82.5 | 70.8 | 125.0 | 153.8 | – | – | 449.5 |
| on_behalf | 733 | 9.7 | 3.2 | 18.4 | 45.9 | – | – | 178.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 7201, answered 2xx 7201, other 0 (error rate 0.00%), answered 80.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.2% over 18448 decisions
- load generator CPU 2% of 4 cores; server CPU 1.5 cores; permission cache hit 90.4%, check cache hit 67.5%
- per request: 6.04 database queries, 3.86 Redis round trips (counted by the server)
- oracle: 450 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 2825 | 539.1 | 441.6 | 722.6 | 1752.2 | – | – | 3369.8 |
| check1 | 4337 | 459.3 | 407.6 | 620.9 | 750.1 | – | – | 3668.4 |
| check10 | 1433 | 2297.8 | 2379.5 | 3096.6 | 3269.5 | – | – | 4061.1 |
| authzen | 2896 | 463.0 | 411.8 | 629.4 | 763.7 | – | – | 3851.1 |
| authzen_batch | 1393 | 2321.2 | 2407.2 | 3103.3 | 3334.4 | – | – | 4119.8 |
| on_behalf | 1439 | 617.2 | 587.9 | 871.9 | 987.4 | – | – | 3799.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 14323, answered 2xx 14323, other 0 (error rate 0.00%), answered 159.1 requests/s
- dropped iterations 78; allowed share of the decisions 78.8% over 36932 decisions
- load generator CPU 3% of 4 cores; server CPU 2.3 cores; permission cache hit 92.2%, check cache hit 71.6%
- per request: 5.20 database queries, 3.68 Redis round trips (counted by the server)
- oracle: 48 answers checked (32 expected granted), 0 mismatches
- validity: **invalid**: 78 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 3334.1 ms above 100ms

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 2855 | 949.7 | 750.6 | 1114.2 | 3407.0 | – | – | 5105.9 |
| check1 | 4344 | 842.4 | 713.2 | 942.8 | 1819.6 | – | – | 5532.3 |
| check10 | 1457 | 4099.4 | 4070.9 | 4854.0 | 5262.7 | – | – | 5977.3 |
| authzen | 2918 | 838.3 | 714.5 | 937.3 | 1530.2 | – | – | 5662.7 |
| authzen_batch | 1426 | 4084.1 | 4048.7 | 4879.1 | 5206.5 | – | – | 6304.4 |
| on_behalf | 1450 | 1176.7 | 1049.0 | 1344.4 | 1845.6 | – | – | 5700.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 14450, answered 2xx 14450, other 0 (error rate 0.00%), answered 160.6 requests/s
- dropped iterations 14351; allowed share of the decisions 78.6% over 37542 decisions
- load generator CPU 3% of 4 cores; server CPU 2.3 cores; permission cache hit 90.0%, check cache hit 70.2%
- per request: 5.22 database queries, 3.67 Redis round trips (counted by the server)
- oracle: 25 answers checked (15 expected granted), 0 mismatches
- validity: **invalid**: 14351 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 5263.0 ms above 100ms; 161 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 2856 | 950.4 | 746.9 | 1075.4 | 3342.1 | – | – | 5638.2 |
| check1 | 4333 | 840.6 | 711.3 | 958.0 | 1559.6 | – | – | 5647.7 |
| check10 | 1474 | 4143.4 | 4080.4 | 4760.3 | 5325.5 | – | – | 6338.3 |
| authzen | 2915 | 878.7 | 714.3 | 1001.7 | 2325.1 | – | – | 5640.0 |
| authzen_batch | 1429 | 4138.3 | 4075.3 | 4875.3 | 5343.4 | – | – | 6202.5 |
| on_behalf | 1458 | 1191.8 | 1056.4 | 1378.0 | 2388.6 | – | – | 6101.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 14465, answered 2xx 14465, other 0 (error rate 0.00%), answered 160.7 requests/s
- dropped iterations 43136; allowed share of the decisions 78.6% over 37736 decisions
- load generator CPU 3% of 4 cores; server CPU 2.3 cores; permission cache hit 91.0%, check cache hit 69.6%
- per request: 5.23 database queries, 3.69 Redis round trips (counted by the server)
- oracle: 26 answers checked (15 expected granted), 0 mismatches
- validity: **invalid**: 43136 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 5379.9 ms above 100ms; 161 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 668024 | 14801 | 45.13 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 213294 | 1112 | 191.81 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 130912 | 14801 | 8.84 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 19051 | 28736 | 0.66 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 13661 | 14801 | 0.92 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 5730 | 73444 | 0.08 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 5436 | 866 | 6.28 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 4127 | 58643 | 0.07 | `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31858688 | 19480576 |
| resource_edge | 95000 | 25829376 | 11272192 |
| resource_ancestor | 195000 | 42115072 | 22347776 |
| assignment_rule | 10000 | 2441216 | 876544 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

# Load test: medium, warm mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 80 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.55 |
| service and types | 0.03 |
| groups | 0.11 |
| rules | 0.50 |
| warm-up permission (first synchronization) | 41.52 |
| warm-up check1 | 0.18 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 169 | 25.5 | 5.9 | 54.1 | 57.1 | – | – | 74.7 |
| check1 | 249 | 13.7 | 1.7 | 46.3 | 50.1 | – | – | 257.2 |
| check10 | 96 | 35.0 | 42.8 | 48.6 | 50.2 | – | – | 54.2 |
| authzen | 193 | 12.0 | 1.7 | 45.1 | 47.5 | – | – | 115.3 |
| authzen_batch | 99 | 34.4 | 41.9 | 50.4 | 53.2 | – | – | 84.7 |
| on_behalf | 95 | 12.5 | 2.2 | 43.9 | 50.0 | – | – | 52.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.1% over 2487 decisions
- load generator CPU 0% of 4 cores; server CPU 0.5 cores; permission cache hit 53.3%, check cache hit 38.3%
- per request: 10.45 database queries, 5.76 Redis round trips (counted by the server)
- oracle: 452 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 336 | 19.6 | 3.6 | 55.1 | 57.4 | – | – | 87.6 |
| check1 | 528 | 8.6 | 1.5 | 42.2 | 48.3 | – | – | 127.5 |
| check10 | 197 | 40.6 | 44.3 | 55.1 | 58.0 | – | – | 119.6 |
| authzen | 362 | 7.9 | 1.5 | 40.0 | 49.1 | – | – | 127.6 |
| authzen_batch | 198 | 39.1 | 44.5 | 53.4 | 56.3 | – | – | 145.6 |
| on_behalf | 180 | 7.7 | 2.0 | 37.5 | 44.7 | – | – | 58.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.4% over 5020 decisions
- load generator CPU 1% of 4 cores; server CPU 0.7 cores; permission cache hit 66.7%, check cache hit 51.8%
- per request: 8.52 database queries, 4.40 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 704 | 14.9 | 3.8 | 54.9 | 65.3 | – | – | 170.3 |
| check1 | 1087 | 5.5 | 1.4 | 4.9 | 44.8 | – | – | 108.3 |
| check10 | 364 | 44.1 | 46.5 | 70.0 | 81.3 | – | – | 144.5 |
| authzen | 741 | 4.1 | 1.4 | 3.4 | 9.1 | – | – | 127.6 |
| authzen_batch | 341 | 44.9 | 47.8 | 69.4 | 77.1 | – | – | 131.3 |
| on_behalf | 363 | 5.3 | 1.9 | 5.3 | 38.2 | – | – | 71.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3600, answered 2xx 3600, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.1% over 9241 decisions
- load generator CPU 1% of 4 cores; server CPU 0.8 cores; permission cache hit 80.5%, check cache hit 65.6%
- per request: 6.54 database queries, 4.03 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1450 | 16.4 | 5.7 | 45.8 | 77.9 | – | – | 506.0 |
| check1 | 2155 | 7.5 | 2.0 | 12.2 | 26.5 | – | – | 514.6 |
| check10 | 731 | 79.1 | 65.4 | 114.8 | 160.1 | – | – | 517.2 |
| authzen | 1449 | 6.9 | 2.0 | 11.3 | 27.9 | – | – | 274.4 |
| authzen_batch | 678 | 78.6 | 64.8 | 115.6 | 154.5 | – | – | 461.9 |
| on_behalf | 737 | 10.3 | 2.8 | 19.5 | 51.7 | – | – | 384.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 7200, answered 2xx 7200, other 0 (error rate 0.00%), answered 80.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.2% over 18431 decisions
- load generator CPU 2% of 4 cores; server CPU 1.4 cores; permission cache hit 90.4%, check cache hit 67.4%
- per request: 6.05 database queries, 3.86 Redis round trips (counted by the server)
- oracle: 448 answers checked (343 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 2824 | 105.9 | 66.0 | 217.3 | 340.8 | – | – | 1427.4 |
| check1 | 4328 | 73.5 | 47.6 | 153.0 | 212.6 | – | – | 1513.4 |
| check10 | 1467 | 455.5 | 390.8 | 808.1 | 998.3 | – | – | 2114.2 |
| authzen | 2895 | 73.8 | 47.9 | 150.9 | 216.2 | – | – | 1512.8 |
| authzen_batch | 1430 | 477.4 | 410.7 | 873.9 | 1063.4 | – | – | 1942.4 |
| on_behalf | 1456 | 88.1 | 56.8 | 185.7 | 262.3 | – | – | 1437.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 14400, answered 2xx 14400, other 0 (error rate 0.00%), answered 160.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.0% over 37649 decisions
- load generator CPU 3% of 4 cores; server CPU 2.2 cores; permission cache hit 94.2%, check cache hit 71.7%
- per request: 5.38 database queries, 3.75 Redis round trips (counted by the server)
- oracle: 232 answers checked (177 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: **fail**: p99 1093.0 ms above 100ms

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 3085 | 857.2 | 687.6 | 1012.3 | 2938.0 | – | – | 4932.7 |
| check1 | 4686 | 786.7 | 654.6 | 932.8 | 1891.3 | – | – | 5515.4 |
| check10 | 1578 | 3791.2 | 3713.7 | 4564.5 | 4994.8 | – | – | 5512.9 |
| authzen | 3144 | 783.0 | 652.9 | 928.2 | 1833.7 | – | – | 5168.6 |
| authzen_batch | 1553 | 3762.8 | 3698.0 | 4642.2 | 5031.9 | – | – | 6049.1 |
| on_behalf | 1563 | 1073.9 | 965.7 | 1306.1 | 1972.2 | – | – | 5565.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 15609, answered 2xx 15609, other 0 (error rate 0.00%), answered 173.4 requests/s
- dropped iterations 13191; allowed share of the decisions 78.7% over 40703 decisions
- load generator CPU 3% of 4 cores; server CPU 2.2 cores; permission cache hit 91.7%, check cache hit 70.1%
- per request: 5.21 database queries, 3.67 Redis round trips (counted by the server)
- oracle: 28 answers checked (17 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 13191 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 5032.5 ms above 100ms; 173 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 3087 | 865.6 | 689.3 | 985.2 | 2940.1 | – | – | 4773.0 |
| check1 | 4708 | 782.7 | 664.2 | 906.0 | 1577.9 | – | – | 5254.6 |
| check10 | 1591 | 3823.1 | 3745.7 | 4476.4 | 4824.2 | – | – | 5505.4 |
| authzen | 3160 | 794.4 | 660.2 | 916.1 | 1782.7 | – | – | 4927.4 |
| authzen_batch | 1560 | 3812.4 | 3741.5 | 4466.3 | 4829.1 | – | – | 5525.9 |
| on_behalf | 1582 | 1109.1 | 977.6 | 1261.6 | 2166.6 | – | – | 5220.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 15688, answered 2xx 15688, other 0 (error rate 0.00%), answered 174.3 requests/s
- dropped iterations 41914; allowed share of the decisions 78.7% over 40960 decisions
- load generator CPU 3% of 4 cores; server CPU 2.2 cores; permission cache hit 91.3%, check cache hit 69.8%
- per request: 5.22 database queries, 3.69 Redis round trips (counted by the server)
- oracle: 27 answers checked (16 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 41914 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 4838.6 ms above 100ms; 174 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 724689 | 15609 | 46.43 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 169383 | 997 | 169.89 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 146271 | 15609 | 9.37 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 22952 | 30337 | 0.76 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 20076 | 15609 | 1.29 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 6410 | 76897 | 0.08 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 5360 | 881 | 6.08 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 4890 | 61288 | 0.08 | `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32038912 | 19660800 |
| resource_edge | 95000 | 25845760 | 11288576 |
| resource_ancestor | 195000 | 42008576 | 22241280 |
| assignment_rule | 10000 | 2424832 | 860160 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

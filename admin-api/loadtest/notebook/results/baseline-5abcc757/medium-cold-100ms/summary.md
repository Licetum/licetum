# Load test: medium, cold mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 8 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.53 |
| service and types | 0.02 |
| groups | 0.10 |
| rules | 0.48 |
| warm-up permission (first synchronization) | 22.46 |
| warm-up check1 | 0.21 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 2 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 36 | 2261.7 | 2203.9 | 2780.1 | 2964.4 | – | – | 3423.1 |
| check1 | 47 | 2268.6 | 2277.2 | 2970.5 | 3137.2 | – | – | 3294.8 |
| check10 | 25 | 2428.3 | 2474.2 | 3026.5 | 3096.4 | – | – | 3145.6 |
| authzen | 35 | 2427.4 | 2578.8 | 2974.0 | 3198.0 | – | – | 3487.3 |
| authzen_batch | 18 | 2255.8 | 2226.6 | 2889.0 | 2962.1 | – | – | 2968.8 |
| on_behalf | 20 | 2577.9 | 2616.3 | 3014.9 | 3214.9 | – | – | 3476.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 181, answered 2xx 181, other 0 (error rate 0.00%), answered 2.0 requests/s
- dropped iterations 0; allowed share of the decisions 81.6% over 532 decisions
- load generator CPU 0% of 4 cores; server CPU 0.4 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.75 database queries, 7.61 Redis round trips (counted by the server)
- oracle: 48 answers checked (32 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 4 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 67 | 2225.3 | 2149.9 | 2722.7 | 2992.3 | – | – | 3113.5 |
| check1 | 91 | 2251.6 | 2284.1 | 2887.7 | 2996.9 | – | – | 3220.3 |
| check10 | 43 | 2334.0 | 2320.2 | 2883.9 | 2913.9 | – | – | 3126.9 |
| authzen | 79 | 2315.4 | 2302.0 | 2989.8 | 3088.7 | – | – | 3311.2 |
| authzen_batch | 47 | 2263.9 | 2232.8 | 2893.0 | 2970.8 | – | – | 3121.5 |
| on_behalf | 33 | 2266.2 | 2158.7 | 2999.0 | 3074.4 | – | – | 3174.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 360, answered 2xx 360, other 0 (error rate 0.00%), answered 4.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.1% over 1103 decisions
- load generator CPU 0% of 4 cores; server CPU 0.6 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.42 database queries, 6.05 Redis round trips (counted by the server)
- oracle: 53 answers checked (36 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 8 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 139 | 2175.1 | 2238.9 | 2808.3 | 2940.9 | – | – | 3076.8 |
| check1 | 198 | 2286.3 | 2249.0 | 2908.9 | 3053.6 | – | – | 3110.3 |
| check10 | 85 | 2192.9 | 2120.1 | 2825.5 | 2926.6 | – | – | 3251.0 |
| authzen | 149 | 2275.2 | 2264.7 | 2889.8 | 2984.4 | – | – | 3283.6 |
| authzen_batch | 80 | 2282.8 | 2353.7 | 2838.5 | 2925.8 | – | – | 3113.2 |
| on_behalf | 70 | 2313.4 | 2253.2 | 2911.7 | 2981.4 | – | – | 3094.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 721, answered 2xx 721, other 0 (error rate 0.00%), answered 8.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.1% over 2067 decisions
- load generator CPU 0% of 4 cores; server CPU 0.8 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.23 database queries, 5.19 Redis round trips (counted by the server)
- oracle: 54 answers checked (37 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 16 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 291 | 2557.9 | 2501.0 | 3287.3 | 3427.1 | – | – | 3846.6 |
| check1 | 389 | 2833.3 | 2858.4 | 3541.9 | 3720.5 | – | – | 4154.5 |
| check10 | 153 | 2848.6 | 2875.5 | 3631.0 | 3745.7 | – | – | 3957.8 |
| authzen | 283 | 2798.0 | 2810.3 | 3497.8 | 3703.2 | – | – | 4076.9 |
| authzen_batch | 170 | 2777.8 | 2706.0 | 3475.2 | 3588.7 | – | – | 3991.5 |
| on_behalf | 143 | 2805.1 | 2761.7 | 3534.8 | 3618.9 | – | – | 3986.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1429, answered 2xx 1429, other 0 (error rate 0.00%), answered 15.9 requests/s
- dropped iterations 12; allowed share of the decisions 79.9% over 4045 decisions
- load generator CPU 0% of 4 cores; server CPU 1.2 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.08 database queries, 4.77 Redis round trips (counted by the server)
- oracle: 45 answers checked (31 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 12 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 32 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 425 | 23657.1 | 25802.4 | 36228.6 | 37054.2 | – | – | 38131.4 |
| check1 | 606 | 25641.0 | 29379.9 | 36785.2 | 37229.8 | – | – | 38107.6 |
| check10 | 232 | 24043.7 | 26698.4 | 36605.1 | 36896.6 | – | – | 38049.9 |
| authzen | 419 | 26034.5 | 30287.0 | 36961.7 | 37477.8 | – | – | 38009.3 |
| authzen_batch | 230 | 26182.8 | 29960.8 | 36775.1 | 37130.2 | – | – | 38056.7 |
| on_behalf | 201 | 26995.9 | 30309.3 | 37165.3 | 37670.7 | – | – | 40294.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2113, answered 2xx 1610, other 503 (error rate 23.81%), answered 17.9 requests/s
- dropped iterations 768; allowed share of the decisions 79.3% over 4333 decisions
- load generator CPU 1% of 4 cores; server CPU 1.2 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.40 database queries, 4.49 Redis round trips (counted by the server)
- oracle: 20 answers checked (13 expected granted), 0 wrong answers, 9 unanswered
- validity: **invalid**: 768 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 23.81% above 1.00%; 18 answers/s, under 95% of the 32 asked

## Window at 64 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 500 | 35455.7 | 39353.8 | 44779.6 | 45670.3 | – | – | 47017.0 |
| check1 | 700 | 36783.8 | 39654.8 | 44683.3 | 45698.9 | – | – | 46970.5 |
| check10 | 266 | 35755.1 | 39361.2 | 44798.5 | 45778.7 | – | – | 46899.4 |
| authzen | 444 | 36862.5 | 39611.3 | 44937.4 | 45910.3 | – | – | 46954.7 |
| authzen_batch | 237 | 37043.2 | 39756.3 | 45367.6 | 46029.4 | – | – | 46986.0 |
| on_behalf | 230 | 37835.0 | 39731.1 | 45010.1 | 45751.1 | – | – | 46765.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2377, answered 2xx 495, other 1882 (error rate 79.18%), answered 5.5 requests/s
- dropped iterations 3286; allowed share of the decisions 78.3% over 1298 decisions
- load generator CPU 1% of 4 cores; server CPU 1.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 11.60 database queries, 2.89 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 3286 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 79.18% above 1.00%; 6 answers/s, under 95% of the 64 asked

## Window at 128 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 495 | 37720.9 | 38993.4 | 44629.3 | 47768.6 | – | – | 50617.2 |
| check1 | 717 | 38458.0 | 39136.2 | 44933.9 | 47777.1 | – | – | 50788.9 |
| check10 | 263 | 38223.5 | 39187.6 | 46128.7 | 48054.3 | – | – | 50562.3 |
| authzen | 450 | 39020.4 | 39628.6 | 46617.9 | 47933.3 | – | – | 50810.0 |
| authzen_batch | 235 | 38800.2 | 39658.0 | 45290.3 | 48799.4 | – | – | 50686.7 |
| on_behalf | 244 | 39202.7 | 39164.0 | 45181.6 | 49177.7 | – | – | 50381.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2404, answered 2xx 486, other 1918 (error rate 79.78%), answered 5.4 requests/s
- dropped iterations 9117; allowed share of the decisions 78.7% over 1159 decisions
- load generator CPU 1% of 4 cores; server CPU 1.2 cores; permission cache hit –, check cache hit 0.0%
- per request: 11.13 database queries, 2.75 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 9117 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 79.78% above 1.00%; 5 answers/s, under 95% of the 128 asked

## Window at 256 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 407 | 38817.5 | 39405.4 | 44996.0 | 46219.9 | – | – | 50916.5 |
| check1 | 587 | 39504.7 | 39999.0 | 45335.4 | 46245.6 | – | – | 51264.5 |
| check10 | 226 | 39677.7 | 40984.5 | 45646.1 | 46370.0 | – | – | 50689.6 |
| authzen | 373 | 39901.4 | 40500.1 | 45802.4 | 46459.1 | – | – | 51249.2 |
| authzen_batch | 203 | 39210.0 | 39646.2 | 45281.1 | 46074.1 | – | – | 51119.4 |
| on_behalf | 204 | 39380.2 | 39162.1 | 45316.7 | 46156.3 | – | – | 51068.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2000, answered 2xx 19, other 1981 (error rate 99.05%), answered 0.2 requests/s
- dropped iterations 20177; allowed share of the decisions – over 0 decisions
- load generator CPU 1% of 4 cores; server CPU 1.1 cores; permission cache hit –, check cache hit 0.0%
- per request: 9.96 database queries, 3.24 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 20177 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 99.05% above 1.00%; 0 answers/s, under 95% of the 256 asked

## Window at 512 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 605 | 38443.4 | 37214.7 | 43888.8 | 44180.8 | – | – | 45577.9 |
| check1 | 905 | 38272.6 | 37116.3 | 43769.6 | 44112.1 | – | – | 45670.7 |
| check10 | 330 | 38050.4 | 36843.5 | 42833.0 | 44021.2 | – | – | 45401.8 |
| authzen | 534 | 38410.3 | 37322.5 | 43842.8 | 44106.8 | – | – | 45635.4 |
| authzen_batch | 291 | 38234.6 | 37204.8 | 43753.9 | 43987.1 | – | – | 45559.8 |
| on_behalf | 312 | 38262.8 | 37139.4 | 43808.2 | 44056.4 | – | – | 45570.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2977, answered 2xx 0, other 2977 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 43103; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.7 cores; permission cache hit –, check cache hit 0.0%
- per request: 9.48 database queries, 2.61 Redis round trips (counted by the server)
- oracle: 16 answers checked (11 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 43103 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 512 asked

## Window at 1024 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 612 | 37871.8 | 36849.9 | 42994.8 | 43374.1 | – | – | 43958.9 |
| check1 | 911 | 38030.6 | 36863.0 | 42954.9 | 43321.9 | – | – | 43924.6 |
| check10 | 330 | 38006.9 | 36880.5 | 42902.9 | 43194.7 | – | – | 43882.0 |
| authzen | 537 | 37831.2 | 36809.5 | 42891.4 | 43306.5 | – | – | 43933.9 |
| authzen_batch | 297 | 38149.5 | 36904.7 | 43004.8 | 43369.5 | – | – | 43800.9 |
| on_behalf | 313 | 37996.0 | 36737.2 | 42929.0 | 43254.5 | – | – | 43877.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3000, answered 2xx 0, other 3000 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 89160; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.5 cores; permission cache hit –, check cache hit 0.0%
- per request: 9.43 database queries, 2.47 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 89160 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 1024 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 726862 | 14057 | 51.71 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 101395 | 18727 | 5.41 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 17341 | 281 | 61.71 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 5852 | 281 | 20.83 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 5150 | 11735 | 0.44 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 3564 | 1170 | 3.05 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 3082 | 6870 | 0.45 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2607 | 37509 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32604160 | 20078592 |
| resource_edge | 95000 | 16695296 | 8626176 |
| resource_ancestor | 195000 | 42082304 | 22315008 |
| assignment_rule | 10000 | 2441216 | 876544 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

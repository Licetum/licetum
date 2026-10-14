# Load test: medium, cold mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 8 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.56 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.56 |
| warm-up permission (first synchronization) | 43.97 |
| warm-up check1 | 0.21 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 2 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 36 | 2389.4 | 2582.6 | 2994.5 | 3023.7 | – | – | 3474.5 |
| check1 | 46 | 2354.2 | 2332.3 | 2937.4 | 2993.1 | – | – | 3237.6 |
| check10 | 25 | 2324.1 | 2277.2 | 3034.0 | 3323.9 | – | – | 3494.3 |
| authzen | 36 | 2296.7 | 2286.9 | 2922.2 | 2983.6 | – | – | 3612.1 |
| authzen_batch | 18 | 2213.6 | 2169.0 | 2896.2 | 2980.6 | – | – | 2982.9 |
| on_behalf | 20 | 2530.7 | 2561.1 | 2924.4 | 2959.9 | – | – | 3199.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 181, answered 2xx 181, other 0 (error rate 0.00%), answered 2.0 requests/s
- dropped iterations 0; allowed share of the decisions 81.8% over 532 decisions
- load generator CPU 0% of 4 cores; server CPU 0.5 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.75 database queries, 7.61 Redis round trips (counted by the server)
- oracle: 47 answers checked (32 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 4 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 67 | 2162.3 | 2152.6 | 2846.5 | 2948.7 | – | – | 3112.7 |
| check1 | 94 | 2213.3 | 2171.6 | 2886.7 | 3002.5 | – | – | 3124.1 |
| check10 | 43 | 2253.5 | 2265.4 | 2897.4 | 3011.4 | – | – | 3118.8 |
| authzen | 79 | 2343.0 | 2326.7 | 2990.4 | 3035.6 | – | – | 3315.6 |
| authzen_batch | 45 | 2403.2 | 2430.7 | 2980.5 | 3101.5 | – | – | 3151.8 |
| on_behalf | 33 | 2418.7 | 2458.1 | 2901.2 | 2928.1 | – | – | 3126.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 361, answered 2xx 361, other 0 (error rate 0.00%), answered 4.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.4% over 1086 decisions
- load generator CPU 0% of 4 cores; server CPU 0.6 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.42 database queries, 6.02 Redis round trips (counted by the server)
- oracle: 53 answers checked (36 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 8 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 139 | 2207.1 | 2226.9 | 2829.0 | 2909.9 | – | – | 3471.4 |
| check1 | 200 | 2304.2 | 2272.0 | 2887.4 | 3027.4 | – | – | 6531.4 |
| check10 | 84 | 2319.6 | 2334.8 | 2959.6 | 3148.2 | – | – | 3210.6 |
| authzen | 146 | 2276.0 | 2239.9 | 2968.2 | 3051.4 | – | – | 3136.9 |
| authzen_batch | 82 | 2257.0 | 2262.0 | 2911.4 | 2999.8 | – | – | 3136.3 |
| on_behalf | 70 | 2365.2 | 2364.1 | 2924.9 | 3105.9 | – | – | 3297.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 721, answered 2xx 721, other 0 (error rate 0.00%), answered 8.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 2076 decisions
- load generator CPU 0% of 4 cores; server CPU 0.9 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.23 database queries, 5.16 Redis round trips (counted by the server)
- oracle: 54 answers checked (37 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 16 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 286 | 2711.0 | 2709.7 | 3399.0 | 3624.4 | – | – | 4349.1 |
| check1 | 398 | 2985.4 | 2980.7 | 3740.1 | 3928.7 | – | – | 4399.6 |
| check10 | 155 | 2907.1 | 2838.7 | 3717.0 | 3897.3 | – | – | 4305.6 |
| authzen | 280 | 2939.1 | 2928.3 | 3682.4 | 3872.9 | – | – | 4580.3 |
| authzen_batch | 165 | 2863.5 | 2773.6 | 3669.5 | 4072.3 | – | – | 4336.4 |
| on_behalf | 143 | 2879.7 | 2884.9 | 3674.0 | 3719.9 | – | – | 4104.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1427, answered 2xx 1427, other 0 (error rate 0.00%), answered 15.9 requests/s
- dropped iterations 14; allowed share of the decisions 79.6% over 4021 decisions
- load generator CPU 1% of 4 cores; server CPU 1.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.10 database queries, 4.73 Redis round trips (counted by the server)
- oracle: 43 answers checked (30 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 14 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 32 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 420 | 24511.9 | 26764.6 | 36642.9 | 37554.4 | – | – | 38526.6 |
| check1 | 553 | 26012.8 | 29693.6 | 37454.9 | 37714.2 | – | – | 38503.7 |
| check10 | 220 | 24731.8 | 26740.4 | 37312.7 | 37708.0 | – | – | 38266.1 |
| authzen | 366 | 25994.6 | 30779.8 | 37436.3 | 37800.9 | – | – | 38493.3 |
| authzen_batch | 208 | 26676.2 | 30724.9 | 37492.0 | 37737.3 | – | – | 38471.1 |
| on_behalf | 177 | 27048.3 | 31581.9 | 37507.4 | 38090.8 | – | – | 40720.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1944, answered 2xx 1367, other 577 (error rate 29.68%), answered 15.2 requests/s
- dropped iterations 781; allowed share of the decisions 78.7% over 3587 decisions
- load generator CPU 1% of 4 cores; server CPU 1.2 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.25 database queries, 4.43 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 0 wrong answers, 10 unanswered
- validity: **invalid**: 781 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 29.68% above 1.00%; 15 answers/s, under 95% of the 32 asked

## Window at 64 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 414 | 34432.5 | 39021.6 | 44454.3 | 45296.8 | – | – | 47505.0 |
| check1 | 604 | 35813.0 | 39273.3 | 45054.7 | 46023.4 | – | – | 47525.1 |
| check10 | 235 | 34735.4 | 37381.4 | 44193.8 | 45376.0 | – | – | 47209.7 |
| authzen | 375 | 35626.8 | 38848.3 | 44341.3 | 45111.3 | – | – | 47167.6 |
| authzen_batch | 205 | 35950.8 | 39333.5 | 44510.7 | 45949.0 | – | – | 47183.9 |
| on_behalf | 208 | 37362.2 | 39402.6 | 44912.2 | 45769.6 | – | – | 47016.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2041, answered 2xx 412, other 1629 (error rate 79.81%), answered 4.6 requests/s
- dropped iterations 3226; allowed share of the decisions 78.1% over 1082 decisions
- load generator CPU 1% of 4 cores; server CPU 1.4 cores; permission cache hit –, check cache hit 0.0%
- per request: 11.76 database queries, 2.91 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 3226 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 79.81% above 1.00%; 5 answers/s, under 95% of the 64 asked

## Window at 128 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 474 | 38671.2 | 39947.5 | 47948.0 | 50785.4 | – | – | 54577.9 |
| check1 | 706 | 39513.1 | 39984.8 | 48267.0 | 51617.7 | – | – | 55086.2 |
| check10 | 255 | 38994.4 | 39798.2 | 48106.5 | 51007.5 | – | – | 54348.5 |
| authzen | 432 | 39782.0 | 40372.2 | 48076.9 | 50754.5 | – | – | 54482.7 |
| authzen_batch | 234 | 39799.6 | 40028.8 | 49124.9 | 52113.5 | – | – | 54780.9 |
| on_behalf | 232 | 40209.0 | 39979.0 | 48257.8 | 51816.1 | – | – | 54676.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2333, answered 2xx 314, other 2019 (error rate 86.54%), answered 3.5 requests/s
- dropped iterations 9188; allowed share of the decisions 80.5% over 789 decisions
- load generator CPU 1% of 4 cores; server CPU 1.1 cores; permission cache hit –, check cache hit 0.0%
- per request: 10.67 database queries, 2.65 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 9188 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 86.54% above 1.00%; 3 answers/s, under 95% of the 128 asked

## Window at 256 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 407 | 39268.4 | 38330.6 | 48177.8 | 49323.2 | – | – | 50215.2 |
| check1 | 587 | 39497.1 | 38417.5 | 48112.5 | 49251.7 | – | – | 50224.8 |
| check10 | 226 | 39229.4 | 37997.4 | 48443.6 | 49427.9 | – | – | 50097.2 |
| authzen | 373 | 39964.1 | 38828.0 | 48707.3 | 49545.6 | – | – | 50307.1 |
| authzen_batch | 204 | 39656.1 | 38573.0 | 48253.3 | 48851.9 | – | – | 50137.2 |
| on_behalf | 204 | 39983.2 | 38516.2 | 48398.1 | 49163.2 | – | – | 50183.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2001, answered 2xx 0, other 2001 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 20058; allowed share of the decisions – over 0 decisions
- load generator CPU 1% of 4 cores; server CPU 0.9 cores; permission cache hit –, check cache hit 0.0%
- per request: 9.76 database queries, 3.21 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 20058 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 256 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 796786 | 11354 | 70.18 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 89017 | 12842 | 6.93 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 20692 | 265 | 78.08 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 7456 | 265 | 28.13 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 6296 | 11355 | 0.55 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 4534 | 1108 | 4.09 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 4017 | 6702 | 0.60 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2059 | 25720 | 0.08 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31956992 | 19578880 |
| resource_edge | 95000 | 25837568 | 11280384 |
| resource_ancestor | 195000 | 41943040 | 22175744 |
| assignment_rule | 10000 | 2424832 | 860160 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

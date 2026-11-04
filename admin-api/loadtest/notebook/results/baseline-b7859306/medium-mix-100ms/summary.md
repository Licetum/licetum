# Load test: medium, mix mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 10 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.55 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.56 |
| warm-up permission (first synchronization) | 43.83 |
| warm-up check1 | 0.21 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.01 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 174 | 957.6 | 512.3 | 1789.6 | 1878.3 | – | – | 2048.8 |
| check1 | 251 | 1096.8 | 510.3 | 1894.1 | 1974.2 | – | – | 2156.5 |
| check10 | 106 | 1189.6 | 1665.6 | 1888.9 | 1941.3 | – | – | 2177.9 |
| authzen | 180 | 1087.6 | 561.6 | 1884.4 | 1917.5 | – | – | 2272.5 |
| authzen_batch | 106 | 1083.9 | 510.8 | 1888.3 | 1953.6 | – | – | 2044.3 |
| on_behalf | 84 | 1179.9 | 713.5 | 1873.8 | 1917.4 | – | – | 2054.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 2635 decisions
- load generator CPU 0% of 4 cores; server CPU 0.3 cores; permission cache hit 62.6%, check cache hit 50.5%
- per request: 8.75 database queries, 5.10 Redis round trips (counted by the server)
- oracle: 49 answers checked (33 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 338 | 3381.7 | 3667.7 | 6000.7 | 6193.9 | – | – | 6716.7 |
| check1 | 495 | 4908.1 | 4933.2 | 6624.2 | 6762.9 | – | – | 7463.5 |
| check10 | 187 | 4793.7 | 4812.0 | 6563.2 | 6757.8 | – | – | 7563.9 |
| authzen | 340 | 4814.1 | 4852.7 | 6581.9 | 6710.5 | – | – | 7324.6 |
| authzen_batch | 190 | 4875.7 | 4959.3 | 6609.1 | 6739.7 | – | – | 7068.5 |
| on_behalf | 173 | 5141.4 | 5238.6 | 7088.9 | 7263.9 | – | – | 7902.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1723, answered 2xx 1723, other 0 (error rate 0.00%), answered 19.1 requests/s
- dropped iterations 78; allowed share of the decisions 79.3% over 4778 decisions
- load generator CPU 1% of 4 cores; server CPU 1.1 cores; permission cache hit 31.4%, check cache hit 1.3%
- per request: 15.07 database queries, 6.20 Redis round trips (counted by the server)
- oracle: 22 answers checked (14 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 78 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 500 | 16748.1 | 10160.7 | 38320.7 | 38722.2 | – | – | 39416.1 |
| check1 | 685 | 23785.1 | 30083.7 | 38399.0 | 38649.1 | – | – | 39425.2 |
| check10 | 260 | 23863.1 | 28598.0 | 38483.9 | 38871.4 | – | – | 39442.4 |
| authzen | 449 | 24116.3 | 29255.8 | 38452.0 | 38893.1 | – | – | 39442.2 |
| authzen_batch | 258 | 25061.8 | 30577.1 | 38519.4 | 38858.0 | – | – | 39396.2 |
| on_behalf | 222 | 25693.6 | 34937.6 | 38589.3 | 38686.3 | – | – | 39223.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2374, answered 2xx 1395, other 979 (error rate 41.24%), answered 15.5 requests/s
- dropped iterations 943; allowed share of the decisions 79.7% over 3701 decisions
- load generator CPU 2% of 4 cores; server CPU 1.1 cores; permission cache hit 35.1%, check cache hit 9.9%
- per request: 12.18 database queries, 5.67 Redis round trips (counted by the server)
- oracle: 15 answers checked (11 expected granted), 0 wrong answers, 10 unanswered
- validity: **invalid**: 943 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 41.24% above 1.00%; 16 answers/s, under 95% of the 40 asked

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 525 | 25983.9 | 31103.1 | 40144.2 | 40423.7 | – | – | 52688.8 |
| check1 | 721 | 35608.2 | 39250.6 | 40434.6 | 40863.1 | – | – | 44452.5 |
| check10 | 273 | 35840.6 | 39209.9 | 40416.0 | 40898.3 | – | – | 44777.0 |
| authzen | 454 | 36201.8 | 39238.7 | 40382.9 | 40873.7 | – | – | 44502.6 |
| authzen_batch | 249 | 36372.5 | 39356.3 | 40558.2 | 40895.6 | – | – | 44927.1 |
| on_behalf | 238 | 36964.4 | 39250.2 | 40277.2 | 40450.3 | – | – | 44291.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2460, answered 2xx 446, other 2014 (error rate 81.87%), answered 5.0 requests/s
- dropped iterations 4418; allowed share of the decisions 79.7% over 836 decisions
- load generator CPU 2% of 4 cores; server CPU 1.3 cores; permission cache hit 21.6%, check cache hit 1.2%
- per request: 11.13 database queries, 5.08 Redis round trips (counted by the server)
- oracle: 17 answers checked (12 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 4418 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 81.87% above 1.00%; 5 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 519 | 29525.9 | 34849.0 | 42121.3 | 43157.6 | – | – | 44258.7 |
| check1 | 693 | 38297.4 | 38711.3 | 42751.2 | 43380.6 | – | – | 48248.2 |
| check10 | 266 | 38189.1 | 38678.7 | 42795.9 | 43309.0 | – | – | 48060.5 |
| authzen | 437 | 38320.0 | 38632.3 | 42240.0 | 43325.9 | – | – | 47961.2 |
| authzen_batch | 237 | 38500.0 | 38732.2 | 42440.6 | 43117.0 | – | – | 47268.0 |
| on_behalf | 228 | 38649.0 | 38718.0 | 42198.0 | 43125.4 | – | – | 44262.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2380, answered 2xx 252, other 2128 (error rate 89.41%), answered 2.8 requests/s
- dropped iterations 11342; allowed share of the decisions 77.5% over 267 decisions
- load generator CPU 2% of 4 cores; server CPU 1.2 cores; permission cache hit 14.8%, check cache hit 0.0%
- per request: 9.71 database queries, 3.66 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 13 unanswered
- validity: **invalid**: 11342 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 89.41% above 1.00%; 3 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 597 | 36970.5 | 38271.9 | 43162.2 | 43640.5 | – | – | 46810.3 |
| check1 | 868 | 38575.1 | 39072.4 | 43516.2 | 43729.1 | – | – | 46836.2 |
| check10 | 314 | 38263.7 | 39100.1 | 43567.8 | 43686.4 | – | – | 44519.7 |
| authzen | 526 | 38669.4 | 38902.4 | 43606.1 | 43747.0 | – | – | 46757.5 |
| authzen_batch | 291 | 38690.9 | 39031.0 | 43534.2 | 43662.2 | – | – | 46764.0 |
| on_behalf | 304 | 38832.2 | 39183.7 | 43529.4 | 43653.8 | – | – | 43825.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2900, answered 2xx 0, other 2900 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 25800; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.9 cores; permission cache hit 0.0%, check cache hit 0.0%
- per request: 9.33 database queries, 3.78 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 25800 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 612 | 36667.1 | 37556.1 | 40042.8 | 40412.5 | – | – | 43803.1 |
| check1 | 909 | 37854.9 | 38802.7 | 40056.2 | 40336.2 | – | – | 41915.6 |
| check10 | 329 | 37701.6 | 38805.5 | 40099.2 | 40321.5 | – | – | 43803.2 |
| authzen | 539 | 37839.3 | 38677.5 | 40046.6 | 40162.8 | – | – | 41799.6 |
| authzen_batch | 298 | 37864.2 | 38839.4 | 40096.6 | 40358.4 | – | – | 41083.3 |
| on_behalf | 313 | 37975.6 | 38812.1 | 39988.9 | 40074.8 | – | – | 40520.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3000, answered 2xx 0, other 3000 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 54600; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 0.0%, check cache hit 0.0%
- per request: 9.04 database queries, 3.31 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 54600 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 687357 | 12325 | 55.77 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 116323 | 15145 | 7.68 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 39200 | 1052 | 37.26 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 4640 | 8058 | 0.58 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 3107 | 4620 | 0.67 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2490 | 565 | 4.41 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 2317 | 32388 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 1973 | 15145 | 0.13 | `SELECT id, group_id, member_group_id, predicate, enabled, valid_from, expires_at FROM group_member WHERE enabled AND (valid_from IS NULL OR valid_from <= now())…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31735808 | 19357696 |
| resource_edge | 95000 | 25886720 | 11329536 |
| resource_ancestor | 195000 | 41852928 | 22085632 |
| assignment_rule | 10000 | 2416640 | 851968 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

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
| groups | 0.11 |
| rules | 0.52 |
| warm-up permission (first synchronization) | 41.22 |
| warm-up check1 | 0.19 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.01 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 179 | 1031.6 | 512.1 | 1773.3 | 1786.2 | – | – | 2027.5 |
| check1 | 251 | 1203.1 | 1659.0 | 1873.4 | 1897.3 | – | – | 2109.3 |
| check10 | 105 | 1214.5 | 1658.3 | 1874.5 | 1880.4 | – | – | 1990.1 |
| authzen | 177 | 1137.1 | 1652.1 | 1868.5 | 1877.1 | – | – | 1975.4 |
| authzen_batch | 104 | 1223.8 | 1658.4 | 1872.1 | 1883.4 | – | – | 2085.2 |
| on_behalf | 85 | 1356.5 | 1762.4 | 1869.5 | 1871.3 | – | – | 1962.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.9% over 2603 decisions
- load generator CPU 0% of 4 cores; server CPU 0.3 cores; permission cache hit 55.3%, check cache hit 41.4%
- per request: 9.96 database queries, 5.24 Redis round trips (counted by the server)
- oracle: 49 answers checked (33 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 335 | 3096.7 | 3408.9 | 5353.9 | 5600.1 | – | – | 6027.2 |
| check1 | 501 | 4445.6 | 4539.4 | 6004.4 | 6167.6 | – | – | 6769.2 |
| check10 | 188 | 4406.8 | 4508.8 | 5866.0 | 6050.1 | – | – | 6276.7 |
| authzen | 345 | 4455.9 | 4513.0 | 5942.7 | 6036.4 | – | – | 7677.0 |
| authzen_batch | 195 | 4466.8 | 4577.4 | 5927.7 | 6089.8 | – | – | 6443.2 |
| on_behalf | 172 | 4668.6 | 4864.5 | 6402.9 | 6524.3 | – | – | 6842.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1736, answered 2xx 1736, other 0 (error rate 0.00%), answered 19.3 requests/s
- dropped iterations 65; allowed share of the decisions 79.4% over 4848 decisions
- load generator CPU 1% of 4 cores; server CPU 0.9 cores; permission cache hit 32.2%, check cache hit 1.9%
- per request: 15.05 database queries, 6.27 Redis round trips (counted by the server)
- oracle: 24 answers checked (15 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 65 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 501 | 17119.1 | 13409.3 | 37889.3 | 38408.6 | – | – | 39191.9 |
| check1 | 680 | 24323.9 | 31722.3 | 38286.1 | 38560.3 | – | – | 39361.2 |
| check10 | 266 | 24777.9 | 30789.1 | 38470.0 | 38843.0 | – | – | 39370.5 |
| authzen | 447 | 24760.6 | 30616.1 | 38275.8 | 38771.1 | – | – | 39417.0 |
| authzen_batch | 257 | 26128.0 | 33802.9 | 38342.7 | 38641.0 | – | – | 39161.6 |
| on_behalf | 221 | 26595.0 | 34936.2 | 38295.9 | 38656.1 | – | – | 39187.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2372, answered 2xx 1372, other 1000 (error rate 42.16%), answered 15.2 requests/s
- dropped iterations 953; allowed share of the decisions 79.6% over 3611 decisions
- load generator CPU 1% of 4 cores; server CPU 1.0 cores; permission cache hit 33.2%, check cache hit 8.3%
- per request: 12.48 database queries, 5.75 Redis round trips (counted by the server)
- oracle: 15 answers checked (11 expected granted), 0 wrong answers, 10 unanswered
- validity: **invalid**: 953 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 42.16% above 1.00%; 15 answers/s, under 95% of the 40 asked

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 535 | 25267.5 | 29338.0 | 39782.8 | 40001.8 | – | – | 40764.7 |
| check1 | 741 | 35139.1 | 38802.9 | 40150.5 | 40235.3 | – | – | 40711.8 |
| check10 | 281 | 35316.5 | 38743.8 | 40147.1 | 40292.4 | – | – | 40591.7 |
| authzen | 462 | 35882.3 | 38847.3 | 40054.8 | 40256.5 | – | – | 40690.3 |
| authzen_batch | 252 | 35999.9 | 38928.5 | 40161.4 | 40379.6 | – | – | 40666.3 |
| on_behalf | 247 | 36477.1 | 38753.0 | 40065.0 | 40148.8 | – | – | 40566.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2518, answered 2xx 484, other 2034 (error rate 80.78%), answered 5.4 requests/s
- dropped iterations 4352; allowed share of the decisions 79.5% over 902 decisions
- load generator CPU 1% of 4 cores; server CPU 1.1 cores; permission cache hit 24.7%, check cache hit 1.7%
- per request: 11.37 database queries, 5.28 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 4352 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 80.78% above 1.00%; 5 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 554 | 29202.2 | 35034.0 | 41097.5 | 41638.6 | – | – | 42706.9 |
| check1 | 769 | 38125.6 | 39058.4 | 41449.1 | 42383.5 | – | – | 48298.1 |
| check10 | 291 | 38030.6 | 39210.1 | 41551.9 | 42470.2 | – | – | 48260.4 |
| authzen | 475 | 38153.8 | 39100.8 | 41218.7 | 42074.6 | – | – | 48310.7 |
| authzen_batch | 258 | 38254.1 | 39151.8 | 41350.4 | 41990.3 | – | – | 48285.4 |
| on_behalf | 262 | 38571.7 | 39085.8 | 41196.8 | 42256.2 | – | – | 54697.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2609, answered 2xx 278, other 2331 (error rate 89.34%), answered 3.1 requests/s
- dropped iterations 11357; allowed share of the decisions 77.7% over 349 decisions
- load generator CPU 1% of 4 cores; server CPU 1.3 cores; permission cache hit 16.4%, check cache hit 0.0%
- per request: 10.07 database queries, 4.04 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 11357 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 89.34% above 1.00%; 3 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 584 | 36769.9 | 38253.8 | 42947.1 | 43463.5 | – | – | 44317.6 |
| check1 | 798 | 38603.3 | 39480.9 | 43392.4 | 43658.0 | – | – | 46913.2 |
| check10 | 306 | 38542.7 | 39632.3 | 43454.4 | 43674.6 | – | – | 46995.7 |
| authzen | 490 | 38727.7 | 39239.7 | 43414.0 | 43630.8 | – | – | 46946.3 |
| authzen_batch | 274 | 38822.4 | 39508.2 | 43388.0 | 43513.4 | – | – | 54885.0 |
| on_behalf | 280 | 38974.9 | 39775.5 | 43382.5 | 43495.7 | – | – | 55048.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2732, answered 2xx 6, other 2726 (error rate 99.78%), answered 0.1 requests/s
- dropped iterations 25796; allowed share of the decisions – over 0 decisions
- load generator CPU 1% of 4 cores; server CPU 1.0 cores; permission cache hit 1.0%, check cache hit 0.0%
- per request: 9.46 database queries, 3.90 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 25796 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 99.78% above 1.00%; 0 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 613 | 36883.5 | 37738.6 | 40569.5 | 41136.1 | – | – | 52331.6 |
| check1 | 909 | 38232.0 | 39289.6 | 40544.1 | 40970.8 | – | – | 51764.6 |
| check10 | 330 | 37980.9 | 39302.1 | 40550.7 | 40870.1 | – | – | 51751.0 |
| authzen | 539 | 38294.2 | 39341.6 | 40596.1 | 40827.9 | – | – | 52330.5 |
| authzen_batch | 299 | 38232.5 | 39396.4 | 40616.6 | 41098.3 | – | – | 46422.6 |
| on_behalf | 313 | 38310.7 | 39339.5 | 40393.7 | 40533.7 | – | – | 51460.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3003, answered 2xx 0, other 3003 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 54597; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 0.0%, check cache hit 0.0%
- per request: 9.21 database queries, 3.50 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 54597 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 649940 | 13541 | 48.00 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 89158 | 15293 | 5.83 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 81463 | 1860 | 43.80 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 3963 | 8462 | 0.47 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 2814 | 4895 | 0.57 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2276 | 32602 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 2078 | 598 | 3.47 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 1798 | 20 | 89.88 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32047104 | 19668992 |
| resource_edge | 95000 | 25837568 | 11280384 |
| resource_ancestor | 195000 | 42033152 | 22265856 |
| assignment_rule | 10000 | 2416640 | 851968 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

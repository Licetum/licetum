# Load test: medium, warm mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 20 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.56 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.58 |
| warm-up permission (first synchronization) | 28.33 |
| warm-up check1 | 0.20 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.01 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 164 | 988.3 | 512.3 | 1670.7 | 1682.0 | – | – | 2008.5 |
| check1 | 254 | 828.4 | 509.0 | 1669.5 | 1678.6 | – | – | 1876.7 |
| check10 | 96 | 1305.7 | 1663.2 | 1769.2 | 1810.6 | – | – | 1984.4 |
| authzen | 193 | 743.8 | 508.8 | 1665.0 | 1675.6 | – | – | 1898.7 |
| authzen_batch | 102 | 1336.4 | 1664.5 | 1766.4 | 1800.1 | – | – | 1871.8 |
| on_behalf | 92 | 931.8 | 611.5 | 1771.5 | 1812.1 | – | – | 1870.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.0% over 2519 decisions
- load generator CPU 0% of 4 cores; server CPU 0.3 cores; permission cache hit 56.7%, check cache hit 59.1%
- per request: 7.70 database queries, 4.39 Redis round trips (counted by the server)
- oracle: 48 answers checked (32 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 334 | 925.3 | 512.1 | 1600.7 | 1701.8 | – | – | 7819.6 |
| check1 | 528 | 703.6 | 508.3 | 1666.0 | 1677.7 | – | – | 1996.3 |
| check10 | 200 | 1601.0 | 1669.9 | 1791.2 | 1874.5 | – | – | 7318.8 |
| authzen | 363 | 660.5 | 508.0 | 1663.2 | 1674.2 | – | – | 2007.4 |
| authzen_batch | 197 | 1482.2 | 1667.1 | 1778.9 | 1805.7 | – | – | 2443.8 |
| on_behalf | 179 | 816.5 | 610.2 | 1771.6 | 1781.7 | – | – | 2028.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.4% over 5040 decisions
- load generator CPU 1% of 4 cores; server CPU 0.4 cores; permission cache hit 64.7%, check cache hit 63.8%
- per request: 6.99 database queries, 4.08 Redis round trips (counted by the server)
- oracle: 48 answers checked (32 expected granted), 0 wrong answers, 1 unanswered
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 715 | 761.8 | 517.8 | 1610.5 | 1689.4 | – | – | 2225.3 |
| check1 | 1080 | 607.6 | 510.8 | 710.5 | 1669.8 | – | – | 2314.6 |
| check10 | 364 | 1577.8 | 1695.7 | 1912.8 | 2001.6 | – | – | 2345.5 |
| authzen | 738 | 616.8 | 511.5 | 711.4 | 1677.8 | – | – | 2200.3 |
| authzen_batch | 344 | 1608.2 | 1696.8 | 1915.4 | 2055.0 | – | – | 2200.9 |
| on_behalf | 358 | 732.1 | 616.1 | 810.2 | 1783.3 | – | – | 2262.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3599, answered 2xx 3599, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 2; allowed share of the decisions 79.0% over 9256 decisions
- load generator CPU 1% of 4 cores; server CPU 0.7 cores; permission cache hit 78.7%, check cache hit 71.0%
- per request: 5.72 database queries, 3.88 Redis round trips (counted by the server)
- oracle: 52 answers checked (35 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 2 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1097 | 10420.3 | 6813.2 | 29360.6 | 32325.5 | – | – | 37324.2 |
| check1 | 1614 | 9603.0 | 6532.5 | 25775.4 | 31432.9 | – | – | 37335.6 |
| check10 | 550 | 27181.4 | 31507.4 | 36841.0 | 37028.9 | – | – | 37359.5 |
| authzen | 1097 | 9518.5 | 6613.9 | 25853.4 | 30740.1 | – | – | 37379.7 |
| authzen_batch | 532 | 27427.1 | 32401.7 | 36808.8 | 36996.1 | – | – | 37331.0 |
| on_behalf | 534 | 11629.5 | 9313.8 | 27045.8 | 34352.5 | – | – | 39392.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5424, answered 2xx 4923, other 501 (error rate 9.24%), answered 54.7 requests/s
- dropped iterations 1777; allowed share of the decisions 78.5% over 11237 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 66.1%, check cache hit 51.0%
- per request: 5.69 database queries, 3.73 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 0 wrong answers, 10 unanswered
- validity: **invalid**: 1777 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 9.24% above 1.00%; 55 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1118 | 14356.6 | 7610.3 | 34705.8 | 36471.7 | – | – | 37771.6 |
| check1 | 1638 | 14256.0 | 7649.5 | 31698.3 | 35510.7 | – | – | 37766.6 |
| check10 | 555 | 32021.5 | 36178.5 | 37637.3 | 37710.9 | – | – | 37856.7 |
| authzen | 1098 | 13711.1 | 7653.8 | 31474.1 | 34835.8 | – | – | 37744.4 |
| authzen_batch | 558 | 31321.2 | 36046.4 | 37656.7 | 37706.2 | – | – | 37797.9 |
| on_behalf | 538 | 16360.1 | 11297.1 | 32885.7 | 35532.6 | – | – | 38107.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5505, answered 2xx 3827, other 1678 (error rate 30.48%), answered 42.5 requests/s
- dropped iterations 8896; allowed share of the decisions 79.0% over 6625 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 55.8%, check cache hit 43.1%
- per request: 5.43 database queries, 3.62 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 8896 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 30.48% above 1.00%; 43 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1135 | 20180.9 | 20506.8 | 36766.9 | 37694.6 | – | – | 38969.2 |
| check1 | 1628 | 13662.2 | 7800.2 | 33828.4 | 36505.3 | – | – | 38514.8 |
| check10 | 559 | 35559.6 | 37405.9 | 38380.1 | 38615.9 | – | – | 39133.7 |
| authzen | 1108 | 13807.0 | 7857.3 | 33379.1 | 36130.7 | – | – | 38592.7 |
| authzen_batch | 557 | 35232.9 | 37368.3 | 38303.5 | 38558.9 | – | – | 38902.5 |
| on_behalf | 537 | 15910.7 | 11594.0 | 33923.6 | 36496.3 | – | – | 39326.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5524, answered 2xx 3565, other 1959 (error rate 35.46%), answered 39.6 requests/s
- dropped iterations 23276; allowed share of the decisions 78.9% over 5261 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 36.4%, check cache hit 50.7%
- per request: 5.36 database queries, 3.70 Redis round trips (counted by the server)
- oracle: 16 answers checked (11 expected granted), 0 wrong answers, 12 unanswered
- validity: **invalid**: 23276 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 35.46% above 1.00%; 40 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1093 | 18251.2 | 10710.9 | 36341.4 | 37900.0 | – | – | 38975.9 |
| check1 | 1585 | 15747.5 | 8216.2 | 34386.4 | 37069.2 | – | – | 38944.6 |
| check10 | 537 | 36160.4 | 37442.4 | 38793.8 | 38879.9 | – | – | 39330.3 |
| authzen | 1056 | 14806.8 | 8166.9 | 33837.7 | 37002.5 | – | – | 38879.0 |
| authzen_batch | 536 | 35867.3 | 37303.9 | 38691.6 | 38885.2 | – | – | 38973.0 |
| on_behalf | 524 | 17929.0 | 12093.6 | 34035.2 | 36602.3 | – | – | 39050.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5331, answered 2xx 3291, other 2040 (error rate 38.27%), answered 36.6 requests/s
- dropped iterations 52270; allowed share of the decisions 79.4% over 4453 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 48.4%, check cache hit 45.5%
- per request: 5.29 database queries, 3.63 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 52270 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 38.27% above 1.00%; 37 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 279796 | 7720 | 36.24 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 106720 | 2517 | 42.40 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 37155 | 7720 | 4.81 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 7278 | 11604 | 0.63 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 5361 | 6936 | 0.77 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 3736 | 726 | 5.15 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 2423 | 36008 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 1788 | 19 | 94.08 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32129024 | 19603456 |
| resource_edge | 95000 | 16711680 | 8642560 |
| resource_ancestor | 195000 | 42000384 | 22233088 |
| assignment_rule | 10000 | 2433024 | 868352 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

# Load test: medium, warm mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 20 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.47 |
| service and types | 0.02 |
| groups | 0.10 |
| rules | 0.39 |
| warm-up permission (first synchronization) | 19.73 |
| warm-up check1 | 0.18 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 166 | 1045.9 | 611.2 | 1662.4 | 1677.7 | – | – | 1879.8 |
| check1 | 252 | 831.4 | 508.7 | 1658.6 | 1668.8 | – | – | 1768.5 |
| check10 | 97 | 1511.1 | 1656.7 | 1771.4 | 1871.7 | – | – | 1975.6 |
| authzen | 194 | 769.0 | 508.3 | 1656.7 | 1659.4 | – | – | 1866.5 |
| authzen_batch | 98 | 1413.3 | 1656.5 | 1759.9 | 1798.0 | – | – | 1901.4 |
| on_behalf | 94 | 979.2 | 610.4 | 1763.9 | 1858.6 | – | – | 1962.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 78.9% over 2490 decisions
- load generator CPU 0% of 4 cores; server CPU 0.2 cores; permission cache hit 51.2%, check cache hit 55.0%
- per request: 8.30 database queries, 4.53 Redis round trips (counted by the server)
- oracle: 48 answers checked (32 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 335 | 895.2 | 509.8 | 1573.2 | 1663.9 | – | – | 1894.5 |
| check1 | 534 | 697.6 | 507.3 | 1655.8 | 1660.4 | – | – | 1887.0 |
| check10 | 196 | 1498.1 | 1655.0 | 1761.5 | 1786.4 | – | – | 1884.7 |
| authzen | 358 | 634.3 | 507.0 | 1650.8 | 1655.1 | – | – | 1880.9 |
| authzen_batch | 199 | 1481.4 | 1656.0 | 1759.7 | 1774.2 | – | – | 1904.7 |
| on_behalf | 178 | 782.5 | 609.0 | 1755.0 | 1760.6 | – | – | 1974.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1800, answered 2xx 1800, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.3% over 5020 decisions
- load generator CPU 1% of 4 cores; server CPU 0.4 cores; permission cache hit 63.6%, check cache hit 64.5%
- per request: 6.92 database queries, 4.07 Redis round trips (counted by the server)
- oracle: 54 answers checked (37 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 715 | 760.2 | 514.4 | 1587.2 | 1650.1 | – | – | 1993.1 |
| check1 | 1077 | 612.1 | 509.9 | 712.2 | 1656.4 | – | – | 2096.9 |
| check10 | 364 | 1551.3 | 1678.7 | 1850.7 | 1927.0 | – | – | 2191.1 |
| authzen | 739 | 600.7 | 510.2 | 710.1 | 1652.6 | – | – | 2163.3 |
| authzen_batch | 342 | 1548.7 | 1675.2 | 1825.5 | 1912.6 | – | – | 2462.9 |
| on_behalf | 362 | 708.3 | 612.8 | 712.5 | 1757.6 | – | – | 2220.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3599, answered 2xx 3599, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 2; allowed share of the decisions 79.0% over 9238 decisions
- load generator CPU 1% of 4 cores; server CPU 0.5 cores; permission cache hit 77.5%, check cache hit 71.7%
- per request: 5.67 database queries, 3.88 Redis round trips (counted by the server)
- oracle: 54 answers checked (37 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 2 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1078 | 10044.4 | 6610.4 | 27497.8 | 30782.9 | – | – | 36715.3 |
| check1 | 1591 | 9649.1 | 6242.2 | 25469.8 | 31426.8 | – | – | 36948.2 |
| check10 | 547 | 26758.0 | 30599.6 | 36307.4 | 36749.7 | – | – | 37013.1 |
| authzen | 1097 | 9620.9 | 6397.1 | 24793.6 | 31042.4 | – | – | 37004.3 |
| authzen_batch | 516 | 26881.6 | 31146.8 | 36476.7 | 36872.6 | – | – | 37281.8 |
| on_behalf | 554 | 12452.4 | 10121.3 | 29620.1 | 33528.9 | – | – | 38601.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5383, answered 2xx 4878, other 505 (error rate 9.38%), answered 54.2 requests/s
- dropped iterations 1818; allowed share of the decisions 78.5% over 11204 decisions
- load generator CPU 2% of 4 cores; server CPU 0.7 cores; permission cache hit 66.5%, check cache hit 49.1%
- per request: 5.68 database queries, 3.73 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 0 wrong answers, 10 unanswered
- validity: **invalid**: 1818 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 9.38% above 1.00%; 54 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1165 | 13043.5 | 7344.8 | 33187.8 | 35476.9 | – | – | 37657.5 |
| check1 | 1691 | 11835.7 | 7343.1 | 29400.0 | 34411.8 | – | – | 37887.3 |
| check10 | 577 | 29122.3 | 35095.6 | 37398.3 | 37694.1 | – | – | 37867.5 |
| authzen | 1140 | 11426.6 | 7340.5 | 27365.5 | 33756.5 | – | – | 37807.4 |
| authzen_batch | 567 | 29387.6 | 36045.4 | 37413.8 | 37715.8 | – | – | 37863.5 |
| on_behalf | 572 | 14399.2 | 10815.1 | 32395.2 | 37157.5 | – | – | 40395.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5712, answered 2xx 4442, other 1270 (error rate 22.23%), answered 49.4 requests/s
- dropped iterations 8689; allowed share of the decisions 79.0% over 8569 decisions
- load generator CPU 2% of 4 cores; server CPU 0.7 cores; permission cache hit 59.1%, check cache hit 49.9%
- per request: 5.24 database queries, 3.60 Redis round trips (counted by the server)
- oracle: 11 answers checked (8 expected granted), 0 wrong answers, 9 unanswered
- validity: **invalid**: 8689 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 22.23% above 1.00%; 49 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1106 | 17497.4 | 13349.1 | 37256.2 | 37778.0 | – | – | 38308.2 |
| check1 | 1625 | 12905.8 | 7840.8 | 30841.4 | 36846.6 | – | – | 38420.4 |
| check10 | 546 | 34168.3 | 37346.0 | 38110.4 | 38177.2 | – | – | 38982.6 |
| authzen | 1089 | 12782.4 | 7857.0 | 31209.4 | 36052.7 | – | – | 38492.9 |
| authzen_batch | 562 | 34451.3 | 37424.9 | 38117.9 | 38193.7 | – | – | 38689.6 |
| on_behalf | 532 | 15635.0 | 11652.4 | 32001.5 | 35367.6 | – | – | 38460.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5460, answered 2xx 3349, other 2111 (error rate 38.66%), answered 37.2 requests/s
- dropped iterations 23341; allowed share of the decisions 77.9% over 5094 decisions
- load generator CPU 2% of 4 cores; server CPU 0.7 cores; permission cache hit 44.8%, check cache hit 49.9%
- per request: 5.26 database queries, 3.67 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 23341 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 38.66% above 1.00%; 37 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1144 | 16156.3 | 7969.6 | 34869.8 | 37829.0 | – | – | 38369.9 |
| check1 | 1692 | 14664.5 | 7884.3 | 32429.2 | 35346.9 | – | – | 38361.3 |
| check10 | 565 | 35333.2 | 36848.7 | 38009.0 | 38148.6 | – | – | 38471.8 |
| authzen | 1118 | 14153.1 | 7851.3 | 31569.6 | 35052.9 | – | – | 38390.6 |
| authzen_batch | 570 | 34976.6 | 36810.5 | 38047.5 | 38233.6 | – | – | 38390.4 |
| on_behalf | 547 | 17135.1 | 11658.2 | 33783.9 | 36347.3 | – | – | 38371.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5636, answered 2xx 4025, other 1611 (error rate 28.58%), answered 44.7 requests/s
- dropped iterations 51964; allowed share of the decisions 78.2% over 6036 decisions
- load generator CPU 2% of 4 cores; server CPU 0.7 cores; permission cache hit 55.9%, check cache hit 47.8%
- per request: 5.34 database queries, 3.63 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 0 wrong answers, 11 unanswered
- validity: **invalid**: 51964 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; errors 28.58% above 1.00%; 45 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 133428 | 7636 | 17.47 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 38740 | 2047 | 18.93 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 21123 | 7636 | 2.77 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 4600 | 12207 | 0.38 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 3064 | 7226 | 0.42 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2025 | 752 | 2.69 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 1894 | 36332 | 0.05 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 1359 | 28696 | 0.05 | `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32309248 | 19783680 |
| resource_edge | 95000 | 16490496 | 8421376 |
| resource_ancestor | 195000 | 42016768 | 22249472 |
| assignment_rule | 10000 | 2441216 | 876544 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

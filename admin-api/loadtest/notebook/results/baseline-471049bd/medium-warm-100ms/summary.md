# Load test: medium, warm mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 20 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.55 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.56 |
| warm-up permission (first synchronization) | 44.67 |
| warm-up check1 | 0.20 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.01 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 167 | 989.8 | 512.4 | 1671.6 | 1706.4 | – | – | 1901.2 |
| check1 | 249 | 821.7 | 509.0 | 1671.4 | 1766.8 | – | – | 5391.6 |
| check10 | 96 | 1335.4 | 1662.9 | 1785.8 | 1828.9 | – | – | 1897.0 |
| authzen | 194 | 766.9 | 508.8 | 1667.7 | 1763.4 | – | – | 1969.9 |
| authzen_batch | 100 | 1274.8 | 1664.1 | 1767.4 | 1830.5 | – | – | 1988.4 |
| on_behalf | 95 | 937.3 | 611.8 | 1770.6 | 1868.3 | – | – | 1988.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.0% over 2498 decisions
- load generator CPU 0% of 4 cores; server CPU 0.3 cores; permission cache hit 56.9%, check cache hit 60.2%
- per request: 7.61 database queries, 4.37 Redis round trips (counted by the server)
- oracle: 48 answers checked (32 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 332 | 889.4 | 512.2 | 1590.8 | 1677.0 | – | – | 1817.7 |
| check1 | 532 | 709.6 | 508.8 | 1669.5 | 1685.5 | – | – | 1996.0 |
| check10 | 197 | 1545.7 | 1668.8 | 1776.2 | 1796.2 | – | – | 4782.2 |
| authzen | 362 | 658.0 | 508.6 | 1664.5 | 1673.0 | – | – | 1897.2 |
| authzen_batch | 198 | 1543.7 | 1670.6 | 1786.2 | 1830.1 | – | – | 7330.4 |
| on_behalf | 180 | 806.7 | 610.7 | 1768.7 | 1777.4 | – | – | 1895.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.4% over 5024 decisions
- load generator CPU 1% of 4 cores; server CPU 0.5 cores; permission cache hit 64.8%, check cache hit 63.4%
- per request: 7.00 database queries, 4.08 Redis round trips (counted by the server)
- oracle: 52 answers checked (35 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 718 | 766.6 | 518.5 | 1603.9 | 1695.8 | – | – | 2551.4 |
| check1 | 1076 | 608.6 | 510.9 | 710.7 | 1670.1 | – | – | 2303.9 |
| check10 | 367 | 1586.5 | 1690.7 | 1917.0 | 2017.0 | – | – | 2364.0 |
| authzen | 734 | 619.5 | 511.9 | 710.8 | 1674.7 | – | – | 2282.4 |
| authzen_batch | 344 | 1618.4 | 1695.6 | 1932.0 | 2089.7 | – | – | 2453.8 |
| on_behalf | 359 | 726.1 | 617.7 | 766.7 | 1779.0 | – | – | 2324.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3598, answered 2xx 3598, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 3; allowed share of the decisions 79.0% over 9279 decisions
- load generator CPU 1% of 4 cores; server CPU 0.7 cores; permission cache hit 78.3%, check cache hit 71.3%
- per request: 5.73 database queries, 3.88 Redis round trips (counted by the server)
- oracle: 54 answers checked (37 expected granted), 0 mismatches
- validity: **invalid**: 3 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1105 | 10820.7 | 7141.0 | 28873.2 | 32417.6 | – | – | 37328.8 |
| check1 | 1632 | 9416.6 | 6844.0 | 24886.4 | 29675.4 | – | – | 37386.9 |
| check10 | 557 | 27473.5 | 29930.4 | 37158.3 | 37240.9 | – | – | 37376.5 |
| authzen | 1101 | 9518.4 | 7057.0 | 24729.2 | 30295.3 | – | – | 37302.3 |
| authzen_batch | 548 | 27789.5 | 31268.9 | 37169.5 | 37238.4 | – | – | 37432.6 |
| on_behalf | 554 | 11883.3 | 10467.5 | 26382.3 | 31621.5 | – | – | 38698.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5497, answered 2xx 4803, other 694 (error rate 12.63%), answered 53.4 requests/s
- dropped iterations 1704; allowed share of the decisions 78.6% over 10924 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 63.3%, check cache hit 51.0%
- per request: 5.61 database queries, 3.72 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 10 mismatches
- validity: **invalid**: 1704 iterations dropped: k6 could not send at the rate; 10 of 13 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 12.63% above 1.00%; 53 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1152 | 14409.6 | 8044.6 | 32831.5 | 36229.8 | – | – | 38186.9 |
| check1 | 1680 | 13139.8 | 7891.8 | 30517.9 | 34413.4 | – | – | 38169.4 |
| check10 | 564 | 32344.1 | 36490.8 | 38028.1 | 38096.2 | – | – | 38307.3 |
| authzen | 1134 | 13118.8 | 7898.7 | 31491.6 | 35305.9 | – | – | 38210.1 |
| authzen_batch | 565 | 31808.2 | 36534.8 | 37984.2 | 38131.3 | – | – | 38279.6 |
| on_behalf | 562 | 16265.0 | 11783.7 | 32577.7 | 36172.8 | – | – | 40263.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5657, answered 2xx 4242, other 1415 (error rate 25.01%), answered 47.1 requests/s
- dropped iterations 8744; allowed share of the decisions 77.8% over 7317 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 56.6%, check cache hit 48.1%
- per request: 5.24 database queries, 3.61 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 11 mismatches
- validity: **invalid**: 8744 iterations dropped: k6 could not send at the rate; 11 of 13 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 25.01% above 1.00%; 47 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1158 | 17004.9 | 11808.9 | 35293.2 | 37536.8 | – | – | 38335.6 |
| check1 | 1662 | 13500.7 | 7802.9 | 30406.7 | 35405.2 | – | – | 38492.8 |
| check10 | 561 | 34778.4 | 36803.7 | 38196.2 | 38285.3 | – | – | 38419.9 |
| authzen | 1126 | 13421.7 | 7825.8 | 29973.7 | 34333.2 | – | – | 38463.9 |
| authzen_batch | 564 | 34661.4 | 36686.6 | 38160.7 | 38273.1 | – | – | 38397.5 |
| on_behalf | 555 | 16317.0 | 11458.8 | 32190.6 | 36033.6 | – | – | 38383.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5626, answered 2xx 3871, other 1755 (error rate 31.19%), answered 43.0 requests/s
- dropped iterations 23175; allowed share of the decisions 78.5% over 6059 decisions
- load generator CPU 2% of 4 cores; server CPU 0.9 cores; permission cache hit 46.5%, check cache hit 47.6%
- per request: 5.41 database queries, 3.69 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 11 mismatches
- validity: **invalid**: 23175 iterations dropped: k6 could not send at the rate; 11 of 12 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 31.19% above 1.00%; 43 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1162 | 17251.9 | 8743.0 | 36112.0 | 37849.6 | – | – | 38839.2 |
| check1 | 1645 | 13683.4 | 7929.5 | 31921.7 | 35887.5 | – | – | 38792.1 |
| check10 | 558 | 35384.6 | 37252.6 | 38542.9 | 38668.2 | – | – | 38867.3 |
| authzen | 1114 | 13454.7 | 7952.3 | 31620.6 | 36446.4 | – | – | 38455.6 |
| authzen_batch | 576 | 35615.4 | 37342.9 | 38311.6 | 38606.4 | – | – | 39153.8 |
| on_behalf | 552 | 16536.2 | 11832.2 | 32674.2 | 37011.1 | – | – | 40934.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 5607, answered 2xx 3533, other 2074 (error rate 36.99%), answered 39.3 requests/s
- dropped iterations 51993; allowed share of the decisions 77.5% over 4979 decisions
- load generator CPU 2% of 4 cores; server CPU 0.9 cores; permission cache hit 51.9%, check cache hit 49.7%
- per request: 5.26 database queries, 3.62 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 11 mismatches
- validity: **invalid**: 51993 iterations dropped: k6 could not send at the rate; 11 of 12 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 36.99% above 1.00%; 39 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 279932 | 7766 | 36.05 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 101060 | 2423 | 41.71 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 37863 | 7766 | 4.88 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 7260 | 11731 | 0.62 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 5100 | 7283 | 0.70 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 3476 | 735 | 4.73 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 2626 | 36657 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 1861 | 28891 | 0.06 | `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32202752 | 19824640 |
| resource_edge | 95000 | 25804800 | 11247616 |
| resource_ancestor | 195000 | 42106880 | 22339584 |
| assignment_rule | 10000 | 2416640 | 851968 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

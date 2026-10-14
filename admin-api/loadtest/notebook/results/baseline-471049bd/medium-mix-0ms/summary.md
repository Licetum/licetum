# Load test: medium, mix mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 20 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.55 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.55 |
| warm-up permission (first synchronization) | 28.50 |
| warm-up check1 | 0.21 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 174 | 22.8 | 5.0 | 55.3 | 58.6 | – | – | 144.3 |
| check1 | 253 | 23.2 | 2.9 | 52.3 | 53.7 | – | – | 104.1 |
| check10 | 104 | 24.4 | 4.2 | 52.2 | 54.5 | – | – | 105.5 |
| authzen | 180 | 22.4 | 2.6 | 51.2 | 55.8 | – | – | 107.1 |
| authzen_batch | 106 | 28.3 | 40.6 | 52.9 | 55.5 | – | – | 248.6 |
| on_behalf | 84 | 26.9 | 3.1 | 52.0 | 53.3 | – | – | 245.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 2617 decisions
- load generator CPU 0% of 4 cores; server CPU 0.6 cores; permission cache hit 62.6%, check cache hit 32.4%
- per request: 11.01 database queries, 6.09 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 359 | 37.2 | 47.0 | 76.7 | 82.8 | – | – | 218.3 |
| check1 | 507 | 49.8 | 50.9 | 70.7 | 78.3 | – | – | 378.1 |
| check10 | 202 | 48.0 | 49.5 | 67.7 | 73.2 | – | – | 267.0 |
| authzen | 345 | 47.5 | 50.1 | 70.3 | 77.4 | – | – | 249.6 |
| authzen_batch | 213 | 48.3 | 48.6 | 69.5 | 74.4 | – | – | 243.3 |
| on_behalf | 175 | 52.1 | 51.7 | 73.0 | 77.7 | – | – | 303.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 5177 decisions
- load generator CPU 1% of 4 cores; server CPU 1.2 cores; permission cache hit 47.4%, check cache hit 13.7%
- per request: 13.42 database queries, 5.78 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 707 | 1063.4 | 1098.6 | 2132.7 | 2301.8 | – | – | 3008.4 |
| check1 | 1016 | 1646.0 | 1689.6 | 2270.6 | 2465.4 | – | – | 3342.5 |
| check10 | 383 | 1657.6 | 1648.4 | 2349.8 | 2562.7 | – | – | 3036.0 |
| authzen | 699 | 1629.2 | 1655.7 | 2326.8 | 2508.6 | – | – | 3397.7 |
| authzen_batch | 378 | 1647.1 | 1616.0 | 2293.6 | 2481.5 | – | – | 3134.8 |
| on_behalf | 370 | 1740.7 | 1726.7 | 2481.9 | 2612.2 | – | – | 3335.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3553, answered 2xx 3553, other 0 (error rate 0.00%), answered 39.5 requests/s
- dropped iterations 47; allowed share of the decisions 79.7% over 9695 decisions
- load generator CPU 1% of 4 cores; server CPU 2.2 cores; permission cache hit 43.6%, check cache hit 2.1%
- per request: 14.71 database queries, 6.13 Redis round trips (counted by the server)
- oracle: 64 answers checked (46 expected granted), 0 mismatches
- validity: **invalid**: 47 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 836 | 3268.4 | 1720.2 | 6570.2 | 7219.0 | – | – | 8714.6 |
| check1 | 1217 | 5321.4 | 6381.1 | 7370.0 | 8169.1 | – | – | 9186.8 |
| check10 | 434 | 5491.9 | 6417.6 | 8030.3 | 8322.5 | – | – | 9205.7 |
| authzen | 795 | 5383.8 | 6377.0 | 7408.7 | 8210.0 | – | – | 9109.0 |
| authzen_batch | 412 | 5525.6 | 6440.0 | 7634.4 | 8250.8 | – | – | 8878.5 |
| on_behalf | 400 | 5958.6 | 6940.5 | 8411.1 | 8863.1 | – | – | 9750.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 4094, answered 2xx 4094, other 0 (error rate 0.00%), answered 45.5 requests/s
- dropped iterations 3107; allowed share of the decisions 79.3% over 10872 decisions
- load generator CPU 1% of 4 cores; server CPU 2.2 cores; permission cache hit 49.8%, check cache hit 11.1%
- per request: 13.44 database queries, 5.92 Redis round trips (counted by the server)
- oracle: 20 answers checked (13 expected granted), 0 mismatches
- validity: **invalid**: 3107 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 45 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 768 | 3921.1 | 5288.9 | 6797.4 | 7625.6 | – | – | 8765.8 |
| check1 | 1111 | 6478.9 | 6517.9 | 7770.5 | 8458.2 | – | – | 9283.5 |
| check10 | 405 | 6505.8 | 6514.5 | 7794.0 | 8403.0 | – | – | 9297.4 |
| authzen | 731 | 6501.1 | 6537.6 | 8029.8 | 8543.6 | – | – | 9211.7 |
| authzen_batch | 397 | 6633.4 | 6580.1 | 7591.4 | 8456.6 | – | – | 9431.7 |
| on_behalf | 354 | 7089.2 | 7058.3 | 8350.5 | 8906.9 | – | – | 9618.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3766, answered 2xx 3766, other 0 (error rate 0.00%), answered 41.8 requests/s
- dropped iterations 10635; allowed share of the decisions 79.3% over 10216 decisions
- load generator CPU 1% of 4 cores; server CPU 2.2 cores; permission cache hit 42.7%, check cache hit 1.5%
- per request: 14.63 database queries, 5.84 Redis round trips (counted by the server)
- oracle: 17 answers checked (12 expected granted), 0 mismatches
- validity: **invalid**: 10635 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 42 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 751 | 4047.5 | 5664.3 | 7327.0 | 7842.8 | – | – | 9716.0 |
| check1 | 1086 | 6697.1 | 6586.7 | 8179.1 | 8511.1 | – | – | 10349.8 |
| check10 | 386 | 6807.0 | 6623.2 | 8175.7 | 8411.2 | – | – | 9659.8 |
| authzen | 721 | 6665.1 | 6597.4 | 8202.1 | 8526.2 | – | – | 10428.6 |
| authzen_batch | 392 | 6853.5 | 6627.2 | 8216.2 | 8406.1 | – | – | 9990.4 |
| on_behalf | 348 | 7246.3 | 7112.7 | 8720.8 | 9028.6 | – | – | 11004.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3684, answered 2xx 3684, other 0 (error rate 0.00%), answered 40.9 requests/s
- dropped iterations 25117; allowed share of the decisions 79.3% over 9935 decisions
- load generator CPU 2% of 4 cores; server CPU 2.2 cores; permission cache hit 42.5%, check cache hit 1.7%
- per request: 14.70 database queries, 5.84 Redis round trips (counted by the server)
- oracle: 16 answers checked (11 expected granted), 0 mismatches
- validity: **invalid**: 25117 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 41 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 763 | 4027.6 | 5686.7 | 6975.9 | 7330.8 | – | – | 8587.6 |
| check1 | 1105 | 6760.4 | 6678.1 | 7895.4 | 8427.3 | – | – | 9983.9 |
| check10 | 400 | 6884.9 | 6668.2 | 7872.1 | 8393.4 | – | – | 9429.3 |
| authzen | 731 | 6720.7 | 6643.7 | 8000.2 | 8524.1 | – | – | 9784.9 |
| authzen_batch | 400 | 6871.8 | 6671.4 | 7821.6 | 8300.2 | – | – | 9832.0 |
| on_behalf | 359 | 7204.9 | 7132.1 | 8336.1 | 8980.4 | – | – | 10134.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3758, answered 2xx 3758, other 0 (error rate 0.00%), answered 41.8 requests/s
- dropped iterations 53842; allowed share of the decisions 79.3% over 10195 decisions
- load generator CPU 2% of 4 cores; server CPU 2.1 cores; permission cache hit 43.0%, check cache hit 1.8%
- per request: 14.67 database queries, 6.05 Redis round trips (counted by the server)
- oracle: 18 answers checked (13 expected granted), 0 mismatches
- validity: **invalid**: 53842 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 42 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 1703851 | 19257 | 88.48 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 665467 | 2319 | 286.96 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 341545 | 19257 | 17.74 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 39362 | 19257 | 2.04 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 38375 | 36248 | 1.06 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 32775 | 2266 | 14.46 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 10495 | 20 | 524.76 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 4838 | 19257 | 0.25 | `SELECT id, group_id, member_group_id, predicate, enabled, valid_from, expires_at FROM group_member WHERE enabled AND (valid_from IS NULL OR valid_from <= now())…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32137216 | 19611648 |
| resource_edge | 95000 | 16670720 | 8601600 |
| resource_ancestor | 195000 | 41926656 | 22159360 |
| assignment_rule | 10000 | 2482176 | 917504 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

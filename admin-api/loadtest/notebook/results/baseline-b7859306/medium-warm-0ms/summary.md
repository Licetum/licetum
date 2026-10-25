# Load test: medium, warm mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 80 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.45 |
| service and types | 0.02 |
| groups | 0.10 |
| rules | 0.39 |
| warm-up permission (first synchronization) | 18.23 |
| warm-up check1 | 0.36 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 168 | 24.6 | 35.7 | 43.7 | 46.2 | – | – | 250.0 |
| check1 | 250 | 12.5 | 1.6 | 38.7 | 40.5 | – | – | 45.9 |
| check10 | 96 | 33.8 | 36.5 | 41.6 | 43.8 | – | – | 64.2 |
| authzen | 193 | 9.4 | 1.5 | 38.2 | 40.6 | – | – | 45.1 |
| authzen_batch | 99 | 34.8 | 37.7 | 41.9 | 42.9 | – | – | 46.0 |
| on_behalf | 95 | 13.2 | 2.1 | 39.8 | 41.0 | – | – | 45.9 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.1% over 2488 decisions
- load generator CPU 0% of 4 cores; server CPU 0.4 cores; permission cache hit 47.6%, check cache hit 34.5%
- per request: 11.01 database queries, 5.88 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 336 | 16.3 | 2.8 | 41.3 | 53.3 | – | – | 62.8 |
| check1 | 529 | 6.1 | 1.3 | 33.5 | 35.8 | – | – | 87.2 |
| check10 | 197 | 33.4 | 34.5 | 49.8 | 52.7 | – | – | 104.0 |
| authzen | 362 | 6.0 | 1.3 | 31.7 | 35.8 | – | – | 73.8 |
| authzen_batch | 198 | 31.1 | 34.3 | 48.9 | 51.2 | – | – | 63.6 |
| on_behalf | 179 | 6.7 | 1.7 | 33.7 | 37.7 | – | – | 72.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.4% over 5020 decisions
- load generator CPU 0% of 4 cores; server CPU 0.5 cores; permission cache hit 65.5%, check cache hit 52.3%
- per request: 8.47 database queries, 4.39 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 705 | 11.6 | 2.8 | 44.4 | 54.3 | – | – | 83.7 |
| check1 | 1086 | 4.6 | 1.2 | 3.3 | 37.0 | – | – | 61.9 |
| check10 | 364 | 33.7 | 36.9 | 53.6 | 60.8 | – | – | 81.5 |
| authzen | 741 | 2.9 | 1.2 | 2.4 | 4.1 | – | – | 72.1 |
| authzen_batch | 342 | 35.3 | 38.1 | 55.0 | 59.6 | – | – | 87.6 |
| on_behalf | 363 | 3.9 | 1.6 | 3.5 | 34.4 | – | – | 54.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3601, answered 2xx 3601, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.1% over 9250 decisions
- load generator CPU 1% of 4 cores; server CPU 0.7 cores; permission cache hit 81.1%, check cache hit 65.6%
- per request: 6.52 database queries, 4.03 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 1453 | 9.9 | 3.7 | 22.6 | 52.2 | – | – | 127.0 |
| check1 | 2155 | 4.0 | 1.3 | 6.0 | 10.9 | – | – | 185.2 |
| check10 | 731 | 50.6 | 46.5 | 71.6 | 80.9 | – | – | 237.6 |
| authzen | 1450 | 4.6 | 1.3 | 6.9 | 12.9 | – | – | 164.6 |
| authzen_batch | 677 | 50.8 | 46.4 | 72.0 | 82.9 | – | – | 285.9 |
| on_behalf | 735 | 5.8 | 1.8 | 9.6 | 32.2 | – | – | 98.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 7201, answered 2xx 7201, other 0 (error rate 0.00%), answered 80.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.2% over 18420 decisions
- load generator CPU 1% of 4 cores; server CPU 1.2 cores; permission cache hit 90.4%, check cache hit 67.3%
- per request: 6.05 database queries, 3.86 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 2835 | 19.4 | 9.4 | 42.8 | 71.1 | – | – | 498.9 |
| check1 | 4323 | 11.2 | 4.7 | 26.3 | 40.9 | – | – | 462.2 |
| check10 | 1469 | 109.3 | 87.7 | 205.4 | 249.1 | – | – | 530.8 |
| authzen | 2894 | 11.1 | 5.0 | 25.9 | 42.4 | – | – | 253.5 |
| authzen_batch | 1427 | 106.6 | 85.9 | 190.8 | 247.4 | – | – | 537.1 |
| on_behalf | 1453 | 12.5 | 5.9 | 29.4 | 42.8 | – | – | 350.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 14401, answered 2xx 14401, other 0 (error rate 0.00%), answered 160.0 requests/s
- dropped iterations 0; allowed share of the decisions 79.0% over 37630 decisions
- load generator CPU 2% of 4 cores; server CPU 1.9 cores; permission cache hit 94.7%, check cache hit 71.0%
- per request: 5.52 database queries, 3.82 Redis round trips (counted by the server)
- oracle: 446 answers checked (341 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: **fail**: p99 253.0 ms above 100ms

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 3807 | 669.6 | 555.7 | 761.4 | 1997.5 | – | – | 4137.9 |
| check1 | 5813 | 616.2 | 539.6 | 720.5 | 916.7 | – | – | 3910.9 |
| check10 | 1950 | 3042.4 | 3038.8 | 3583.4 | 3922.3 | – | – | 4441.2 |
| authzen | 3898 | 610.7 | 537.4 | 712.2 | 871.0 | – | – | 4073.6 |
| authzen_batch | 1926 | 3049.5 | 3036.1 | 3634.9 | 3919.8 | – | – | 4605.1 |
| on_behalf | 1930 | 858.0 | 787.4 | 1007.8 | 1177.6 | – | – | 4380.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 19324, answered 2xx 19324, other 0 (error rate 0.00%), answered 214.7 requests/s
- dropped iterations 9476; allowed share of the decisions 79.0% over 50401 decisions
- load generator CPU 3% of 4 cores; server CPU 2.3 cores; permission cache hit 92.9%, check cache hit 70.7%
- per request: 5.17 database queries, 3.66 Redis round trips (counted by the server)
- oracle: 35 answers checked (23 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 9476 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 3936.1 ms above 100ms; 215 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 3867 | 685.3 | 559.4 | 769.0 | 2100.3 | – | – | 4077.6 |
| check1 | 5908 | 610.1 | 539.3 | 689.7 | 860.5 | – | – | 4156.2 |
| check10 | 1988 | 3092.6 | 3046.7 | 3532.8 | 3829.8 | – | – | 4722.1 |
| authzen | 3951 | 623.0 | 540.6 | 696.1 | 929.4 | – | – | 3987.1 |
| authzen_batch | 1954 | 3086.9 | 3052.4 | 3565.4 | 3816.9 | – | – | 4711.9 |
| on_behalf | 1966 | 849.9 | 786.8 | 955.2 | 1151.5 | – | – | 4436.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 19634, answered 2xx 19634, other 0 (error rate 0.00%), answered 218.2 requests/s
- dropped iterations 37967; allowed share of the decisions 78.9% over 51245 decisions
- load generator CPU 3% of 4 cores; server CPU 2.3 cores; permission cache hit 92.3%, check cache hit 71.0%
- per request: 5.16 database queries, 3.67 Redis round trips (counted by the server)
- oracle: 33 answers checked (21 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 37967 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; p99 3840.4 ms above 100ms; 218 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 468233 | 17419 | 26.88 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 93328 | 1044 | 89.39 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 91743 | 17419 | 5.27 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 15935 | 33940 | 0.47 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 10545 | 17419 | 0.61 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 4225 | 86600 | 0.05 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 3092 | 898 | 3.44 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 3066 | 69181 | 0.04 | `SELECT id FROM service WHERE slug = $1 AND deleted_at IS NULL` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32751616 | 20226048 |
| resource_edge | 95000 | 16719872 | 8650752 |
| resource_ancestor | 195000 | 41975808 | 22208512 |
| assignment_rule | 10000 | 2441216 | 876544 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

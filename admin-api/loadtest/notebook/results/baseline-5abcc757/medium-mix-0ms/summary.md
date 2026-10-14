# Load test: medium, mix mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 20 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.56 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.57 |
| warm-up permission (first synchronization) | 44.40 |
| warm-up check1 | 0.21 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 174 | 24.6 | 5.9 | 57.1 | 59.9 | – | – | 125.1 |
| check1 | 252 | 23.3 | 2.6 | 55.7 | 58.5 | – | – | 94.5 |
| check10 | 104 | 25.2 | 4.6 | 54.3 | 56.5 | – | – | 66.6 |
| authzen | 180 | 24.8 | 3.1 | 53.5 | 56.6 | – | – | 113.3 |
| authzen_batch | 106 | 25.9 | 3.4 | 55.5 | 58.3 | – | – | 88.4 |
| on_behalf | 84 | 21.8 | 3.6 | 52.0 | 56.2 | – | – | 69.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 900, answered 2xx 900, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 2616 decisions
- load generator CPU 0% of 4 cores; server CPU 0.6 cores; permission cache hit 60.9%, check cache hit 33.9%
- per request: 10.86 database queries, 6.06 Redis round trips (counted by the server)
- oracle: 450 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 359 | 41.3 | 48.7 | 84.9 | 92.7 | – | – | 287.8 |
| check1 | 507 | 55.7 | 54.4 | 77.9 | 106.4 | – | – | 310.0 |
| check10 | 202 | 52.0 | 51.2 | 75.7 | 82.7 | – | – | 323.6 |
| authzen | 345 | 51.3 | 52.7 | 73.8 | 80.1 | – | – | 367.7 |
| authzen_batch | 213 | 53.8 | 54.4 | 77.8 | 86.1 | – | – | 245.9 |
| on_behalf | 175 | 53.4 | 54.9 | 73.2 | 80.9 | – | – | 263.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 5177 decisions
- load generator CPU 1% of 4 cores; server CPU 1.3 cores; permission cache hit 47.4%, check cache hit 13.8%
- per request: 13.42 database queries, 5.78 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 702 | 679.0 | 318.5 | 1684.3 | 1833.4 | – | – | 2543.7 |
| check1 | 1028 | 1041.7 | 1175.2 | 1834.0 | 1979.1 | – | – | 2381.6 |
| check10 | 373 | 1011.0 | 1147.0 | 1861.8 | 2024.9 | – | – | 2286.9 |
| authzen | 731 | 1038.9 | 1156.3 | 1831.0 | 1971.0 | – | – | 2501.1 |
| authzen_batch | 374 | 1023.9 | 1148.3 | 1797.6 | 2019.0 | – | – | 2763.2 |
| on_behalf | 365 | 1067.2 | 1225.4 | 1945.8 | 2089.2 | – | – | 2639.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3573, answered 2xx 3573, other 0 (error rate 0.00%), answered 39.7 requests/s
- dropped iterations 27; allowed share of the decisions 79.5% over 9594 decisions
- load generator CPU 1% of 4 cores; server CPU 2.1 cores; permission cache hit 46.0%, check cache hit 9.8%
- per request: 13.77 database queries, 5.91 Redis round trips (counted by the server)
- oracle: 162 answers checked (124 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 27 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 769 | 3801.1 | 3861.0 | 6785.5 | 7777.1 | – | – | 9292.3 |
| check1 | 1104 | 6345.5 | 6603.7 | 8159.0 | 8643.0 | – | – | 9601.2 |
| check10 | 398 | 6422.8 | 6622.8 | 8360.4 | 8688.1 | – | – | 9817.5 |
| authzen | 740 | 6146.6 | 6550.7 | 7988.9 | 8596.1 | – | – | 9598.6 |
| authzen_batch | 401 | 6378.7 | 6596.3 | 8161.3 | 8716.0 | – | – | 9909.9 |
| on_behalf | 359 | 6898.7 | 7164.8 | 8462.8 | 9327.7 | – | – | 9900.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3771, answered 2xx 3771, other 0 (error rate 0.00%), answered 41.9 requests/s
- dropped iterations 3430; allowed share of the decisions 79.2% over 10193 decisions
- load generator CPU 1% of 4 cores; server CPU 2.2 cores; permission cache hit 44.9%, check cache hit 4.9%
- per request: 14.24 database queries, 6.17 Redis round trips (counted by the server)
- oracle: 17 answers checked (12 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 3430 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 42 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 731 | 4178.0 | 5811.0 | 7088.7 | 7782.3 | – | – | 8844.0 |
| check1 | 1058 | 6813.3 | 6753.0 | 8452.8 | 8741.8 | – | – | 9592.8 |
| check10 | 379 | 6913.4 | 6770.4 | 8346.7 | 8702.3 | – | – | 9752.8 |
| authzen | 704 | 6742.2 | 6736.4 | 8329.4 | 8601.0 | – | – | 9453.3 |
| authzen_batch | 386 | 6975.4 | 6775.3 | 8549.7 | 8800.1 | – | – | 9527.2 |
| on_behalf | 346 | 7375.4 | 7344.6 | 8923.3 | 9263.5 | – | – | 10003.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3604, answered 2xx 3604, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 10797; allowed share of the decisions 79.2% over 9758 decisions
- load generator CPU 1% of 4 cores; server CPU 2.2 cores; permission cache hit 41.2%, check cache hit 1.6%
- per request: 14.73 database queries, 5.83 Redis round trips (counted by the server)
- oracle: 16 answers checked (11 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 10797 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 40 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 745 | 4102.4 | 5699.8 | 7142.7 | 7756.5 | – | – | 8993.3 |
| check1 | 1080 | 6731.6 | 6710.5 | 8002.2 | 8512.1 | – | – | 9544.3 |
| check10 | 386 | 6846.5 | 6730.2 | 7760.2 | 8501.1 | – | – | 9169.1 |
| authzen | 714 | 6726.3 | 6718.0 | 8171.4 | 8636.3 | – | – | 9766.3 |
| authzen_batch | 392 | 6851.2 | 6700.2 | 7717.7 | 8478.0 | – | – | 9180.5 |
| on_behalf | 350 | 7262.8 | 7171.4 | 8548.0 | 9181.7 | – | – | 10136.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3667, answered 2xx 3667, other 0 (error rate 0.00%), answered 40.7 requests/s
- dropped iterations 25134; allowed share of the decisions 79.3% over 9924 decisions
- load generator CPU 2% of 4 cores; server CPU 2.2 cores; permission cache hit 42.3%, check cache hit 1.6%
- per request: 14.66 database queries, 5.83 Redis round trips (counted by the server)
- oracle: 16 answers checked (11 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 25134 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 41 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 739 | 4173.7 | 5767.3 | 7591.1 | 8028.8 | – | – | 9626.2 |
| check1 | 1064 | 6774.6 | 6751.8 | 8217.2 | 8498.6 | – | – | 10201.7 |
| check10 | 385 | 6944.4 | 6827.4 | 8228.8 | 8542.9 | – | – | 9509.2 |
| authzen | 710 | 6728.8 | 6745.2 | 8094.9 | 8438.2 | – | – | 10640.6 |
| authzen_batch | 387 | 6933.9 | 6795.4 | 8197.1 | 8578.6 | – | – | 9281.9 |
| on_behalf | 348 | 7287.5 | 7199.0 | 8685.8 | 9114.0 | – | – | 11751.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3633, answered 2xx 3633, other 0 (error rate 0.00%), answered 40.4 requests/s
- dropped iterations 53968; allowed share of the decisions 79.3% over 9842 decisions
- load generator CPU 2% of 4 cores; server CPU 2.1 cores; permission cache hit 41.5%, check cache hit 1.7%
- per request: 14.71 database queries, 5.95 Redis round trips (counted by the server)
- oracle: 17 answers checked (12 expected granted), 0 wrong answers, 1 unanswered
- validity: **invalid**: 53968 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 40 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 1631523 | 18812 | 86.73 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 588919 | 2158 | 272.90 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 329624 | 18812 | 17.52 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 43280 | 35390 | 1.22 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 41826 | 18812 | 2.22 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 32722 | 2234 | 14.65 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 5114 | 19 | 269.14 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 4420 | 18812 | 0.23 | `SELECT id, group_id, member_group_id, predicate, enabled, valid_from, expires_at FROM group_member WHERE enabled AND (valid_from IS NULL OR valid_from <= now())…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31842304 | 19464192 |
| resource_edge | 95000 | 25640960 | 11083776 |
| resource_ancestor | 195000 | 41926656 | 22159360 |
| assignment_rule | 10000 | 2465792 | 901120 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

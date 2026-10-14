# Load test: medium, mix mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 250 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 40 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.53 |
| service and types | 0.02 |
| groups | 0.10 |
| rules | 0.44 |
| warm-up permission (first synchronization) | 26.17 |
| warm-up check1 | 0.19 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 174 | 24.9 | 6.4 | 52.3 | 53.3 | – | – | 56.8 |
| check1 | 252 | 33.0 | 42.2 | 49.6 | 51.3 | – | – | 248.4 |
| check10 | 105 | 30.7 | 41.4 | 48.3 | 49.2 | – | – | 51.3 |
| authzen | 180 | 32.7 | 41.1 | 49.5 | 50.9 | – | – | 86.6 |
| authzen_batch | 106 | 33.6 | 41.7 | 48.9 | 51.1 | – | – | 77.7 |
| on_behalf | 84 | 31.2 | 41.1 | 49.0 | 49.9 | – | – | 60.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 2626 decisions
- load generator CPU 0% of 4 cores; server CPU 0.6 cores; permission cache hit 52.9%, check cache hit 18.5%
- per request: 12.89 database queries, 6.50 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 359 | 29.3 | 41.4 | 57.6 | 62.3 | – | – | 135.5 |
| check1 | 507 | 37.3 | 41.9 | 50.6 | 53.3 | – | – | 134.9 |
| check10 | 202 | 38.5 | 41.9 | 53.2 | 56.0 | – | – | 133.3 |
| authzen | 345 | 37.5 | 41.7 | 51.7 | 54.4 | – | – | 255.7 |
| authzen_batch | 213 | 37.9 | 42.6 | 50.8 | 52.4 | – | – | 104.1 |
| on_behalf | 175 | 38.9 | 40.9 | 51.6 | 54.8 | – | – | 139.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1801, answered 2xx 1801, other 0 (error rate 0.00%), answered 20.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 5177 decisions
- load generator CPU 1% of 4 cores; server CPU 1.1 cores; permission cache hit 47.1%, check cache hit 13.7%
- per request: 13.42 database queries, 5.78 Redis round trips (counted by the server)
- oracle: 451 answers checked (345 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 719 | 106.9 | 67.3 | 229.8 | 364.6 | – | – | 1456.7 |
| check1 | 1061 | 146.0 | 102.0 | 279.1 | 508.3 | – | – | 1421.6 |
| check10 | 378 | 146.9 | 104.1 | 311.7 | 540.0 | – | – | 1006.6 |
| authzen | 719 | 154.7 | 111.3 | 327.5 | 596.2 | – | – | 1241.0 |
| authzen_batch | 374 | 162.6 | 112.7 | 357.8 | 609.5 | – | – | 1224.7 |
| on_behalf | 350 | 140.1 | 109.3 | 240.2 | 498.9 | – | – | 959.4 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3601, answered 2xx 3601, other 0 (error rate 0.00%), answered 40.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 9650 decisions
- load generator CPU 1% of 4 cores; server CPU 2.0 cores; permission cache hit 49.9%, check cache hit 15.8%
- per request: 13.03 database queries, 5.73 Redis round trips (counted by the server)
- oracle: 423 answers checked (328 expected granted), 0 wrong answers, 0 unanswered
- validity: valid
- step: pass

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 887 | 3272.3 | 3221.4 | 5834.8 | 6227.9 | – | – | 7564.6 |
| check1 | 1262 | 5469.0 | 5716.4 | 6622.2 | 7090.0 | – | – | 8257.4 |
| check10 | 459 | 5586.6 | 5722.4 | 6712.4 | 7089.0 | – | – | 7711.4 |
| authzen | 841 | 5508.3 | 5708.6 | 6666.8 | 7110.8 | – | – | 8204.2 |
| authzen_batch | 441 | 5722.3 | 5780.0 | 6789.0 | 7163.3 | – | – | 8035.9 |
| on_behalf | 418 | 5919.8 | 6121.5 | 6842.1 | 7402.0 | – | – | 8035.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 4308, answered 2xx 4308, other 0 (error rate 0.00%), answered 47.9 requests/s
- dropped iterations 2892; allowed share of the decisions 79.4% over 11521 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit 44.8%, check cache hit 2.0%
- per request: 14.55 database queries, 6.26 Redis round trips (counted by the server)
- oracle: 21 answers checked (13 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 2892 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 48 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 893 | 3454.3 | 4870.8 | 6103.3 | 6641.6 | – | – | 7408.6 |
| check1 | 1267 | 5699.1 | 5716.7 | 6968.1 | 7244.9 | – | – | 8305.5 |
| check10 | 466 | 5838.7 | 5747.5 | 7062.1 | 7299.4 | – | – | 7823.4 |
| authzen | 851 | 5745.5 | 5752.2 | 6946.3 | 7262.5 | – | – | 8178.6 |
| authzen_batch | 449 | 5870.2 | 5745.7 | 7088.7 | 7259.6 | – | – | 8480.5 |
| on_behalf | 419 | 6210.8 | 6200.4 | 7447.4 | 7713.2 | – | – | 8290.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 4345, answered 2xx 4345, other 0 (error rate 0.00%), answered 48.3 requests/s
- dropped iterations 10056; allowed share of the decisions 79.5% over 11687 decisions
- load generator CPU 1% of 4 cores; server CPU 2.3 cores; permission cache hit 43.8%, check cache hit 1.8%
- per request: 14.61 database queries, 5.82 Redis round trips (counted by the server)
- oracle: 19 answers checked (13 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 10056 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 48 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 896 | 3341.5 | 4689.8 | 5704.6 | 6164.9 | – | – | 7377.6 |
| check1 | 1280 | 5698.6 | 5726.5 | 6683.4 | 7201.3 | – | – | 8298.6 |
| check10 | 466 | 5825.5 | 5733.0 | 6703.2 | 7226.4 | – | – | 8511.9 |
| authzen | 849 | 5693.6 | 5737.5 | 6499.4 | 7213.5 | – | – | 8144.8 |
| authzen_batch | 449 | 5829.8 | 5741.3 | 6561.8 | 7157.0 | – | – | 7708.9 |
| on_behalf | 419 | 6174.7 | 6181.3 | 7372.7 | 7726.8 | – | – | 8474.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 4359, answered 2xx 4359, other 0 (error rate 0.00%), answered 48.4 requests/s
- dropped iterations 24442; allowed share of the decisions 79.6% over 11698 decisions
- load generator CPU 2% of 4 cores; server CPU 2.4 cores; permission cache hit 45.2%, check cache hit 2.0%
- per request: 14.54 database queries, 5.75 Redis round trips (counted by the server)
- oracle: 20 answers checked (13 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 24442 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 48 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 890 | 3394.0 | 4795.4 | 6066.0 | 6425.1 | – | – | 7939.8 |
| check1 | 1268 | 5791.3 | 5741.5 | 6878.1 | 7197.2 | – | – | 8462.5 |
| check10 | 459 | 5912.5 | 5763.0 | 6907.9 | 7105.3 | – | – | 7945.7 |
| authzen | 845 | 5784.3 | 5756.9 | 6891.3 | 7166.2 | – | – | 8614.1 |
| authzen_batch | 443 | 5967.6 | 5771.1 | 6972.2 | 7255.8 | – | – | 8230.1 |
| on_behalf | 412 | 6206.8 | 6167.7 | 7191.0 | 7561.1 | – | – | 9598.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 4317, answered 2xx 4317, other 0 (error rate 0.00%), answered 48.0 requests/s
- dropped iterations 53283; allowed share of the decisions 79.5% over 11545 decisions
- load generator CPU 2% of 4 cores; server CPU 2.3 cores; permission cache hit 44.7%, check cache hit 1.9%
- per request: 14.57 database queries, 5.91 Redis round trips (counted by the server)
- oracle: 19 answers checked (13 expected granted), 0 wrong answers, 0 unanswered
- validity: **invalid**: 53283 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid; 48 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 1051848 | 21398 | 49.16 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 367215 | 2203 | 166.69 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 199295 | 21398 | 9.31 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 26685 | 40310 | 0.66 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 22152 | 21398 | 1.04 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 19590 | 2486 | 7.88 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 4412 | 20 | 220.60 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 3680 | 21398 | 0.17 | `SELECT id, group_id, member_group_id, predicate, enabled, valid_from, expires_at FROM group_member WHERE enabled AND (valid_from IS NULL OR valid_from <= now())…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31784960 | 19406848 |
| resource_edge | 95000 | 26107904 | 11550720 |
| resource_ancestor | 195000 | 41926656 | 22159360 |
| assignment_rule | 10000 | 2441216 | 876544 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

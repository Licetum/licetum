# Load test: medium, mix mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 10 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.56 |
| service and types | 0.03 |
| groups | 0.13 |
| rules | 0.54 |
| warm-up permission (first synchronization) | 28.90 |
| warm-up check1 | 0.21 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.01 |

## Window at 10 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 178 | 1174.9 | 1568.2 | 1789.7 | 1890.8 | – | – | 3039.8 |
| check1 | 249 | 1308.1 | 1665.8 | 1885.5 | 1902.1 | – | – | 2014.1 |
| check10 | 104 | 1440.9 | 1669.7 | 1887.9 | 1958.8 | – | – | 2039.4 |
| authzen | 179 | 1392.7 | 1669.7 | 1891.9 | 1923.3 | – | – | 2033.6 |
| authzen_batch | 106 | 1380.7 | 1669.8 | 1887.7 | 1975.0 | – | – | 4980.1 |
| on_behalf | 85 | 1450.3 | 1771.7 | 1866.5 | 1878.2 | – | – | 1986.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 901, answered 2xx 901, other 0 (error rate 0.00%), answered 10.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 2613 decisions
- load generator CPU 0% of 4 cores; server CPU 0.4 cores; permission cache hit 43.3%, check cache hit 29.5%
- per request: 11.57 database queries, 5.70 Redis round trips (counted by the server)
- oracle: 50 answers checked (33 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 20 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 336 | 3350.2 | 3540.7 | 6064.9 | 6170.8 | – | – | 6985.9 |
| check1 | 497 | 4991.5 | 5172.7 | 6691.0 | 6783.6 | – | – | 7529.9 |
| check10 | 189 | 4908.4 | 5032.8 | 6616.0 | 6753.1 | – | – | 7412.3 |
| authzen | 342 | 4933.5 | 5005.3 | 6694.1 | 6820.0 | – | – | 7797.4 |
| authzen_batch | 185 | 4936.5 | 5178.5 | 6581.4 | 6724.3 | – | – | 7202.5 |
| on_behalf | 174 | 5339.1 | 5511.8 | 7141.5 | 7335.8 | – | – | 7980.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1723, answered 2xx 1723, other 0 (error rate 0.00%), answered 19.1 requests/s
- dropped iterations 78; allowed share of the decisions 79.4% over 4753 decisions
- load generator CPU 1% of 4 cores; server CPU 1.0 cores; permission cache hit 35.7%, check cache hit 1.1%
- per request: 15.04 database queries, 6.20 Redis round trips (counted by the server)
- oracle: 21 answers checked (13 expected granted), 0 mismatches
- validity: **invalid**: 78 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 40 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 509 | 15862.3 | 9191.0 | 37565.2 | 38373.3 | – | – | 39808.8 |
| check1 | 697 | 22799.6 | 28069.1 | 38095.7 | 38921.7 | – | – | 39428.5 |
| check10 | 264 | 23142.6 | 28273.1 | 37973.5 | 39043.8 | – | – | 39427.2 |
| authzen | 452 | 22916.9 | 27620.1 | 38015.7 | 38865.8 | – | – | 39443.7 |
| authzen_batch | 264 | 23950.0 | 27578.6 | 38444.5 | 38937.0 | – | – | 39418.5 |
| on_behalf | 222 | 24919.3 | 33917.8 | 38386.6 | 38882.3 | – | – | 39199.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2408, answered 2xx 1522, other 886 (error rate 36.79%), answered 16.9 requests/s
- dropped iterations 942; allowed share of the decisions 79.7% over 3988 decisions
- load generator CPU 2% of 4 cores; server CPU 1.1 cores; permission cache hit 36.5%, check cache hit 11.9%
- per request: 12.33 database queries, 5.70 Redis round trips (counted by the server)
- oracle: 15 answers checked (11 expected granted), 9 mismatches
- validity: **invalid**: 942 iterations dropped: k6 could not send at the rate; 9 of 15 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 36.79% above 1.00%; 17 answers/s, under 95% of the 40 asked

## Window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 536 | 25694.0 | 30532.5 | 40119.9 | 40526.7 | – | – | 41037.2 |
| check1 | 747 | 34906.3 | 39037.7 | 40515.6 | 40645.4 | – | – | 47064.7 |
| check10 | 278 | 35424.4 | 39023.6 | 40467.3 | 40647.5 | – | – | 40992.4 |
| authzen | 468 | 35784.8 | 39125.9 | 40525.5 | 40638.7 | – | – | 48390.0 |
| authzen_batch | 253 | 35979.2 | 39194.9 | 40534.1 | 40636.1 | – | – | 40856.9 |
| on_behalf | 249 | 36009.1 | 39041.8 | 40465.3 | 40612.4 | – | – | 40852.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2531, answered 2xx 464, other 2067 (error rate 81.67%), answered 5.2 requests/s
- dropped iterations 4327; allowed share of the decisions 79.8% over 835 decisions
- load generator CPU 2% of 4 cores; server CPU 1.3 cores; permission cache hit 23.6%, check cache hit 3.1%
- per request: 11.10 database queries, 5.27 Redis round trips (counted by the server)
- oracle: 14 answers checked (11 expected granted), 11 mismatches
- validity: **invalid**: 4327 iterations dropped: k6 could not send at the rate; 11 of 14 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 81.67% above 1.00%; 5 answers/s, under 95% of the 80 asked

## Window at 160 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 523 | 32087.4 | 35697.0 | 42356.6 | 43231.0 | – | – | 44624.6 |
| check1 | 704 | 38595.6 | 38781.7 | 42770.8 | 43290.6 | – | – | 44790.2 |
| check10 | 268 | 38385.7 | 38746.1 | 42532.2 | 43295.6 | – | – | 43939.3 |
| authzen | 440 | 38462.0 | 38614.5 | 42436.1 | 43244.8 | – | – | 43906.5 |
| authzen_batch | 237 | 38719.5 | 38849.1 | 42415.4 | 43233.7 | – | – | 45013.3 |
| on_behalf | 234 | 38927.1 | 38919.4 | 42417.3 | 43153.0 | – | – | 43691.5 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2406, answered 2xx 156, other 2250 (error rate 93.52%), answered 1.7 requests/s
- dropped iterations 11359; allowed share of the decisions 83.2% over 113 decisions
- load generator CPU 2% of 4 cores; server CPU 1.1 cores; permission cache hit 9.4%, check cache hit 0.0%
- per request: 9.66 database queries, 3.59 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 12 mismatches
- validity: **invalid**: 11359 iterations dropped: k6 could not send at the rate; 12 of 13 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 93.52% above 1.00%; 2 answers/s, under 95% of the 160 asked

## Window at 320 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 585 | 36881.7 | 38232.0 | 43199.2 | 43690.9 | – | – | 44342.4 |
| check1 | 876 | 38489.8 | 38859.0 | 43514.3 | 43814.4 | – | – | 46784.4 |
| check10 | 318 | 38259.3 | 38910.5 | 43674.0 | 43825.5 | – | – | 46815.2 |
| authzen | 532 | 38696.7 | 38844.2 | 43734.9 | 43886.2 | – | – | 46856.0 |
| authzen_batch | 289 | 38716.0 | 38948.0 | 43663.2 | 43810.9 | – | – | 46893.2 |
| on_behalf | 298 | 38790.4 | 38948.9 | 43627.6 | 43812.9 | – | – | 46746.6 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2898, answered 2xx 0, other 2898 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 25801; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.9 cores; permission cache hit 0.0%, check cache hit 0.0%
- per request: 9.33 database queries, 3.79 Redis round trips (counted by the server)
- oracle: 17 answers checked (12 expected granted), 12 mismatches
- validity: **invalid**: 25801 iterations dropped: k6 could not send at the rate; 12 of 17 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 320 asked

## Window at 640 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 612 | 36467.9 | 37299.7 | 40219.2 | 40516.7 | – | – | 41587.6 |
| check1 | 909 | 37738.6 | 38413.7 | 40174.0 | 40396.5 | – | – | 44057.4 |
| check10 | 329 | 37551.8 | 38422.4 | 40204.5 | 40441.9 | – | – | 42385.2 |
| authzen | 539 | 37788.4 | 38401.7 | 40227.0 | 40436.1 | – | – | 44097.4 |
| authzen_batch | 298 | 37777.6 | 38415.6 | 40248.6 | 40557.9 | – | – | 42299.2 |
| on_behalf | 313 | 37880.1 | 38502.3 | 40154.9 | 40249.0 | – | – | 40497.7 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 3000, answered 2xx 0, other 3000 (error rate 100.00%), answered 0.0 requests/s
- dropped iterations 54600; allowed share of the decisions – over 0 decisions
- load generator CPU 2% of 4 cores; server CPU 0.8 cores; permission cache hit 0.0%, check cache hit 0.0%
- per request: 9.07 database queries, 3.37 Redis round trips (counted by the server)
- oracle: 12 answers checked (9 expected granted), 12 mismatches
- validity: **invalid**: 54600 iterations dropped: k6 could not send at the rate; 12 of 12 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 100.00% above 1.00%; 0 answers/s, under 95% of the 640 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 685156 | 12333 | 55.55 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 119621 | 15305 | 7.82 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 48579 | 1330 | 36.53 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 4793 | 8790 | 0.55 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 3479 | 5056 | 0.69 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2706 | 614 | 4.41 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 2410 | 32622 | 0.07 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |
| 1942 | 15305 | 0.13 | `SELECT id, group_id, member_group_id, predicate, enabled, valid_from, expires_at FROM group_member WHERE enabled AND (valid_from IS NULL OR valid_from <= now())…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 32538624 | 20013056 |
| resource_edge | 95000 | 16703488 | 8634368 |
| resource_ancestor | 195000 | 42033152 | 22265856 |
| assignment_rule | 10000 | 2424832 | 860160 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

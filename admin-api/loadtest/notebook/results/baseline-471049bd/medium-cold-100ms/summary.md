# Load test: medium, cold mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 30 connections, consistency `minimize_latency`, access log off, CPUs `all`. Requests: `permission=2,check1=3,check10=1,authzen=2,authzen_batch=1,on_behalf=1`.
Load generator: k6 2.2.0 (commit/00a9a1b7f5, go1.26.5, linux/amd64), at most 1000 virtual users, CPUs `all`. Machine: 4 CPUs, linux/amd64, go1.26.0.
Warm-up: 60 s before the measurement. Measured window: 90 s per window. Environment: `environment.json`.

**Sustained: 8 requests/s** (errors within 1.00%; a p99 is judged only from 10,000 samples).

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.32 |
| service and types | 0.02 |
| groups | 0.06 |
| rules | 0.33 |
| warm-up permission (first synchronization) | 20.89 |
| warm-up check1 | 0.12 |
| warm-up authzen | 0.00 |
| warm-up authzen_batch | 0.00 |
| warm-up on_behalf | 0.00 |

## Window at 2 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 36 | 2092.7 | 1995.7 | 2658.2 | 2722.2 | – | – | 2812.2 |
| check1 | 46 | 2302.7 | 2212.5 | 2798.9 | 2892.3 | – | – | 3052.9 |
| check10 | 25 | 2258.1 | 2189.2 | 2896.6 | 2909.1 | – | – | 2911.8 |
| authzen | 36 | 2124.0 | 2098.3 | 2641.9 | 2710.7 | – | – | 2811.0 |
| authzen_batch | 17 | 2218.4 | 2181.1 | 2765.1 | 2852.3 | – | – | 2911.6 |
| on_behalf | 20 | 2237.8 | 2255.1 | 2912.7 | 3035.3 | – | – | 3133.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 180, answered 2xx 180, other 0 (error rate 0.00%), answered 2.0 requests/s
- dropped iterations 0; allowed share of the decisions 82.0% over 522 decisions
- load generator CPU 0% of 4 cores; server CPU 0.3 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.79 database queries, 7.84 Redis round trips (counted by the server)
- oracle: 50 answers checked (33 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 4 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 67 | 2175.6 | 2233.2 | 2907.6 | 3013.4 | – | – | 3171.2 |
| check1 | 91 | 2196.3 | 2198.9 | 2737.1 | 2849.9 | – | – | 2954.8 |
| check10 | 43 | 2141.7 | 2094.1 | 2720.4 | 2856.5 | – | – | 3223.1 |
| authzen | 79 | 2255.9 | 2227.2 | 2860.0 | 2943.6 | – | – | 3054.2 |
| authzen_batch | 47 | 2285.3 | 2354.0 | 2834.1 | 2921.0 | – | – | 3026.0 |
| on_behalf | 33 | 2217.9 | 2098.9 | 2804.6 | 2926.0 | – | – | 3061.0 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 360, answered 2xx 360, other 0 (error rate 0.00%), answered 4.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 1103 decisions
- load generator CPU 0% of 4 cores; server CPU 0.4 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.45 database queries, 6.14 Redis round trips (counted by the server)
- oracle: 53 answers checked (36 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 8 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 140 | 2042.1 | 2039.4 | 2599.7 | 2668.0 | – | – | 2811.5 |
| check1 | 198 | 2132.4 | 2124.5 | 2661.0 | 2795.1 | – | – | 2991.6 |
| check10 | 87 | 2147.2 | 2163.5 | 2702.0 | 2766.5 | – | – | 2862.2 |
| authzen | 145 | 2156.4 | 2197.6 | 2639.6 | 2766.3 | – | – | 3094.4 |
| authzen_batch | 81 | 2127.1 | 2141.6 | 2701.7 | 2820.1 | – | – | 2874.6 |
| on_behalf | 70 | 2148.9 | 2088.2 | 2695.8 | 2776.2 | – | – | 2842.1 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 721, answered 2xx 721, other 0 (error rate 0.00%), answered 8.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.2% over 2093 decisions
- load generator CPU 0% of 4 cores; server CPU 0.6 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.22 database queries, 5.31 Redis round trips (counted by the server)
- oracle: 55 answers checked (38 expected granted), 0 mismatches
- validity: valid
- step: pass

## Window at 16 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 285 | 2405.8 | 2396.1 | 3093.5 | 3206.2 | – | – | 3494.7 |
| check1 | 410 | 2638.3 | 2644.0 | 3273.4 | 3444.4 | – | – | 3771.5 |
| check10 | 153 | 2607.8 | 2577.7 | 3215.9 | 3409.3 | – | – | 3667.8 |
| authzen | 283 | 2637.0 | 2638.5 | 3257.9 | 3378.5 | – | – | 3590.2 |
| authzen_batch | 167 | 2651.1 | 2604.0 | 3334.8 | 3467.2 | – | – | 3616.5 |
| on_behalf | 139 | 2686.8 | 2736.6 | 3306.1 | 3354.5 | – | – | 3521.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 1437, answered 2xx 1437, other 0 (error rate 0.00%), answered 16.0 requests/s
- dropped iterations 4; allowed share of the decisions 80.3% over 4032 decisions
- load generator CPU 0% of 4 cores; server CPU 0.9 cores; permission cache hit –, check cache hit 0.0%
- per request: 15.10 database queries, 4.85 Redis round trips (counted by the server)
- oracle: 45 answers checked (31 expected granted), 0 mismatches
- validity: **invalid**: 4 iterations dropped: k6 could not send at the rate
- step: **fail**: the window is not valid

## Window at 32 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 429 | 20984.7 | 22401.5 | 33202.9 | 34978.5 | – | – | 36668.2 |
| check1 | 624 | 23426.4 | 25996.9 | 35593.4 | 36124.6 | – | – | 36601.6 |
| check10 | 235 | 22062.5 | 24084.2 | 35128.3 | 36042.6 | – | – | 36539.2 |
| authzen | 420 | 23511.4 | 26094.0 | 35736.9 | 36195.1 | – | – | 36683.0 |
| authzen_batch | 239 | 23659.6 | 25917.3 | 35680.8 | 35981.8 | – | – | 36710.0 |
| on_behalf | 202 | 24822.8 | 27526.0 | 36238.6 | 36607.0 | – | – | 39517.8 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2149, answered 2xx 1921, other 228 (error rate 10.61%), answered 21.3 requests/s
- dropped iterations 732; allowed share of the decisions 79.2% over 5235 decisions
- load generator CPU 1% of 4 cores; server CPU 0.8 cores; permission cache hit –, check cache hit 0.0%
- per request: 14.84 database queries, 4.62 Redis round trips (counted by the server)
- oracle: 16 answers checked (11 expected granted), 9 mismatches
- validity: **invalid**: 732 iterations dropped: k6 could not send at the rate; 9 of 16 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 10.61% above 1.00%; 21 answers/s, under 95% of the 32 asked

## Window at 64 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 514 | 33719.3 | 38107.8 | 40575.7 | 41539.6 | – | – | 42543.7 |
| check1 | 756 | 35121.8 | 38668.0 | 41169.5 | 41777.6 | – | – | 42512.9 |
| check10 | 281 | 33962.7 | 37830.1 | 40879.5 | 41394.4 | – | – | 42312.0 |
| authzen | 475 | 34931.1 | 38668.3 | 40780.8 | 41508.0 | – | – | 42578.5 |
| authzen_batch | 250 | 35017.7 | 38656.3 | 40777.0 | 41475.1 | – | – | 42607.8 |
| on_behalf | 262 | 35888.6 | 38332.3 | 40636.4 | 41532.4 | – | – | 42482.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2538, answered 2xx 667, other 1871 (error rate 73.72%), answered 7.4 requests/s
- dropped iterations 3139; allowed share of the decisions 79.4% over 1697 decisions
- load generator CPU 1% of 4 cores; server CPU 0.9 cores; permission cache hit –, check cache hit 0.0%
- per request: 12.07 database queries, 2.95 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 11 mismatches
- validity: **invalid**: 3139 iterations dropped: k6 could not send at the rate; 11 of 13 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 73.72% above 1.00%; 7 answers/s, under 95% of the 64 asked

## Window at 128 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 492 | 37154.0 | 38908.9 | 43748.3 | 44303.6 | – | – | 45418.0 |
| check1 | 712 | 38202.1 | 39347.5 | 43763.8 | 44251.8 | – | – | 45586.1 |
| check10 | 261 | 37551.3 | 38631.8 | 43810.7 | 44507.3 | – | – | 45683.0 |
| authzen | 449 | 38245.2 | 39257.5 | 44012.2 | 44446.0 | – | – | 45500.5 |
| authzen_batch | 235 | 38350.1 | 39973.1 | 44158.4 | 44724.3 | – | – | 45495.3 |
| on_behalf | 241 | 38663.5 | 39619.6 | 43842.3 | 44295.5 | – | – | 45118.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2390, answered 2xx 392, other 1998 (error rate 83.60%), answered 4.4 requests/s
- dropped iterations 8844; allowed share of the decisions 78.9% over 892 decisions
- load generator CPU 1% of 4 cores; server CPU 0.9 cores; permission cache hit –, check cache hit 0.0%
- per request: 11.69 database queries, 2.76 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 12 mismatches
- validity: **invalid**: 8844 iterations dropped: k6 could not send at the rate; 12 of 13 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 83.60% above 1.00%; 4 answers/s, under 95% of the 128 asked

## Window at 256 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| permission | 492 | 38537.7 | 40011.5 | 41986.7 | 42414.1 | – | – | 44122.1 |
| check1 | 714 | 39087.1 | 40147.4 | 42137.0 | 42520.8 | – | – | 43953.9 |
| check10 | 263 | 38792.0 | 39973.3 | 41974.2 | 42371.5 | – | – | 44117.8 |
| authzen | 447 | 39169.5 | 40151.0 | 42312.8 | 42662.3 | – | – | 44129.3 |
| authzen_batch | 235 | 38963.7 | 40121.3 | 42262.2 | 42602.6 | – | – | 43883.7 |
| on_behalf | 242 | 39186.7 | 40178.3 | 41979.3 | 42343.7 | – | – | 42917.3 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 2393, answered 2xx 30, other 2363 (error rate 98.75%), answered 0.3 requests/s
- dropped iterations 20039; allowed share of the decisions – over 0 decisions
- load generator CPU 1% of 4 cores; server CPU 0.9 cores; permission cache hit –, check cache hit 0.0%
- per request: 10.55 database queries, 3.16 Redis round trips (counted by the server)
- oracle: 13 answers checked (10 expected granted), 12 mismatches
- validity: **invalid**: 20039 iterations dropped: k6 could not send at the rate; 12 of 13 sampled answers differ from the expected ones
- step: **fail**: the window is not valid; errors 98.75% above 1.00%; 0 answers/s, under 95% of the 256 asked

## Statements with the most time during the measurement

| total ms | calls | mean ms | statement |
|---:|---:|---:|---|
| 316758 | 13358 | 23.71 | `SELECT ar.id, ar.role_id, ar.type_resource_id, ar.resource_id, ar.resource_from_claim, ar.access, ar.subject, ar.predicate, ar.member_of_group_id, ar.enabled, a…` |
| 32571 | 13358 | 2.44 | `SELECT min(b) FROM ( -- a role assignment has no type of its own: its boundaries count for every type SELECT valid_from AS b FROM assignment_rule WHERE valid_fr…` |
| 10729 | 303 | 35.41 | `SELECT id, name, folder_id AS parent_id, owner FROM lt_document` |
| 4538 | 13994 | 0.32 | `SELECT c.type_resource_id, c.external_id, c.id FROM unnest($1::uuid[], $2::varchar[]) AS q(type_id, ext) JOIN resource_cache c ON c.type_resource_id = q.type_id…` |
| 3891 | 303 | 12.84 | `SELECT type_resource_id, count(*) FROM resource_cache WHERE active AND type_resource_id = ANY($1) GROUP BY type_resource_id` |
| 3239 | 8424 | 0.38 | `SELECT DISTINCT q.idx, anc.id, anc.type_resource_id, anc.external_id, anc.name FROM unnest($1::bigint[], $2::uuid[], $3::uuid[], $4::text[], $5::text[]) AS q(id…` |
| 2742 | 1346 | 2.04 | `SELECT q.ord, w.descendant_id, w.descendant_type_id, w.descendant_external_id, w.descendant_name, w.inherit FROM unnest($1::uuid[]) WITH ORDINALITY AS q(id, ord…` |
| 1594 | 26752 | 0.06 | `SELECT id, name, slug, url, icon, authorization_url, token_url, jwks_url, issuer, client_id, client_secret, trusted_clients FROM service WHERE id = $1 AND delet…` |

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 31809536 | 19431424 |
| resource_edge | 95000 | 25419776 | 10862592 |
| resource_ancestor | 195000 | 42033152 | 22265856 |
| assignment_rule | 10000 | 2416640 | 851968 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

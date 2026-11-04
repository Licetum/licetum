# Load test: medium, mix mode, seed 1

Policy: 5000 subjects in 50 groups, 10000 assignment rules, 100000 resources (5000 folders, 95000 documents).
Check server: DB pool 16 connections, access log off, CPUs `0-2`. Requests: `search_resource=2,evaluation=6,evaluations10=2`.
Load generator: k6 1.8.2 (go1.26.10, linux/amd64), at most 250 virtual users, CPUs `3`. Machine: 5 CPUs, linux/amd64, go1.26.10.
Warm-up: 60 s before the measurement. Measured window: 180 s per window. Environment: `environment.json`.

**Measured window is valid.**

## Seeding and warm-up

| phase | seconds |
|---|---:|
| source tables | 0.63 |
| service and types | 0.03 |
| PEP client and key | 0.01 |
| groups | 0.16 |
| rules | 0.18 |
| warm-up search_resource (first synchronization) | 17.59 |
| warm-up evaluation | 0.03 |
| warm-up evaluations10 | 0.02 |

## Measured window at 80 requests/s

| request | samples | mean ms | p50 ms | p90 ms | p95 ms | p99 ms | p99.9 ms | max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| search_resource | 2884 | 28.9 | 0.9 | 76.8 | 87.1 | – | – | 199.6 |
| evaluation | 8628 | 10.8 | 10.4 | 19.1 | 22.7 | – | – | 66.2 |
| evaluations10 | 2889 | 11.9 | 11.3 | 19.9 | 23.6 | – | – | 75.2 |

A percentile is published only when its quantile has enough samples: p99 from 10,000, p99.9 from 100,000 ("–" below that).

- requests 14401, answered 2xx 14401, other 0 (error rate 0.00%), answered 80.0 requests/s
- dropped iterations 0; allowed share of the decisions 80.0% over 37518 decisions
- load generator CPU 6% of 1 cores; server CPU 1.2 cores; permission cache hit –, check cache hit 10.8%
- per request: 6.65 database queries (counted by the server)
- oracle: 901 answers checked (675 expected granted), 0 wrong answers, 0 unanswered
- validity: valid

## Storage after the run

| table | rows | total bytes | index bytes |
|---|---:|---:|---:|
| resource_cache | 100000 | 37363712 | 24838144 |
| resource_edge | 95000 | 16556032 | 8486912 |
| resource_ancestor | 195000 | 49577984 | 29810688 |
| assignment_rule | 10000 | 2441216 | 876544 |
| group_member | 50 | 65536 | 49152 |
| user_group | 50 | 49152 | 32768 |
| type_resource | 2 | 65536 | 49152 |

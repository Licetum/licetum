# P3-29: literal subject type matching

One matched medium-profile pair for #262 and ADR 0001 §10.6, measured on 2026-11-01.
The baseline is commit `cbfd132b41d9f7371354557fbaa9ed7848e088c7`; the second run uses the P3-29 working tree.
`before/` and `after/` contain the original machine-readable results and environments, including binary SHA-256 hashes.

Both runs use 100,000 resources, 10,000 rules, 5,000 subjects, 50 groups, seed 1 and the same request mix.
Each has a 60-second warm-up and a 300-second measured window at 50 requests/s; the PDP uses CPUs 0–2 and k6 CPU 3.
PostgreSQL and the harness share the remaining host resources. No other builds or tests ran during either measured window.

| Measure | Before | After |
|---|---:|---:|
| Answered requests/s | 50.003 | 50.003 |
| Overall p99, ms | 74.090 | 73.654 |
| Errors | 0 | 0 |
| Dropped iterations | 0 | 0 |
| Oracle checks | 1501 | 1500 |
| Oracle mismatches | 0 | 0 |
| Valid window | yes | yes |

Overall p99 changed by -0.59%, within the 300 ms SLO in both runs.
This pair detects no regression at the measured rate; it does not establish maximum throughput or a statistically significant improvement.
Individual operations have fewer than 10,000 samples, so their p99 values are not published here.
`pg_stat_statements` was unavailable in both runs; no statement-statistics comparison is claimed.
The load policy uses the default user type; typed identities, NULL type, denials, groups, cache separation and mixed batches are checked by DEC15, DECP seeds 1–20 and API tests.

Reproduce with binaries built on either side of the change, PostgreSQL 16 and k6 1.8.2:

```sh
export DB__POOL_SIZE=8 DB__MAX_OVERFLOW=0 SYNC__DBOS_POOL_SIZE=8
export LOG__SUBJECT_SALT=p3-reproducible-loadtest
# Set LICETUM_TEST_POSTGRES_URI to the test PostgreSQL instance.
licetum-loadtest run -profile medium -mode mix -seed 1 -rate 50 \
  -warm 60s -measure 5m -slo-p99 300ms -db-pool 16 \
  -check-cpus 0-2 -k6-cpus 3 -check-bin /path/to/licetum \
  -k6-bin /path/to/k6 -out /path/to/results
```

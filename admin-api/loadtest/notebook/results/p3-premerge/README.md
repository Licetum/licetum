# P3 pre-merge medium measurements

Measured on 2026-11-01 for #54 and the owner-approved measurement rule in #94.
P3-04 and P3-18 use their exact historical code and matching seed harness; the final run uses the clean validated code commit `72dd04a1370410b67cdf65aaa26282bd5175ada8`.
These checkpoints include other intervening P3 changes. They are not an experiment attributing a difference to a single change.
The matched P3-29 pair is recorded separately in [p3-subject-type](../p3-subject-type/README.md), at a different rate and duration.

All runs: medium (100,000 resources / 10,000 rules / 5,000 subjects / 50 groups), mix, seed 1, 80 requests/s,
60 s warm-up and 180 s measured window, PDP pool 16, `GOMAXPROCS=4`, PDP CPUs 0–2 and k6 CPU 3.
Go 1.26.10, k6 1.8.2 and PostgreSQL 16.15; the host has 5 CPUs. PostgreSQL and the harness share host resources.
A dedicated fresh PostgreSQL container/volume was used for all three runs. No builds or tests ran during the windows.

| Code | Commit | Answered req/s | p50 ms | Overall p99 ms | DB queries/request | Oracle checks |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| [p3-04](p3-04/summary.md) | `d2bb4cf` | 80.006 | 10.054 | 83.541 | 6.652 | 901 |
| [p3-18](p3-18/summary.md) | `2a2e434` | 80.006 | 10.387 | 87.121 | 6.648 | 901 |
| [final](final/summary.md) | `72dd04a` | 80.006 | 10.686 | 90.645 | 6.537 | 901 |

Every window is valid, contains 14,401 requests and has zero HTTP errors, dropped iterations and oracle mismatches.
Each oracle checks both allowed and denied cases. Every measured overall p99 is below the selected 100 ms SLO.
Per-operation p99 is not published here because those operations have fewer than 10,000 samples.
This fixed-rate check does not establish maximum throughput or a statistically significant performance improvement.

The unchanged [committed budgets](../../../budgets.yaml) pass for all three runs: rate floor, p50 and database queries per request.
The committed p99 budget is zero (not calibrated), so the budget command skips it; the 100 ms p99 check above is separate.
Each directory preserves `budget.md`, original `result.json`, `environment.json`, k6 summary and binary/source provenance.
`pg_stat_statements` was not preloaded; no normalized statement-statistics comparison is claimed.

[Local validation receipts](validation.json) cover the code of the final run. Expected baseline failures prove the inline-fetch
and retained-generation regressions; the final code passes them. The existing opt-in `TestSyncWorkerMemory` is the only skipped
API test. These receipts do not claim GitHub CI, CodeQL or independent human approval.

Reproduce with the historical matching harness (both binaries built from the named archive), or with the final code:

```sh
export LICETUM_TEST_POSTGRES_URI=postgresql://... # a disposable local test PostgreSQL instance
export DB__POOL_SIZE=8 DB__MAX_OVERFLOW=0 SYNC__DBOS_POOL_SIZE=8 GOMAXPROCS=4
export LOG__SUBJECT_SALT=p3-reproducible-loadtest
licetum-loadtest run -profile medium -mode mix -seed 1 -rate 80 \
  -warm 60s -measure 180s -slo-p99 100ms -db-pool 16 \
  -check-cpus 0-2 -k6-cpus 3 -check-bin /path/to/licetum \
  -k6-bin /path/to/k6 -out /path/to/results
licetum-loadtest budget -budgets loadtest/budgets.yaml /path/to/results
```

Historical archives were built with `go build -buildvcs=false` (and `CGO_ENABLED=0` for the PDP); their hashes are in provenance.
The final binaries were built from the clean code commit with normal VCS stamping. Original report files are preserved unchanged.
For the historical runs, `environment.json.git_commit` names the launch directory's HEAD (`72dd04a`), not the archived binary's source;
use `provenance.json.source_commit` and the matching `check_binary_sha256` for the measured historical code. The salt in these
synthetic fixture reports is a fixed public test value, not a production secret.

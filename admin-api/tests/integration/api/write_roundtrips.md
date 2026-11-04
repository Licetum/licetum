# Measured admin PostgreSQL round trips

Measured on PostgreSQL 16.15 and Go 1.26.10, with two independent fixtures per route. Each budget is the maximum actually observed; no allowance is added. Authorization is primed at the final fixture revision before measuring.

`total` counts application-pool pgx calls throughout the HTTP request, including authorization, preflight and autocommit scenario writes. `write` counts calls inside explicit read-write transactions, including BEGIN and COMMIT; read-only snapshots are excluded. A pgx.Batch or CopyFrom is one call. Existing per-statement query metrics are unchanged.

A refresh request waits for a worker; the worker uses its own context and pool. Its background apply is outside this HTTP-request measurement. Synchronizer create/delete run ChangeMode inline and are measured separately. Compound budgets describe the shared catalogue request, including its operation count.

| Operation | Class | Total | Write transaction | Transactions | Samples |
|---|---|---:|---:|---:|---:|
| `create_access_group` | standalone | 13 | 10 | 1 | 2 |
| `create_assignment_rule` | standalone | 12 | 8 | 1 | 2 |
| `create_group` | standalone | 11 | 8 | 1 | 2 |
| `create_group_member` | standalone | 11 | 8 | 1 | 2 |
| `create_pep_client` | standalone | 12 | 9 | 1 | 2 |
| `create_policy_scenario` | standalone | 4 | 0 | 0 | 2 |
| `create_role` | standalone | 11 | 8 | 1 | 2 |
| `create_role_permission` | standalone | 14 | 10 | 1 | 2 |
| `create_service` | standalone | 10 | 7 | 1 | 2 |
| `create_synchronizer` | change_mode | 21 | 15 | 1 | 2 |
| `create_type_resource` | standalone | 18 | 15 | 1 | 2 |
| `delete_access_group` | standalone | 13 | 10 | 1 | 2 |
| `delete_assignment_rule` | standalone | 14 | 9 | 1 | 2 |
| `delete_directory_entry` | standalone | 7 | 4 | 1 | 2 |
| `delete_group` | standalone | 14 | 11 | 1 | 2 |
| `delete_group_member` | standalone | 11 | 8 | 1 | 2 |
| `delete_pep_client` | standalone | 15 | 13 | 1 | 2 |
| `delete_pep_issuer` | standalone | 11 | 9 | 1 | 2 |
| `delete_policy_scenario` | standalone | 3 | 0 | 0 | 2 |
| `delete_role` | standalone | 14 | 11 | 1 | 2 |
| `delete_role_permission` | standalone | 14 | 9 | 1 | 2 |
| `delete_service` | standalone | 11 | 8 | 1 | 2 |
| `delete_synchronizer` | change_mode | 26 | 23 | 1 | 2 |
| `delete_type_resource` | standalone | 17 | 14 | 1 | 2 |
| `import_policy` | compound | 17 | 8 | 1 | 2 |
| `issue_pep_key` | standalone | 17 | 12 | 1 | 2 |
| `preview_assignment_rule` | standalone | 15 | 0 | 0 | 2 |
| `put_directory` | standalone | 8 | 5 | 1 | 2 |
| `put_directory_settings` | standalone | 9 | 6 | 1 | 2 |
| `put_log_settings` | standalone | 10 | 7 | 1 | 2 |
| `put_pep_issuer` | standalone | 10 | 8 | 1 | 2 |
| `refresh_resources_of_type` | synchronization | 8 | 0 | 0 | 2 |
| `revert_change` | compound | 15 | 11 | 1 | 2 |
| `revoke_pep_key` | standalone | 13 | 10 | 1 | 2 |
| `rollback_changes` | compound | 15 | 11 | 1 | 2 |
| `run_batch` | compound | 11 | 8 | 1 | 2 |
| `run_policy_tests` | standalone | 14 | 0 | 0 | 2 |
| `simulate_evaluation` | standalone | 16 | 0 | 0 | 2 |
| `simulate_evaluations` | standalone | 16 | 0 | 0 | 2 |
| `simulate_search_action` | standalone | 11 | 0 | 0 | 2 |
| `simulate_search_resource` | standalone | 11 | 0 | 0 | 2 |
| `simulate_search_subject` | standalone | 17 | 0 | 0 | 2 |
| `test_synchronizer` | standalone | 9 | 0 | 0 | 2 |
| `update_access_group` | standalone | 14 | 11 | 1 | 2 |
| `update_assignment_rule` | standalone | 16 | 10 | 1 | 2 |
| `update_group` | standalone | 12 | 9 | 1 | 2 |
| `update_group_member` | standalone | 12 | 9 | 1 | 2 |
| `update_pep_client` | standalone | 12 | 10 | 1 | 2 |
| `update_policy_scenario` | standalone | 3 | 0 | 0 | 2 |
| `update_role` | standalone | 12 | 9 | 1 | 2 |
| `update_role_permission` | standalone | 18 | 12 | 1 | 2 |
| `update_service` | standalone | 12 | 8 | 1 | 2 |
| `update_synchronizer` | standalone | 14 | 8 | 1 | 2 |
| `update_type_resource` | standalone | 16 | 13 | 1 | 2 |

Reproduce the contract:

```sh
LICETUM_TEST_POSTGRES_URI=postgresql://... go test -tags integration ./tests/integration/api -run TestContract_WR06 -count=1
```

Record a new measurement after an intentional SQL or catalogue change:

```sh
LICETUM_UPDATE_WRITE_BUDGETS=1 LICETUM_TEST_POSTGRES_URI=postgresql://... go test -v -tags integration ./tests/integration/api -run TestContract_WR06 -count=2
```

The update mode records maxima and sample counts from actual successful requests. Run the ordinary contract afterwards. New routes, orphan budgets, changed method/path/class and increased counts fail the ordinary contract.

The one-off Toxiproxy check of the retired F6-7 used 150 ms per round trip.
The slowest of five warmed direct rule writes took 1.367 s (9.12 round trips);
the tracer counted 9 total calls and 8 calls in the write transaction, against
11 statements. This agrees with the measured latency. The old fixed six-trip
assertion failed and is replaced by the per-route WR06 budgets above.

# Multi-region test environment (F6, tier R)

> **Switched off.** The nightly workflow does not run this environment (`if: false` in `nightly-suite.yml`) and the files below are
> not kept up to date: they still describe a deployment that no longer exists — a Redis in each region, read replicas read by the
> PDP (`DB__READ_URI`, `DB__MAX_REPLICA_LAG`), `WORKER__REGION` and the table synchronization engine. All of them were removed from
> the product. The environment is rewritten with the regional deployment (ADR 0001, §10.7); until then only `task go:vet:tags`
> keeps it compiling. Nothing here describes how to deploy Licetum: see [`docs/deployment.md`](../../../docs/deployment.md).

Three "regions" on one machine, with the latency of the links between them:

```
region A (the primary's)         region B                      region C
  check-a1, check-a2               check-b1, check-b2            check-c1, check-c2
  admin-a, worker-a, migrate       admin-b                       -
  redis-a                          redis-b                       redis-c
  pg-primary ◄── toxiproxy ──────► pg-replica-b                  pg-replica-c
  source (only region A reaches it)
```

- One PostgreSQL primary and a streaming replica in each of the other regions. The check pods of B and C read their replica
  (`DB__READ_URI`) and write to the primary through **toxiproxy**, which adds the latency of the link (`setLink`: both the path of the
  writes and the replication stream). A region cannot reach the primary directly: it is on the network of region A and the `wan`
  network, and the pods of B and C are on their own network and the proxy's.
- A Redis per region, no replication between them.
- The **source** (nginx, on the network of region A only) serves the type `branch`, whose synchronizer lists the region `a`; the worker of
  A (`WORKER__REGION=a`, table engine) is the only one that asks it. The log of the source says who asked.
- The identity provider is the test process (`core/testsupport` `NewIdPForContainers`), reached from the containers as
  `host.docker.internal`.
- A load generator per region is the test process, sending to the ports of that region's check pods.

## Running

```
task go:test:multiregion
```

builds the images (`licetum:mr`, `licetum-admin:mr`), starts the stack (`docker compose up -d`), waits until the pods are ready, seeds a policy, runs the scenarios
and takes the stack down. Environment of the scenarios:

| variable | |
|---|---|
| `MR_KEEP=1` | leave the stack up after the run |
| `MR_EXTERNAL=1` + `MR_IDP_URL` | the stack is already up |
| `LICETUM_F6_REVOCATIONS` (200), `LICETUM_F6_STRONG_READS` (200), `LICETUM_F6_CUT_SECONDS` (60), `LICETUM_F6_LOAD_SECONDS` (600; 120 for F6-9) | length of the scenarios |

## Scenarios

| test | criterion |
|---|---|
| `TestF6_1_*` | p99 of a warm request in a region 150 ms from the primary ≤ 30 ms |
| `TestF6_2_*` | a denial written in region A is enforced in region B at p99 ≤ 5 s over 200 revocations |
| `TestF6_3_*` | `at_least_as_fresh` in B is answered correctly at once, also with the replication stream delayed by 2 s |
| `TestF6_4_*` | the lag above `DB__MAX_REPLICA_LAG` (5 s here) makes B not ready within 15 s, and it recovers |
| `TestF6_5_*` | the link of B is cut for 60 s: it answers within `POLICY__MAX_STALENESS` (20 s here), then fails closed (503); the denial made meanwhile is enforced within 5 s of the link's return; this needs `wal_retrieve_retry_interval` of 1 s or less on the standby (`postgres/replica-entrypoint.sh` sets it): once the link is back a standby asks for its stream again only that often (5 s by default) and replays nothing until it does, which would use up the 5 s before the replay starts |
| `TestF6_6_*` | the source is asked by the worker of A (and the admin API of A once, when the synchronizer was created), never by a check pod |
| `TestF6_7_*` | an admin write from B: reported (the write itself is six round trips, asserted in `chaos/f6_test.go`) |
| `TestF6_8_*` | 600 requests a second over the three regions split 50/30/20 for 10 minutes: ≤ 0.1 % errors, p99 within 50 ms, no wrong answers |
| `TestF6_9_*` | region C is stopped under the load; the others take its traffic |

F6-10 (a cell opens no connection outside itself) is `cluster/f6_cell_test.go` and the network policy of the chart
(`deploy/helm/test.sh`); F6-11 (failover) is `cluster/failover_test.go` `TestF6_11_*` and the runbook in `docs/deployment.md`.

The same scenarios run in the nightly workflow `.github/workflows/multiregion.yml` (and on demand). They were written without a Docker daemon at hand: they compile
(`go vet -tags multiregion`) and `docker compose config` accepts the stack, but the first run on a machine with Docker may need adjustments.

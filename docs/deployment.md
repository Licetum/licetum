# Deploying Licetum in several regions

This is the topology Licetum is built and tested for: several `pdp` instances in several regions behind one domain, at least
600 requests a second of the PDP in total, with stable availability and latency. The Helm chart is
[`deploy/helm/licetum`](../deploy/helm/licetum); the settings are described in [`core/README.md`](../core/README.md).

## Topology

```
                      GeoDNS / Anycast / global load balancer   (outside Licetum)
                 ┌────────────────────┼────────────────────┐
            region A (primary)    region B               region C
            ┌───────────────┐    ┌───────────────┐    ┌───────────────┐
            │ pdp × n       │    │ pdp × n       │    │ pdp × n       │
            │ admin, worker │    │               │    │               │
            │ migrate (job) │    │               │    │               │
            │ Redis         │    │ Redis         │    │ Redis         │
            │ PostgreSQL    │    │ replica       │    │ replica       │
            │   PRIMARY  ───┼───►│   (streaming) │    │   (streaming) │
            └───────┬───────┘    └───────────────┘    └───────────────┘
                    └──────────────────────────────────────►
```

- **PostgreSQL**: one primary and asynchronous streaming replicas, one or more per region (Patroni, CloudNativePG, Aurora Global
  or similar; the chart takes `secrets.dbUri` and `secrets.dbReadUri`). **Not multi-master**: advisory locks are node-local there, and the
  uniqueness checks and the last-admin check are not linearizable.
- **Redis**: an independent instance per region, with **no replication between regions**. It holds regional caches only (answers,
  verified tokens, fetched source lists). Losing or flushing it never changes a decision; it makes the pods slower for a moment
  (`licetum_degraded{component="redis"}`).
- **Pods**: `pdp` in every region (reads the policy from the replica of its region, writes nothing to the graph); `admin`, the
  `worker` and the migration Job in the region of the primary only. A PDP pod of another region writes to the primary only
  rarely: it queues a synchronization job, and records a delegated call in the `strict` audit mode.
- **Entry**: a GeoDNS, an Anycast address or a global load balancer sends a client to the nearest region whose pods are ready. That
  layer is not part of Licetum. `/readyz` is what it should probe (the reasons are in `core/README.md`).
- **The revision and the data it numbers travel in one stream** (`revision_state` is a table of the same database): a region that
  sees revision R in its replica has the data of R.

### What is regional and what is global

| | where | notes |
|---|---|---|
| the policy (services, types, rules, groups, roles) | global, one primary | written by the admin API in the primary region |
| the copy of the policy a pod decides from | per pod, in memory | built from the replica, renewed every `REVISION__POLL_INTERVAL` |
| the resource graph (`resource_cache`, the closure) | global, one primary | written by the worker only |
| answers, tokens, source lists | per region, in Redis and in memory | disposable |
| the audit of delegated calls, known subjects | global | written to the primary (async mode buffers them) |
| the change log | global | written in the transaction of the change |

## Settings of a region

| setting | primary region | other regions |
|---|---|---|
| `secrets.dbUri` | the primary | the primary (writes, strong reads), through PgBouncer when the link is long |
| `secrets.dbReadUri` | optional (the primary serves the reads) | the replica of the region |
| `admin.enabled`, `worker.enabled`, `migrate.enabled` | `true` | `false` |
| `pdp.replicas` / `pdp.autoscaling` | as needed | as needed |
| `db.lagAction` | `unready` or `primary` | `unready` (the balancer moves the traffic) or `primary` (reads cross the region meanwhile) |
| `db.maxReplicaLag` | 10 s | 10 s |
| `policyMaxStaleness` | the longest time a pod may answer without having confirmed the policy | the same |
| `worker.engine` | `postgres` when the worker serves sources of other regions; `dbos` next to the database | - |

The worker needs a fast link to PostgreSQL (`SYNC__ENGINE=dbos` measured ~1.4 s of overhead at 150 ms): place it next to the
primary, or use the table engine (`worker.engine: postgres`). With `worker.regionAware` a worker takes only the synchronization jobs of the
types whose synchronizer lists its region (`regions` of the synchronizer, empty: anywhere) - for a source that can be reached from
one region only. That needs the table engine.

### The administration far from the primary

The admin API writes to the primary and reads it for every request (a person who has just saved a rule must see it). A write is six
round trips at most (`chaos/f6_test.go` keeps it so); the authorization of the request and the refresh of the policy after a write add
more. With 150 ms to the primary an admin write takes about a second, and a page of the interface several. It works, but run the
administration in the region of the primary: that is the supported placement.

## Sizing of pools and connections

A PDP pod holds up to `DB__POOL_SIZE + DB__MAX_OVERFLOW` connections to the **replica** (the read pool) and as many to the
**primary** (writes, strong reads, queueing synchronizations), plus the pool of the DBOS client (`SYNC__DBOS_POOL_SIZE`, 20) when
the engine is `dbos`. The pools open connections as they are needed, but keep `DB__POOL_SIZE` of them idle once opened. The
connections of all the pods add up against `max_connections` of each server (`licetum_db_pool_*`, the line `database connections` at start):

```
replica   connections = PDP pods of the region x (DB__POOL_SIZE + DB__MAX_OVERFLOW)
primary   connections = PDP pods of all regions x (DB__POOL_SIZE + DB__MAX_OVERFLOW)
                      + admin + worker (DB__SYNC_POOL_SIZE) + the migration Job
```

With many pods lower `DB__POOL_SIZE` and `DB__MAX_OVERFLOW`, or put PgBouncer in transaction mode (`db.pgbouncer`) in front of the
primary (and the replicas) of the remote regions; a long link makes every connection costly to open.
The compute concurrency (`COMPUTE__CONCURRENCY`) bounds how many answers a pod computes at once.

## Revocation: the SLO

A rule that takes access away (a denial, a deleted rule, a removed membership) is enforced by every region within a bound:

```
time to enforcement  <=  replication lag of the region  +  REVISION__POLL_INTERVAL (250 ms)  +  the rebuild of the snapshot
```

The objective is **p99 <= 5 s** (decision D3), measured over 200 revocations in the multi-region test environment
(`admin-api/tests/multiregion`). A client that needs a write at once passes the revision of the write
(`X-Authz-Revision`) as `at_least_as_fresh`: the region waits for its replica for up to `REVISION__WAIT_MAX`, then answers from
the primary. A replica that lags more than `DB__MAX_REPLICA_LAG` is taken out of rotation (`unready`) or bypassed (`primary`).

## Failure matrix

| failure | what happens | what to do |
|---|---|---|
| **a replica is lost** | the pods of the region cannot read it: `/readyz` fails, the views age. Within `POLICY__MAX_STALENESS` they answer from memory, beyond it 503 `policy_unavailable` (**fail closed**, decision D2). The balancer moves the traffic to another region | restore or replace the replica; the pods recover by themselves |
| **the link between regions is cut** | a region keeps answering from the policy it has for `POLICY__MAX_STALENESS` (no limit by default: set one), then fails closed. Writes from that region fail. The region catches up when the link returns; revocations made meanwhile are enforced within the SLO | set `policyMaxStaleness` to the longest staleness you accept |
| **the replica lags** | above `DB__MAX_REPLICA_LAG`: not ready (`unready`) or reads from the primary (`primary`); `licetum_replica_lag_seconds` | find the cause (network, load, a long query on the replica) |
| **the primary fails over** (a replica is promoted) | see below | run `licetum failover` after the promotion |
| **Redis is lost** in a region | the pods compute answers from memory and the replica, slower; no decision changes. Verified tokens are verified again | restore Redis |
| **a region is lost** | the global load balancer moves its traffic; the other regions take it (plan for 1.5-2x the load per region) | - |
| **PostgreSQL is gone everywhere** | the pods answer from memory until `POLICY__MAX_STALENESS`, then fail closed | restore PostgreSQL |

### Failing over the primary

PostgreSQL replication is asynchronous: a promoted replica may be missing the last transactions of the old primary, **including
revision bumps the other regions have already seen**. After the promotion the new primary would number its next writes with
revisions the regions already used for other content, and answers cached under such a revision would be wrong. Run, once, against the
new primary and before the traffic is sent to it:

```
licetum failover            # raises every revision of revision_state by one jump (default 1 000 000), in one transaction
```

Every view and every cached answer is then older than the data, and is replaced within `REVISION__POLL_INTERVAL`. Check that
`licetum_revision_regressions_total` stops growing (a process counts a revision that went backwards).

## Cells (no cross-border replication)

A **cell** has its own PostgreSQL and Redis and no network path to any other cell; the China deployment is one (decision D4). It is a
complete installation (`deploy/helm/licetum/values-cell.yaml`: the network policy allows PostgreSQL and Redis of the cell and nothing
else), with its own administrators and its own policy. The policy is carried between cells as a document
(`GET /service/{id}/policy/export` and `POST .../policy/import`), never by replication, so a cell never depends on another one being
reachable. `deploy/helm/test.sh` checks that the network policy of the profile names nothing outside the cell.

## Upgrading

1. **Migrations** run once, by the `migrate` Job of `helm upgrade` in the primary region (a hook; the migrations take an advisory
   lock, so a second run waits). Every migration works with the previous release (expand/contract, `CONTRIBUTING.md`).
2. The new schema reaches the replicas by replication. A new PDP pod is **not ready** until the schema its pool reads is at least
   the one of the release, so a region rolls out only after its replica has replayed the migration.
3. Roll out the primary region (admin, worker), then the others, one region at a time. A rolling update of the PDP pods loses no
   request: `maxUnavailable: 0`, readiness, a `preStop` sleep of `shutdown.drain`, the drain of the process and
   `terminationGracePeriodSeconds` of `2 x drain + timeout + 10`.
4. A release that drops what the previous one uses is split in two: the release that stops using it, and, after it has been
   rolled out everywhere, the release that drops it.

# Deploying Licetum

This is the topology Licetum is built and tested for in this release: several `pdp` pods behind one address, at least
600 requests a second of the PDP in total, with stable availability and latency, **all next to one PostgreSQL**. The Helm
chart is [`deploy/helm/licetum`](../deploy/helm/licetum); the settings are described in [`core/README.md`](../core/README.md).

A deployment in several regions is not covered by this release. A PDP no longer routes its reads to a replica and keeps no
regional cache, so a pod far from the database pays the link on every request it has to compute and on every revision poll
(see [Several regions](#several-regions)).

## Topology

```
                         load balancer / ingress   (outside Licetum)
                                      │
          ┌───────────────────────────┴───────────────────────────┐
          │  pdp × n                                               │
          │  admin, worker                                         │
          │  migrate (job)                                         │
          │                 PostgreSQL   (a primary; a standby    │
          │                               for availability)       │
          └────────────────────────────────────────────────────────┘
```

- **PostgreSQL**: one primary, with a standby for availability if you want it (Patroni, CloudNativePG, a managed service; the
  chart takes `secrets.dbUri`, the address of the primary). **Not multi-master**: advisory locks are node-local there, and the
  uniqueness checks and the last-admin check are not linearizable. There is no other store: answers are kept in the memory of
  each pod, and losing a pod loses only a cache.
- **Pods**: `pdp` (reads the policy from PostgreSQL into memory, writes nothing to the graph); `admin`, the `worker` and the
  migration Job. A PDP pod writes to PostgreSQL only rarely: it queues a synchronization job.
- **Entry**: a load balancer or an ingress sends a client to a ready pod. That layer is not part of Licetum. It should use the
  readiness of the pod on its ops port (`:9090`), not on the public one (the reasons are in `core/README.md`). The ops port is
  reached from inside the cluster (the Service's `ops` port), so a layer outside the cluster needs a health check that runs there.
- **The revision and the data it numbers travel in one stream** (`revision_state` is a table of the same database): a pod that
  sees revision R has the data of R.

### The ports of a PDP

| port | setting | serves | reached by |
|---|---|---|---|
| `8001` | `PDP__ADDR` (`pdp.port`) | the AuthZEN API | the PEP clients of the services, through the entry layer |
| `9090` | `PDP__OPS_ADDR` (`pdp.opsPort`) | `/healthz`, `/readyz`, `/metrics` | the kubelet and the health checks of the entry layer; Prometheus, from `networkPolicy.monitoringNamespace` |

The public port serves no probe and no metric. The chart's probes, its Service port `ops` and its ServiceMonitor use the ops port.

### Where the state lives

| | where | notes |
|---|---|---|
| the policy (services, types, rules, groups, roles) | PostgreSQL | written by the admin API |
| the copy of the policy a pod decides from | per pod, in memory | built from PostgreSQL, renewed every `REVISION__POLL_INTERVAL` |
| the resource graph (`resource_cache`, the closure) | PostgreSQL | written by the worker only |
| answers | per pod, in memory | disposable |
| the subject directory | PostgreSQL | written through the admin API |
| the change log | PostgreSQL | written in the transaction of the change |

## Settings

| setting | meaning |
|---|---|
| `secrets.dbUri` | the PostgreSQL every process reads and writes; through PgBouncer when the link is long |
| `secrets.dbMigrateUri` | where `licetum migrate` connects when `secrets.dbUri` is behind PgBouncer: PostgreSQL itself |
| `admin.enabled`, `worker.enabled`, `migrate.enabled` | `true` for the installation that runs them (one per database) |
| `pdp.replicas` / `pdp.autoscaling` | as needed |
| `db.pgbouncer` | `true` behind PgBouncer in transaction mode |
| `policyMaxStaleness` | the longest time a pod may answer without having confirmed the policy against PostgreSQL |
| `pdp.maxInflight` | the requests a PDP pod serves at once, see `core/README.md` |

Settings that no longer exist (`secrets.redisUri`, `secrets.dbReadUri`, `db.maxReplicaLag`, `db.lagAction`, `worker.engine`,
`worker.regionAware`, `networkPolicy.redisCidrs`, `networkPolicy.redisPort`) are ignored by the chart: it has no schema of its
values, so a values file that still sets them renders without them and gives no error. Remove them.

### The worker

The worker needs a fast link to PostgreSQL: the synchronization runs on DBOS, which measured about 1.4 s of overhead per call at
a round trip of 150 ms. Run it next to the database. The `regions` of a synchronizer are stored and shown but do not choose an
executor: any worker may ask any source.

### The administration far from the database

The admin API writes to PostgreSQL and reads it for every request (a person who has just saved a rule must see it). A write is six
round trips at most (`chaos/f6_test.go` keeps it so); the authorization of the request and the refresh of the policy after a write add
more. With 150 ms to the database an admin write takes about a second, and a page of the interface several. It works, but run the
administration next to the database: that is the supported placement.

### Limits per client

The rate limit of a PEP client (`PEP__RATE_LIMIT`, `PEP__RATE_BURST`, passed through `env`) is kept **per pod**: with `n` pods a
client may make up to `n` times the limit. A limit that holds for the whole cluster belongs to the ingress or the gateway in front
of the PDP; Licetum keeps no shared counter.

## Several regions

Earlier releases put a PDP in each region on a streaming replica of the primary and a Redis of the region. That is gone: a PDP
reads the one PostgreSQL named by `DB__URI`, and nothing in the process knows about replicas, regions or a second cache. Until
the PostgreSQL store with read replicas returns (planned with the next phases of the regional design: the PostgreSQL adapter on
streaming replicas, then the distributed store) a PDP belongs in the region of its database. A pod placed elsewhere works, but:

- every request it has to compute (a cache miss) and every poll of the revision pays the round trip to the database;
- a lost link is a lost database for that pod (see the failure matrix), and the balancer cannot tell the difference from a
  healthy region;
- the revocation bound below grows with the link.

This guide is rewritten when the regional deployment returns.

## Sizing of pools and connections

A PDP pod holds up to `DB__POOL_SIZE + DB__MAX_OVERFLOW` connections to PostgreSQL, plus the pool of the DBOS client
(`SYNC__DBOS_POOL_SIZE`, 20). The pools open connections as they are needed, but keep `DB__POOL_SIZE` of them idle once opened. The
connections of all the pods add up against `max_connections` of the server (`licetum_db_pool_*`, the line `database connections` at start):

```
connections = PDP pods x (DB__POOL_SIZE + DB__MAX_OVERFLOW + SYNC__DBOS_POOL_SIZE)
            + admin + worker (DB__SYNC_POOL_SIZE + request pool + DBOS pool) + the migration Job
```

With many pods lower `DB__POOL_SIZE`, `DB__MAX_OVERFLOW` and `SYNC__DBOS_POOL_SIZE`, or put PgBouncer in transaction mode
(`db.pgbouncer`) in front (DBOS needs PgBouncer 1.21 or newer with `max_prepared_statements`, see `core/README.md`).
The compute concurrency (`COMPUTE__CONCURRENCY`) bounds how many answers a pod computes at once.

## Revocation: the bound

A rule that takes access away (a denial, a deleted rule, a removed membership) is enforced by every pod within a bound:

```
time to enforcement  <=  REVISION__POLL_INTERVAL (250 ms)  +  the rebuild of the snapshot
```

A client that needs a write at once passes the revision of the write (`X-Authz-Revision`) as `context.licetum.min_revision`: the
pod waits for that revision for up to `REVISION__WAIT_MAX`, then answers from the data. The objective of 5 s at p99 for a
region far from the primary (decision D3) was measured in the multi-region test environment, which is switched off with the
regional deployment (`admin-api/tests/multiregion`).

## Failure matrix

| failure | what happens | what to do |
|---|---|---|
| **a pod is lost** | the load balancer stops sending to it (readiness); the others take its share | plan for the loss of one pod in the sizing |
| **PostgreSQL is unreachable from a pod** (the link is cut, the database is gone) | the pod answers from memory within `POLICY__MAX_STALENESS` (no limit by default: set one), beyond it 503 `policy_unavailable` (**fail closed**, decision D2); `/readyz` fails, so a balancer that obeys it moves the traffic. Revocations made meanwhile are not seen by that pod; it catches up when the link returns | set `policyMaxStaleness` to the longest staleness you accept |
| **PostgreSQL is gone everywhere** | the pods answer from memory until `POLICY__MAX_STALENESS`, then fail closed | restore PostgreSQL |
| **the primary fails over** (a standby is promoted) | see below | run `licetum failover` after the promotion |
| **the worker is lost** | the data of the resource graph stops following the sources; decisions use what exists, the other processes keep answering; a replacement worker takes over the unfinished jobs after `SYNC__LEASE` | start the worker again |

### Failing over the primary

PostgreSQL replication is asynchronous: a promoted standby may be missing the last transactions of the old primary, **including
revision bumps the pods have already seen**. After the promotion the new primary would number its next writes with
revisions the pods already used for other content, and answers cached under such a revision would be wrong. Run, once, against the
new primary and before the traffic is sent to it:

```
licetum failover            # raises every revision of revision_state by one jump (default 1 000 000), in one transaction
```

Every view and every cached answer is then older than the data, and is replaced within `REVISION__POLL_INTERVAL`. Check that
`licetum_revision_regressions_total` stops growing (a process counts a revision that went backwards).

## Cells (no cross-border replication)

A **cell** has its own PostgreSQL and no network path to any other cell; the China deployment is one (decision D4). It is a
complete installation (`deploy/helm/licetum/values-cell.yaml`: the network policy allows the PostgreSQL of the cell and nothing
else), with its own administrators and its own policy. The policy is carried between cells as a document
(`GET /service/{id}/policy/export` and `POST .../policy/import`), never by replication, so a cell never depends on another one being
reachable. `deploy/helm/test.sh` checks that the network policy of the profile names nothing outside the cell.

## Upgrading

1. **Migrations** run once, by the `migrate` Job of `helm upgrade` (a hook; the migrations take an advisory
   lock, so a second run waits). Every migration works with the previous release (expand/contract, `CONTRIBUTING.md`).
2. A new PDP pod is **not ready** until the schema its pool reads is at least the one of the release.
3. Roll out the admin and the worker, then the PDP pods. A rolling update of the PDP pods loses no
   request: `maxUnavailable: 0`, readiness, a `preStop` sleep of `shutdown.drain`, the drain of the process and
   `terminationGracePeriodSeconds` of `2 x drain + timeout + 10`.
4. A release that drops what the previous one uses is split in two: the release that stops using it, and, after it has been
   rolled out everywhere, the release that drops it.

The release that moves the PDP's probes and metrics to the ops port (`:9090`) is such a release: the public port stops answering
`/readyz`, `/healthz` and `/metrics` (they are a JSON 404 on `:8001`). Move every health check and scrape of a PDP pod to `:9090`
before the PDP pods are upgraded; the chart's own probes and ServiceMonitor already use it.

### Upgrading to this release

This release removes Redis, the routing of reads to a replica and the table synchronization engine. Nothing has to be migrated, but
the configuration and the monitoring need a clean-up:

- **Delete** `REDIS__URI` and `REDIS__HOT_TIMEOUT`, `DB__READ_URI`, `DB__MAX_REPLICA_LAG`, `DB__LAG_ACTION`, `DB__LAG_INTERVAL`,
  `SYNC__ENGINE`, `SYNC__FETCH_TTL`, `SYNC__LOCK_TTL`, `SYNC__LOCK_WAIT` and `WORKER__REGION` from the environment, and the
  matching chart values (`secrets.redisUri`, `secrets.dbReadUri`, `db.maxReplicaLag`, `db.lagAction`, `worker.engine`,
  `worker.regionAware`, `networkPolicy.redisCidrs`, `networkPolicy.redisPort`). A process that still has them starts and logs one
  warning per setting with the reason; the chart renders without them and says nothing.
- **Stop running Redis** for Licetum. Nothing reads it; the PDP keeps its answers in memory only, so after a restart a pod
  computes what it does not find.
- **`SYNC__ENGINE=postgres`** users move to DBOS (the only engine). Put the workers next to the database: DBOS needs a fast
  link (about 1.4 s of overhead at 150 ms). The `sync_job` rows an old worker left queued are never run; the PDP asks through
  DBOS. The first worker creates the DBOS tables in the schema `SYNC__DBOS_SCHEMA` (default `dbos`): the role of the workers
  needs the right to create it. Behind PgBouncer in transaction mode, DBOS needs version 1.21 or newer with
  `max_prepared_statements`, or `SYNC__DBOS_URI` pointing at PostgreSQL.
- **A synchronizer's `cache_ttl`** is ignored (a body that carries it is accepted); every synchronization asks the source, and
  the repeated reads of a list are spaced by `SYNC__FRESH_MIN_INTERVAL` and the backoff only.
- **Dashboards and alerts** on `licetum_redis_*`, `licetum_replica_*`, `licetum_breaker_open_total` and
  `licetum_degraded{component="redis"}` stop reporting: remove them. The `sync_job` branch of `licetum_sync_jobs` is gone (the
  gauge counts the DBOS queue). `/readyz` no longer fails with the reason `replica_lag`.
- **Multi-region**: a PDP that read a regional replica now reads the PostgreSQL named by `DB__URI`; see
  [Several regions](#several-regions) before you upgrade such a deployment.

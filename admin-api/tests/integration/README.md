# Integration, multi-replica and fault injection tests

Tests that need PostgreSQL and Redis. They start disposable containers (Testcontainers; Docker is needed), or use running
servers named by `LICETUM_TEST_POSTGRES_URI` and `LICETUM_TEST_REDIS_URI`.

| directory | build tag | tier | what |
|---|---|---|---|
| `api/` | `integration` | I | the admin and check APIs against real servers |
| `cluster/` | `integration` | M | several check replicas on one PostgreSQL and one Redis; request cost counters; the fakes |
| `chaos/` | `chaos` | C | Toxiproxy in front of PostgreSQL and Redis: latency, cut, black hole, a read-only Redis |
| `contracts/` | `integration` | I | the engine runner of the decision contracts (DEC* in `contracts.yaml`) and their tests |
| `harness/` | none | | the helpers the packages above share (not tests) |

```sh
task go:test:integration   # tags integration: api/, cluster/, contracts/ (and core/tests/integration)
task go:test:chaos         # tag chaos: chaos/ (slower, nightly in .github/workflows/chaos.yml)
task go:vet:tags           # no services: compiles and vets all the tagged code (integration, chaos, multiregion), with and without -race
```

The chaos tests and the multi-region environment run only nightly, but `task go:vet:tags` is part of `task check` (`task go:lint`, the CI job
`go-lint`), so a change that stops them compiling fails the pull request. A new build tag goes into `GO_BUILD_TAGS` of `Taskfile.yaml`.

## The decision contracts (P1)

`contracts.Load(t, infra, fixture)` loads a fixture of `core/testsupport/policyfix` (YAML under its `testdata/decisions`) into a
cluster of its own (`harness.StartReplicas`, one check replica, a database per fixture): the services by slug, the types with their
parents, inherit masks and `access_implies`, an http synchronizer per type with resources fed by the fake source (names, the declared
parents, by the name of the parent type, and the attributes; a parent no resource has is an unresolved link), and the policy through
`POST /batch`. `Loaded.Ask(case)` asks the questions of a case through `POST /service/{slug}/check?consistency=higher` with `explain`, as
the case's subject (`preferred_username`) with its claims and request attributes, at the case's time: the replica's clock
(`engine.App.SetClock`) is the time of the case, so the validity windows and the conditions on the hour are decided then. The answers
name the rules of the fixture. `contracts.Compare` lists every difference from the expectations; `Loaded.ReplaceRules` swaps the policy
(and the resources) on the same types, for the property test. `Loaded.CrossCheck(case, answers)` asks the case's
`GET /service/{slug}/permission?consistency=higher` (same token, request attributes and time) and lists every question on which the
list disagrees with the check: a resource of a sync type is allowed exactly when the list names it with the action, a not_sync type as
a whole exactly when the action is among its levels (a sync type as a whole, `type_not_found` and `resource_not_found` are not compared).
The check decides the asked resources (`CoversOf`), the list every resource (`coverNodes`): the cross-check keeps the two paths equal.

| test | what it proves |
|---|---|
| `TestContract_DEC01_*` … `TestContract_DEC13_*` | the fixtures that name the contract get the answers they expect from the engine, and the permission list of each case agrees with them; a fixture that names several contracts is loaded and asked once per test binary; a contract without a fixture is skipped |
| `TestContract_DECP_*` | on the generated fixtures of the seeds (`LICETUM_DECP_SEEDS`, default 1-20) the engine answers like the reference evaluator, and the permission list of each case agrees with the check |
| `TestRunner_*` | every field of the fixture format reaches the engine as the fixture means it (validity windows and the hour at the case's time included), `ReplaceRules` leaves nothing of the previous policy, and `Compare` reports each mismatch; each case is cross-checked too |
| `TestCrossCheck*` | the cross-check reports a disagreement in either direction and a resource listed twice, and leaves out what the list cannot answer |

## The harness

`harness.StartReplicas(t, infra, n, opts...)` builds a `Cluster`: `n` check API replicas, each with **its own** `engine.App`,
database pool, Redis client and HTTP server, on one shared PostgreSQL schema and one shared Redis (which is flushed), next to an
admin API that sets the policy up, a fake identity provider (`support.IdP`) and a fake source of resources (`support.Source`).
Tests of a package run one after another: they share the Redis.

```go
c := harness.StartReplicas(t, infra, 3)
p := c.SeedPolicy("org", 0)                      // a service, two types fed by c.Source over HTTP, a rule for "alice"
res := c.Replicas[1].Permission(c, p.Slug, c.UserToken("alice"))
res.Trace.DBQueries()                            // what the request cost: statements to PostgreSQL ...
res.Trace.RedisRoundTrips()                      // ... and round trips to Redis (a pipeline is one)
c.Replicas[1].Warm(c, "GET", "/service/org/permission", token) // until the cost of a request is steady
```

- **Counting.** The same counts are in `GET /metrics` (`licetum_db_queries_total`, `licetum_db_query_seconds`,
  `licetum_redis_ops_total`, `licetum_redis_op_seconds`). The Trace does not count what the Redis client sends when it opens a
  connection (`HELLO`, ...), nor background work (writing a cache entry); the metrics do. Assert exact counts only after
  `Warm`, or `waitForKeys` for the cache entries to exist.
- **Fakes.** `support.Source` counts the requests per type (`Calls`, `CallsTo`, `History`), can be delayed (`SetDelay`), made to
  fail (`SetStatus`), padded (`SetBodySize`) or held (`Hold`/`Release`, `WaitForCalls`, `InFlight`). `support.IdP` counts the
  requests for its JWKS (`JWKSRequests`), signs tokens (`Token`), rotates its key (`RotateKey`) and can fail (`SetJWKSStatus`).
- **Faults.** `harness.WithFaultProxies()` puts a Toxiproxy proxy in front of PostgreSQL (`c.PostgresFault`) and Redis
  (`c.RedisFault`) for the replicas: `Latency`, `Cut`, `BlackHole`, `Reset`. Toxiproxy runs inside the test process (the
  toxiproxy server library, driven through its HTTP API), so no extra image is needed. `harness.WithReplicaRedis(uri)` points
  the replicas at another Redis, for the read-only test (`REPLICAOF` to an address nobody serves).
- **Levers.** The freshness of a request is `?consistency=higher` for its caller, and `engine.WithConsistency` in the context for
  the process that serves it (it wins over the query, and no network input sets it). `harness.Levers(handler)` is the test-only
  wrapper that turns the header `X-Test-Consistency` (`higher` or `minimize_latency`) into it; it lives under `tests/`, so no
  binary has it. `core/testsupport/authzenreq` builds the AuthZEN requests (`Fresh()`, `AtLeast(rev)`) and decodes the answers.
  A test that needs to know whether an answer lacked the data of a type reads `Trace.SyncPending()` of its own `engine.WithTrace`.

Name a test after the acceptance criterion it proves (`TestF3_4_…`) and say in the pull request which criteria it covers.

## The fast read path (F1)

| test | what it proves |
|---|---|
| `cluster/f1_test.go` `TestF1_1`, `_2`, `_3`, `_5`, `_6`, `_7`, `_8` | no query and no Redis command for a warm request; one round trip for an answer only Redis holds; a miss costs the same few queries for 1 000 and for 100 000 rules; slugs and deleted services follow the revision within a second; one key set fetch per TTL, none for bad tokens, one refetch per rotation |
| `api/snapshot_differential_test.go` `TestF1_11` | random policies (validity windows, claim-based and attribute-based rules, roles, nested groups, conditions, denials, exceptions) give the same answers and explanations from the snapshot and from the queries, also after changes and after windows have passed. `LICETUM_DIFF_SEED` repeats a run |
| `chaos/f1_test.go` `TestF1_4`, `_9`, `_10` | a warm request ignores 150 ms on PostgreSQL and Redis; a Redis that answers after 5 s does not pile up goroutines or memory under 600 rps; a skipped Redis adds at most its timeout |

The replicas follow the revision with a delay of up to `REVISION__POLL_INTERVAL`: `Replica.Permission` asks for at least the revision
of the last write made through `AdminDo`/`Must`; use `Replica.Do` for a plain request. Run the packages one at a time (`-p 1`) when they
share one Redis (`LICETUM_TEST_REDIS_URI`).

## The revision in PostgreSQL (F2)

| test | what it proves |
|---|---|
| `cluster/f2_test.go` `TestF2_1`, `_2` | a write and its revision commit together (a failed write or batch leaves it, a batch is +1), and the revisions of 20 concurrent writers are strictly increasing without gaps |
| `TestF2_3`, `_4`, `_5` | `at_least_as_fresh` gives read-your-writes on every replica (100 iterations); an answer for a revision the replica cannot confirm is computed from the data and not kept; a deny rule reaches every replica within a second without any revision asked for |
| `TestF2_6` | 50 synchronizations of service A leave every answer of service B a hit, while A's follow its data |
| `TestF2_8`, `_9`, `_10` | `FLUSHALL` while serving changes no answer; the Redis counters of the previous release are still bumped and the answer keys have the new format; the poller costs 4 statements a second at rest |
| `chaos/f2_test.go` `TestF2_7` | a revision bump under 1 500 hot keys at 600 rps: no failure, the pool never over its size |

Not covered yet: the read replica that lags by two seconds (F2-3, F2-4 in full) needs the read pool of F6; F2-11 (stage 2, scoped
bumps) is a separate pull request.

## Resilience and operations (F5)

| test | what it proves |
|---|---|
| `core/tests/integration/redisx` `TestF5_12_*` | random sequences of failures, successes and time never leave the failure counter of the breaker without an expiry; a counter the previous release left without one is repaired; concurrent failures are all counted (`LICETUM_BREAKER_SEED` repeats a run) |
| `core/tests/integration/db` `TestF5_10_*` | five `migrate` runs started together on an empty database apply each migration once and seed once (repeated over fresh schemas); `migrate` waits for the lock held by another run (deterministic); the schema version equals the latest embedded migration |
| `cluster/f5_test.go` `TestF5_1_*` | `/readyz` is 503 with PostgreSQL cut or the schema older than the binary and 200 otherwise (also with a *newer* schema), `/healthz` stays 200, the result is reused for a second, Redis is not part of readiness (`licetum_degraded{component="redis"}`) |
| `TestF5_3_*`, `TestF5_8_*` | a draining replica is not ready, serves, and asks for `Connection: close`; above `HTTP__MAX_INFLIGHT` the rest get 503 with `Retry-After` while the probes and the metrics are answered; a request past `HTTP__REQUEST_TIMEOUT` is answered 503 `request_timeout` |
| `TestF5_9_*` | a pool never holds more than `DB__POOL_SIZE + DB__MAX_OVERFLOW` connections under more concurrent users than connections, and the pool metrics say that acquires waited |
| `chaos/f5_test.go` `TestF5_2_*`, `TestF5_5_*` | Redis cut, black-holed or read-only (a spare Redis, see below) for a minute (`LICETUM_CHAOS_REDIS_OUTAGE` shortens it) at 600 requests a second: at most 1 % fail, PostgreSQL gets at most twice the statements of the healthy baseline, no key set is fetched again, at most 5 log lines of level warning a second, and the 99th percentile is back within the budget after Redis returns |
| `TestF5_6_*` | PostgreSQL cut: the replica answers from memory until `POLICY__MAX_STALENESS` has passed since it last confirmed the policy, then 503 `policy_unavailable`; with no limit it keeps answering; it recovers without a restart |
| `TestF5_7_*` | a rolling restart of three replicas at 600 requests a second behind a balancer that probes `/readyz`: no connection error and no 5xx (and 61 failures without the drain) |

Not covered here: F5-8 at three times the sustainable rate (a load test: `admin-api/loadtest`), and F5-11 (the previous release against the
schema of this one, `.github/workflows/ci.yml`, job `previous-release`).

### Read replica (F6)

`cluster/f6_test.go` needs a real streaming replica of the PostgreSQL of `LICETUM_TEST_POSTGRES_URI`: `LICETUM_TEST_REPLICA_ADDR=host:port`
(skipped without it). `scripts/pg-replica.sh up` starts a primary and a replica from the server binaries of the machine (no Docker) and prints
the two exports; `task go:test:integration:replica` does all of it. `harness.WithReadReplica()` gives the check replicas a read pool on it;
`Cluster.PauseReplay`/`ResumeReplay` (`pg_wal_replay_pause`) make it lag.

| test | proves |
|---|---|
| `TestF6_3_*` | with `at_least_as_fresh` the answer is current while the replica replays nothing (40 writes, 0 stale answers); a plain request is answered from the replica; nothing is kept under a revision the answer did not reflect |
| `TestF6_4_*` | the lag above `DB__MAX_REPLICA_LAG` makes `/readyz` 503 `replica_lag` (`unready`) or moves the reads to the primary (`primary`), within 15 s, and recovers by itself; `/healthz` stays 200 |
| `TestF6_5_*` | a replica behind `POLICY__MAX_STALENESS` fails closed with 503 `policy_unavailable` and recovers; with the link to the replica cut the region answers within the limit, then refuses, and enforces a revocation made meanwhile within the SLO once the link is back |
| `TestF6_1_*`, `TestF6_2_*` | the p99 of a warm request with 150 ms to the primary; the propagation of 50 revocations (`LICETUM_F6_REVOCATIONS`) |
| `TestF6_10_*` | while a cell works, the sockets of the process go only to the cell's own endpoints (`/proc/net/tcp`), and nothing connects to the decoys standing for a neighbouring cell |
| `TestF6_11_*` | after a promotion that lost the last writes, `licetum failover` makes the regions drop what they kept under the reused revisions (the test shows the stale answer without it) |
| `chaos/f6_test.go` | `TestF6_7_*`: an admin write with 150 ms to the primary costs at most six round trips; `TestF6_8_*`, `TestF6_9_*`: 600 requests a second over three regions split 50/30/20 (`LICETUM_F6_LOAD_SECONDS`), one region removed |
| `api/regions_test.go` | `synchronizer.regions` and `WORKER__REGION`: a source of one region is asked only by the workers of that region |

### Through PgBouncer (F5-9)

`LICETUM_TEST_PGBOUNCER_ADDR=host:port` makes the suites connect through a PgBouncer in **transaction mode** that stands in front of the
PostgreSQL of `LICETUM_TEST_POSTGRES_URI`, with `DB__PGBOUNCER=true`. PgBouncer does not carry the `search_path` of a connection from one
transaction to the next, so each test gets a database of its own (`Infrastructure.Isolate`) instead of a schema, and the migrations connect
to PostgreSQL directly (`DB__MIGRATE_URI`). The pooler needs `max_prepared_statements` (1.21 or newer) for DBOS, which prepares its
statements. A configuration that works (`task go:test:integration:pgbouncer` runs the suites with it):

```ini
[databases]
* = host=127.0.0.1 port=5432

[pgbouncer]
listen_addr = 127.0.0.1
listen_port = 6432
auth_type = trust
auth_file = /etc/pgbouncer/userlist.txt   ; "licetum" ""
pool_mode = transaction
max_client_conn = 2000
default_pool_size = 60
max_prepared_statements = 200
```

`TestF5_5` needs a Redis it can make read-only: it starts a container, or uses the running server named by
`LICETUM_TEST_REDIS_SPARE_URI` (and makes it writable again afterwards).

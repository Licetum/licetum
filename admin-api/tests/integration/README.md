# Integration, multi-replica and fault injection tests

Tests that need PostgreSQL and Redis. They start disposable containers (Testcontainers; Docker is needed), or use running
servers named by `LICETUM_TEST_POSTGRES_URI` and `LICETUM_TEST_REDIS_URI`.

| directory | build tag | tier | what |
|---|---|---|---|
| `api/` | `integration` | I | the admin API and the PDP against real servers |
| `cluster/` | `integration` | M | several PDP replicas on one PostgreSQL and one Redis; request cost counters; the fakes |
| `chaos/` | `chaos` | C | Toxiproxy in front of PostgreSQL and Redis: latency, cut, black hole, a read-only Redis |
| `contracts/` | `integration` | I | the engine runner of the decision contracts (DEC* in `contracts.yaml`) and their tests |
| `harness/` | none | | the helpers the packages above share (not tests) |

```sh
task go:test:integration   # tags integration: api/, cluster/, contracts/ (and core/tests/integration)
task go:test:chaos         # tag chaos: chaos/ (slower, nightly in .github/workflows/chaos.yml)
task go:vet:tags           # no services: compiles and vets all the tagged code (integration, chaos, multiregion), with and without -race
```

The chaos tests run only nightly, the multi-region environment is switched off until P7 (ADR 0001 §10.7), but `task go:vet:tags` is part of `task check` (`task go:lint`, the CI job
`go-lint`), so a change that stops them compiling fails the pull request. A new build tag goes into `GO_BUILD_TAGS` of `Taskfile.yaml`.

## The decision contracts (P1)

`contracts.Load(t, infra, fixture)` loads a fixture of `core/testsupport/policyfix` (YAML under its `testdata/decisions`) into a
cluster of its own (`harness.StartReplicas`, one PDP replica, a database per fixture): the services by slug, the types with their
parents, inherit masks and `access_implies`, an http synchronizer per type with resources fed by the fake source (names, the declared
parents, by the name of the parent type, and the attributes; a parent no resource has is an unresolved link), and the policy through
`POST /batch`. `Loaded.Ask(case)` asks the questions of a case through `POST /service/{slug}/access/v1/evaluations` under the lever `harness.Higher()` with
`explain` (requests built by `core/testsupport/authzenreq`, the explanation read from `context.reason_admin`; an evaluation the API
could not decide is a failure, not a denial), as the case's subject (`subject.id`, which is `preferred_username`) with its claims as `subject.properties` and the
request attributes in the context, at the case's time: the replica's clock (`engine.App.SetClock`) is the time of the case, so the
validity windows and the conditions on the hour are decided then. The answers name the rules of the fixture. `contracts.Compare`
lists every difference from the expectations; `Loaded.ReplaceRules` swaps the policy (and the resources) on the same types, for the
property test. `Loaded.CrossCheck(case, answers)` asks the searches of the case (`…/access/v1/search/resource` for each type with
resources and action that a question asks about a resource, `…/search/action` for each type without resources asked as a whole, and
one `search/resource` for that type, which must list nothing; same token, request attributes and time, read page by page) and lists
every question on which they disagree with the evaluations: a resource of a type with resources is allowed exactly when the search
lists it, a type without resources as a whole exactly when the action is among its levels (a type with resources as a whole,
a resource of a type without resources and `type_not_found` are not compared; a `resource_not_found` on a resource of a type with
resources is the exception: the resource search of its type and action must not list the resource). The evaluations decide the
asked resources (`CoversOf`), the searches every resource (`coverNodes`): the cross-check keeps the two paths equal.

| test | what it proves |
|---|---|
| `TestContract_DEC01_*` … `TestContract_DEC13_*` | the fixtures that name the contract get the answers they expect from the engine, and the searches and the older routes of each case agree with them; a fixture that names several contracts is loaded and asked once per test binary; a contract without a fixture is skipped |
| `TestContract_DECP_*` | on the generated fixtures of the seeds (`LICETUM_DECP_SEEDS`, default 1-20) the engine answers like the reference evaluator, and the searches and the older routes of each case agree with the evaluations |
| `TestRunner_*` | every field of the fixture format reaches the engine as the fixture means it (validity windows and the hour at the case's time included), `ReplaceRules` leaves nothing of the previous policy, and `Compare` and `Same` report each mismatch; each case is cross-checked and asked through the older routes too |
| `TestCrossCheck*`, `TestSearchesFor*`, `TestEvaluationsOf*` | the cross-check reports a disagreement in either direction, a resource listed twice and a resource listed for a type without resources, and leaves out what the searches cannot answer; each search is asked once; a case is asked as one evaluations request that does not change the fixture |

## The harness

`harness.StartReplicas(t, infra, n, opts...)` builds a `Cluster`: `n` PDP replicas, each with **its own** `engine.App`,
database pool, Redis client and HTTP server, on one shared PostgreSQL schema and one shared Redis (which is flushed), next to an
admin API that sets the policy up, a fake identity provider (`support.IdP`) and a fake source of resources (`support.Source`).
Tests of a package run one after another: they share the Redis.

Each replica has two listeners, as the PDP does: the public one (`Replica.Server`, the AuthZEN and legacy routes, and nothing else)
and the ops one (`Replica.Ops`, `/healthz`, `/readyz` and `/metrics`). `Replica.Do` and `Replica.Metrics` send each path to the
right one, so the tests that read `/readyz` or `/metrics` of a replica do not name the listener themselves.

```go
c := harness.StartReplicas(t, infra, 3)
p := c.SeedPolicy("org", 0)                      // a service, two types fed by c.Source over HTTP, a rule for "alice"
alice := c.UserToken("alice")
res := c.Replicas[1].Permission(c, p.Slug, alice) // the branches alice may read: a resource search of AuthZEN
res.IDs(t)                                       // ["b1" "b2" "b3"]
res.Trace.DBQueries()                            // what the request cost: statements to PostgreSQL ...
res.Trace.RedisRoundTrips()                      // ... and round trips to Redis (a pipeline is one)
c.Replicas[1].Warm(c, harness.Branches(p.Slug, alice), alice) // until the cost of a request is steady
```

- **Requests.** The replicas are asked the way their clients ask: the AuthZEN API of the service, with the requests of
  `core/testsupport/authzenreq`. `Replica.Permission`, `Fresh` and `TryPermission` send `harness.Branches`: the resources of the type
  `branch` that the holder of the token may `read` (`SeedPolicy` gives alice read on every branch). `Replica.Send(c, request, token)`,
  `Warm` and `Try` (a client that gives up after a timeout) send any `authzenreq.Request`: an evaluation, a batch, another search.
  `Result.IDs`, `Decision` and `Sees` read the answer (`Sees` is whether a search lists anything; it is false for a refusal, which a
  test tells from an absence of access by the status). A test with a client of its own (a balancer, a load generator) builds its
  `http.Request` with `harness.HTTPRequest`.
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
- **Levers.** The freshness of a request is `engine.WithConsistency` in the context of the process that serves it (no network
  input sets it; the PDP has no parameter for it). `harness.Levers(handler)` is the test-only
  wrapper that turns the header `X-Test-Consistency` (`higher` or `minimize_latency`) into it; it lives under `tests/`, so no
  binary has it. The PDP replicas of the harness are behind it, and a test asks for the freshest answer with the lever
  (`Replica.Fresh`, or `harness.Higher()` as the last argument of `Send`, `Try` and `HTTPRequest`), so that no test depends on a
  parameter of the API (there is none). The revision to reflect is `authzenreq.AtLeast(rev)` (`Replica.Permission` asks for the last
  write of the test). A test that needs to know whether an answer lacked the data of a type reads `Trace.SyncPending()` of its own
  `engine.WithTrace`.

Name a test after the acceptance criterion it proves (`TestF3_4_…`) and say in the pull request which criteria it covers.

## The fast read path (F1)

| test | what it proves |
|---|---|
| `cluster/f1_test.go` `TestF1_1`, `_2`, `_3`, `_5`, `_6`, `_7`, `_8` | no query and no Redis command for a warm request; one round trip for an answer only Redis holds; a miss costs the same few queries for 1 000 and for 100 000 rules; slugs and deleted services follow the revision within a second; one key set fetch per TTL, none for bad tokens, one refetch per rotation |
| `api/snapshot_differential_test.go` `TestF1_11` | random policies (validity windows, claim-based and attribute-based rules, roles, nested groups, conditions, denials, exceptions) give the same answers and explanations from the snapshot and from the queries, also after changes and after windows have passed. `LICETUM_DIFF_SEED` repeats a run |
| `chaos/f1_test.go` `TestF1_4`, `_9`, `_10` | a warm request ignores 150 ms on PostgreSQL and Redis; a Redis that answers after 5 s does not pile up goroutines or memory under 600 rps; a skipped Redis adds at most its timeout |

The replicas follow the revision with a delay of up to `REVISION__POLL_INTERVAL`: `Replica.Permission` asks for at least the revision
of the last write made through `AdminDo`/`Must`; send `harness.Branches` with `Replica.Send` for a plain request. Run the packages one at
a time (`-p 1`) when they share one Redis (`LICETUM_TEST_REDIS_URI`).

## The revision in PostgreSQL (F2)

| test | what it proves |
|---|---|
| `cluster/f2_test.go` `TestF2_1`, `_2` | a write and its revision commit together (a failed write or batch leaves it, a batch is +1), and the revisions of 20 concurrent writers are strictly increasing without gaps |
| `TestF2_3`, `_4`, `_5` | `at_least_as_fresh` gives read-your-writes on every replica (100 iterations); an answer for a revision the replica cannot confirm is computed from the data and not kept; a deny rule reaches every replica within a second without any revision asked for |
| `TestF2_6` | 50 synchronizations of service A leave every answer of service B a hit, while A's follow its data |
| `TestF2_8`, `_9`, `_10` | `FLUSHALL` while serving changes no answer; the Redis counters of the previous release are still bumped and the answer keys have the new format; the poller costs 4 statements a second at rest |
| `chaos/f2_test.go` `TestF2_7` | a revision bump under 1 500 hot keys at 600 rps: no failure, the pool never over its size |

Not covered: a read replica that lags by two seconds (F2-3, F2-4 in full): the tests of the replica went with its routing and the contract CONS01 (P5)
brings them back; F2-11 (stage 2, scoped bumps) is a separate pull request.

## Synchronization (F3)

`api/sync_test.go` and `api/sync_crash_test.go` run the synchronization on the DBOS engine, the only one; the tests have no engine switch. The
`licetum worker` of the kill tests is started with the defaults.

| test | what it proves |
|---|---|
| `api/sync_test.go` `TestSyncOneSourceCallForAConcurrentFirstRequest`, `TestSyncFreshRequestsShareTheSourceCall` | concurrent first requests, and concurrent `fresh` ones, ask the source once |
| `TestSyncFailingSourceIsAskedOncePerBackoff` | the cluster asks a source that fails once per backoff window, whatever the number of replicas and requests |
| `TestSyncLateExecutorIsFenced` | an executor that outlives its lease applies nothing once another one has taken the job over (`licetum_sync_lease_lost_total`), and the job ends once |
| `TestSyncConcurrentDuplicateEnqueuesDoNotExhaustTheDBOSPool` | concurrent duplicate enqueues of many types do not wedge the pool of the DBOS client |
| `api/sync_crash_test.go` `TestSyncKillNineOfTheWorker` | a worker killed while it fetches or while it commits: the job is taken over within twice the lease, no partial type, a consistent closure, one done job |
| `TestSyncBusyComponentLockDoesNotStartABackoff` | a synchronization whose component lock stays busy does not start the backoff of the source |
| `api/regions_test.go` `TestF6_6_RegionsOfASynchronizerThroughTheAPI` | the API stores, sorts, deduplicates and validates the `regions` of a synchronizer. No test says who asks a source with regions until P5 (the regional-fetch contract); any executor may |

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

### Failover and a far region (F6)

These need no read replica (the PDP no longer reads a replica; `scripts/pg-replica.sh` stays for the PostgreSQL-mode suites of a later phase).

| test | proves |
|---|---|
| `TestF6_10_*` | while a cell works, the sockets of the process go only to the cell's own endpoints (`/proc/net/tcp`), and nothing connects to the decoys standing for a neighbouring cell |
| `TestF6_11_*` (`cluster/failover_test.go`) | after a promotion that lost the last writes, `licetum failover` makes the regions drop what they kept under the reused revisions (the test shows the stale answer without it) |
| `chaos/f6_test.go` `TestF6_7_*` | an admin write with 150 ms to the primary costs at most six round trips |

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

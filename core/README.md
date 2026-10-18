# Licetum core (Go)

The engine of Licetum: the permission computation, the check API that client services call at request time,
the synchronization of resources from their sources, the database schema and the `licetum` binary. The module is
`github.com/licetum/licetum/core`, under the MIT License (see [`LICENSE`](LICENSE)).

The admin API (`admin-api/`, AGPL-3.0-only) is a separate module that depends on this one.

## Commands

`licetum` has three subcommands:

| Command | What it does |
|---|---|
| `licetum check` | the permission API, `GET /service/{id}/permission` and the check endpoints (default `:8001`) |
| `licetum migrate` | applies the database migrations and seeds the platform self-service (idempotent) |
| `licetum openapi check [-yaml] [-lang en\|ru\|zh]` | prints the OpenAPI spec without a server, database or Redis |

## Quick start

```bash
cd core
export DB__URI=postgresql://authz:authz@localhost:5432/authz_db   # a +asyncpg suffix is accepted too
export REDIS__URI=redis://localhost:6379/0
# the platform self-service is seeded from the PLATFORM__* variables (see Configuration)

go run ./cmd/licetum migrate
go run ./cmd/licetum check
```

The check API has its own docs, like FastAPI: Swagger UI at `/docs` and the OpenAPI 3.1 spec at `/openapi.json`
(and `/openapi.yaml`). The spec is generated from the Go structs that handle requests (Huma v2), so it cannot drift
from the code. Without a server:

```bash
go run ./cmd/licetum openapi check -yaml > check.openapi.yaml   # -lang ru|zh for the translations
```

`schema/check.yaml` is the exported spec. `task go:schema` regenerates it and `task go:schema:check` (part of
`task check`) fails when it is out of date.

Logs are JSON on stdout. `LOG__LEVEL=debug` enables debug logs, and `DB__ECHO=true` adds the SQL.
The address is set with `-addr :9000` or `HTTP__ADDR`.

## AuthZEN

The check API implements the [OpenID AuthZEN Authorization API 1.0](https://openid.net/specs/authorization-api-1_0.html)
for every service, under `/service/{id or slug}/access/v1/`: `evaluation`, `evaluations` (with
`options.evaluations_semantic`), `search/subject`, `search/resource` and `search/action`. The Policy Decision Point of
a service is `<HTTP__PUBLIC_URL>/service/{id or slug}`; its metadata is at
`/.well-known/authzen-configuration/service/{id or slug}` (served only when `HTTP__PUBLIC_URL` is set).

- The resource type is a resource type of the service, the resource id its id in the external system (empty: the type as a whole),
  the action an access level. `context` holds the attributes of the request the rules read; `{"explain": true}` returns the
  explanation in `context.reason_admin`.
- The subject is the holder of the bearer token. A client application listed in the service's `trusted_clients` may ask about any
  person, giving the person's claims in `subject.properties`; these calls are recorded like the calls on behalf of a person.
- The subject search is for trusted clients only and decides the known subjects (`SUBJECTS__ENABLED`).
- A request that is not valid is a 400; a missing or invalid token a 401; a subject the caller may not ask about a 403. Errors keep
  Licetum's usual JSON body (`{"detail": …, "extra": {"code": …}}`), which carries the message the specification asks for.

## Full stack

`task dev` (= `docker compose up`) from the repository root starts PostgreSQL, Redis, a local Dex OIDC provider,
the migration, the check API, the admin API and the web interface. Open `http://localhost:3000` and sign in as
`admin@example.com` with password `password`. These credentials and the Dex client are for development only; set the
`PLATFORM__*` variables in `.env` to use another identity provider.

The Compose service uses the `dev` target of [`Dockerfile`](Dockerfile): the sources are mounted and
[air](https://github.com/air-verse/air) rebuilds the binary on every change. The `runtime` target is the production
image: a static binary in distroless.

## Configuration

Environment variables, read by `config.Load()` (`config/config.go` has the full list). Names are case-insensitive;
lists accept a JSON array or a comma-separated string.

| Prefix | Variables |
|---|---|
| `DB__` | `DB__URI` (required), `DB__READ_URI` (read replica of a check process), `DB__MAX_REPLICA_LAG`, `DB__LAG_ACTION`, `DB__LAG_INTERVAL` — see [Read replica](#read-replica-db__read_uri), pool settings (`DB__POOL_SIZE` default 10, `DB__MAX_OVERFLOW` default 20, `DB__POOL_TIMEOUT`, …), `DB__SYNC_POOL_SIZE` (the worker's synchronization pool, default 4), `DB__ECHO`; behind PgBouncer: `DB__PGBOUNCER` (simple protocol, nothing prepared) and `DB__MIGRATE_URI` (where `licetum migrate` connects, PostgreSQL itself) — see [Resilience and operations](#resilience-and-operations) |
| `REDIS__` | `REDIS__URI`: the permission cache, the cached source lists, the circuit breakers. `REDIS__HOT_TIMEOUT` (default `50ms`): the check API's read and write timeout for Redis, without retries; after three failures in a row Redis is skipped for five seconds |
| `SNAPSHOT__` | the policy kept in memory: `SNAPSHOT__MAX_BYTES` (default 256 MiB; a policy that does not fit is read with queries, a negative value turns the snapshot off), `SNAPSHOT__MAX_AGE` (default `1m`: the snapshot is read again at this age even when the revision did not move) |
| `REVISION__` | the revision of the policy lives in PostgreSQL (`revision_state`): `REVISION__POLL_INTERVAL` (default `250ms`: how soon a write made through another replica is seen), `REVISION__WAIT_MAX` (default `500ms`: how long a request with `at_least_as_fresh` waits for its revision before the answer is computed from the data) |
| `COMPUTE__` | `COMPUTE__CONCURRENCY`: answers computed at once (default half of `DB__POOL_SIZE`); the rest of a wave of cache misses queues. A computation that waits for a synchronization job (the worker's) gives its slot back meanwhile |
| `AUTH__` | `AUTH__STATE_SECRET`, `AUTH__STATE_TTL`; the token and key caches: `AUTH__TOKEN_L1_SIZE` (verified tokens kept in memory, default 50 000), `AUTH__JWKS_TTL` (default `10m`), `AUTH__JWKS_MIN_REFRESH` (default `30s`: an unknown key id refetches the key set at most this often), `AUTH__INVALID_TOKEN_TTL` (default `10s`) |
| `CACHE__` | `CACHE__ANSWER_L1_SIZE`: answers kept in memory, default 50 000 |
| `BACKGROUND__` | `BACKGROUND__WORKERS` (default 16) and `BACKGROUND__QUEUE` (default 1024): the cache writes and refreshes done after a request is answered; a full queue drops the work |
| `PLATFORM__` | the self-service: its OAuth2 client (`PLATFORM__CLIENT_ID`, `PLATFORM__CLIENT_SECRET`, the authorization, token and JWKS URLs, the issuer) and the platform admin group (`PLATFORM__ADMIN_GROUP_NAME`, `PLATFORM__ADMIN_CLAIM`, `PLATFORM__ADMIN_CLAIM_VALUES`) |
| `SYNC__` | `SYNC__FETCH_TTL` (how long a source's list is kept), `SYNC__ENGINE` (`dbos`, the default, or `postgres`), `SYNC__DBOS_SCHEMA`, `SYNC__DBOS_POOL_SIZE` (connections of the DBOS client of each process, default 20, at least 3, outside `DB__POOL_SIZE`; the process runs at most (size - 1) / 2 enqueues at once, since a duplicate enqueue takes two connections and the listener keeps one), and the synchronization pipeline, see below. `SYNC__LOCK_TTL` and `SYNC__LOCK_WAIT` are deprecated and ignored |
| `SUBJECTS__` | `SUBJECTS__ENABLED` (the registry of known subjects, off by default), `SUBJECTS__RETENTION_DAYS` |
| | The retention of both runs in the **worker** only (`licetum worker`): give it the same `SUBJECTS__RETENTION_DAYS` and `DELEGATION__AUDIT_RETENTION_DAYS` as the other processes, or the defaults (90 and 365 days) apply. |
| `DELEGATION__` | `DELEGATION__AUDIT_RETENTION_DAYS` (the audit of calls made on behalf of a person), `DELEGATION__AUDIT_MODE` (`strict`, default: the record is committed before the answer; `async`: buffered and written in batches), `DELEGATION__AUDIT_BUFFER` (async buffer size, default 100000) |
| `HTTP__ADDR`, `HTTP__PUBLIC_URL` | the listen address; the address clients reach the check API at (`https://licetum.example.com`), which the AuthZEN discovery document is built from |
| `HTTP__MAX_INFLIGHT`, `HTTP__REQUEST_TIMEOUT` | admission control: the requests served at once (default `GOMAXPROCS × 256`, a negative value removes the limit) and the deadline of a request (default `10s` for the check API, `60s` for the admin API, a negative value sets none) |
| `POLICY__MAX_STALENESS` | how long a process goes on answering from its copy of the policy when it cannot confirm it against PostgreSQL; beyond it requests fail closed (503 `policy_unavailable`). Default `0`: **no limit** — the process answers for as long as PostgreSQL is unreachable and does not see a revocation made meanwhile; set it where that is not acceptable |
| `SHUTDOWN__DRAIN`, `SHUTDOWN__TIMEOUT` | on SIGTERM: how long the process is not ready but serves (default `10s`) and how long it waits for the requests in progress (default `30s`) |
| `LOG__LEVEL` | `debug` for debug logs |

## Synchronization pipeline

A request to the check API that finds its token and its answer in memory sends no query to PostgreSQL and no command to
Redis. The pieces, all per process:

- **The policy snapshot** (`engine/snapshot.go`): the services, the type graph, the memberships, the rules, the role items
  and the access groups, built in one read-only transaction for one revision of the policy (`revision_state`, scope `rules`) and
  replaced as a whole when the revision moves. The validity windows of rules and memberships are compared with the clock
  at every request, never built into the snapshot. A policy that does not fit `SNAPSHOT__MAX_BYTES` is not copied: the
  queries read it (`licetum_snapshot_fallback_total`), as does `consistency=higher`, which asks for the freshest answer.
- **The view** (`engine/localstate.go`): the revision of the rules and of the data of each service as read from PostgreSQL
  (one statement), with the snapshot of that revision. It is renewed every `REVISION__POLL_INTERVAL` (`WatchRevision`, and
  the requests), and at once after a write made through the process or a request that asks for `at_least_as_fresh` a newer
  revision. When the revisions cannot be read the last view is served as it is, marked degraded, for as long as
  `POLICY__MAX_STALENESS` allows (no limit by default), and answers are kept in memory only. The revision and the data change in
  one transaction, so a replica never sees a revision before its data.
- **Answers and tokens** are kept in memory first (`CACHE__ANSWER_L1_SIZE`, ten seconds at most; `AUTH__TOKEN_L1_SIZE`, a
  minute at most) and in Redis second: a request that finds its answer only in Redis makes one round trip.
- **Key sets** of the identity providers are kept per URL (`AUTH__JWKS_TTL`), fetched once for all the requests that wait
  for them, and fetched again for an unknown key id at most once per `AUTH__JWKS_MIN_REFRESH`; a token that failed
  verification is remembered for `AUTH__INVALID_TOKEN_TTL`.

## Resilience and operations

### Probes

- `GET /healthz` is liveness: 200 while the process answers, whatever its dependencies do. Use it for the liveness probe.
- `GET /readyz` is readiness: 200 when PostgreSQL answers within a second, the schema of the database is **at least** the version
  of the binary (a newer schema is fine: during a rolling update the pods of the previous release stay in service on the schema
  the new release has migrated) and the copy of the policy is loaded (a policy too large for `SNAPSHOT__MAX_BYTES` is not copied by
  decision, and does not hold readiness back); 503 `not_ready` with the reason (`database`, `schema`, `snapshot`, `draining`)
  otherwise. The result of the database check is reused for a second. **Redis is not part of readiness**: without it the process
  is degraded, not unavailable. The worker answers `/readyz` too (database and schema only).
- The metric `licetum_degraded{component="redis"|"postgres"}` is 1 while the process runs without that dependency, and
  `licetum_snapshot_age_seconds` is the time since it last confirmed its copy of the policy against PostgreSQL.

Note that a balancer which takes a pod out of rotation on a failing readiness probe takes it out when PostgreSQL is gone, too: the
degraded mode below is for clients that reach the pod anyway (a probe with a longer failure threshold, a mesh that retries,
liveness-only probing), or for a PostgreSQL that is gone for less than the probe tolerates.

### Degraded mode

- **Redis gone, slow or read-only**: the check API skips Redis after three failures in a row (`REDIS__HOT_TIMEOUT`), keeps its
  tokens, key sets and answers in memory (answers are kept until they stop being current while Redis is down, not ten seconds),
  and computes what it does not find, bounded by `COMPUTE__CONCURRENCY`. Failures of Redis are logged once a second per kind,
  with the count of the records left out (`licetum_log_suppressed_total`).
- **PostgreSQL gone**: requests are answered from the copy of the policy and the answers in memory, also past their time
  as long as no validity window has started or ended since (`licetum_permission_cache_total{result="stale"}`), until the copy
  was last confirmed longer ago than `POLICY__MAX_STALENESS`. A request that cannot be answered from memory, or comes after
  that, is refused with 503 `policy_unavailable` and `Retry-After`: the process fails closed. It recovers by itself when
  PostgreSQL is back.

### Shutdown

On SIGTERM the process turns not ready (`/readyz` is 503 with the reason `draining`, responses carry `Connection: close`),
goes on serving for `SHUTDOWN__DRAIN` (10 s) so that the balancer stops sending it requests, stops listening and waits up
to `SHUTDOWN__TIMEOUT` (30 s) for the requests in progress, then closes the engine. In Kubernetes set
`terminationGracePeriodSeconds` to at least drain + timeout + 10, and a `preStop` sleep equal to the drain when the endpoint is
removed only as the pod is deleted.

### Admission control

At most `HTTP__MAX_INFLIGHT` requests are served at once; a request above it is refused at once with 503 `overloaded` and
`Retry-After: 1` (`licetum_shed_total`, `licetum_http_inflight`). `/healthz`, `/readyz` and `/metrics` are never refused. A request has
`HTTP__REQUEST_TIMEOUT` to finish; past it the answer is 503 `request_timeout`.

**Sizing `HTTP__MAX_INFLIGHT`.** The default (`GOMAXPROCS × 256`) is a guard against exhaustion; it does not keep a latency. The
requests that are admitted wait behind at most that many others, so under overload their latency is about the limit over the
goodput (Little's law): in the load test of F5-8 (`TestF5_8_*`, 4 shared vCPUs, a request that reads the policy from PostgreSQL,
three times the sustainable rate) the default admits everything it can hold and the p99 of the admitted requests is 20 to 80
times the p99 at the sustainable rate, while a limit of three quarters of *sustainable rate × p99 at that rate* (at least twice the
CPUs) keeps it near twice (1.7 to 2.0 in six runs on a laptop; on a 4-vCPU shared CI runner 1.7 once and 3.1 twice, with a limit of 8, where the CPU spent on the refusals is a larger part of the little there is) and refuses about two thirds of the offered requests, with the goodput above
the capacity measured flat out. To keep the latency of what is served under overload, measure the sustainable rate and its p99
(the highest step of the ramp of `admin-api/loadtest` that meets its objectives), set the limit to the product, and check it with
the same test. The price of a tight limit is that a burst that would have been queued is refused: a limit under the number of
requests in flight when PostgreSQL is slow turns that slowness into 503 `overloaded`.

### Connections

One process may hold `DB__POOL_SIZE + DB__MAX_OVERFLOW` connections in its request pool, `DB__SYNC_POOL_SIZE` in the pool of the
synchronization transactions (the worker and the admin API) and `SYNC__DBOS_POOL_SIZE` for the DBOS client (not with
`SYNC__ENGINE=postgres`); each process logs its total at start and warns when it alone reaches `max_connections`. The total
against the server is

    connections = replicas × (pool + overflow + DBOS pool) + workers × (sync pool + request pool + DBOS pool) + admin API + a few for you

and must stay under `max_connections` of PostgreSQL (or `default_pool_size` of PgBouncer). The defaults (10 + 20 + 20) are for a
few replicas: with many, lower `DB__POOL_SIZE`, `DB__MAX_OVERFLOW` and `SYNC__DBOS_POOL_SIZE`, or put PgBouncer in front. The
metrics `licetum_db_pool_connections`, `licetum_db_pool_max_connections`, `licetum_db_pool_acquire_total`,
`licetum_db_pool_empty_acquire_total` and `licetum_db_pool_acquire_seconds_total` (label `pool` is `request` or `sync`) say whether a
pool is too small (acquires that waited) or too large for the server.

**PgBouncer in transaction mode**: set `DB__PGBOUNCER=true` (the statements go by the simple protocol and nothing is prepared).
`licetum migrate` holds a session-level advisory lock for its whole run, which transaction pooling cannot carry: give it
`DB__MIGRATE_URI` pointing at PostgreSQL itself. DBOS prepares its statements: use PgBouncer 1.21 or newer with
`max_prepared_statements` set, or point `SYNC__DBOS_URI` at PostgreSQL directly, or use `SYNC__ENGINE=postgres`.

### Read replica (`DB__READ_URI`)

A `check` process can read the policy from a read replica of PostgreSQL, normally the one of its own region, so that a request never
crosses a region. Set `DB__READ_URI` (asynchronous streaming replica; the primary stays `DB__URI`). The snapshot of the policy, the
revision it follows, the type graph and the closure are read from the replica; the revision a process acts on is therefore the one of
the data it has. Everything that writes (the admin API, the worker, the revision bump) uses the primary, and so does every read that
must be current: `consistency=higher`, and `at_least_as_fresh=R` when the replica has not reached `R` within `REVISION__WAIT_MAX`
(such an answer is computed from the primary and not kept under the replica's revision). Use a client that passes `X-Authz-Revision`
of its write as `at_least_as_fresh` when it must see the write at once; otherwise a write is seen within the replication lag plus
`REVISION__POLL_INTERVAL`.

The process measures the lag every `DB__LAG_INTERVAL` (1 s): the larger of what the replica reports (`pg_last_xact_replay_timestamp`,
zero when it has replayed everything it received) and how long its revision has been behind the primary's. It is
`licetum_replica_lag_seconds` (also `licetum_replica_revision_lag` and `licetum_replica_lagging`). Above `DB__MAX_REPLICA_LAG`
(10 s) `DB__LAG_ACTION` decides: `unready` (default) makes `/readyz` fail with the reason `replica_lag`, so that the balancer sends
the traffic to another region, and the process recovers by itself; `primary` moves the reads to the primary until the replica has
caught up. `POLICY__MAX_STALENESS` applies to the replica too: a replica more behind than that, with the reads on the replica, fails
closed (503 `policy_unavailable`). The pool of the replica is the `read` pool in `licetum_db_pool_*`; size it like the request pool.

### Migrations

`licetum migrate` takes an advisory lock, so several runs started together (one per pod) apply each migration once; the seed of the
self-service runs under another one. A migration must work with the previous release (expand/contract), see
[CONTRIBUTING.md](../CONTRIBUTING.md#migrations-must-work-with-the-previous-release).

## Synchronization worker

The resource sources are synchronized by a separate process: `licetum worker`. The check API (`licetum check`) and the admin
API only *ask* for a synchronization (a row in `sync_job`) and wait for it for a bounded time; they never write the resource
graph. The state of the synchronization (`sync_state`: what was applied, when a type is due again, the backoff of a source that
fails) is in PostgreSQL, so a Redis flush or failover starts nothing.

- A job is claimed with `FOR UPDATE SKIP LOCKED` under a lease (`SYNC__LEASE`, 30 s) that the executor renews every lease/3. The
  transaction that applies the result ends with an update of the job conditioned on the lease token: an executor that lost its
  lease applies nothing (`licetum_sync_lease_lost_total`).
- Requests for the same type, in any process, share one job (a unique index on its key); `consistency=higher` has its own job,
  and the source of a type is asked by fresh jobs at most once per `SYNC__FRESH_MIN_INTERVAL` (1 s).
- A request that needs the data of a type that was never synchronized waits `SYNC__REQUEST_WAIT` (5 s) for the job. When the
  time passes it answers from what exists — the type contributes no resources — and sets `X-Authz-Sync: pending`. The admin
  paths (resource lists, refresh) wait `SYNC__ADMIN_WAIT` (30 s). A type whose throttle (30 s) ran out is refreshed in the
  background; the request answers from the data as of the last synchronization.
- A `consistency=higher` request that arrives while a fresh job of the type is already running shares it, so its source call may have started before the request: the source is asked once per `SYNC__FRESH_MIN_INTERVAL`, not once per request. A client that must see a write it has just made at the source asks again after the response (or waits the interval).
- A source that does not answer is put in backoff for the whole cluster: `min(30 s · 2^attempts, 10 min)`; nobody asks it
  until the backoff ends, and the data stays.
- Writing the graph takes an advisory lock per connected component of the type graph (so unrelated types do not wait for each
  other), with `lock_timeout` 5 s and `statement_timeout` `SYNC__STATEMENT_TIMEOUT` (120 s), on its own pool
  (`DB__SYNC_POOL_SIZE`). A job that cannot get the lock in time is queued again.
- The worker puts the types that are due on a schedule (`SYNC__SCHEDULE_TICK`, 5 s) when somebody asked for them within
  `SYNC__DEMAND_WINDOW` (10 min), or always with `SYNC__KEEP_WARM`. `SYNC__CONCURRENCY` jobs run at once (default
  `DB__SYNC_POOL_SIZE`).
- The worker holds the whole list of a type in memory while it applies it; the check and admin processes never do. Measured
  (`LICETUM_SYNC_MEMORY=1 go test -tags integration -run TestSyncWorkerMemory`, a source of 1 000 000 resources with three
  fields): peak RSS of the worker 1.42 GiB, about 1.5 KB per resource. Documented limit: **2 GiB of memory for the worker per
  1 000 000 resources in its largest type** (the worker applies `SYNC__CONCURRENCY` types at once, so add up the largest types
  that can be applied together).
- The database role of the check pods needs `SELECT` on everything, `INSERT`/`UPDATE` on `sync_job`, and no `INSERT`,
  `UPDATE` or `DELETE` on `resource_cache`, `resource_edge`, `resource_ancestor` and `sync_state`.

Metrics: `licetum_sync_jobs{state}`, `licetum_sync_queue_age_seconds` (read from the database at every scrape, so it grows in
the check and admin processes when every worker is down), `licetum_sync_job_seconds`, `licetum_sync_jobs_finished_total{state}`,
`licetum_sync_lease_lost_total`, `licetum_source_calls_total{type,outcome}`.

### The DBOS engine

`SYNC__ENGINE=dbos` (the default; `postgres` selects the `sync_job` table) runs the same pipeline on [DBOS Transact](https://github.com/dbos-inc/dbos-transact-golang)
instead of the `sync_job` table: a database-backed queue `licetum-sync`, a workflow `SyncType` per type, and the DBOS
deduplication id ("return existing") as the shared job. The system tables are in the schema `SYNC__DBOS_SCHEMA` (default `dbos`)
of the same database, created by the first worker; the check and admin processes talk to it through a DBOS client and answer
from the existing data until it is ready. Fetch and apply are one step, so a checkpoint is a few dozen bytes whatever the size
of the type. All workers must run the same `SYNC__ENGINE`.

What the spike found, and what the engine does about it:

- DBOS Go does not give the interrupted workflows of a crashed executor to a live one (the Conductor service, or a restart under
  the same executor id, does). Every executor writes a heartbeat to `sync_executor`; each worker cancels the workflows of an
  executor whose heartbeat is older than `SYNC__LEASE` and queues a replacement under `<id>+r`, which the waiters follow.
- The fence of the apply transaction is the status of the workflow row, read `FOR SHARE`: a workflow that was taken over applies
  nothing (`licetum_sync_lease_lost_total`), and the cancel of a takeover waits for a commit in flight.
- The DBOS client holds a connection per waiting `GetResult`, and its pool has 20: the waiters of one workflow share a single
  poller per process, and `pool_max_conns` is set to 20 unless the URI says otherwise.
- Through a link with a round trip of 150 ms an enqueue takes about 4 round trips (0.6 s) and the whole call about 1.4 s more than
  on a local network: put the workers and the check processes in the region of the database.
- PgBouncer in transaction mode was not tried: DBOS listens for notifications and needs session connections. Use direct
  connections or session pooling for the check, admin and worker processes.

## Tests

```bash
go test -race ./...                                       # unit tests
go test -race -tags integration ./tests/integration/...   # integration tests
task go:coverage                                          # coverage reports in reports/ (both modules)
```

Integration tests start disposable PostgreSQL and Redis containers with `testcontainers-go`, one isolated database per
test. Docker is needed only for them. The Taskfile uses the active Docker context; set `DOCKER_HOST` only when running
`go test` directly against a non-default socket.

Where Docker cannot create containers, point the tests at servers that are already running:
`LICETUM_TEST_POSTGRES_URI` (an administrative connection string; every test gets its own database, dropped at the end)
and `LICETUM_TEST_REDIS_URI`. No containers are started then.

## Linting

`task go:lint` runs in both modules (`task check` is the CI mode, which changes nothing):

- golangci-lint v2 (`.golangci.yml`): gofmt and goimports, the standard set (govet, staticcheck, errcheck, unused),
  gosec, and depguard enforcing the layers: `server` → `engine` → `db`, `redisx`, `synchronizer` → `model`, `config`,
  `i18n`, `apperr`; the core must not import the admin module;
- `go mod tidy -diff`;
- the exported OpenAPI schema matches the code.

`task go:vuln` runs govulncheck (needs access to vuln.go.dev).

## Layout

```
cmd/licetum/       entry point: check / migrate / openapi subcommands, graceful shutdown
cli/               the shared command code: configuration, migrations, serving, the OpenAPI printer
config/            environment variables
logx/              structured logs, their settings can change at run time
apperr/            API errors → {"detail": ..., "extra": {"type": ..., "code": ...}}
i18n/              supported languages, Accept-Language negotiation, the error message catalog
db/                pgx pool, goose migrations (embedded SQL in db/migrations), the platform seed
redisx/            Redis: the permission cache version, the circuit breaker
synchronizer/      resource source adapters (SQL and HTTP)
model/             request and response structs and their validation (the pydantic equivalent)
engine/            business logic: permission computation (permission.go), decisions (decision.go), synchronization
server/            HTTP on Huma v2: the check API, documentation, localization, middleware
schema/            the exported OpenAPI spec of the check API
tests/             unit and integration tests; testsupport/ starts the containers they use
```

Dependencies: `huma/v2` (HTTP, validation, OpenAPI, on top of `net/http`), `pgx/v5`, `go-redis/v9`,
`golang-jwt/jwt/v5`, `go-jose/v4` (JWK parsing), `goose/v3`, `rs/cors`, `x/sync/singleflight`, `x/text/language`,
`google/uuid`.

## Describing the API

- Field descriptions, examples and constraints are struct tags in `model/`: `doc:"..."`, `example:"..."`,
  `enum:"a,b"`, `format:"uuid"`, `minItems:"1"`. Required fields are value fields; optional ones are pointers with
  `required:"false"`.
- Request bodies accept extra fields (`` _ struct{} `additionalProperties:"true"` ``), so a client may send auxiliary
  form fields.
- An operation is registered with `register(api, huma.Operation{OperationID, Method, Path, Summary, Description, Tags,
  Errors}, handler)` in `server/`. Cross-field rules (for example "subject XOR predicate") are a model's `Validate()`
  method, called from the handler.
- Service-layer errors are created from catalog keys: `apperr.NotFound(i18n.ServiceNotFound, id)`. Every error,
  Huma's validation errors included, has the `{"detail", "extra"}` shape and is localized.

## Notes

- The check API takes a service by its id or its slug (`/service/{service_id}/...`). A slug is never a UUID, so the two
  cannot be confused. The slug `licetum` belongs to the platform itself.
- Invalid or expired tokens are 401 and never trip the JWKS circuit breaker. 424 means that the identity provider's
  JWKS or a synchronizer source did not answer.
- Predicate regular expressions run on RE2: backreferences and lookaround are not supported. An invalid regular
  expression is rejected when the rule is saved (422).
- Swagger UI at `/docs` loads its scripts from a CDN (unpkg). In an isolated network the page does not render, but
  `/openapi.json` always works.

## License

MIT — see [`LICENSE`](LICENSE).

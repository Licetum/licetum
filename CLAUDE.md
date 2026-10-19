# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository layout (since the module split)

- `core/` — Go module `github.com/licetum/licetum/core`, MIT: the engine (`core/engine`), the PDP (`core/server`), the HTTP helpers (`core/httpx`) and the key sets of the identity providers (`core/pepauth`, leaves: see depguard),
  the database schema and migrations (`core/db`), synchronizers, models. Binary: `core/cmd/licetum` (`pdp`, `migrate`,
  `openapi pdp`). Schema: `core/schema/pdp.yaml`.
- `admin-api/` — Go module `github.com/licetum/licetum/admin-api`, AGPL-3.0-only: the admin API (`admin-api/api`), the admin
  services (`admin-api/service`, which embed `engine.App`), the binary `admin-api/cmd/licetum-admin` (`admin`,
  `restore-admin`, `openapi admin`). Schema: `admin-api/schema/admin.yaml`. Depends on `core` through a `replace`.
- `web/` — the admin interface, AGPL-3.0-only.
- Tasks: `task go:lint`, `go:test`, `go:coverage`, `go:schema` run both Go modules; `web:*` the interface.

The sections below still name the old `backend/internal/...` paths: read `backend/internal/api` as `admin-api/api` or
`core/server`, and `backend/internal/service` as `admin-api/service` (admin) or `core/engine` (decisions, synchronization).

## What this is

Licetum — a centralized authorization service. Client services register themselves, define a
type hierarchy for their resources, and ask Licetum "can subject X do Y on resource Z?" instead of
implementing their own RBAC. Code, comments and docs are written in English. User-facing text is
localized: English (default), Russian and Chinese — see "Localization" below. The repository is
licensed per part (`LICENSE`): `core/` under MIT, `admin-api/` and `web/` under AGPL-3.0-only; a library or SDK meant to be
embedded into other software gets its own permissive license when it is added (not before).

The backend is written in Go (`backend/`), one binary with subcommands:
- `licetum admin` — admin API (services, type_resources, resources, groups, group_members, roles and their
  permission items, assignment_rules, oauth callback, platform self-service). Port 8000. Routes: `internal/api/admin.go`.
- `licetum pdp` — the PDP, the single runtime endpoint client services actually call at request time
  (`GET /service/{service_id}/permission`), kept in its own process/port (8001, `-addr` or `PDP__ADDR` overrides it) so it can be
  scaled/deployed independently of the admin surface. Its probes and `/metrics` are on the ops port (9090, `PDP__OPS_ADDR`), never on
  this one. Route: `internal/api/check.go`.
- `licetum worker` — the synchronization worker: executes the jobs that fetch the resource sources into the local copy, and keeps the
  types in use fresh on a schedule. Metrics on :8002. The PDP and admin processes never apply a synchronization.
- `licetum migrate` — apply DB migrations and seed the platform self-service.
- `licetum restore-admin [-user name]...` — the way back in when nobody can manage the platform: puts the admin group, its
  memberships (`PLATFORM__ADMIN_CLAIM_VALUES` and each `-user`, matched by the claim of `ADMIN_AUTH__SUBJECT_CLAIM`), the admin rules of every policy back in force and switches off
  the denials that cover a policy (`db.RestoreAdminAccess`, `internal/db/restore.go`). The seed never does this on purpose.
- `licetum openapi pdp [-yaml] [-lang en|ru|zh]` — print the PDP's OpenAPI spec without a server (the admin API's is `licetum-admin openapi admin`).

The admin API is itself protected by Licetum permissions — see "Self-service: access policies for
Licetum itself" below.

## Commands

Tasks for the whole repo live in `Taskfile.yaml` (go-task): `task check` runs every linter/check
without changing files (as CI would), `task lint` does the same but fixes and formats, `task test`,
`task generate` (OpenAPI schemas → orval client), `task dev` (docker compose). `task --list` for the rest.
CI (`.github/workflows/ci.yml`, on pull requests and pushes to main) runs `task check` + `pnpm build`
and `task backend:coverage` with disposable PostgreSQL/Redis Testcontainers, so integration tests
run there too. `task go:vet:tags` (part of `task go:lint`, so of `task check` and of the CI job `go-lint`) compiles and vets the
code behind the build tags `integration`, `chaos` and `multiregion` in both modules, with and without `-race`, and builds the
load test, so the suites that run only nightly cannot stop compiling unseen; a new build tag goes into `GO_BUILD_TAGS` of
`Taskfile.yaml` (the task fails until it does).

The epic is being refactored to the design of issue #49 (regional AuthZEN PDP, service-scoped administration). Tests become
**contracts**: `contracts.yaml` (root) lists them (id, statement, oracle, level, phase, status); a test checking one is named
`TestContract_<ID>_<What>`; `task contracts:check` (part of `task check`, `core/internal/tools/contractscheck`) fails on an
active contract without a test, a test naming an unknown contract, or a planned contract that already has a test. What used to
run on every pull request through PgBouncer runs nightly: `.github/workflows/nightly-suite.yml` (PgBouncer, DECP, chaos,
load test; multi-region is switched off until P7, ADR 0001 §10.7) is called by the dispatcher `nightly.yml` on `main` with the ref of the epic.

The decision contracts (DEC01–DEC14, DECP) are tables, not code: `core/testsupport/policyfix` is the fixture format (services,
types, resources, groups, roles, rules, cases with claims, context and a time, questions with the expected decision and
explanation), its `testdata/decisions/*.yaml` the tables. Two independent sides answer them: the engine through the AuthZEN API
of the PDP (`admin-api/tests/integration/contracts`: `Load` builds a cluster per fixture, the fake source serves its
resources, `Ask` asks the case's evaluations (`POST …/access/v1/evaluations`, with explain) at the case's time through
`App.SetClock`, `CrossCheck` asks the case's resource and action searches (`…/access/v1/search/resource`, `…/search/action`) and
fails every DEC and DECP test where the list (`coverNodes`) disagrees with the evaluations (`CoversOf`); level `pg`; until P2-23
`SecondOpinion` asks the legacy `/check` and `/permission` routes as well), and `core/testsupport/refeval`, a
reference evaluator written from the semantics that shares no code with the engine (depguard keeps it away from engine, server
and db). `core/testsupport/policygen` generates random valid fixtures (also types that had a synchronizer and lost it, with a `before`
block: rows left behind, links to them, rules and claim lookups that name them); DECP compares the two sides on them (seeds 1–20 per pull
request, `LICETUM_DECP_SEEDS`, `task go:test:decp`, `decp.yml` nightly over 21–520). A disagreement is a defect of one side or
an open point of the semantics: settle it in both, then pin it in a table.

Backend (run from `backend/`, Go 1.26):
```
go run ./cmd/licetum migrate           # apply migrations + seed self-service (idempotent)
go run ./cmd/licetum admin             # admin API on :8000 (-addr / HTTP__ADDR to override)
go run ./cmd/licetum pdp               # the PDP (AuthZEN, permission endpoints) on :8001
go run ./cmd/licetum worker            # synchronization worker (jobs that fetch the resource sources), metrics on :8002
go vet ./... && go test -race ./...                    # unit tests
go test -race -tags integration ./...                  # integration tests via Testcontainers (set DOCKER_HOST for non-default contexts;
                                                       # LICETUM_TEST_POSTGRES_URI / LICETUM_TEST_REDIS_URI use running servers instead)
task backend:coverage                                  # coverage reports in backend/reports/
```
Lint: `golangci-lint run` (config `backend/.golangci.yml`: gofmt/goimports, staticcheck, gosec,
unused, depguard layer rules) + `go mod tidy -diff`. After changing the API run `task backend:schema`
— `backend/schema/{admin,pdp}.yaml` are committed (orval input) and `task check` fails on drift.

Migrations: `core/db/migrations/00001_init.sql` is the whole schema of a fresh install (goose, one file: before the first
public release all earlier migrations were folded into it). A schema change from now on is a new `0000N_name.sql` (goose
format, `-- +goose Up` / `Down`), embedded into the binary; it must not rewrite an applied one. The platform self-service
(the Licetum service within itself, its policy types, their sources, the admin group) is seeded by `db.SeedPlatform`, run by
`licetum migrate` after the migrations: it depends on the environment, so it is not in a migration.

Fast read path (`core/engine`: `snapshot.go`, `localstate.go`, `lru.go`, `background.go`, `tuning.go`, `keysets.go`; the key sets in `core/pepauth/jwks.go`; `core/redisx/fastfail.go`;
settings `SNAPSHOT__*`, `AUTH__TOKEN_L1_SIZE`, `AUTH__JWKS_*`, `AUTH__INVALID_TOKEN_TTL`, `CACHE__ANSWER_L1_SIZE`, `BACKGROUND__*`,
`REDIS__HOT_TIMEOUT`; the numbers are in `core/README.md`):
- **The revision lives in PostgreSQL** (`revision_state`, migration `00002`, `db.BumpRevision` / `db.LoadRevisions`, `core/db/revision.go`):
  scope `rules` numbers every write that can change a decision (rules, memberships, groups, roles and items, access groups, services, types,
  synchronizers), scope `data:<service id>` numbers the resource data of a service. A write bumps `rules` **in its own transaction**
  (`mutate`, once per batch in `RunBatch`, `RestoreAdminAccess`, the seed), a synchronization that changed data bumps `data:<service>` in the
  transaction of `applyType` (`bumpDataRevisions`; also `resolve`), so the number cannot reach a reader before the data. After the commit
  `App.AfterWrite(ctx, revision)` sets the revision of the request (`X-Authz-Revision`), invalidates the view and, for the replicas of the
  previous release during a rolling update, still bumps the Redis counters (`authz:cache_version`, `authz:data_versions`);
  this release reads neither. Answer keys are `authz:permission:t9:r<rules>:…` / `authz:check:t5:r<rules>:…`.
- The check path decides from `cacheState` (`App.cacheState`). In a process with the snapshot on
  (`SNAPSHOT__MAX_BYTES` positive, the default) the state is the `localView`: the revisions last read from `revision_state`
  (`readCacheState`: one statement), with the `snapshot` of that revision. A request uses the view; the view is renewed by `WatchRevision`
  (started by `cli.Serve`) and by the requests every `REVISION__POLL_INTERVAL` (`singleflight`, so never twice at once), in the request when it
  is older than `staleLimit` (2 s or 8 polls), after a write of this process (`invalidateView`, from `AfterWrite` and the synchronizations)
  and when the request asks for `at_least_as_fresh` a newer revision: `renewView` then waits for the revision for at most
  `REVISION__WAIT_MAX`, and if it does not come the answer is computed from the data and **not kept** (`cachedAnswer`, `store`).
  Cache misses queue for `COMPUTE__CONCURRENCY` slots (`computeSlots`, default half of `DB__POOL_SIZE`).
  A write made through another replica is therefore seen within the poll interval, not at once: a client that needs it at once passes the
  `X-Authz-Revision` of its write as `at_least_as_fresh`. When the revisions cannot be read, the last view is served (as long as `POLICY__MAX_STALENESS` allows, no limit by default; see "Resilience") and marked
  `degraded` (answers are kept in memory only); a view invalidated by a local write is not served then (the queries read the policy).
- The snapshot holds what a decision reads: services (without `client_secret`: `ServiceCheckColumns`), the type graph, the enabled
  memberships and rules *with* their validity windows (compared with the clock per request, `ruleActive`/`memberActive`), the role items, the
  access groups, the request attributes and the validity boundaries (for the TTL of an answer); the types with a live synchronizer are in the type
  graph (`TypeNode.Sync`). It is built in one `REPEATABLE READ` read-only transaction and never changed afterwards. `ServiceIDByRef` and `GetServiceForCheck` answer from it (an
  unknown or deleted slug is a 404 without a query). Every write that changes any of this must bump the revision (all admin writes go through
  `mutate` or the batch, which do); a change made by other means (`restore-admin` does bump) shows up at `SNAPSHOT__MAX_AGE` at the latest.
  `consistency=higher` does not use the snapshot (`cacheState.forConsistency`): it reads everything with queries, as before the snapshot.
- Resilience and operations (F5; `core/README.md` has the user-facing description):
  - **Probes** (`engine/ready.go`, `server.NewServer`), on the PDP's ops listener (`PDP__OPS_ADDR`, 9090; the public listener has none):
    `GET /healthz` has no dependency; `GET /readyz` (`App.Ready`) is 200 when the process is
    not draining (`App.SetDraining`), PostgreSQL answers one statement within 1 s (`db.SchemaVersion`, result reused for 1 s, not counted in the
    metrics) with the schema **at least** `db.LatestMigration()` (a newer one is fine: the previous release stays ready during a rolling
    update), and the view holds its snapshot (or the policy is deliberately not copied: `oversized`). Redis is not part of it. The worker has
    `/readyz` too (database and schema only). `licetum_degraded{component}` (redis: `redisx.Degraded` = the FastFail is tripped, open or half-open,
    or a recent failure; postgres: the view is unconfirmed for `staleLimit` or the statements fail) and `licetum_snapshot_age_seconds`
    (age of `localView.readAt`).
  - **Degraded PostgreSQL**: `readView` reads the revisions with a 1 s timeout (`revisionReadTimeout`; the snapshot build has its own 30 s),
    `currentView` serves an age-stale view degraded without joining a reading in progress (`viewState.reading`) while `degraded()` allows it
    (`POLICY__MAX_STALENESS`, 0 = no limit); beyond it the state is not ok, the queries fail and `server.LocalizeError` turns
    `db.IsUnavailable` errors into 503 `policy_unavailable` (and an expired request context into 503 `request_timeout`, a canceled one
    quietly). A degraded view serves answers past their `Exp` while their `Hold` (the next validity boundary, uncapped by `ResultCacheTTL`; `computed.hold`,
    `Prepared.hold`, `validityUntil`: the exact distance to the boundary, no one-second floor, counted from the start of the computation) has not
    passed (`cachedEntry.stale`, `result="stale"`; `hold_property_test.go` checks it against random policies with a manual clock, `App.clock`); `cachedEntry.serveUntil` is the ordinary serving
    time of the L1 entry, which now lives in the LRU until `Hold` (at most `maxStaleAnswerTTL`). While Redis is down (`App.redisDegraded`) an L1
    entry is served until its `Exp`, not ten seconds (`liveWithoutRedis`). `cachedAnswer` waits for the shared computation with `DoChan` and the
    caller's context, so a request deadline is honoured. `HTTP__MAX_INFLIGHT` keeps no latency under overload (the admitted requests wait behind
    at most that many others): `core/README.md` says how to size it, `chaos/f5_8_test.go` measures it.
  - **Shutdown** (`cli.StopServing`): `SetDraining` (responses get `Connection: close`), wait `SHUTDOWN__DRAIN`, `http.Server.Shutdown` with
    `SHUTDOWN__TIMEOUT`, then the deferred `app.Close()`. **Admission** (`server.withAdmission`, outermost; `App.Admit`): `HTTP__MAX_INFLIGHT`
    (default `GOMAXPROCS×256`) → 503 `overloaded` + `Retry-After: 1`, `licetum_shed_total`; `HTTP__REQUEST_TIMEOUT` (`App.RequestTimeout`: 10 s pdp,
    60 s admin/worker, none for an engine without a role); `/healthz`, `/readyz`, `/metrics` exempt.
  - **Connections**: `db.Connect` registers the uuid types for the simple protocol (`AfterConnect`) and, with `DB__PGBOUNCER`, uses
    `QueryExecModeSimpleProtocol` with no statement cache; `licetum_db_pool_*` (`poolCollector`); each process logs `connectionBudget`.
    `db.RunMigrate` (`DB__MIGRATE_URI`, never the simple protocol) is what `licetum migrate` and the tests call; `db.Migrate` uses the goose
    session locker (`newProvider`, 1 s polling) and `SeedPlatform` runs under `pg_advisory_xact_lock(seedLockKey)`. `SYNC__DBOS_URI` gives
    DBOS a URI of its own (it prepares statements).
  - **Breaker**: `redisx.RecordFailure` is one Lua script (INCR, PEXPIRE on the first failure or when the key has none, SET of the open key); `redisx.Breaker`
    carries the parameters for tests. `FastFail` does not gate the connection-setup commands (HELLO, ...) that run inside the probe. **Logs**: failures of a
    dependency on the request path go through `logx.Repeated` (`logDependency`, `errKind`; `licetum_log_suppressed_total`): one record per kind per second.
  - Tests: `cluster/f5_test.go` (I/M), `chaos/f5_test.go` (C, `LICETUM_CHAOS_REDIS_OUTAGE` shortens the minute, `LICETUM_CHAOS_POSTGRES_OUTAGE` the 30 s without PostgreSQL), `core/tests/integration/{db,redisx}`. The
    suites run through PgBouncer in transaction mode with `LICETUM_TEST_PGBOUNCER_ADDR` (`Infrastructure.Isolate` gives each test a database).
- **Read replica** (F6; `engine/replica.go`, `DB__READ_URI`, `DB__MAX_REPLICA_LAG`, `DB__LAG_ACTION`, `DB__LAG_INTERVAL`; `core/README.md` has the description): a PDP process
  may set `App.ReadDB` (`UseReadReplica`, `cli.Serve`). `App.reader(ctx)` is the pool of a read of the policy: the replica, or `DB` when there is none, when
  the request needs a current read (`withPrimary`: `consistency=higher`, or a revision the view did not reach, set in `cachedAnswer`; such an answer is not kept)
  or while the replica lags with `DB__LAG_ACTION=primary`. Reads of the check path (snapshot, revisions, closure, type graph, `ServiceIDByRef`) use `reader(ctx)`;
  writes, the worker, the sync pool and the OAuth reads use `DB`. `WatchReplica` measures the lag (replay lag and the time the revision has been behind the
  primary's); `/readyz` fails with `replica_lag` under the `unready` action; `replicaGate` makes a replica behind `POLICY__MAX_STALENESS` fail closed (503). Tests:
  `cluster/f6_test.go` against a real streaming replica (`scripts/pg-replica.sh`, `LICETUM_TEST_REPLICA_ADDR`, `task go:test:integration:replica`).
  Beyond that: a replica that cannot be compared with the primary counts as stale (`replicaUnconfirmed`, only for `POLICY__MAX_STALENESS`, not for
  readiness); `licetum failover` (`db.BumpAllRevisions`) raises every revision after a promotion and a revision that goes backwards is counted
  (`licetum_revision_regressions_total`) and drops the in-memory answers; `synchronizer.regions` + `WORKER__REGION` (table engine only) keep a source that one
  region reaches off the workers of the others (`claimJob`); `deploy/helm/licetum` (`deploy/helm/test.sh`, golden files in `tests/golden`, `kind-test.sh`),
  `docs/deployment.md`, the Compose environment `admin-api/tests/multiregion` (tag `multiregion`, `task go:test:multiregion`, `multiregion.yml`, switched off until P7 per ADR 0001 §10.7).
  An admin write is six round trips to the primary at most (`mutate` sends the change record and the revision bump as one batch; `checkAssignmentRuleRefs` reads
  the type once and returns its service): `chaos/f6_test.go` `TestF6_7_*` keeps it so, since the admin API of a far region pays the link for each.
- The query path is kept next to the snapshot path on purpose (`prepare`, `applicableRules` / `applicableFromSnapshot`, `cacheTTL` /
  `snapshot.ttl`, ...): a policy that does not fit, `consistency=higher` and the admin simulations use it. Change one, change the other;
  `TestF1_11_TheSnapshotPathAnswersLikeTheQueryPath` (random policies, both paths, answers and explanations) keeps them equal.
- Answers: `cachedAnswer` looks in `answersL1` (keyed like the Redis key, so the revision is in it; entries also record the data versions they
  read and an `exp`, which the value in Redis carries too) before Redis; tokens: `VerifyToken` looks in `tokensL1`, then Redis, then the
  provider, and remembers an `InvalidToken` failure by service and token hash in `invalidTokens`. `App.ClearMemoryCaches` empties the levels
  (tests). Values in these caches are shared by requests and must be treated as read-only.
- `jwksKeyFor`: one key set per URL, shared fetches (singleflight per URL), refetch for an unknown `kid` at most once per
  `AUTH__JWKS_MIN_REFRESH`; transport failures are `errJWKSUnavailable` (breaker), never cached.
- `App.Background` runs on a fixed pool with a bounded queue and returns whether the work was accepted; a full queue drops it
  (`licetum_background_dropped_total`). Work that registers a marker (`refreshInBackground`) must clear it when dropped.
- `redisx.ConnectWith(HotTimeout)` is how the PDP connects (`cli.Serve`, name `pdp`): read/write timeout, no retries, and a
  `FastFail` hook that skips Redis for 5 s after 3 failures in a row (half-open probe afterwards). Skipped commands are not round trips
  (`observer.ObserveRedis`).

Test infrastructure for the production-load work (`admin-api/tests/integration/README.md`): every pool made by `db.Connect` and every
Redis client given to `engine.New` is counted (`licetum_db_*`, `licetum_redis_*` metrics, `engine.Trace.DBQueries()/RedisRoundTrips()` for
tests); `harness.StartReplicas` builds several PDP replicas on one PostgreSQL and Redis (`WithConfig`, `WithHotRedis`). The tests build
their AuthZEN requests with `core/testsupport/authzenreq`: `Replica.Permission` sends the resource search for branches
(`harness.Branches`) for at least the revision of the last admin write (`Cluster.LastRevision`), and `Replica.Fresh` sends it with the
test lever `harness.Higher()` (the `X-Test-Consistency` header, not a query parameter). `admin-api/tests/oldendpoints_test.go` counts
the lines that still send an old route or spell an AuthZEN path, per file; the allowance of each file can only go down, and P2-23
deletes the file with the routes. `core/testsupport` has the fake source, the
fake IdP and the Toxiproxy `FaultProxy`; tests under the `chaos` build tag (`task go:test:chaos`, nightly `chaos.yml`) inject faults;
`admin-api/loadtest/budgets.yaml` holds the budgets the nightly `loadtest.yml` compares runs with (`licetum-loadtest budget`).

Web interface (run from `web/`, Next.js + antd; package manager pnpm — `packageManager` in `package.json`,
lock `pnpm-lock.yaml`):
```
pnpm dev            # next dev server on :3000
pnpm build          # locale check + next build
pnpm typecheck      # tsc --noEmit (noUnusedLocals / noUnusedParameters on)
pnpm knip           # unused files/exports/deps (src/api is generated and ignored)
pnpm test           # Jest: unit and component tests, the API is mocked
pnpm format         # oxfmt (format:check in CI); config .oxfmtrc.json — single quotes, width 100
pnpm orval          # generate src/api from ../backend/schema/admin.yaml (gitignored, never edit)
```

Full stack via Docker Compose (from repo root): `docker compose up` (`task dev`) — a dev setup: sources
are mounted, the Go apps run under air (hot reload, `dev` target of `backend/Dockerfile`), the web interface
runs `pnpm dev`. It runs postgres, redis, a one-shot `postgres_init` migration container
(`licetum migrate`), both backend apps and the web interface — see `docker-compose.yml` /
`infra/*.compose.yml` for the env vars each service expects (`DB__*`, `AUTH__*`, `REDIS__*`,
`CORS__*`, `PLATFORM__*`). The `runtime` target of `backend/Dockerfile` is the production image.

The orval client `web/src/api` is **not committed**: only `backend/schema/*.yaml` is. It is generated
by `pnpm orval` / `task generate`, by `task web:lint` (so `task check` type-checks it), by the
web image (`build-deps` stage copies `backend/schema` to `/backend/schema`) and by the dev compose
service on start. `web/Dockerfile` stages: `ci` (Node + Task for quality checks), `build-deps`
(deps + code + generated client, used by compose), `prod-builder` (`pnpm build`), `prod` (the standalone Next.js
server on :3000).

## Backend architecture

Layout under `backend/internal/`: `api/` (Huma v2 on top of stdlib `net/http`: operations with
OpenAPI docs, schema validation, middleware — HTTP concerns only) → `service/` (business logic, one file per domain, methods on
`*service.App`) → `db/` (pgx pool, `Querier` interface shared by pool and tx, goose migrations,
seed), `redisx/` (cache version, circuit breaker), `synchronizer/` (resource source adapters).
`model/` holds request/response structs and their `Validate()` (the pydantic equivalent);
`config/` reads env vars. Handlers never touch the DB directly; they call `service.App` methods.

API docs are generated from code, FastAPI-style: `/docs` (Swagger UI) and `/openapi.json` on both
apps, or `licetum openapi pdp` (the admin API: `licetum-admin openapi admin`) without a server. When adding or changing an endpoint:
register it with `register(api, huma.Operation{...}, handler)` in `internal/api` (OperationID,
Summary, Description, Tags, extra Errors), and document every field of its model with struct tags
(`doc`, `example`, `enum`, `format:"uuid"`; required = value field, optional = pointer +
`required:"false"`). Input body structs keep `` _ struct{} `additionalProperties:"true"` `` —
the web interface sends extra form fields. Path-param structs that are embedded must be exported types,
or Huma won't see their fields.

Settings: `config.Load()` reads env vars with the same prefixes as before (`DB__`, `AUTH__`,
`REDIS__`, `CORS__`, `PLATFORM__`, `ADMIN_AUTH__`, `SYNC__`, plus `HTTP__ADDR`, `PDP__ADDR`, `PDP__OPS_ADDR`, `PDP__PUBLIC_URL`, `PDP__AUDIENCE`, `PDP__SEARCH_SCAN_BUDGET`, `PEP__RATE_LIMIT` and `PEP__RATE_BURST`); names are case-insensitive, lists accept a
JSON array or CSV. `DB__URI` may keep the SQLAlchemy `+asyncpg` suffix (`db.NormalizeDSN`).
`PLATFORM__ADMIN_GROUP_NAME` replaces `PLATFORM__ADMIN_ROLE_NAME`; the old name is still read as a fallback. The OAuth client of the
self-service (`PLATFORM__AUTHORIZATION_URL`, `__TOKEN_URL`, `__JWKS_URL`, `__ISSUER`, `__CLIENT_ID`, `__CLIENT_SECRET`) is gone (they are in
`config.DeprecatedSettings`): administrators authenticate through `ADMIN_AUTH__*`, see "Administrator sign-in" below.

Errors: return `*apperr.Error` from the service layer, created from an i18n catalog key
(`apperr.NotFound(i18n.ServiceNotFound, id)`, `apperr.BlockedBy(list, i18n.RoleDeleteBlocked, id)`,
`apperr.FailedDependency(...)` for upstream/circuit-breaker failures). It implements
`huma.StatusError` and is itself the error body `{"detail": ..., "extra": {"type":
"generic"|"missing"|"blocked_by", "code": "<catalog key>"}}`; `huma.NewErrorWithContext` is overridden
(`internal/api/server.go`) so Huma's own validation errors use the same shape. Any other error becomes a logged 500 without details. Admin writes that affect permissions bump the revision of
the rules in their own transaction (`mutate`) and call `App.AfterWrite`. A 401 carries `WWW-Authenticate` (RFC 6750):
`apperr.Unauthorized` — no usable credentials (`Bearer`), `apperr.InvalidToken` — a malformed,
expired or rejected token (`Bearer error="invalid_token"`); `apperr.Error.WithHeader` adds others.

### Localization (backend)

- Languages live in `internal/i18n` (`Supported`: en, ru, zh; English is the default and the source).
  The request language is `?lang=` → `Accept-Language` → en (`withLang` middleware); responses carry
  `Content-Language`.
- Error messages: add a key + en/ru/zh texts to `internal/i18n/messages.go`; keys are also the public
  `extra.code`, so a key keeps its value when its entity is renamed. The people union was `role` before it
  became a group: its keys keep their legacy `role_*` values (`GroupNotFound` = `role_not_found`, …). The new
  roles use `role_definition_*` values. Russian texts keep the legacy Python wording.
- API docs: write summaries/descriptions/`doc` tags in English, then add the same string with its
  translation to `internal/api/locales/ru.json` and `zh.json`. `TestContract_API02_DocsTranslationsComplete` and
  `TestContract_API02_CatalogComplete` fail on anything missing or unused.

### Domain model

- **Service**: a client application registered with Licetum. Has its own OAuth2 config
  (authorization/token/jwks URLs, issuer, client id/secret) used to verify tokens presented to
  `/permission`, and a **slug**: lowercase Latin letters, digits, `-`, `_`, unique among live services and
  never a UUID (`model.ValidSlug`; `00009_service_slug.sql`). The PDP takes the service as one path
  segment — its id or its slug (`App.ServiceIDByRef`). Licetum itself is registered with the slug
  `model.PlatformSlug` (`licetum`, reserved: no other service takes it; a name that makes an invalid slug
  falls back to `service-<12 hex digits of the id>`, `00009_service_slug.sql`). An empty/NULL `client_secret` is a public client (sign-in with PKCE);
  in PATCH an absent `client_secret` keeps it, `null`/`""` removes it (`model.ClearableString`); the response says only
  whether one is stored (`has_client_secret`). The application `url` and the `icon` are optional and are never requested
  (no live check from the server: `""` removes them in PATCH). `trusted_clients` lists the client applications (the `azp`, or
  `client_id`, of their tokens) the service trusts to ask on behalf of a person: see "Calls on behalf of a person".
- **TypeResource**: a resource *type* belonging to a service (e.g. "project", "branch"), with any
  number of parent types (`type_resource_parent(type_resource_id, parent_id, inherit)`) — a DAG, not
  a tree. The parent link is both structural *and* the permission-inheritance chain — it can cross
  service boundaries (e.g. `division → branch → planogram` where each level belongs to a different
  service), which is how a grant on one service's type cascades into another's. `inherit` on a link
  is the set of access levels (grants *and* denials) that flow down along it; NULL = all levels.
  `access_implies` (`{"write": ["read"]}`) is the level hierarchy of the type: granting write grants
  read, denying read denies write. Cycles are rejected on write (`checkParents`). The request body
  still accepts the deprecated `type_resource_parent_id` (= one parent, all levels; `Validate()`
  normalizes it into `parents`), the response keeps it only for a type with exactly one parent. The name is unique within the service among live rows
  (partial unique index `uq_type_resource_service_name` + 409 `type_resource_name_taken`) — types
  are keyed by name in `/permission`.
- **Type mode is derived, not stored**: a type is `sync` if a live (not deleted) Synchronizer points
  at it, otherwise `not_sync` — a "permission without resources" (a capability such as
  `advanced_working_mode`): it has no resources, and a rule with `resource_id = NULL` grants access
  to the type as a whole. The single SQL definition is `liveSynchronizerExists`
  (`internal/service/synchronizers.go`, used by `hasSynchronizer` and `wholeTypeAccess`). Hence a
  rule on a specific resource (`resource_id` / `resource_from_claim`) is only allowed on a type with
  a synchronizer (`checkRuleTarget`, 400 `assignment_rule_needs_synchronizer`; re-checked on PATCH
  only when the target changes, so a rule that died with its synchronizer can still be disabled);
  the rule list marks such dead rules with `target_valid = false`. The SQL definition is `engine.LiveSynchronizerExists`; the engine reads it
  once per type graph (`TypeNode.Sync`, `TypeGraph.IsSync`, so the snapshot has it).
  **A type without a live synchronizer has no resources**, also one whose synchronizer was deleted: the rows it reported stay in
  `resource_cache` (a synchronizer created again finds them) but no decision reads them. `IsSync` is what every path that reads a resource
  asks: a point rule on one anchors nothing (a denial stays as an absent anchor, see "Decision semantics"), a question about one is
  `resource_not_found` (`decideChecks`, `CacheLookup`, the reverse lookup, AuthZEN, the simulations), no cascade reaches the type (the result
  types are the types that have resources) and `resource_where` matches none; a `resource_from_claim` rule whose lookup type has none matches
  nothing and its climb does not pass through such a type (`climbTypes`). An allow rule that finds nothing for that reason covers nothing;
  a **denial** must not lapse with the resources it read, so it keeps an absent anchor at the rule's type (`buildInstances`: a
  `resource_from_claim` denial with a claim in the request whose lookup type, rule type or a type on a route between them has no resources
  (`TypeGraph.LookupCut`), and a `resource_where` denial on a type that has none; whether or not the claim matches anything, since the
  anchor is not known): like the denial on a missing resource it covers nothing for real, and the resources below a link to a parent that
  is not there fall under it. Such a rule is `target_valid = false` and `GET /service/{id}/policy/lint` reports it
  (`lint_rule_lookup_dead`). A link of a resource of a type below to one of those rows is
  unresolved (`UnresolvedParents`, `resourceParents`): the closure does not lead through them (`rebuildClosureSQL`). The closure is stored,
  so it follows the mode in the write that changes it: creating or deleting a synchronizer runs `engine.App.ChangeMode` in the same
  transaction (the lock of the type's component, the timeouts of a synchronization, `rebuildClosure` of the type's rows and of the rows
  that hang below them by the declared links, the data revision of the services of the types below), and `AfterClosureMove` follows the
  commit; the routes are cut or back when the request returns, whatever the sources and the workers of the types below do. `rebuildClosure`
  reads the types that have resources in its own transaction (`typesWithoutResources`), not from the graph a synchronization loaded
  before it fetched, so a synchronization that was running while the synchronizer was deleted does not write the route back. The structure
  signature of the types below also marks a type that lost its synchronizer (`TypeNode.SourceDeleted`), so their next synchronization
  rebuilds again (harmless, and it stores the new signature). A mode that changes by other means (`restore-admin` switching the
  synchronizers of the platform types back on) and a change of the type links are followed by the synchronization of the types below
  only. The query path and the snapshot path read the same graph, so they agree.
- **Synchronizer**: per-TypeResource adapter (SQL query or HTTP endpoint, see
  `internal/synchronizer/adapter.go`) that fetches the live list of concrete resources for a type
  from the owning service. Wrapped in a Redis circuit breaker (`internal/redisx`) and
  `singleflight` to collapse concurrent duplicate fetches. SQL sources use SQLAlchemy-style `:id`
  in the stored query (translated to `$1` by `BindIDParam`) — keep that format for compatibility.
  An HTTP config takes optional `headers` (e.g. `Authorization`) sent to the source and never
  logged. Secrets (the DSN password, header values) are returned masked as `********`
  (`SynchronizerConfig.Masked`); the mask sent back in an update means "keep"
  (`SynchronizerConfig.RestoreSecrets`, applied before the probe).
  `cache_ttl` (seconds, 0–86400; omitted means `SYNC__FETCH_TTL`, default 10) keeps the fetched list in Redis
  under `authz:fetch_all:<synchronizer id>:<sha256 of the config with its secrets masked>` (`Adapter.FetchAll`;
  an empty list is kept too: it is the source's answer). `consistency=higher` (`fresh`) asks the source anyway and
  refreshes that list. A source that does not answer (transport error, bad status, malformed or `null` body, open breaker,
  SQL source without `list_query`) is an error (`FetchAll` returns it): the sync is skipped and the data stays.
  A synchronization is a **job** (`sync_job`, `engine/sync_jobs.go`, `engine/sync_worker.go`): the PDP and admin processes
  ask for it with `Ensure` and wait for the result for a bounded time, the `worker` role executes it (see item 3 of the
  permission computation). Requests for the same type, in any process, share one job; the source of a type is asked once.
  A type has **one** source: a second `POST` is 409 `synchronizer_exists`. `POST …/resource/synchronizer/test` tries a
  configuration without saving it (`service/source_check.go`: total, sample, repeated ids, parents that are nowhere, attributes;
  a failure message has the secrets of the configuration redacted) and `POST /type_resource/{id}/resource/refresh` asks the
  source now (`RefreshResources`: `ensure` with `Fresh`; 422 without a source, 424 when it did not answer or the worker did not finish in `SYNC__ADMIN_WAIT`).
- **ResourceCache**: the local copy of the resource hierarchy fetched by the synchronizers
  (`internal/service/resource_cache.go`) with an `active` flag (a resource gone from its source is
  switched off, not deleted). The permission computation works on this copy only and never asks a
  source per resource. A resource may have several parents, of different types: `resource_edge(child,
  parent_type, parent_external_id, parent_id)` holds the declared links (`parent_id` NULL = the parent
  is not in the cache yet — an *unresolved* edge), and `resource_ancestor` is the transitive closure
  (ancestor, descendant, `inherit` = union over routes of the intersection along a route). The
  descendant's type, external id and name are stored in the closure row itself, so enumerating the
  descendants of an anchor is one index range scan without a join (measured faster than the old ltree
  GiST scan; the naive closure + join was 6–10× slower). Sources report parents either in the
  `parent_id` column (one parent, the type has one parent type) or, for several, in the optional
  `parents_query`/`parents_list_query` (columns `parent_type`, `parent_id`, plus `id` for the list).
  **Sync contract**: a source resource has the required `id` and `name`, the optional `parent_id` (or
  `parents`) and *any other column (SQL) / field (HTTP, or an explicit `meta` object)*, which becomes
  the resource's attribute in `resource_cache.meta` (jsonb; `synchronizer.normalizeMeta`: strings —
  numbers and booleans are rendered like claim values — or lists of strings; NULLs, objects, invalid
  names (`model.ValidMetaKey`: `[A-Za-z0-9_-]{1,63}`, not `id`/`name`/`parent_id`/`parents`/`meta`),
  over 32 attributes, values over 1024 characters and lists over 64 items are dropped). Attributes
  are part of the sync digest, so a change is written by the next sync, which also bumps the data version
  of the service that owns the type (`revision_state`, scope `data:<service>`, see item 7 of the permission computation). The admin list
  (`GET /type_resource/{id}/resource`) also brings the parent types up to date, so a link is `resolved: false` only when its
  parent is really missing; a resource gone from its source stays with `active: false` and is listed with
  `?include_inactive=true`.
- **Group** (was `role` before the split, see `00007_role_to_group.sql`): a union of people — a *global*
  identity, not itself a service or type-scoped concept. A group is just another `subject`: it gets
  permissions the same way a user does, via AssignmentRule. Its synthetic subject string is
  `model.GroupSubject(groupID)` = `group:<uuid>`. Table `user_group` (`group` is a reserved word in SQL).
- **GroupMember**: makes a subject a member of a Group, either directly or via a `predicate` matched against
  JWT claims (e.g. "everyone with `department=sales`"). Members can also be other groups (`member_group_id`),
  forming nested group membership resolved transitively in `core/engine/permission.go::IdentityOf`: round by round, each
  reading the previous one, through at most `MaxGroupDepth` (16) nestings whatever the order of the memberships.
- **Role** (`00008_role_permission.sql`): a *named set of permissions*. Its items (`role_permission`) each carry
  a target and access levels, the same shape as an inline rule — the whole type, a `resource_id` or a
  `resource_from_claim` (checked by `checkResourceTarget`, like a rule). A role is neither a subject nor a member
  of anything: it is granted by an AssignmentRule with `role_id`. A group answers "who", a role answers "what".
  Items cannot contain `*` — denying everything is possible only with an inline deny rule. An item on a platform
  policy needs write on that policy as a whole (`checkPlatformTargets`, as creating a rule on it does): a point
  write on the role does not reach the platform. Deleting a role that
  a rule grants is 409 (`extra.blocked_by: assignment_rule`); a type that an item points at cannot be deleted
  (409, `role_permission`), and so is a type that a claim-based target uses as its lookup type
  (`resource_from_claim.lookup_type_resource_id`); items go with their role.
- **AssignmentRule** (formerly "Grant" — some comments still reference the old name): grants either a **role**
  (`role_id`; the inline fields are then NULL, DB CHECK `ck_assignment_rule_role_xor_inline`) or an **inline
  permission**: `access` (a list of strings, e.g. `["read","write"]`) on a TypeResource. To whoever it applies:
  exactly one of three mutually-exclusive targets (enforced by DB CHECK constraints): a literal `subject` (a user,
  or `group:<uuid>`), a `predicate` over claims, or `member_of_group_id`. An inline permission targets exactly one
  of two resource shapes: a literal `resource_id` (or `None` = the whole type), or a
  `resource_from_claim` spec that resolves a concrete `resource_id` dynamically from the caller's
  claims at check time (e.g. "job title → the branch-hierarchy node they're scoped to").
  `resource_from_claim.match_by` is `name`, `id` or `meta.<attribute>`: with an attribute the claim
  value is matched against the attribute the source reported (equal, or a list containing it — both
  forms use the GIN index on `meta`). The anchors are the matched resource's ancestors of the rule's type through the
  resolved resource links, whatever the inherit masks (`findAncestorsBatch` walks the edges, not the closure, which keeps
  only pairs some level flows between); the types the climb passes are synchronized with the rest (`TypeGraph.TypesBetween`).
  The climb stops at a resource of the rule's type (the type graph has no cycle) and follows only the links to those types
  (`ancestorWalkSQL`); `core/tests/integration/engine/walk_test.go` checks it against a climb to the roots and has its
  benchmark. With the lookup type equal to the rule's type this is the
  "owner" pattern: one rule `{claim: preferred_username, lookup type: project, match_by: meta.owner}`
  gives each user access to the projects (and their descendants) whose `owner` attribute is theirs.
  `effect` is `allow` (default) or `deny`; a denial may use the level `*` (every level). An allow
  rule with `exception = true` lifts the denials above it. `valid_from`/`expires_at` bound the time
  the rule is in force (also on GroupMember). Predicate operators: `eq`, `ne`, `in`, `not_in`,
  `regex`; a claim missing from the token never satisfies a condition, `ne`/`not_in` included
  ("everyone except ivanovich" is `username ne ivanovich`). `eq`, `ne` and `regex` take one value: with a list value they
  never hold (validation lets it through, as the Python version did; use `in`/`not_in`). A role assignment takes the effect and the
  exception of the rule for all of the role's items; `expandRoles` turns it into one rule per item (same id,
  subject and validity, the item's target and access), so the engine treats role permissions like inline ones.
- **Conditions and request context** (`predicate.go`, `conditions.go`): a condition reads a claim (`source` claim, the
  default), an attribute of the request (`source` context: the check body, the `context` query parameter, the AuthZEN
  `context`), or the clock (`source` now: `hour`, `weekday`, UTC). Operators: `eq ne in not_in regex`, numeric
  `gt ge lt le`, `in_cidr`. An attribute the request does not carry makes a condition *unknown*: an allow rule grants
  nothing on it; a deny rule applies on it only with `on_missing_attribute: apply` (the default of a new deny rule;
  `skip` for allow rules; a deny that stays a deny keeps its value, an allow turned into a deny takes `apply`). A group
  membership whose predicate reads a missing attribute is unknown too (`identitySet`). `requires_predicate` and
  `requires_group_id` guard any
  rule. `resource_where` covers the resources whose attributes, as the synchronizer reported them (`resource_cache.meta`),
  match (exclusive with `resource_id` and `resource_from_claim`; needs a synchronizer). The database narrows the rows by
  containment on `meta` for equality and membership conditions, as a string and as a list element (containment does not
  match a list against a scalar, so both shapes are looked up); `resourceMatches` decides. `subject_kind` is a label only.
  The cache key of an answer covers the attributes the rules read, and an answer that reads the clock lives until the
  next minute.
- **AccessGroup** (`access_group`, `access_groups.go`): named access levels of one resource type. A rule or a role item
  lists `@name` in its `access` (`expandAccess`). Renaming or deleting a group that a rule or an item names is refused
  (409, `blocked_by`).

- **Subject directory** (`subject_directory`, `subject_directory_seen`, `subject_directory_settings`, migration `00008`; `core/db/directory.go`,
  `admin-api/service/directory.go`, `admin-api/api/admin_directory.go`; ADR 0001, 8.8): the people of a **service**, sent on purpose by its
  application or an editor. It replaces `known_subject` (global, filled by every token; the registry is removed in the second step of the
  directory step). Six routes under `/service/{id}/subjects`: `PUT` (up to 1000 people, each once; `{type?, id, name?, properties?}`, the
  properties stored as sent within 100 entries and 16 KiB), `GET` (list: `type`, `search`, `limit`, `offset`, with a total, ordered by type
  and then bytewise by id), `GET|DELETE …/{type}/{id}` (an id with `/` goes percent-encoded; the entry comes with the groups the stored
  properties put the person in), `GET|PUT …/settings` (`retention_days`, null: forever, the default). The id of a person is kept exactly
  as sent and never starts with `group:`. The upsert writes only a row whose name or properties differ (`UpsertDirectory`, `IS DISTINCT
  FROM`): an unchanged batch touches no row of `subject_directory` and takes **no revision** (no decision reads the directory); the time
  an entry was last sent is kept apart in `subject_directory_seen` and refreshed at most hourly. The worker's retention (`RunRetention`,
  `db.PruneDirectory`, 10 000-row chunks) deletes the entries not sent within their service's `retention_days`. Until the roles of the
  service (P4) the routes are governed by the platform policy `group_member` (whole type). The key of a scan is (type, id) bytewise
  (`DirectoryAfter`, index `ix_subject_directory_scan`). Tests: `directory_test.go` in `admin-api/tests/integration/api` (DIR02, DIR04) and
  in `core/tests/integration/db`.
  **The registry of known subjects is gone.** `known_subject` (global, filled by every verified token, `SUBJECTS__ENABLED`/`SUBJECTS__RETENTION_DAYS`,
  `NoteSubject`, `/subject`) was removed in favour of the directory: nothing writes the table any more, and `RunRetention` drops what the previous
  release left in it after a fixed 90 days (no setting; the table goes with the schema of P3). The directory feeds the AuthZEN subject search
  (below), the people a rule reaches (`peopleReached`, `service/access_lookup.go`) and the person page. `peopleReached` tries each rule on the
  people of the directories of **the services of the rule's types** (the type it targets, the type its claim is looked up in, the types of the
  items of the role it grants; `servicesOfRules`), decided with the stored properties as claims: a service with an empty directory has no people, the
  directory of another service is not counted, a person of two of a rule's services is counted once (DIR03); at most `engine.EvaluationLimit`
  (5000) people are tried (`limited`). The administrators of the platform are no longer noted when they sign in: they appear in the rules and
  memberships that name them, or in the directory of the platform service if somebody sends them there.

### Self-service: access policies for Licetum itself

The admin API is protected by the same permission mechanism as any client service. `licetum migrate`
(`db.SeedPlatform`, when `ADMIN_AUTH__*` is configured) registers Licetum as a Service within itself (`PLATFORM__*`, deterministic uuid5
ids in `internal/model/platform.go`) and creates resource types that act as policies
(`model.PlatformTypes`): `service`, `type_resource` (+ its synchronizers and resource list), `group`,
`group_member`, `role` (+ its permission items: access to them is access to the role itself) and
`assignment_rule`. Each has a Synchronizer(kind=sql) reading its own table — without it a type has no
resources, and hence no whole-type access (that is expanded into per-resource entries).

Together with the service it seeds the platform admin group (`PLATFORM__ADMIN_GROUP_NAME`), its members
matched by a predicate over claims (`PLATFORM__ADMIN_CLAIM` in `PLATFORM__ADMIN_CLAIM_VALUES`), and rules
giving the group read+write on each policy as a whole. The seed never touches existing rows and only
creates the admin group and its rules in the run that creates the policy types, so revoked admin access is
not restored by a later deploy. If the self-service is not seeded, the admin API is closed to everyone (503).
The migrations `00007` and `00008` move the previous release's `role` and `role_member` policies to `group`,
`group_member` and the new `role` (same uuid5 ids as `model.PlatformTypeResourceID`), keeping the rules; the admin
rules have the ids `model.PlatformAdminRuleID` gives (a fresh install and an upgraded one are the same).

**Last admin access** (`service/admin_access.go`): a write that edits or deletes a rule, a membership, a group, a role or a role
item is refused (409 `last_admin_access`) when it leaves no rule in force that allows write on the whole `assignment_rule` policy for
somebody (a group needs a live member; a predicate or a nested group counts as non-empty), while somebody could before. It runs
in the write's transaction, and once at the end of a batch (so a batch may replace the admin access); a platform that is already
locked is not blocked further. A denial rule is not looked at: `licetum restore-admin` is the way out.

**Administrator sign-in** (ADR 0001, 8.12; `core/config` `AdminAuth`, `engine/admin_auth.go`, `admin-api/service/platform.go::Authenticate`, `web/src/lib/oidc.ts`):
administrators authenticate at one static OpenID Connect provider, `ADMIN_AUTH__ISSUER` / `__AUDIENCE` (required) / `__JWKS_URL` /
`__SUBJECT_CLAIM` (default `preferred_username`, then `sub`). The admin API verifies the bearer token with a `pepauth.Verifier` (the
key sets of the PEP issuers share its code): `iss` exact, `aud` must contain the audience, `exp` required, `nbf`, an asymmetric `alg`;
a missing or bad token is a 401 (`WWW-Authenticate`, `invalid_token` for a token), keys that cannot be read a 424, no `ADMIN_AUTH__*`
a 503 `admin_auth_not_configured` (the `admin` command refuses to start without them; half of the three is a config error). The
permissions still come from the platform policies below. The seed and `restore-admin` need only `ADMIN_AUTH__*` (the service row has no OAuth
client: its OAuth columns stay NULL); `restore-admin -user` matches the subject claim. The web interface is the OIDC client (code flow with
PKCE; `OIDC_*`): `/auth/login` seals the state, nonce and PKCE verifier into the cookie `licetum_flow` (ten minutes, path `/auth`),
`/auth/callback` checks it, exchanges the code at the token endpoint and writes the session cookie, the tokens are renewed at the token
endpoint; nothing is kept on a server. Tests: `TestContract_ADM01_*` (`admin-api/tests/integration/api/admin_auth_test.go`),
`TestContract_ADM13_*` (`core/tests/integration/db/admin_auth_test.go`), `TestContract_WEB01_*` (`web/tests/oidc.test.ts`).

Enforcement: `internal/api/auth.go`. An operation declares its policy with
`withPolicy(op, requireAny|requireWholeType|requireResource(type, action, pathParam))`; the Huma middleware
`platformAuth` verifies the token against the self-service (`service.App.Authenticate`) and checks the policy
before the body is parsed. Lists need read on any entity and are filtered with `filterAllowed`; changing an
entity needs write on its id, except that a rule's target (type, role, resource, `resource_where`) and a rule or a role
item on a platform policy need more: the policy as a whole (`api/admin_targets.go`, the same checks in a batch);
**creating needs write on the whole type** (a rule with `resource_id = NULL`, see
`service.wholeTypeAccess`) — a point write does not allow creating. `GET /platform` returns the platform service id, the id of the admin group and the platform policy types with their ids; `GET /platform/me` returns the caller's
access per type (`whole_type`, `entries` — entities whose levels differ from it, `denied` — entities with denied
levels; a denial beats the whole-type access, `Access.Can` looks at the entity's own entry only); `GET /platform`
and the OAuth endpoints stay public. Writes that change a policy's entities (services, types, groups, members,
roles, role items, rules) bump the permission cache version.

### Permission computation (`internal/service/permission.go`)

This is the core algorithm and the most important part to understand before touching anything
authorization-related. The entry point is `GetPermission(ctx, service, subject, env, consistency)`, where `env` (`service.NewEnv`) holds the
claims, the request context and the time of the decision;
it returns the typed result `{type_name: sync | not_sync}` used by the PDP and the self-service
(`Authenticate` uses `getPermissionData`, which also returns the denied entities).
1. Resolve the caller's identity set: `{subject} ∪ {group_subject(g) for every group g they belong to,
   transitively}`; group memberships and rules outside `valid_from`/`expires_at` are ignored
   (`resolveIdentitySet`, `ruleActiveSQL`); whether a rule targets the caller is `ruleApplies`.
2. Load the type graph (`typegraph.go`, in memory: `flow[a][b]` = levels that flow from type a down to
   type b along all routes) and the applicable assignments in two passes: direct subject rows (indexed) and
   predicate/group rows (all enabled rules are scanned, matching subjects cannot be looked up). Then
   `expandRoles` replaces every role assignment by one rule per item of the role, keeping only the items on
   types the service reaches (`role_permission` joined with `type_resource`, deleted types excluded).
3. `ensure` (`engine/sync_jobs.go`) makes sure the types the rules reach are synchronized; **the check path never writes the
   graph**. State is in PostgreSQL: `sync_state` (per type: the digest `format`/`data_hash`/`graph_sig` of the last
   synchronization, `applied_at`, `next_due_at` = the throttle of 30 s or the end of the backoff, `attempts`, `last_error`) and
   `sync_job` (queued | running | done | failed, with the lease). One query reads the states of the involved types, and each
   type falls in one case: no data under the current structure (never synchronized, or the type graph above it or its
   synchronizer changed: `structureOf`) or a platform type that a write made due → the request enqueues the job and
   **waits** for it up to `SYNC__REQUEST_WAIT` (5 s), then answers from what exists (the type contributes no resources, the
   answer is cached for 2 s only, the response has `X-Authz-Sync: pending`); throttle run out → the job is queued without
   waiting and the answer comes from the data as of the last synchronization; in backoff → nobody asks. `consistency=higher`
   always waits for a *fresh* job (its own dedup key) that skips the throttle and the cached source list; fresh jobs ask
   a type's source at most once per `SYNC__FRESH_MIN_INTERVAL`. There is one job per type (`dedup_key`, unique while
   queued/running): concurrent requests in any process attach to it and poll `sync_job` (50–500 ms, one poller per job per
   process). The worker claims a job with `FOR UPDATE SKIP LOCKED` under a lease (`SYNC__LEASE`, renewed every lease/3, new
   `lease_token` per claim), fetches the sources in parallel (circuit breaker + singleflight; lists cached in Redis for the
   synchronizer's `cache_ttl`), compares the digest with `sync_state` and applies the changed types in **one transaction**: it
   takes the advisory lock of the connected component of the type graph (`ComponentKeys`; `lock_timeout` 5 s, `statement_timeout`
   `SYNC__STATEMENT_TIMEOUT`; on `DB__SYNC_POOL_SIZE` connections, apart from the request pool), upserts rows, switches missing
   ones off, diffs edges, resolves unresolved edges, rebuilds the closure for the touched nodes and their descendants
   (`rebuildClosureSQL`), writes `sync_state`, and ends with `UPDATE sync_job SET state='done' … WHERE id AND lease_token`:
   zero rows = the lease was lost, the transaction rolls back (`licetum_sync_lease_lost_total`). A source that did not answer
   sets `attempts+1`, `last_error` and `next_due_at = now() + min(30 s · 2^attempts, 10 min)` for all replicas; the data stays.
   An answer with no resources is the source's truth: it switches off every resource of the type. An admin write marks the
   platform types due (`AfterWrite` → `invalidatePlatformSync`, `invalidated_at`): the next request waits for a job that
   fetched after the write; it is the only effect of a write on the synchronization (a change of a type or a synchronizer
   changes its structure signature instead). The digest keeps its shape; `format` 5 (4 is read too) is rewritten only when
   data or graph signature changed, and a start of the worker imports the previous release's Redis hashes
   (`authz:resource_sync:hash:<type>`, `v4:<data>:<graph>`) for types without a row. The worker's schedule (`SYNC__SCHEDULE_TICK`)
   queues due types that someone asked for within `SYNC__DEMAND_WINDOW` (all with `SYNC__KEEP_WARM`), gives up a job claimed
   5 times, and prunes old jobs. `SYNC__ENGINE` is `dbos` by default (`postgres` selects the `sync_job` table below); the DBOS engine runs the same pipeline on DBOS workflows (`engine/sync_dbos.go`, see core/README.md: queue `licetum-sync`, dedup id as the shared job, an executor heartbeat in `sync_executor` and a janitor that takes over the workflows of dead executors, the workflow row read `FOR SHARE` as the fence); both engines pass the same acceptance tests (`LICETUM_SYNC_ENGINE=postgres go test -tags integration …` runs the suite on the table engine). `App.Role` (pdp, admin, worker; none = unrestricted for tools and tests) refuses to run jobs
   outside the worker. Every committed change of a type's data bumps the data revision of its service in the same transaction
   (`bumpDataRevisions`, see F2), and the worker then calls `invalidateView` (and `recordDataChange` for the previous release). `resolve` (one resource
   refreshed inline by the admin API) writes under the same component lock and timeouts.
4. Rules become *instances* (rule × anchor; a `resource_from_claim` rule yields the anchor found by one
   `findAncestors` query). Each instance covers the anchor's descendants through the closure
   (`expandAnchors`) with the level mask that flows to them; a whole-type rule covers all resources of the type
   and of its descendant types.
5. The pure engine (`decision.go`) decides every level of every resource — see "Decision semantics".
6. The result is `{type_resource_name: [{id, name, access}]}` filtered to the requested service;
   `not_sync` types come from `decideWhole` (whole-type rules only, no cascade from ancestors; a whole-type rule
   on a `not_sync` type with `sync` descendants still cascades to their resources). Response:
   `{name: {"type": "sync", "resources": [...]} | {"type": "not_sync", "access": [...]}}`; a `not_sync` type
   without rules is absent, on a name collision `sync` wins.
7. Cached in memory and in Redis per `(service, subject, claims fingerprint, revision of the rules)`: key
   `authz:permission:t9:r<revision>:<service>:<subject>:<fingerprint>` (bump `t9` when the format or the decisions change; the
   fingerprint covers only the claims and the request-context attributes the rules and the role items look at, which the snapshot
   knows — a process without it remembers them per revision, `attrsMemo`). TTL = min(60 s, the next moment a rule, a role item's
   assignment or a membership starts/stops being in force). Admin writes bump the revision of the rules in `revision_state`. A cached
   entry also records the revisions of the data of the services whose resource data it read (scope `data:<service>`, bumped by
   synchronizations and by `resolve`): it is served only while those are unchanged, so a change in one service leaves the answers that
   do not read it in place. The whole computation runs through `cachedAnswer` (`engine/cache.go`): the revisions come from the view
   (`readCacheState`), concurrent misses for the same key share one computation (singleflight) and queue for a computation slot, and the
   entry is written in the background. `POST /check` answers are cached the same way (`authz:check:t5:…`, keyed by a hash of the
   questions and of `explain`).

### Decision semantics (deny wins, explicit exceptions)

- allow rules add levels; deny rules take levels away from everything they cover, wherever an allow
  rule lies. A grant on a *resource* covers its descendants in the graph (only the levels the links
  `inherit`); a whole-type rule covers all resources of the type and of the lower types.
- An **exception** (allow rule with `exception = true`) lifts a denial for the resources the exception
  covers, if it lies *strictly below* the denial. Every denial covering a resource needs its own
  exception below it, so the deepest of the denials and exceptions above a resource decides: a denial
  below an exception stands. Ties (same anchor, same type) go to the denial. Exceptions work on
  whole types as well (`resource_id = NULL`, `exception = true`).
- "Below" is decided per level: a resource anchor is below another if that one is its ancestor
  (`pairs` from the closure); a whole-type anchor is below another if its type is a lower type; a point
  anchor is below a whole-type anchor of its own type.
- Level hierarchy (`access_implies`): granting `write` grants `read`; denying `read` denies `write`.
  Denial level `*` denies every level that flows to the resource, and with it every level that includes one: `*` arriving
  through a read-only link denies read, and write where write includes read (`denyMatches`).
- **Fail closed**: a resource with an unresolved parent edge (the parent is not in the cache) is
  treated as possibly lying under any denial that could cover it (`assumed` covers, reported as
  `unresolved_parent` in explanations); it recovers when the parent appears. A denial on a resource the cache does not
  have stays as an absent anchor (`Instance.Absent`): it covers nothing for real but is assumed below an unresolved link
  like any point denial, so an orphan does not escape a denial on its own missing parent. A denial whose anchor is already a
  known ancestor is assumed too, for the levels the known routes do not carry (`CoversOf`, pass C of `coverNodes`; the
  explanation flags the assumed levels only). Only an exception provably below a denial lifts it, so a point exception on the
  orphan itself does not lift an assumed one. The same holds for a denial that cannot find its anchors because a type it needs
  has no synchronizer (see "Domain model", Type mode). Known gap: a `resource_from_claim` denial whose climb from the matched resource
  reaches an unresolved link (a parent that is not in the cache, not a type without resources) before the rule's type finds no anchor
  (both the engine and the reference evaluator). A denial by claim whose lookup type has no resources denies the rule's own type nowhere
  (its anchor is not known), only what lies below a missing parent.
- Worked example (division → rrs → branch → item; `D1 ⊃ R1, R2`, `R1 ⊃ B1, B2`, `R2 ⊃ B3`, `B1 ⊃ I1`):

  | rules | branches visible |
  |---|---|
  | allow read on D1 | B1, B2, B3 |
  | + deny read on R1 | B3 |
  | + exception read on B2 | B2, B3 |
  | + exception on B1 and deny on I1 | B1, B2, B3; I1 is still gone (denial below the exception) |
  | allow read on the whole type `branch` + deny `*` on B2 | all but B2 |

  These scenarios, and the rest of the semantics, are the decision contracts: the tables in
  `core/testsupport/policyfix/testdata/decisions` (`worked_example.yaml` is this one), checked against the engine and the
  reference evaluator (see "Commands").

### Batch, history and policy tools

- **Reverse lookup and preview** (`access_lookup.go`, `grant_preview.go`): `GET …/type_resource/{id}/access?resource_id=` lists the rules
  in force that cover a resource (`coversOf` over the instances of every subject's rules; denials first; rules found by a claim apart),
  one entry per instance with `via`, the levels that reach it, `who` and the known people (a denial covered both for real and through a
  missing parent is one entry: `assumed_levels` are the levels only assumed, `assumed` says none reaches for real; `reachesOf`; one rule
  can still make several entries reached the same way — role items, `resource_where` anchors — so the interface keys rows by position);
  `POST /assignment_rule/preview` says what a rule body would do:
  the resources it covers through the closure (`expandAnchors`), the known people it reaches, the denials and exceptions above or
  inside it that may reach the same people (`mayReachSame`), and the rules that already give the same. Both need read/write on the
  whole `assignment_rule` policy because they show other rules, and neither writes.
- Policy scenarios are stored per service (`policy_scenario`, `/service/{id}/policy/scenarios`, unique name per service): fixtures, so
  no cache bump and no change-log record.

- `POST /batch` (`batch.go`, `api/admin_batch.go`): create, update and delete operations on assignment rules, groups,
  group members, roles, role items and access groups, in one transaction. A failing operation is named in
  `extra.operation` and nothing is applied; `CheckOperations` checks the shape first. Each operation needs the policy of
  the admin route that makes the same write. An entity the batch creates is covered by the right that created it: the
  caller's access was read before the batch.
- Inside a batch, `mutate` writes into the batch's transaction (`withBatch`, `tx.go`), and the cache version is bumped
  once, after the commit. Creates take an optional id: the history and the import use it to restore or name entities.
- The change log keeps the entity before and after each change, so it is the undo log. `PlanRevert` and `PlanRollback`
  (`history.go`) turn records into operations: create -> delete, delete -> create with the same id, update -> write the
  previous body back. `POST /changes/{id}/revert` and `POST /changes/rollback` apply them through the batch (`dry_run`
  plans only). Only the batch's entities can be reverted; deleting a role records its items too. `GET /changes?wait=N`
  long-polls the log: the service polls the table, so the records of every replica are seen. Known limitation: ids are not
  in commit order, so a poller can pass a record that commits after a higher id; a fix needs a commit horizon.
- Policy tools (admin, read access on the service): `POST /service/{id}/policy/test` runs scenarios through the
  simulation and reports the rules that decided each answer and the coverage (`policy_tests.go`);
  `GET /service/{id}/policy/lint` (`lint.go`, messages in the request language); `GET /service/{id}/policy/export` and
  `POST /service/{id}/policy/import?dry_run&prune` (`policy_document.go`). The document (`licetum.policy/v1`) names
  everything: a rule is matched by its name, or by its id when it is named `rule-<id>`. The import plans operations with
  the ids of new entities chosen up front, so one batch can name them.
- A create that is given an id another entity has is refused with 409 (`idTaken`, `db.IsPrimaryKeyViolation`), so a
  batch or an import rolls back as a whole. The policy document lists are optional on input.
- Access groups have a page (`pages/access-groups`); they are also managed through the batch and the policy document.

### Check, AuthZEN, metrics

- Every PDP path takes the service as `{id}` — the service id or its slug (`App.ServiceIDByRef`).
- Request context (`context`, a JSON object): in the check body, as the `context` query parameter of `GET
  /permission`, as the AuthZEN `context` (every key but `explain`), and in the admin simulations. The service that calls
  Licetum sets it, as it sets the token: never copy it from end-user input. Names are identifiers
  (`model.ValidContextName`); at most 64 attributes per request, a string of at most 1024 characters, a list of at most 64 items; an object, or a
  list within a list, is refused.
- `POST /service/{id}/check` (`service/check.go`, `model/check.go`): up to 100 questions
  `{type, resource_id?, action}`; without `resource_id` the question is about the type as a whole.
  `explain: true` adds `{decision: allowed|denied|no_access|resource_not_found|type_not_found,
  granted_by, denials[{rule, level, lifted_by, unresolved_parent}]}`. It decides only for the asked
  resources (`coversOf`, the questions of one request are read together: `decideChecks`), with the same engine as `/permission`.
- **AuthZEN** (`core/server/authzen_run.go` the run layer, `authzen_caller.go`, `authzen_routes.go`; `core/model/authzen.go`; tag `AuthZEN`): the OpenID AuthZEN Authorization API 1.0 scoped to a service
  (the service, by id or slug, is in the path, so the "PDP" is `<public url>/service/<ref>` and the default paths of the spec are appended to it).
  `POST …/access/v1/evaluation` and `…/evaluations` (defaults at the top level, `options.evaluations_semantic` = `execute_all` | `deny_on_first_deny`
  | `permit_on_first_permit`: the batch is decided as a whole and cut at the stopping evaluation; without `evaluations` it is one evaluation and the
  answer is `{decision}`; an evaluation that cannot be decided in a batch is `decision: false` with `context.error {status, message}`, the single
  endpoint answers the HTTP error); `POST …/access/v1/search/{subject,resource,action}` (`page.limit` up to 1000, `page.token` is an opaque cursor with
  a digest of the request, a changed request is 400 `authzen_page_invalid`); `GET /.well-known/authzen-configuration/service/{ref}` (the discovery
  document, from `PDP__PUBLIC_URL`; 404 `authzen_metadata_disabled` without it). A request that is not valid is a 400, not Huma's 422 (the
  `NewErrorWithContext` hook maps it for the `AuthZEN` tag; `badRequest` does it for errors of the handlers). `subject.id` is the token's subject
  and the claims come from the token; any other subject needs a client the service trusts (`trusted_clients`, as for the calls on behalf of a
  person; else 403 `authzen_subject_mismatch`), which gives the person's claims in `subject.properties` (`PrepareDelegated`); such calls are audited
  in `delegated_call` (evaluations and subject search as `check`, resource and action searches as `permission`). `context.explain` puts the explanation
  into `context.reason_admin`; the other context keys are the request attributes. Resource search (`engine.SearchResources`) reads the candidates of the type from `resource_cache` by a keyset scan in bytewise order
  (`ORDER BY external_id COLLATE "C"`, the index `ix_resource_cache_type_external_c` of migration `00007`) and decides them in batches of 500 under
  one `prepare` of the rules; a page is cached like a check answer (`authz:search:t1:…`, `licetum_search_cache_total`), so a repeated page costs no
  query. The page token holds the last key covered (the empty string is a key too: `ResourceSearch.After` is a pointer) and a digest of the
  request; at most `PDP__SEARCH_SCAN_BUDGET` (10 000) candidates are read per request, so a page can be shorter than its limit, even empty, and
  still carry a `next_token`; a caller to whom no rule allows anything gets a finished empty page without a scan. Known limit: the scan reads the
  keys of the type, not only the resources the rules reach (ADR 7.6 reads the descendants of the anchors; that arrives with the read-model port
  in P5), so a caller with few grants on a very large type pays up to the budget per page; the result has no `total` (`properties.name` is the name; a `not_sync` type has none); action search lists the levels on a resource (or on the type as a whole:
  the candidates are the levels the person has on some resource of the type, each decided by `CheckAccess`); subject search (a PEP client with
  `search_subject`, or a trusted client) decides the people of the **directory of the service** of the asked `subject.type` (`DirectoryAfter`,
  keyset by type and id, bytewise) with their stored properties as claims: batches of 100 from the cursor until the page is full or 1000 people
  were gone through, the page token is the last person covered (`type:id`, no `page.total`), so no page skips or repeats a person; the decisions are
  the cached `CheckAccess` ones. A person who is not in the directory is never found, an empty directory gives an empty result (DIR01); the result
  carries the stored display name as `properties.name`.
- **PEP clients** (`engine/pep.go`, `engine/pep_auth.go`, `server/authzen_auth.go`, `server/pep_limits.go`, `pepauth/`, migration `00006`; ADR 0001, 7.10): the
  callers of the AuthZEN routes. The directory (`pep_issuer`, `pep_client`, `pep_credential`) is read as a whole by `App.PEPDirectory` and follows the
  `pep` scope of `revision_state`; a write to it bumps that scope only (no decision reads the directory, so no answer key moves) and calls
  `App.AfterPEPWrite`. A client belongs to one service and is `api_key` (a key `lct_<16 hex digits>_<43 characters>`, stored as the sha256 of its
  secret, at most two active at once) or `jwt` (the `iss` and the `azp`/`client_id` of the token of an issuer registered in `pep_issuer`: the algorithm
  allowlist, `aud` always required, the issuer's `audiences` or else `PDP__AUDIENCE` (default `PDP__PUBLIC_URL`), `at+jwt` when the issuer asks, 60 s of
  leeway). `operations` lists what it may call: `evaluate` (the default), `search_resource`, `search_action`, `search_subject` and `explain`. The routes ask
  `AuthenticatePEP` first (`authzenAuth.caller`): an API key or a token of a registered issuer that does not verify is a 401 (`Bearer error="invalid_token"`,
  a key's answer takes the same time whether its id exists or not); a valid credential that gives nothing on the service (no such service, a client of
  another service, a disabled one, an operation it lacks, `explain` without the `explain` operation) is **one** 403 `authzen_forbidden`, the same body for
  every reason, so a caller learns nothing about which services exist. Any other bearer token is the token of a person and goes the old way (the
  service's own OAuth) until stage 2c. The person of a question is the body's `subject` (`PrepareDelegated`, the claims in `subject.properties`), never the
  credential, and a PEP client may ask the subject search without being a `trusted_clients` entry. `PEP__RATE_LIMIT`/`PEP__RATE_BURST` is a token bucket per
  client and per process (429 `authzen_rate_limited` + `Retry-After`; off by default; a global limit belongs to the ingress). The directory holds enabled
  clients only, so the key of a disabled client is a 401, not a 403. Tests: `core/server/authzen_pep_test.go`, `admin-api/tests/integration/api/pep_test.go`.
  **Management** (`admin-api/api/admin_pep.go`, `admin-api/service/pep.go`, `core/model/pep.go`; minimal routes at their final paths, re-gated by the roles of the
  service in P4): `GET|POST /service/{id}/pep-clients`, `GET|PATCH|DELETE …/pep-clients/{client_id}`, `GET|POST …/pep-clients/{client_id}/keys`,
  `DELETE …/keys/{key_id}`, and `GET|PUT|DELETE /platform/pep-issuers` (the issuer to delete is the `issuer` query parameter: it is a URL). They are governed by the
  platform policy `service`: read on the service to read its clients and keys, write to change them, the whole policy for the issuers. The path takes the service id
  (the slug arrives with the routes of P4); a service that does not exist is refused like one the caller may not touch (403). A write goes through `db.CreatePEPClient`,
  `ModifyPEPClient` (the row read under its lock, so a PATCH never loses a concurrent change), `IssueAPIKey`, `RevokeAPIKey`, … each in its own transaction with the bump of
  the `pep` scope, then `App.AfterPEPWrite`. The change log does not record them (nor does a rollback restore them). The key is in the answer that issues it
  (`Cache-Control: no-store`) and nowhere else; a client has two active keys at most (409), and a revoked key, a disabled or a deleted client is refused by every replica
  within the revision poll interval (PEP02, `cluster/pep_test.go`).
- **Calls on behalf of a person** (`service/delegation.go`, `api/check.go`): `POST /service/{id}/on_behalf/check` and `…/permission` answer
  as the check and the permission do, for the `subject` and the `claims` named in the body instead of the holder of the token. The caller
  sends its own token (same IdP and keys as a user's, typically client-credentials); `AuthorizeDelegate` takes its client from `azp`, then
  `client_id`, and the service must list it in `trusted_clients` (403 `delegation_not_trusted` / `delegation_no_client`). The caller
  vouches for the claims, so a trusted client has to send the ones the rules read (`GET /service/{id}/policy/claims` lists them: the
  conditions and guards of the rules that apply to the service's types, claim-based targets, and the group memberships);
  `PrepareDelegated` sets `preferred_username` to the subject when it is absent and refuses a subject that starts with `group:` (it would
  be taken for the group) or claims over 100 entries / 16 KiB. Every answered call is written to `delegated_call` before the answer is
  returned (a call that cannot be recorded is not answered; `GET /service/{id}/delegated_calls` pages it;
  `DELEGATION__AUDIT_RETENTION_DAYS`, default 365). `DELEGATION__AUDIT_MODE=async` (default `strict`) buffers the record (`DELEGATION__AUDIT_BUFFER`, default 100 000) and writes it with one multi-row INSERT every 500 ms or at 500 records (`engine/audit.go`); a full buffer drops and counts (`licetum_audit_dropped_total`, `licetum_audit_buffer_records`, `licetum_audit_lag_seconds`), `Close` flushes. `NoteSubject` fills a buffer flushed every second in batches of 500; the upsert only changes a row older than 10 minutes, so replicas change it once per interval. Retention (`engine/retention.go`) runs in the `worker` only, hourly, in 10 000-row chunks, under a Redis lock (once per interval across workers). A log-settings change bumps `revision_state` scope `log_settings` in its transaction; every process applies it from the revision poll (`WatchRevision`, no ticker of its own). `WaitChanges` shares one `max(id)` query per second per process between all waiters. The access log names the client as `user` and the person as `on_behalf_of`. Known
  limit: a user token of a trusted client is not told apart from its own client-credentials token, so trust a backend client.
- `GET /metrics` on the PDP's ops port and on the admin API (Prometheus, own registry per `App`, `service/metrics.go`): permission
  compute time and cache hit/miss/bypass, the in-process caches (`licetum_l1_cache_total`), JWKS fetches (`licetum_jwks_fetch_total`),
  dropped background work (`licetum_background_dropped_total`), requests that had no policy snapshot (`licetum_snapshot_fallback_total`),
  Redis commands skipped by the fast-fail (`licetum_redis_skipped_total`), check decisions, source fetch time and sync results,
  breaker-open counters, HTTP requests/duration by operation. `App.metrics` may be nil (an `App`
  built only to describe the API) — helpers are nil-safe.

Token verification (`internal/service/oauth.go::VerifyToken`) fetches the service's JWKS, verifies
the JWT, checks issuer, and caches the verified claims in Redis keyed by token hash until near
expiry — also behind a circuit breaker + singleflight. Only JWKS transport failures count toward
the breaker; an invalid token is a 401. `POST /service/{id}/oauth/refresh` (`RefreshTokens`, public
like login/callback) exchanges a refresh token at the service IdP: `invalid_grant` is a 401 (sign in
again) and does not count toward the breaker; any other rejection is a 424.

Claim values are compared as Python `str()` would render them (`service.PyStr`: `True`, `5`,
`['a', 'b']`) so predicates written for the old implementation keep matching.

Access log (`logAndRecover` in `internal/api/server.go`): one `request` line per HTTP request with `request_id`
(the caller's `X-Request-ID` when it is sane, else a generated UUID; returned in the response), `operation`, `user`,
status and duration. `user` is set only after the token is verified (`setRequestUser`, called by `platformAuth` and
the PDP's `authenticateCaller`), so a forged token never shows up. Request bodies are never logged: they carry
client secrets and tokens.

## Web interface (`web/`)

The interface: Next.js 16 (App Router) + Ant Design 6 + TanStack Query, in `web/`. `web/README.md` is the full
description; in short:

- Sign-in runs on the server (`src/app/auth/*`), and the web interface is the OpenID Connect client (`OIDC_*`: code flow with PKCE, a
  state and a nonce, `src/lib/oidc.ts`): the tokens live in an encrypted httpOnly session cookie, never in the page's JavaScript, and
  what a sign-in under way remembers lives in a second short-lived encrypted cookie. The browser reaches the admin API only through
  `src/app/api/admin/[...path]/route.ts`, which adds the token and renews it at the identity provider's token endpoint. The admin API
  needs no CORS setting for it; the redirect URI to register at the provider is `${APP_BASE_URL}/auth/callback`.
- The API client is generated by orval from `backend/schema/admin.yaml` into `web/src/api` (not committed): `pnpm orval` /
  `task web:orval` / `task generate`. After an API change run `task backend:schema`, then regenerate.
- `task web:lint` (format, orval, tsc, locale check, knip), `task web:test` (`pnpm test`, Jest in `web/tests`: the domain helpers,
  the grant form model, the schemas, the session cookie, the OIDC sign-in, and components rendered with mocked data), `pnpm build`. There is no
  e2e suite against a running stack: the interface is tested on mocks by decision of the owner.
- Data that crosses a border is parsed, not cast: `src/lib/schemas.ts` (zod) holds the shape of the token answers, the discovery
  document, what the callback page posts and what a person uploads; `src/lib/env.ts` checks the settings the same way. `tsc` runs with
  `noUnusedLocals`/`noUnusedParameters` and `knip` (part of `task web:lint`) rejects unused files, exports and dependencies.
  `agentation` (devDependency, `components/dev-tools.tsx`) is a feedback toolbar for coding agents in `next dev` only.
- Languages: ru, en and zh (`src/i18n/locales`, en is the source; zh has only the `_other` plural form, as the language needs). A key is text or a group, never both; the check script fails on
  mismatches and on keys the code uses but no dictionary defines.
- The visual system is in `src/theme/tokens.ts`.

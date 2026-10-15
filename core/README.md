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
| `DB__` | `DB__URI` (required), pool settings (`DB__POOL_SIZE`, `DB__POOL_TIMEOUT`, …), `DB__SYNC_POOL_SIZE` (the worker's synchronization pool, default 4), `DB__ECHO` |
| `REDIS__` | `REDIS__URI`: the permission cache, the cached source lists, the circuit breakers. `REDIS__HOT_TIMEOUT` (default `50ms`): the check API's read and write timeout for Redis, without retries; after three failures in a row Redis is skipped for five seconds |
| `SNAPSHOT__` | the policy kept in memory: `SNAPSHOT__MAX_BYTES` (default 256 MiB; a policy that does not fit is read with queries, a negative value turns the snapshot off), `SNAPSHOT__MAX_AGE` (default `1m`: the snapshot is read again at this age even when the revision did not move) |
| `REVISION__` | the revision of the policy lives in PostgreSQL (`revision_state`): `REVISION__POLL_INTERVAL` (default `250ms`: how soon a write made through another replica is seen), `REVISION__WAIT_MAX` (default `500ms`: how long a request with `at_least_as_fresh` waits for its revision before the answer is computed from the data) |
| `COMPUTE__` | `COMPUTE__CONCURRENCY`: answers computed at once (default half of `DB__POOL_SIZE`); the rest of a wave of cache misses queues |
| `AUTH__` | `AUTH__STATE_SECRET`, `AUTH__STATE_TTL`; the token and key caches: `AUTH__TOKEN_L1_SIZE` (verified tokens kept in memory, default 50 000), `AUTH__JWKS_TTL` (default `10m`), `AUTH__JWKS_MIN_REFRESH` (default `30s`: an unknown key id refetches the key set at most this often), `AUTH__INVALID_TOKEN_TTL` (default `10s`) |
| `CACHE__` | `CACHE__ANSWER_L1_SIZE`: answers kept in memory, default 50 000 |
| `BACKGROUND__` | `BACKGROUND__WORKERS` (default 16) and `BACKGROUND__QUEUE` (default 1024): the cache writes and refreshes done after a request is answered; a full queue drops the work |
| `PLATFORM__` | the self-service: its OAuth2 client (`PLATFORM__CLIENT_ID`, `PLATFORM__CLIENT_SECRET`, the authorization, token and JWKS URLs, the issuer) and the platform admin group (`PLATFORM__ADMIN_GROUP_NAME`, `PLATFORM__ADMIN_CLAIM`, `PLATFORM__ADMIN_CLAIM_VALUES`) |
| `SYNC__` | `SYNC__FETCH_TTL` (how long a source's list is kept), and the synchronization pipeline, see below. `SYNC__LOCK_TTL` and `SYNC__LOCK_WAIT` are deprecated and ignored |
| `SUBJECTS__` | `SUBJECTS__ENABLED` (the registry of known subjects, off by default), `SUBJECTS__RETENTION_DAYS` |
| `DELEGATION__` | `DELEGATION__AUDIT_RETENTION_DAYS` (the audit of calls made on behalf of a person) |
| `HTTP__ADDR`, `HTTP__PUBLIC_URL` | the listen address; the address clients reach the check API at (`https://licetum.example.com`), which the AuthZEN discovery document is built from |
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
  revision. When the revisions cannot be read the last view is served for up to `SNAPSHOT__MAX_AGE`, and answers are kept in
  memory only. The revision and the data change in one transaction, so a replica never sees a revision before its data.
- **Answers and tokens** are kept in memory first (`CACHE__ANSWER_L1_SIZE`, ten seconds at most; `AUTH__TOKEN_L1_SIZE`, a
  minute at most) and in Redis second: a request that finds its answer only in Redis makes one round trip.
- **Key sets** of the identity providers are kept per URL (`AUTH__JWKS_TTL`), fetched once for all the requests that wait
  for them, and fetched again for an unknown key id at most once per `AUTH__JWKS_MIN_REFRESH`; a token that failed
  verification is remembered for `AUTH__INVALID_TOKEN_TTL`.

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

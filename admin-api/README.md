# Licetum admin API (Go)

The admin API of Licetum: the HTTP interface the web interface and the scripts use to manage services, resource types,
synchronizers, groups, roles, access rules and the policy tools. The module is `github.com/licetum/licetum/admin-api`,
under the GNU Affero General Public License v3.0 only (see [`LICENSE`](LICENSE)). It depends on the engine in
[`../core`](../core/README.md) (MIT) and is built on it: `service.App` embeds `engine.App`.

## Commands

`licetum-admin` has three subcommands:

| Command | What it does |
|---|---|
| `licetum-admin admin` | the admin API (default `:8000`) |
| `licetum-admin restore-admin [-user name]...` | puts the access of the platform administrators back (see [Locked out](#locked-out)) |
| `licetum-admin openapi admin [-yaml] [-lang en\|ru\|zh]` | prints the OpenAPI spec without a server or a database |

The database schema is migrated by the core binary: run `licetum migrate` from [`../core`](../core/README.md) first.

## Quick start

```bash
cd core && go run ./cmd/licetum migrate        # the schema and the platform self-service
cd ../admin-api
export DB__URI=postgresql://authz:authz@localhost:5432/authz_db
export ADMIN_AUTH__ISSUER=http://localhost:5556/dex ADMIN_AUTH__AUDIENCE=licetum-local ADMIN_AUTH__JWKS_URL=http://localhost:5556/dex/keys
go run ./cmd/licetum-admin admin
```

The variables are the same as for the core (see [`../core/README.md`](../core/README.md#configuration)). Administrators
sign in at the OpenID Connect provider of `ADMIN_AUTH__*`; the browser reaches the admin API only through the web
interface's server, so the API has no CORS setting.

The API documents itself like FastAPI: Swagger UI at `/docs` (`/docs?lang=ru` or `zh` for the translations) and the
OpenAPI 3.1 spec at `/openapi.json` and `/openapi.yaml`. `schema/admin.yaml` is the exported spec; the web interface
generates its client from it. `task go:schema` regenerates the file and `task go:schema:check` (part of `task check`)
fails when it is out of date.

## Access control: the platform policies

The admin API is protected by the same permission mechanism as any client service. Administrators sign in at the OpenID
Connect provider of `ADMIN_AUTH__*` (`ADMIN_AUTH__ISSUER`, `ADMIN_AUTH__AUDIENCE`, `ADMIN_AUTH__JWKS_URL`, `ADMIN_AUTH__SUBJECT_CLAIM`):
the admin API verifies the bearer token against its keys (`iss` exact, `aud` must contain the audience, `exp` and `nbf`, an
asymmetric algorithm) and a missing or bad token is a 401 with `WWW-Authenticate`; what the administrator may do then comes
from the policies below. The web interface is the OIDC client of the same provider. `licetum migrate` registers Licetum
as a service within itself (`PLATFORM__*`, once `ADMIN_AUTH__*` is set) and seeds its access policies: resource types of that service
(`core/model/platform.go`):

| Policy | Covers |
|---|---|
| `service` | services |
| `type_resource` | resource types, plus their synchronizers and resources |
| `group` | groups of people |
| `group_member` | group membership rules |
| `role` | roles, plus their permission items (`/role/{id}/permission` is checked against the role) |
| `assignment_rule` | access rules, including those that grant a role |

Each policy has a synchronizer (kind `sql`) that reads its own table of this instance. Without it a type has no resources
and therefore no whole-type access. The seed also creates the platform admin group (`PLATFORM__ADMIN_GROUP_NAME`, default
`Platform administrator`; the former `PLATFORM__ADMIN_ROLE_NAME` is still read when the new name is not set). Its members
are everyone whose `PLATFORM__ADMIN_CLAIM` claim (default `ADMIN_AUTH__SUBJECT_CLAIM`, itself `preferred_username`) takes one of
`PLATFORM__ADMIN_CLAIM_VALUES` (a JSON list or a comma-separated string), and it has rules granting read and write on every
policy as a whole. Without values the group is empty and nobody can manage the platform.

The seed is conservative: existing rows are never touched, and the admin group and its rules are created only in the run
that creates the policy types. Once an administrator edits or revokes them, a later deploy does not bring them back.

Every admin request carries `Authorization: Bearer <token>`, issued by the self-service identity provider. The checks
(`api/auth.go`) run before the body is parsed:

- listing needs `read` on at least one entity, and the response is filtered to the readable ones;
- changing an entity needs `write` on it (the id comes from the path);
- a rule's target, and a rule or a role item on a platform policy, needs more: the policy as a whole;
- **creating needs `write` on the whole type**: a rule with neither `resource_id` nor `resource_from_claim`. A point
  `write` on one entity does not allow creating new ones.

`GET /platform/me` returns the caller's access per type (`whole_type` and the point `entries`) for the interface;
`GET /platform` (public) returns the self-service id for signing in. Errors: 401 for no or an invalid token, 403 with
`access_denied`, 503 when the self-service is not seeded (the admin API is then closed to everyone).

A write that would leave nobody able to manage the access rules is refused (409 `last_admin_access`).

### Locked out

A denial rule, or an edit made directly in the database, can still lock everyone out. `restore-admin` puts the platform
admin group, its memberships and the admin rules back, and switches off the denials that cover a policy:

```bash
go run ./cmd/licetum-admin restore-admin -user alice    # alice becomes an administrator too (matched by ADMIN_AUTH__SUBJECT_CLAIM); PLATFORM__ADMIN_CLAIM_VALUES are restored
```

It needs the database variables. The seed never does this on purpose.

## The subject directory

The people of a service, as its application sends them: `PUT /service/{id}/subjects` takes up to 1000 people per call (`type`,
`id`, `name`, `properties`), an unchanged entry is left untouched and a write takes no revision. The directory lets the
interface tell which groups a person is in and whom a rule reaches (the people of the directories of the services of the rule's
types), and answers the AuthZEN subject search. `GET /service/{id}/subjects` lists it (`type`, `search`, `limit`, `offset`),
`GET` and `DELETE /service/{id}/subjects/{type}/{id}` read and remove one person, and `GET`/`PUT /service/{id}/subjects/settings`
read and set `retention_days` (none by default: an entry is kept until it is deleted; the worker removes the entries not sent
again within it). Until the roles of the service arrive the routes need read or write on the whole `group_member` policy.

## Policy tools

- `POST /batch`: create, update and delete rules, groups, memberships, roles, role items and access groups in one
  transaction; a failing operation is named and nothing is applied.
- The change log (`GET /changes`) keeps the entity before and after each change. `POST /changes/{id}/revert` and
  `POST /changes/rollback` apply the undo through the batch (`dry_run` plans only).
- `POST /service/{id}/policy/test` runs scenarios through the simulation, `GET /service/{id}/policy/lint` checks the
  rules, and `GET`/`POST /service/{id}/policy/export` and `import` move the whole policy as one document
  (`licetum.policy/v1`).
- `POST /assignment_rule/preview` says what a rule would do before it is saved.

## Languages

English is the default; Russian and Chinese are supported as well. The request language is `?lang=`, then
`Accept-Language`. Errors carry `detail` in that language and a stable `extra.code` to branch on. Translations of the
messages live in `core/i18n/messages.go`; the API descriptions in `api/locales/{ru,zh}.json`. The tests fail on a missing
or unused translation.

## Tests

```bash
go test -race ./...                                       # unit tests
go test -race -tags integration ./tests/integration/...   # integration tests with disposable containers
```

The integration tests create an isolated schema per scenario, seed the self-service against a mock identity provider and
call the admin API as a platform administrator. The variables described in the [core guide](../core/README.md#tests) apply
here too.

## Load test

`loadtest/` measures the PDP (`licetum pdp`) at a policy size. The harness seeds a service through this module's
service layer, starts the PDP binary as its own process, and ramps the traffic up with [k6](https://grafana.com/docs/k6/)
until a step misses its objective. The admin API takes no load: it writes the policy once.

```bash
task loadtest -- -profile smoke                  # about a minute: the volume of every change
task loadtest -- -profile medium -slo-p99 300ms  # 10k rules, 100k resources
```

k6 must be on the `PATH` (or pass `-k6-bin`); CI installs the version the workflows pin.

What a step measures:

- **k6** sends the requests of the mix at the rate of the step. They are the AuthZEN API: `search_resource` (a page of
  the documents a person may read), and `evaluation`, `evaluations10` and `evaluations100` (one, ten and a hundred questions
  about a person). The mix is set with `-mix name=weight,...`. Every request is asked as one PEP client of the service, the way
  a service's own backend asks: the seeding makes the client (kind `api_key`, every operation) and issues its one API key, which
  is the bearer of every request; the person a question is about is the `subject` of the body, and the claims the rules read
  (the `department` that picks the groups) are its `properties`. No token of a person is involved, and no identity provider. The
  PDPs read the client and its key from the database; the rate limit of a client (`PEP__RATE_LIMIT`) is switched off for them.
  The arrivals are an open model: a slow server does not lower the rate, it shows as late answers and as `dropped` arrivals,
  which are the sign that a step is saturated.
- **The server** is read at the edges of each step: its CPU (from `/proc`), its cache hits and the time it spent per
  request (from `/metrics`), and the database: its blocks read, and when the server preloads `pg_stat_statements`, the
  time of its statements and the most expensive ones during the ramp.

The results go to `reports/loadtest/`: `summary.md` (the table of the steps and where the time went), `result.json`,
the k6 summaries and logs, and `check.log`. The API key of the run is written, with the script, to a temporary directory and
removed when the run ends.

Docker runs PostgreSQL; a server named by `LICETUM_TEST_POSTGRES_URI` is used
instead. The containers preload `pg_stat_statements`; an external server needs it in `shared_preload_libraries` for the
statement figures. The PDP's pool is `-db-pool` connections (30 by default, with no overflow, so the pool is
the setting under test); a pool smaller than the traffic makes the requests wait for a connection, which shows as a tail of slow answers.

Two switches for reading a run: `LICETUM_LOADTEST_K6_RAW=1` keeps the point of every request (`*.points.json.gz`), and
`LICETUM_LOADTEST_KEEP_DB=1` keeps the run's database on an external server, to read its query plans.

The nightly run is `.github/workflows/loadtest.yml`; the smoke volume runs on pull requests that touch the Go code.

## Layout

```
cmd/licetum-admin/     entry point: admin / restore-admin / openapi subcommands
cmd/licetum-loadtest/  load test of the PDP (see Load test)
loadtest/           the seeding, the traffic and the report of the load test
api/                HTTP operations on Huma v2: documented routes, the policy checks (auth.go, admin_targets.go), batches
service/            business logic of the admin: services, types, synchronizers, groups, roles, rules, changes, policy tools;
                    service.App embeds the engine of ../core
schema/admin.yaml   the exported OpenAPI spec (input of the web interface's client)
tests/integration/  integration tests of the API
```

Dependencies are those of the core, plus what the HTTP layer needs (`huma/v2` and its adapter for `net/http`).

## License

GNU AGPL-3.0-only — see [`LICENSE`](LICENSE).

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
| `licetum-admin openapi admin [-yaml] [-lang en\|ru\|zh]` | prints the OpenAPI spec without a server, database or Redis |

The database schema is migrated by the core binary: run `licetum migrate` from [`../core`](../core/README.md) first.

## Quick start

```bash
cd core && go run ./cmd/licetum migrate        # the schema and the platform self-service
cd ../admin-api
export DB__URI=postgresql://authz:authz@localhost:5432/authz_db
export REDIS__URI=redis://localhost:6379/0
export CORS__ALLOW_ORIGINS=http://localhost:3000   # the origin of the web interface
go run ./cmd/licetum-admin admin
```

The variables are the same as for the core (see [`../core/README.md`](../core/README.md#configuration)). The web
interface must be in `CORS__ALLOW_ORIGINS`: its sign-in redirect is checked against that list.

The API documents itself like FastAPI: Swagger UI at `/docs` (`/docs?lang=ru` or `zh` for the translations) and the
OpenAPI 3.1 spec at `/openapi.json` and `/openapi.yaml`. `schema/admin.yaml` is the exported spec; the web interface
generates its client from it. `task go:schema` regenerates the file and `task go:schema:check` (part of `task check`)
fails when it is out of date.

## Access control: the platform policies

The admin API is protected by the same permission mechanism as any client service. `licetum migrate` registers Licetum
as a service within itself (`PLATFORM__*`) and seeds its access policies: resource types of that service
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
are everyone whose `PLATFORM__ADMIN_CLAIM` claim (default `preferred_username`) takes one of
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
go run ./cmd/licetum-admin restore-admin -user alice    # alice becomes an administrator too; PLATFORM__ADMIN_CLAIM_VALUES are restored
```

It needs the database variables. The seed never does this on purpose.

## Known subjects

The subjects of verified tokens can be remembered (the name, and only the claims the policies read), so the interface can
tell which groups a person is in and whom a rule reaches. It is off by default: `SUBJECTS__ENABLED=true` turns it on (the
development stack does). `SUBJECTS__RETENTION_DAYS` (default `90`; `0` keeps them forever) removes the subjects that are not
seen again. `GET /subject` lists them and `DELETE /subject/{subject}` forgets one.

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

## Layout

```
cmd/licetum-admin/  entry point: admin / restore-admin / openapi subcommands
api/                HTTP operations on Huma v2: documented routes, the policy checks (auth.go, admin_targets.go), batches
service/            business logic of the admin: services, types, synchronizers, groups, roles, rules, changes, policy tools;
                    service.App embeds the engine of ../core
schema/admin.yaml   the exported OpenAPI spec (input of the web interface's client)
tests/integration/  integration tests of the API
```

Dependencies are those of the core, plus what the HTTP layer needs (`huma/v2` and its adapter for `net/http`).

## License

GNU AGPL-3.0-only — see [`LICENSE`](LICENSE).

# Contributing to Licetum

Thanks for contributing to Licetum. This document covers the local development workflow, quality
checks, and conventions that apply to changes in this repository.

## Development setup

### Requirements

- Go 1.26 or newer
- Node.js 22 or newer
- pnpm via Corepack
- [Task](https://taskfile.dev/)
- Docker and Docker Compose
- `golangci-lint` v2.14 or newer for the Go checks

Install dependencies from the repository root:

```bash
corepack enable
task install
```

Start the complete development stack:

```bash
task dev
```

This starts PostgreSQL, Dex, the Go services (`core/`, `admin-api/`) and the Next.js interface (`web/`). Open
<http://localhost:3000> and sign in with the development account:

- Email: `admin@example.com`
- Password: `password`

The Dex client, credentials, and signing configuration are for local development only. Configure a
real identity provider (`ADMIN_AUTH__*` of the admin API and `OIDC_*` of the web interface) before using Licetum outside the
local Compose stack.

## Repository layout

- `core/` — the MIT module: the engine, the PDP (the AuthZEN API), migrations, synchronizers and the PDP schema
- `admin-api/` — the AGPL module: the admin API and the commands that go with it (depends on `core/`)
- `web/` — the Next.js + Ant Design interface (see `web/README.md`)
- `infra/` — Compose fragments for PostgreSQL and Dex
- `core/schema/` and `admin-api/schema/` — committed OpenAPI schemas used to generate the API client of `web/`
- `.github/workflows/ci.yml` — checks and tests run by GitHub Actions

## Common commands

Run commands from the repository root unless noted otherwise:

```bash
task check       # all CI checks without modifying files
task lint        # format and auto-fix where supported
task test        # Go unit and integration tests (both modules); integration uses Testcontainers
task generate    # regenerate OpenAPI schemas and the web API client
task dev         # start the Docker Compose development stack
```

Useful package-specific commands:

```bash
cd core                      # the MIT module: the engine, the check API
go test -race ./...

cd ../admin-api             # the AGPL module: the admin API
go test -race ./...

cd web
pnpm build
pnpm typecheck
pnpm format
pnpm test
```

`web/src/api` is generated and ignored by Git. Do not edit it manually. Run `task generate`
after changing an API contract; the generated schemas in `core/schema/` and `admin-api/schema/` are committed.

## Testing

`task check` is the local equivalent of the CI quality job. It checks Go formatting and linting,
that the code behind the build tags (`integration`, `chaos`, `multiregion`) compiles, module tidiness,
OpenAPI schema drift, web formatting, TypeScript, and translations.

Backend integration tests use disposable PostgreSQL containers:

```bash
task go:test:unit
task go:test:integration
task go:coverage
```

Docker is required for integration and coverage runs. The suite creates disposable containers and
its own database schema, so no shared PostgreSQL instance is needed (a server named by `LICETUM_TEST_POSTGRES_URI` is used instead of a container when Docker is not available). The integration and
coverage tasks resolve the active Docker context automatically; direct `go test` runs should set
`DOCKER_HOST` when Docker Desktop or another non-default context is active.

## Making changes

### Backend and API changes

- Keep the dependency direction: `admin-api/api` → `admin-api/service` → `core/engine`, and in the core `server` → `engine` → `db`/`synchronizer` → `model`/`config`/`i18n`/`apperr` (depguard enforces it). No package imports a Redis client: there is no Redis, and `task go:deps:check` (part of `task check`) fails on `github.com/redis/go-redis`.
- Keep HTTP concerns in `admin-api/api/` and `core/server/`; business logic belongs in `admin-api/service/` (admin) or `core/engine/` (decisions, synchronization).
- Register new Huma operations with an `OperationID`, summary, description, tags, documented errors,
  and model field documentation.
- Write API descriptions and user-facing API messages in English first.
- Update `internal/api/locales/ru.json` and `internal/api/locales/zh.json` when API descriptions
  change. Add error messages to all supported locales.
- Add a new numbered Goose migration for schema changes. Do not rewrite an already-applied migration.
- Run `task generate` after changing an API schema and verify that `task check` passes.

### Schema-v2 migrations

Schema v2 is a fresh-database boundary. The released v0.1.x schema and public API have no upgrade path. `migrate` refuses a legacy
`goose_db_version` on its search path before any write; `goose_db_version_v2` owns v2 history. For disposable local development data:

```sh
docker compose down -v
docker compose up
```

The temporary P3 chain has been folded into `core/db/migrations/00001_init.sql` with [recorded schema equivalence](core/tests/integration/db/schema_v2_equivalence.md).
From this point, add new numbered Goose files for schema changes and never rewrite an applied initializer or migration. Keep compatible-release changes in expand/contract steps;
this rule does not create a v0.1.x upgrade path. The previous-release compatibility job is removed. Remove its old required status check from branch protection if configured.
See [the approved P3 decisions](docs/adr/0001-regional-pdp.md#1171-p3-decisions).

`licetum migrate` takes an advisory lock, so several runs started together apply each migration once. It needs a
connection that lasts for the whole run; behind PgBouncer in transaction mode, point `DB__MIGRATE_URI` at PostgreSQL
itself. Readiness requires the schema version expected by the running binary; it does not establish v0.1.x compatibility.

### Interface changes

- Put user-facing text in the locale dictionaries; English is the source locale.
- `web/`: add matching keys and variables to `src/i18n/locales/en.json`, `ru.json` and `zh.json` (`pnpm check:i18n` checks them).
- `web/`: call the API through the generated client and `/api/admin` (the server adds and renews the token).
- Run `pnpm build` before submitting UI changes. Cover a changed screen with a Jest test that mocks the API responses.

### Tests and documentation

Add or update tests with behavior changes, especially for authorization, graph traversal, token
handling, synchronization, and API contracts. Update the relevant README or documentation when a
developer-facing workflow changes.

## Pull requests

Before opening a pull request:

1. Rebase or update your branch from the current target branch.
2. Run `task check` and `task test`.
3. Run `pnpm build` from `web/` for interface changes.
4. Review the diff for generated files, secrets, debug output, and unrelated formatting changes.
5. Describe the behavior change, tests run, and any migration or configuration impact in the PR.

Keep pull requests focused. If a change is intentionally incomplete, explain the follow-up work and
the current limitation in the PR description.

## Releasing and publishing Docker images

The release workflow uses `git-cliff` to read Conventional Commits since the previous `v*` tag,
calculate the next SemVer version, generate GitHub Release notes, and publish Docker images to GHCR.

To create a release, open **Actions → Release and publish Docker images → Run workflow** and select
the version increment:

- `auto` — `fix:` increments patch, `feat:` increments minor, and breaking changes increment major;
- `patch`, `minor`, or `major` — force that increment.

When the repository has no SemVer tags, the version calculation starts from `0.0.0`, so the first
feature release becomes `0.1.0`.

The workflow also supports creating a release by pushing a SemVer Git tag directly:

```bash
git tag v1.2.3
git push origin v1.2.3
```

The generated changelog is stored in the GitHub Release notes. The published images are:

```text
ghcr.io/licetum/licetum/core:1.2.3
ghcr.io/licetum/licetum/admin-api:1.2.3
ghcr.io/licetum/licetum/web:1.2.3
```

Stable releases also update the `latest` tag. Pre-releases such as `v1.2.3-rc.1` publish only
SemVer tags and do not update `latest`.

Use clear conventional commit subjects for P3 and release-noted work (`feat`, `fix`, `refactor`, `docs`, `test`), with `!` for breaking changes so git-cliff records them.
P3 task subjects end with their issue number and every commit is signed off. Keep unrelated changes in separate commits.

## License

Each part of the repository has its own license (`LICENSE`): a contribution is licensed under the license of the part
it changes. See [Licensing and sign-off](#licensing-and-sign-off).

## Security

Do not commit passwords, tokens, private keys, production connection strings, or real user data. The
credentials in the local Dex configuration are deliberately public development credentials and must
not be reused elsewhere.

If you find a security vulnerability, do not open a public issue or pull request. Report it privately as
described in [SECURITY.md](SECURITY.md).

## Licensing and sign-off

The contributions to `core/` are licensed under the MIT License, the contributions to `admin-api/` and `web/` under the
GNU AGPL-3.0-only (see `LICENSE`). A contribution is accepted under the Developer Certificate of Origin 1.1: every commit
ends with a `Signed-off-by:` line with your name and e-mail, which says that you wrote the change or have the right to submit
it under the license of its part. Commit with `git commit -s`. The CI checks it on every pull request.

This is not a CLA: the project does not take the right to relicense your code. Only the author's own code may be offered
under another license.

### Policy writes in migrations

Once the mandatory-outbox triggers exist, any migration that rewrites or truncates a covered policy table must also
write an outbox entry in that transaction. Foreign-key cascades count too. Do not disable the triggers, use replica mode,
or change expected checksums to make a migration pass. The folded v2 init inserts its initial rows before installing
triggers. Test fixture SQL uses `core/testsupport.RawPolicyTx`; production writers use `PolicyTx` or `TakeRevision`.

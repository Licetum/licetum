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

This starts PostgreSQL, Redis, Dex, the Go services (`core/`, `admin-api/`) and the Next.js interface (`web/`). Open
<http://localhost:3000> and sign in with the development account:

- Email: `admin@example.com`
- Password: `password`

The Dex client, credentials, and signing configuration are for local development only. Configure a
real identity provider through `PLATFORM__*` variables before using Licetum outside the local
Compose stack.

## Repository layout

- `core/` — the MIT module: the engine, the check API, migrations, synchronizers and the check schema
- `admin-api/` — the AGPL module: the admin API and the commands that go with it (depends on `core/`)
- `web/` — the Next.js + Ant Design interface (see `web/README.md`)
- `infra/` — Compose fragments for PostgreSQL, Redis, and Dex
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
module tidiness, OpenAPI schema drift, web formatting, TypeScript, and translations.

Backend integration tests use disposable PostgreSQL and Redis containers:

```bash
task go:test:unit
task go:test:integration
task go:coverage
```

Docker is required for integration and coverage runs. The suite creates disposable containers and
its own database schema, so no shared PostgreSQL or Redis instance is needed. The integration and
coverage tasks resolve the active Docker context automatically; direct `go test` runs should set
`DOCKER_HOST` when Docker Desktop or another non-default context is active.

## Making changes

### Backend and API changes

- Keep the dependency direction: `api` → `service` → `db`/`redisx`/`synchronizer` → `model`/`config`/`i18n`/`apperr`.
- Keep HTTP concerns in `admin-api/api/` and `core/server/`; business logic belongs in `admin-api/service/` (admin) or `core/engine/` (check).
- Register new Huma operations with an `OperationID`, summary, description, tags, documented errors,
  and model field documentation.
- Write API descriptions and user-facing API messages in English first.
- Update `internal/api/locales/ru.json` and `internal/api/locales/zh.json` when API descriptions
  change. Add error messages to all supported locales.
- Add a new numbered Goose migration for schema changes. Do not rewrite an already-applied migration.
- Run `task generate` after changing an API schema and verify that `task check` passes.

### Migrations must work with the previous release

During a rolling update the new schema is applied (`licetum migrate`, which runs first) while pods of the **previous**
release are still serving requests, and a rollback puts those pods back for good. So a migration must leave the schema
usable by the release that came before it. Make every change in two steps, **expand** and **contract**, in two releases:

1. **Expand** (release N): add what the new code needs, and change nothing the old code reads or writes. Add a column
   as nullable or with a default, add a table or an index, add an enum value. The new code reads and writes both the old
   and the new shape.
2. **Contract** (release N+1 or later, once release N-1 can no longer run): drop the column, the table or the constraint
   nothing uses any more, make a column `NOT NULL`, tighten a check.

In particular, in one release do not: drop or rename a column, a table or an enum value; add a `NOT NULL` column without
a default; narrow a column type; add a constraint that rows written by the old code can violate; change the meaning of
an existing value. A rename is a new column, a copy, a switch of the readers, and a drop two releases later. Long
statements (a table rewrite, an index on a large table) need `CREATE INDEX CONCURRENTLY` or batches, because they hold
locks that the pods of the previous release wait on.

The check API reports ready (`/readyz`) when the schema is **at least** the version it was built for, so pods of the
previous release stay in service while the new schema is applied. `licetum migrate` takes an advisory lock, so several
runs started together (one per pod) apply each migration once; it needs a connection that lasts for the whole run, which
behind PgBouncer in transaction mode means `DB__MIGRATE_URI` pointing at PostgreSQL itself.

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

Use clear, descriptive commit messages in the imperative mood. There is no requirement to use a
specific commit-message format, but unrelated changes should not be combined into one commit.

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

## What and why

<!-- What changes, and the reason. Link the issue it closes: "Closes #123". -->

## Kind of change

- [ ] Fix
- [ ] Feature
- [ ] Refactoring or cleanup (no behaviour change)
- [ ] Documentation only
- [ ] Build, CI or dependencies

## Checklist

- [ ] Every commit ends with `Signed-off-by:` (`git commit -s`), see [CONTRIBUTING.md](../CONTRIBUTING.md#licensing-and-sign-off).
- [ ] `task check` passes locally (or the reason it cannot run is given below).
- [ ] Tests cover the change: unit tests, and integration tests when the database or the API is involved.
- [ ] If the API changed: `task go:schema` was run and `schema/*.yaml` is committed. Descriptions and messages have their translations.
- [ ] If the interface changed: the texts are in ru, en and zh, and `task web:lint` passes.
- [ ] No secrets, tokens, real connection strings or personal data in the code, the tests or the logs.
- [ ] The change is in the part of the repository it belongs to: `core/` (MIT), `admin-api/` and `web/` (AGPL-3.0-only).

## Notes for the reviewer

<!-- Anything that needs attention: a trade-off, a migration, a follow-up. -->

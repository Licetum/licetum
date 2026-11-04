# P3 implementation and verification

P3 (#54) targets `epic/production-load-readiness`, from `a7436a8ce204adc4f3826b9ab92cab02625c3916`.
The owner accepted the defaults in #94. This branch is prepared for a draft PR; it is not merged.

## Scope and migration boundary

Schema v2 has one initializer and its own `goose_db_version_v2` ledger. A v0.1.x database is refused before migration writes.
There is no in-place database upgrade or v1 policy import. Reinstall on a new database and recreate the policy.
For disposable development data, `docker compose down -v` removes the old volumes before `docker compose up`.
Set `LOG__SUBJECT_SALT` outside the database.

[Schema equivalence](../core/tests/integration/db/schema_v2_equivalence.md) records the complete dump comparison,
including function ACLs. The folded initializer is immutable; subsequent schema changes need numbered migrations.
DBOS owns its scheduler tables. The initializer does not create copies of DBOS tables.

The approved #94 contract allocation supersedes the older exit line in #54: WR07 belongs to P5, and HIS02/HIS03 belong to P4.
P3 activates PUB02, PUB05, WR01–WR06, HIS01, HIS04, HIS05, ADM11, OPS04 (PostgreSQL half), OPS08 and the approved DEC15.
`contracts.yaml` and `contractscheck` enforce that allocation.

P3 deliberately retains global cache invalidation, global admin routes with service ownership in their bodies,
atomic desired/effective type-link updates and the existing platform authorization model. Per-service cache precision,
binding authorization and the asynchronous closure writer belong to later phases, as recorded in the ADR's P3 decisions.

## Whole-stage review

The review used separate sequential passes over security/authorization, references and removals, ADR conformance,
test integrity, and transaction/code correctness. Each suspected finding was checked against both its implementation
and its actual callers or a reproducing test before a change. These are agent review passes, not independent human approvals.

Verified findings fixed during closure:

| Finding | Verification and correction |
| --- | --- |
| The existing seed-image case ran after platform initialization and therefore exercised a no-op seed. | Checked `SeedPlatform`'s creation guard and the fixture's eager seed. Added `TestContract_WR03_InitialSeedPublishesEveryCreatedRow` on a migrated, unseeded database, with nonempty publication/history assertions and an idempotent second call. The existing restore case remains. |
| The deep-hierarchy fixture wrote covered tables without publication and did not model a completed first synchronization. | Reproduced mandatory-outbox failures, then zero-anchor failures after fixing publication. The fixture now uses `RawExec`, sets `synced_at` for its preloaded snapshot, and closes its engine. Both existing hierarchy assertions remain unchanged. |
| New tests duplicated AuthZEN paths outside the request builders. | The existing endpoint ratchet reproduced the violations. Canned simulations now derive their method, path and body from `authzenreq`; the mixed-type batch derives its path there too. No ratchet allowance was added. |
| The standard integration task missed tagged tests located in `core/engine`. | Compared task package selection with the added first-apply/fencing suite. Both integration tasks now run all packages sequentially with `-p 1`, including the PgBouncer task. |
| Admin readers retained the pre-first-apply graph after waiting for synchronization. | Seven existing API tests reproduced missing resources, unresolved parents and empty previews. `EnsureSyncedGraph` returns the refreshed immutable graph to resource lists, source tests, access lookup and previews; a first point lookup waits for the full snapshot. Resource lists also hide retained rows when a new generation has not completed its first apply, including failed/backoff cases. The seven original assertions pass, and the resource-attributes test now also checks that all sibling rows were initialized. |
| Inline point fetches lacked the source-generation fence used by full apply. | Traced both fetch/apply paths and added `TestInlineFetchCannotApplyARetiredSourceGeneration` for deletion, reconfiguration and configuration ABA. Inline writes now take the same source fence before touching rows; all three cases reject the stale result. |
| The replica test still expected service slug mutation to succeed. | The old scenario reproduced a 422 `service_slug_immutable` response, as ADM11 requires. It now checks that refusal and continued access on the original slug across three replicas. The service-deletion case and its one-second propagation assertion remain. |
| The comprehensive runner fixture used `subject` inside three `from_property` targets. | Strict parsing reproduced the unknown-field error. Corrected those keys to `property`, retaining strict decoding and every expected decision. The focused runner test and full contract suite pass. |
| The hot-key chaos test still equated one admin write with exactly one global revision. | Its failed run advanced from 21 to 24. Inspection in the corrected test proves `data → svc → data`: one audited admin publication and two legitimate data publications. The real admin write, load, cache-miss threshold, zero-error requirement, pool bound and latency limit remain. Added exact admin/header/history allocation, allowed-data classification and a gap-free check over the full global range; the focused run passes with 1,200 requests and no errors. |
| Comments still described retired revision scopes and an old migration number. | Compared comments with the global-position queries and v2 initializer, then corrected the comments. |

Rejected suspicion: `TakeRevision` captures a position before outer commit, but directory adapters call
`NoteCommittedPosition` only after their database operation returns successfully. `PolicyTx` captures its position
after successful commit. The suspected failed-commit response-header leak did not follow from the actual callers;
no production change was made for it.

The trigger review checked fixed schema search paths, revoked PUBLIC function privileges, `ENABLE ALWAYS`, deferred
same-XID checking, and the owner-bypass boundary. Ownership review checked composite foreign keys and immutable slugs.
Source review checked pre-write source fencing, configuration generation/ABA checks and first-apply mode transitions.
Canonical-image review checked independent SQL snapshots, cascades, first-before/final-after deduplication and no-op writes.
The executable evidence is in the P3 contract map in [the integration README](../admin-api/tests/integration/README.md).

## Reproduction

With the pinned Go, Node/pnpm, Task, Helm and kubeconform tools, and Docker or `LICETUM_TEST_POSTGRES_URI`:

```sh
task go:test:unit
task go:test:integration
CI=true task check
deploy/helm/test.sh
docker compose config -q
(cd web && CI=true pnpm test --runInBand && pnpm build)
task go:test:chaos
```

The integration run includes DEC01–DEC15 and DECP seeds 1–20, API, cluster, schema and first-apply/fencing suites.
Do not run independent PostgreSQL suite processes concurrently against a small shared instance.

The local fresh-volume smoke used the resolved Compose service configuration with prebuilt static binaries in place
of the development image builds, on a dedicated project, network and new volumes. Migration 1 and platform seed
completed; the database had position `1.1`, one seed outbox row and no dirty transaction marks. PDP readiness and
service discovery returned 200, the unauthenticated admin request returned 401, and the DBOS worker launched.
The web production build was checked separately. This smoke does not claim to verify the development Docker image builds.

The measured 54-route write budgets and transaction counts are recorded in
[the WR06 table](../admin-api/tests/integration/api/write_roundtrips.md). The old fixed-six-RTT assumption was
manually measured with 150 ms Toxiproxy latency and replaced by WR06; it is not presented as a passing criterion.
[The subject-type measurement](../admin-api/loadtest/notebook/results/p3-subject-type/README.md) contains the matched
P3-29 medium-profile runs and their machine-readable evidence.

## Release checks outside this local workspace

CI and CodeQL must pass on the PR head before merge. A local lint/test run does not establish that there are no new CodeQL alerts.
Independent reviewer approval remains a PR step. Merge, issue closure/checklist updates and nightly dispatch on the merged epic
are intentionally not performed as part of preparing this draft branch.

The multi-region environment is already disabled until P7 in `nightly-suite.yml`; compiling it does not validate its obsolete
Redis/replica topology. The nightly PgBouncer and full load matrices remain release checks on the merged epic.

## Completed local checks

The final code and corrected fixtures passed the following checks on 2026-11-01 with Go 1.26.10 and PostgreSQL 16.15.
Test-event counts include parent tests and subtests; they are not counts of unique contracts.

| Check | Result |
| --- | --- |
| Unit tests of both modules, `-race` | passed |
| All core packages with `integration`, `-race`, `-p 1` | 22 passing packages, 732 passing test events, no skipped tests |
| Full API suite, `-race` | passed in 1535.328 s; 380 passing events; only the existing opt-in `TestSyncWorkerMemory` skipped |
| Full cluster suite, `-race` | 39 passing events, no skipped tests |
| Full contracts suite, `-race` | 855 passing events, including DEC01–DEC15, DECP seeds 1–20 and the corrected comprehensive runner fixture |
| Full chaos suite, `-race` | all packages passed, 14 passing test events, no skipped tests |
| Harness tests | 5 passing events |
| `CI=true task check` | passed after the final test corrections |
| Web Jest tests and production build | 13 suites / 111 tests passed; build passed |
| Helm lint, golden renders, kubeconform; Compose config | passed |
| Final-binary smoke on a new Compose volume | passed; exact schema/seed invariants, readiness, discovery, authentication enforcement and worker startup verified |

The inline-fetch and retained-row regressions were also run against the previous code (`38e15a0`) with identical new
regression tests. All four expected failures reproduced there; the corrected core suite passes them. Those baseline
failures are deliberate regression evidence, not failures of the final implementation.

The [P3 pre-merge measurements](../admin-api/loadtest/notebook/results/p3-premerge/README.md) now include P3-04, P3-18 and the
validated final code. All three medium runs sustain the committed 80 requests/s reference rate with zero errors, drops or oracle
mismatches, and pass the unchanged budgets. Overall p99 is 83.541 ms, 87.121 ms and 90.645 ms respectively, below 100 ms.
The final report commit changes only evidence and this documentation; the validated Go/web/SQL code is unchanged.

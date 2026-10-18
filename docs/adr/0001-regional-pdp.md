# ADR 0001: Regional AuthZEN PDP, PostgreSQL as the source of truth, service-scoped administration

- **Status:** accepted (the owner asked for the plan to be implemented in full)
- **Date:** 2026-10-18
- **Issue:** #49 (phases #50–#58)
- **Supersedes:** the read-replica design kept at tag `archive/pg-replica-design`

## 1. Context and decision

### 1.1 Context

The facts below were checked against `229c4ed`, the tip of `epic/production-load-readiness` (PR #59); its product code is the same as at `8989ed4` (PR #48), the last commit of the read-replica design. It adds `contracts.yaml`, the tool `core/internal/tools/contractscheck` with its task, and CI workflows. File references point into that tree.

**What runs today**

- `licetum check` (`core/cmd/licetum`, :8001) answers `GET /service/{id}/permission`, `POST /service/{id}/check`, the calls on behalf of a person (`…/on_behalf/check`, `…/on_behalf/permission`) and the AuthZEN endpoints.
  - It verifies end-user tokens against an OAuth configuration stored on each `service`: `authorization_url`, `token_url`, `jwks_url`, `issuer`, `client_id`, `client_secret` and `trusted_clients` (`ServiceUpdate`, `core/model/service.go`).
- `licetum-admin admin` (`admin-api`, :8000) is the admin API. Licetum's own permissions protect it.
  - The platform is registered as a service within itself, with six policy types (`service`, `type_resource`, `group`, `group_member`, `role`, `assignment_rule`).
  - Each of these types is filled by a SQL synchronizer that reads Licetum's own tables.
  - The web interface signs in through the OAuth endpoints of that platform service: `web/src/app/auth/login/route.ts` redirects to `/service/{id}/oauth/login`.
- `licetum worker` runs the synchronizations and the retention. It uses DBOS by default, or the `sync_job` table engine when that is selected.
- `licetum migrate`, `licetum failover` and `licetum-admin restore-admin` are the operator commands.
- PostgreSQL holds the policy and the local copy of the resources.
- Redis holds:
  - the second level of the answer and token caches;
  - the fetched source lists (`authz:fetch_all:*`);
  - the PKCE state of the sign-in (`authz:pkce:*`, `core/engine/oauth.go`);
  - the breaker state;
  - the revision counters that are kept for the previous release.
- Multi-region (F6) is a PostgreSQL primary with asynchronous streaming replicas, one or more per region (`docs/deployment.md`). A check process sends each read either to the replica or to the primary (`App.reader`, `withPrimary`, `DB__LAG_ACTION`). The tag `archive/pg-replica-design` (on `8989ed4`) keeps this design for reference.

**Released state**

- `v0.1.0` is a published release: tag `v0.1.0` on `0fc5b0b`, 2026-10-12.
- Its `core/db/migrations/00001_init.sql` is identical to the current one. Migrations `00002`–`00005` were added after it.
- Its public API includes `/permission`, `/check` and the calls on behalf of a person.
- The `previous-release` job of `.github/workflows/ci.yml` finds this tag and runs the release's integration tests against the migrations of each change.

**The owner's proposal**

- A PDP in every region.
- ScyllaDB multi-DC as the replicated store of the rules.
- A separate service that writes to PostgreSQL, and a DBOS service that writes to PostgreSQL and ScyllaDB "in one transaction".
- Only AuthZEN 1.0 on the public surface, with authentication only for the administrators and the AuthZEN callers.
- Administration of Licetum scoped per service, with a hierarchy.
- Tests as contracts, permission to break the current contracts, and replacement of the F-numbered tests.

**Defects of the current code that the decision must not carry over** (each confirmed by reading the code)

1. **Transitive fail-open.**
   - `UnresolvedParents` (`core/engine/resource_cache.go:836`) returns only the unresolved edges whose child is the resource itself (`resource_edge.child_id`).
   - So `CoversOf` (`core/engine/check.go:189`) and pass C of `coverNodes` (`core/engine/permission.go:982`) do not treat a resource as possibly under a denial when the unresolved link belongs to one of its ancestors.
2. **No staleness limit.** `POLICY__MAX_STALENESS` defaults to `0`, which means no limit (`core/config/config.go:314`). A partitioned check process keeps answering from its last view for as long as the partition lasts.
3. **No audience check.** `fetchAndVerify` (`core/engine/oauth.go:512`) checks the signature, `iss` and `iat`, but not `aud`.
4. **Snapshot labelled with an earlier revision.** The snapshot gets a revision that was read before its own transaction: `snapshotFor` passes the revision into `loadSnapshot` (`core/engine/snapshot.go:129`).
5. **Data revisions name only the owning services.** `bumpDataRevisions` (`core/engine/cache.go:353`) bumps only the services that own the synchronized types. This is correct for the current cache, whose answers record the data revisions of every involved type's service (Appendix), but the per-type streams of v2 must name every service whose lineage changed (4.8).
6. **Rollback is not scoped to a service.** `PlanRollback` (`admin-api/service/history.go:37`) undoes every change after the position, for every service.
7. **Database password in a synchronizer.** `seedPlatformType` (`core/db/migrate.go:273`) stores Licetum's own `DB__URI`, password included, in the `synchronizer.config` of the platform types.
8. **The service in the path is not checked.**
   - `GET /type_resource`, `/type_resource/{type_resource_id}/resource` and `/type_resource/{type_resource_id}/resource/refresh` have no service in their path (`admin-api/api/admin.go:267,296,314`).
   - `ListAccessGroups` and `CreateAccessGroup` look the type up with `GetExistingTypeResourceAnyService`.
9. **Short-circuit semantics do not short-circuit.** `deny_on_first_deny` and `permit_on_first_permit` decide every evaluation and only then cut the response (`applySemantic`, `core/server/authzen.go:294`).
10. **The check path writes to PostgreSQL.**
    - `ensure` writes `sync_demand` and enqueues a synchronization: a DBOS workflow by default, a `sync_job` row with `SYNC__ENGINE=postgres` (`enqueueSync`, `core/engine/sync_jobs.go:254`).
    - When `SUBJECTS__ENABLED` is on (it is off by default; the Compose stack turns it on), `NoteSubject` records `known_subject` after a token verification, at most once per ten minutes per subject and process.
    - Every delegated call writes `delegated_call`.
11. **AuthZEN deviations.**
    - The `Bearer ` prefix is matched case-sensitively, and any other scheme gives 400 (`core/server/check.go:67`).
    - `resource.id` is optional (`core/model/authzen.go`).
    - A nested `context` value is refused with 400.
    - An `X-Request-ID` that fails `validRequestID` is replaced with a generated id.

**Origin of this decision**

- Three drafts (A, B and C) answered the proposal.
- This ADR starts from draft A, which had the highest combined review score: 114, against 109 for each of the others.
- It replaces A's data plane with B's. All three reviewers required that change, because A's direct projection fails open.
- From C it takes the PostgreSQL mode for one region, the removal of Redis, and C's operational practices.
- An independent review of the resulting plan then corrected it. The decision below includes those corrections and the owner's decisions. Section 2 lists what changed.

### 1.2 Decision

1. **Processes and images.** There are four roles and three images: `core` (holding `licetum-pdp` and `licetum-writer`), `admin-api`, and `web`.
   - `licetum-pdp` (core, MIT) runs in every region.
     - Its public port serves only AuthZEN. `/healthz`, `/readyz` and `/metrics` move to a separate ops port.
     - It writes nothing, has no Redis client, and reads the policy only through the read model (point 4).
   - `licetum-admin` (admin-api, AGPL) runs in the home region. It is the write API, and it writes only to PostgreSQL.
   - `licetum-writer` (core, MIT) runs in the home region as two StatefulSets, `sync` and `project`.
     - `sync` has a stable `DBOS__VMID`, because DBOS v1.6.0 recovers only the workflows of its own executor (`dbos.go:894`); `project` runs no DBOS workflow.
     - It synchronizes the sources into PostgreSQL, publishes the outbox, writes the heartbeat and the checkpoints, reconciles, applies retention, and collects the key sets of the PEP issuers.
     - With the CQL adapter, its role `apply` is the regional applier of the log, and it runs in every region.
     - Its operator subcommands are `migrate`, `rebuild`, `reconcile`, `checkpoint`, `failover`, `freeze` and `unfreeze`.
   - `web` is the Next.js BFF and the OIDC client of the administrators' IdP. It talks only to `licetum-admin`.

2. **PostgreSQL is the source of truth.**
   - **One global revision.** There is a single global revision `(epoch, revision)`. It has no gaps and follows commit order.
     - A transaction takes its revision with its last statement: a CTE bumps `revision_state` and inserts into `outbox`.
     - The row lock on `revision_state` is held until COMMIT.
     - An admin transaction first takes `pg_advisory_xact_lock(admin_write)`.
   - **Outbox row required.** Every transaction that changes data a decision reads must write an `outbox` row. The schema enforces this at COMMIT; an agreement in code is not enough.
     - Statement-level triggers with transition tables mark the transaction only when a statement actually inserted, updated or deleted tuples of a checked table, and a deferred constraint trigger checks the mark at COMMIT (4.7). `pg_stat_get_xact_tuples_*` is not used: it also counts changes of rolled-back subtransactions.
     - The check requires an `outbox` row only from a marked transaction. A synchronization that changed no row therefore spends no revision.
     - `AFTER TRUNCATE` is covered too.
     - P3 puts the check on the policy tables. The resource tables follow in P5.
     - The triggers are `ENABLE ALWAYS`, so `session_replication_role = replica` does not switch them off. A table owner or superuser can still disable or drop a trigger, or restore data with triggers off. Reconcile catches such writes. This ADR does not claim that a bypass is impossible.

3. **Publication, and the CQL store as a replicated log.**
   - **Publication through the outbox.**
     - The outbox is the durable queue. A single publisher leader reads `outbox WHERE revision > last`.
     - The writing transaction issues `pg_notify` to wake the leader. NOTIFY is delivered only on commit, including from a synchronization transaction.
     - A periodic tick is the safety net. The heartbeat is a ticker of the same leader.
     - There is no DBOS workflow per revision. DBOS stays for source synchronization.
   - **The CQL store replicates a log, not rule tables.**
     - The log is immutable, ordered, checked by hashes, and split into streams.
     - A regional applier materializes the log into a DC-local keyspace as MVCC rows, where the version is the log position.
     - The applier advances the region's position only over a contiguous prefix.
     - The PDP reads every partition of one decision at one position P. Torn and mixed reads, the cause of the fail-open, cannot happen.
   - **Writes to the CQL store are at-least-once and deterministic.**
     - The outbox fixes the content, the key is the position, and `USING TIMESTAMP` is derived from the position.
     - Retries, recovery and duplicate executors converge to the same bytes. Exactly-once delivery to the CQL store is neither needed nor claimed.
   - **Choice of store.**
     - Whether the CQL store is ScyllaDB or Apache Cassandra 5 is decided in P0.5. That phase runs a spike and settles the license question. ScyllaDB 2025.1 and later is source-available and free up to 50 vCPU and 10 TB per organization; its last AGPL release is 6.2.
     - The CQL model stays Cassandra-compatible: no LWT, no materialized views, no secondary indexes, no tablets-only features.
   - **Durability.**
     - Repair, or repair-based node operations after a node is replaced, is a correctness requirement.
     - A nightly reconcile per region is mandatory.
     - P6 starts append-only, with manual rotation of generations. The MVCC GC is enabled only after the APP and ASOF contracts pass under kill tests.

4. **The read model is a port.** `ReadModel` and `LogSource` are ports with two adapters. The packages `core/pdp`, `core/applier`, `core/decide` and `core/policy` do not import `pgx`, `core/db` or `dbos`.
   - **`postgres` (the default).**
     - The PDP reads the canonical tables under a role that has only SELECT.
     - It reads in a `REPEATABLE READ READ ONLY` transaction, with the revision read inside that transaction, and then follows the outbox.
     - The adapter works against the primary or against a regional streaming replica. A replica's `REPEATABLE READ` snapshot is a consistent committed prefix, so multi-region works without a CQL store.
     - In this mode the outbox is kept for a fixed age.
     - The heartbeat is a row that the writer commits into PostgreSQL. T(P) is therefore read in the same snapshot as the policy, and it lags with the replica.
   - **CQL.** For multi-region through the log and the regional applier. The regions hold no PostgreSQL credentials: contract OPS05 checks that there are none and that no connection is opened to 5432.
   - **Keeping the adapters equal.** A shared `storetest` suite and a differential contract keep the two adapters equal.
   - **Moving from `postgres` to CQL.** An installation moves with a documented `rebuild --all`, which has its own contract.
   - **Replica routing is removed.** The current routing between a replica and the primary inside one process (`App.reader`, `withPrimary`, `DB__LAG_ACTION`) goes in P2e.

5. **Redis is removed from every process** (P2e, its own PR).
   - The PKCE verifier, `state` and `nonce` of the administrators' sign-in move to a short-lived encrypted cookie of the BFF.
   - `synchronizer.cache_ttl` and `SYNC__FETCH_TTL` are removed from the model, the schema and the UI.

6. **The public API is strictly AuthZEN 1.0.**
   - **PEP authentication.** A PEP authenticates with one of:
     - a JWT from client credentials, with mandatory `aud` and an `alg` allowlist;
     - an API key, stored as sha256, with two active keys at most.
   - **PEP clients.** A `pep_client` belongs to one service. Its rights are listed per operation: `evaluate`, `explain`, `search_resource`, `search_action`, `search_subject`. Explain and search are off by default.
   - **Removed.** Per-service OAuth, `/permission`, `/check`, `on_behalf`, `trusted_clients`, `delegated_call` and `known_subject`. This breaks the public API of `v0.1.0`, and the release notes say so.
   - **Extensions.** Licetum's extensions use only the slots the specification allows:
     - `context.licetum.{min_revision, explain}`, read only from the top-level `context` of a request;
     - `resource.id = "*"` for a question about the whole type.
   - **Response headers.** Responses carry `Licetum-Revision` and `Licetum-Error-Code`.
   - **Service in the path.** The path names the service by slug only. A slug cannot change after creation and is never reused.
   - **Required fields.** Each endpoint has its own list. In a search, `subject.id` and `resource.id` follow the specification ("SHOULD be omitted" / "MUST be ignored").
   - **Search pagination.** Each page is computed at the current position. The cursor is (bucket, last key, request digest). There is no snapshot paging across pages and no `authzen_page_expired`.
   - **Explain and upstream rules.** For rules of an upstream service, explain gives only `upstream: <slug>` and the level.
   - **Route check.** The public port serves exactly the AuthZEN routes. A snapshot test of the route table keeps it so.
   - **Subject search.** It works on a per-service `subject_directory`.
     - The directory is filled through the admin API by the role `directory_writer` (a machine client of the admin IdP), with a rate limit.
     - Its writes take only the revision row, not `admin_write`.

7. **Licetum's rights over itself.**
   - **Scoped role bindings.** Roles are bound to the scopes `platform ⊃ service ⊃ group`.
     - The bindings are stored in PostgreSQL. They are checked in the request and checked again inside the write transaction. The PDP takes no part.
     - Revoking an administrator takes effect at the next request.
   - **Ownership.** Groups, roles and rules belong to a service. Composite foreign keys enforce this.
   - **Dogfooding removed (P4).** The platform service, its six types, the SQL self-synchronizers with `DB__URI`, and `invalidatePlatformSync` are all removed.
   - **Administrator sign-in.** It is a static OIDC configuration (`ADMIN_AUTH__*`), with the BFF as the OIDC client.
   - **Seed and `restore-admin`.** These now depend on `ADMIN_AUTH__*`, not on `PLATFORM__*`.
   - **Cross-service type links.**
     - A type may point only at a parent type that the parent's owner marked `exported`.
     - The link is allowed only within one identity domain.
     - While a type is linked, its `exported` flag cannot be cleared and the type cannot be deleted (409 `blocked_by`).

8. **Revocation has a hard bound.**
   - **The bound.** An answer is served at PDP time τ only while τ − T(P) ≤ S. A revocation committed at t is therefore enforced from t + S + ε.
     - T(P) is a time on PostgreSQL's clock: the start of the heartbeat statement (4.9.3), or, for a PDP that reads the primary, the start of its own read of the revision (6.2).
     - S defaults to 60 s, and configuration validation rejects `0`.
     - ε is the clock skew between the PDP and PostgreSQL. It is measured from the hosts' clock discipline (chrony, `node_timex_*`), not from the heartbeat.
   - **Beyond the bound** the region fails closed: 503 and `/readyz` false.
   - **`freeze --dc`.** This audited command disables only the staleness gate. Application of the log continues.
   - **PostgreSQL failover.**
     - Failover uses epochs, detects divergence, and publishes a corrective `epoch_start`.
     - Fencing the old primary is mandatory.
     - Either the standby is synchronous, or S is derived from the measured failover time.

9. **Tests are a catalog of contracts with independent oracles.**
   - **Catalog.** `contracts.yaml` lists the contracts. The oracles are:
     - the reference evaluator `refeval`;
     - the PostgreSQL closure, as the storage oracle;
     - an analytic load oracle.
   - **External terms.** The P1 contracts and `refeval` use external terms (type names, external ids) from the first day, so they survive the change of anchors in P5.
   - **F numbering is removed.**
   - **Contracts not yet reachable.** A contract that cannot be met before a given phase is marked `planned`, for example OPS02 and POL05 until P7.
   - **Rule for every PR.** Each PR keeps the web check, `compose up` and the Helm golden files green, or marks the gap `planned`.

10. **Order of work.**
    - **Phases.** P0, P0.5 and P1–P7 run on the existing branch `epic/production-load-readiness`.
      - P0.5 is the CQL spike and the license decision, and runs in parallel with P1.
      - PR #40 stays a draft until P7.
      - Issue #31 is closed as superseded.
    - **P1** fixes the transitive fail-open before anything moves. It also measures the cost on the loadtest medium profile before merging.
    - **P2** runs in this order: 2a, 2b, 2d, then 2c.
      - Step 2d brings in the admin OIDC and moves the seed and `restore-admin` off `PLATFORM__*`. It must come before 2c, which removes per-service OAuth.
      - `subject_directory` arrives in P2, together with the removal of `known_subject`.
      - Step 2e (Redis, replica routing, the table engine) is its own PR.
    - **P3** introduces schema v2 with its own goose version table.
      - `migrate` refuses a `v0.1.x` database with a clear message. There is no upgrade path.
      - The `previous-release` CI job is removed in P3.

## 2. Corrections to the original proposal

### 2.1 The owner's proposal

| Owner's item | What is wrong with it | What we do |
|---|---|---|
| 1.4: "a transaction is needed to insert into PostgreSQL and ScyllaDB, hence DBOS" | No transaction spans PostgreSQL and a CQL store, and DBOS does not provide one. `RunAsTransaction` and `WithEnqueueTransaction` are exactly-once for PostgreSQL only. A step that writes to the CQL store runs at least once. DBOS v1.6.0 also refuses `WithEnqueueTransaction` inside a workflow (`dbos/workflow.go:2126`), and synchronizations apply their changes inside workflows. | The atomic unit is one PostgreSQL transaction that writes the entity, the outbox row and the revision. Writes to the CQL store are deterministic: the outbox fixes the content, the key is the position, and `USING TIMESTAMP` is derived from the position. Publication is a single leader woken by `pg_notify`, not a DBOS workflow per revision. |
| 1.3 and 1.4 as two services, where the second inserts into PostgreSQL for the first | Validation, 409 `blocked_by`/`idTaken`, the last-admin guard and read-your-writes all need a synchronous PostgreSQL transaction. A hop through a queue would turn 4xx answers into asynchronous workflow failures. | `licetum-admin` commits the policy together with its outbox row. `licetum-writer` writes what does not come from an administrator (the resource graph from the sources) and publishes. |
| 1.2: "ScyllaDB multi-DC replicates the rules" | Replication works per partition, with no order between partitions and no signal that a DC is complete. A PDP that reads materialized tables directly can see a watermark before the rows and assemble a state that never existed. Counterexample: R is under denial D (through F) and under exception E. One change moves E under D and takes R out from under E. A PDP that sees R's old lineage and E's new one concludes "E below D, R below E" and lifts the denial, although R is denied both before and after the change. This is a fail-open. | The CQL store replicates an immutable log (`licetum_log`, RF 3 in each DC). A regional applier checks continuity and hashes, then writes MVCC rows into a DC-local keyspace. The PDP reads at one position P. |
| 1.2, implied: "ScyllaDB is mandatory" | A three-node cluster with regular repair is excess operational load for a single-region self-hosted installation. ScyllaDB 2025.1 and later is source-available, not open source, which is a licensing question for an MIT/AGPL self-hosted product. | `ReadModel` is a port. `postgres` is the default and works against the primary or a regional streaming replica, so multi-region does not need a CQL store. The CQL adapter serves multi-region. The store (ScyllaDB or Apache Cassandra 5) is chosen in P0.5. The CQL model stays Cassandra-compatible. |
| 2: "authentication only for the admin interface and the AuthZEN services" | Removing per-service OAuth also removes three other things. (a) The administrators' sign-in, which goes through `/service/{platform id}/oauth/*`. (b) The only trusted source of claims. (c) The only writer of `known_subject` (`NoteSubject`). In addition, `SeedPlatform` and `restore-admin` require `Platform.IsConfigured()`, that is all six OAuth variables `PLATFORM__*` (`core/config/config.go:145`, `core/db/migrate.go:218`, `core/db/restore.go:36`). Without them the seed is skipped silently, the admin API answers 503, and break-glass stops working. | Static administrator OIDC (`ADMIN_AUTH__*`) and the BFF as OIDC client arrive in step 2d. The seed and `restore-admin` switch to them before per-service OAuth is removed in step 2c. A registry of PEP clients with rights per operation, and the per-service `subject_directory`, also arrive in P2. The trust model is stated explicitly: a bound PEP may assert any subject and any properties for its own service. Cross-service links are allowed only within one identity domain. |
| 2: "the full AuthZEN API and no extra endpoints" | This cannot be done literally. Read-your-writes, explain and whole-type questions need a carrier, and probes and metrics are not part of AuthZEN. The current code also deviates from the specification (section 1.1, items 9 and 11). | The carriers are `context.licetum` (top-level `context` only) and `resource.id = "*"`. The probes move to an ops port. Every deviation is fixed. The public port serves exactly the AuthZEN routes, checked by a snapshot test of the route table. |
| 3: "scaling becomes simpler" | This is true for reads only. The cost moves to the CQL store, the ordered publisher, the PostgreSQL↔CQL reconcile and an applier per region. | The cost is budgeted and listed among the risks. Repair (or repair-based node operations after a node replacement) and a nightly reconcile per region are correctness requirements, not only durability measures. |
| 4: "Licetum's rights over itself need a hierarchy" | The flaw is in the mechanism as well as the missing hierarchy. Six flat platform types are filled by SQL self-synchronizers that keep `DB__URI`, password included, in `synchronizer.config`. `invalidatePlatformSync` makes the next admin request wait for the worker. If administration went through the replicated store, revoking an administrator would become eventual. | Explicit role bindings per scope, computed in PostgreSQL within the request and checked again in the write transaction. The dogfooding is removed entirely. |
| 5: "contracts may be broken" | True, but the epic is the only executable specification of the semantics today. | Contracts are written against the current engine first (P1), in external terms. Removals come only after them. |
| 6: "the F tests are inconvenient" | The problem is the oracles, not the names. `decision.go` has no direct tests (there is no `core/engine/decision_test.go`). `TestF1_11_TheSnapshotPathAnswersLikeTheQueryPath` compares two loaders on top of the same `decision.go`. `chaos.yml` and `multiregion.yml` are not on `main`, so their schedules never ran. `helm.yml` is not on `main` either; it has no schedule and runs on pull requests that touch the chart. | A catalog of contracts, an independent reference evaluator (`refeval`), and the PostgreSQL closure as the storage oracle. Nightly runs are started from `main` by a thin dispatcher that calls reusable workflows at the epic's ref. |
| Implied: the PDP "only reads" | The check path writes to PostgreSQL today: `sync_demand` and a synchronization job (`ensure`: a DBOS workflow by default, a `sync_job` row with the table engine), `known_subject` after a token verification, and `delegated_call` on every delegated call. | All of these are removed. The writer schedules synchronization, and requests never start it. Contract OPS06 lists every path by which the PDP reaches a database. |
| Implied: fail-closed | The current code has the transitive fail-open described in section 1.1, item 1. | Fixed in P1, before anything moves. A transitive set `unresolved_up` is computed together with the closure and fixed by contract DEC11. The change of semantics is announced. |
| Implied: staleness | `POLICY__MAX_STALENESS = 0` means no limit, which fails open under a network partition. | The default is 60 s, and validation rejects `0`. Only the audited `freeze` disables the gate, and application of the log continues under it. |
| Implied: failover | Failing over to an asynchronous standby can lose commits that were already published. One jump of the revision is not enough. | Epochs, automatic detection of divergence, and a corrective `epoch_start` record. Fencing the old primary is a precondition. Either the standby is synchronous, or S is derived from the measured failover time. |

### 2.2 Choices between the drafts

| Question | Choice | Why |
|---|---|---|
| Revision sequence: two (`policy`/`data`, A), one global (B), or one per service (C) | One global position `(epoch, revision)`. The payload is split into streams: `catalog`, `svc:<id>`, `data:<type>`, `dir:<svc>`, `pep`, `settings`. | A decision about service S reads the rules of upstream services, data and structure, so a consistent cut needs one position. Streams give targeted invalidation, sharding of PDP memory and incremental checkpoints. Each cached answer records the positions of `data:<type>` for every type the decision read, upstream ancestor types included, and is served only while they are unchanged (5.12). `log_settings.salt` (read in `core/engine/log_runtime.go:40`) is a secret and is not carried by the `settings` stream: it becomes the deployment secret `LOG__SUBJECT_SALT`, and schema v2 drops the column (7.12). |
| Where the revision is taken: first statement (A) or last statement through a CTE under an advisory lock (B) | An admin transaction first takes `pg_advisory_xact_lock(admin_write)` and takes the revision with its last statement (CTE bump plus `INSERT outbox`). The advisory lock and the binding re-check go in one `pgx.Batch`. `NOTIFY` goes in the final batch with the CTE. | Bumping first would block synchronization commits for the whole admin transaction. The advisory lock removes write skew. Revision order equals commit order because the row lock is held until COMMIT. The round-trip budget (WR06) is measured, not assumed. |
| Direct projection into multi-DC tables (A) or a log plus a regional applier (B) | Log plus applier. The applier is a role of `licetum-writer`. The migrate job creates the generation keyspaces (or tables) ahead of time from the home region; the applier only writes into them. | Without a component inside the DC, nobody can know that all rows up to position P have arrived. The alternative, EACH_QUORUM writes, would let one failed DC stop projection in every region. Creating a keyspace is a schema change that needs cluster-wide agreement, so a cut-off region could not do it. |
| LWT for `region_state` (B) | No LWT. The region's position is written `USING TIMESTAMP = position`, so it only grows. | The applier is deterministic, so two concurrent appliers do no harm. A Kubernetes Lease is used only to save resources. This also keeps the model Cassandra-compatible. |
| Deleting rows (A relied on `tombstone_gc=repair`) | The newest version of a key is never deleted. A deactivation is written as a new version with `gone=true`. Range deletes touch only superseded versions. A full cleanup is a rotation of the generation. | Resurrecting a superseded version changes no read at P. This does not make repair optional: if two of three replicas lose the newest version, LOCAL_QUORUM returns an older one, which is a fail-open. P6 therefore starts append-only, the GC waits for APP and ASOF under kill tests, and repair stays mandatory. |
| Consistency level for listings (A allowed LOCAL_ONE) | LOCAL_QUORUM everywhere by default. LOCAL_ONE can be enabled only for the tables that supply listing candidates. | Each candidate is decided again on `resource_v` at P under LOCAL_QUORUM. A stale listing can miss a resource but cannot grant an extra right. |
| Heartbeat while PostgreSQL is unreachable (C: `pg_reachable=false`) | Rejected. The heartbeat stops, and the regions close after S. Loss of the home region is handled by the audited `freeze`. | The projector cannot tell "PostgreSQL is down" from "PostgreSQL is reachable by the administrators but not by me", and it cannot know the commits made after its last read. |
| Carrier of read-your-writes and explain: headers (B) or body (A/C) | The body: `context.licetum.{min_revision, explain}`, read only from the top-level `context`. Headers appear only in the response: `Licetum-Revision`, `Licetum-Error-Code`. | AuthZEN SDKs pass the JSON body through, and often cannot set headers. In `/evaluations`, defaults are merged as a whole object, so a per-item `context` would otherwise drop the token. |
| Subject search: remove it (B/C) or keep it (A) | Keep it, on the per-service `subject_directory`. The directory is written through the admin API by the role `directory_writer` and published through the log. It arrives in P2. Its retention also goes through the outbox. | The owner asked for the full API. The directory is visible only to the roles of its service. It has no invariants, so it does not take `admin_write`. |
| Cross-service type links: "editor on both" (A) or the owner's consent (B/C) | The flag `type_resource.exported`, set by the owner of the parent type. It cannot be cleared, and the type cannot be deleted, while a link exists (409 `blocked_by`). Links are allowed only within one identity domain. | Consent is given once, not per link, and the owner sees the dependents. Without identity domains, rules of A would apply to the subjects of B, whose IdP may name a different person "alice". |
| PEP client: per service (A) or with a separate binding to services (B/C) | `pep_client` belongs to a service. The same IdP client in another service is registered as a separate row. | Everything stays in one scope of rights, and no object needs two owners. |
| JWKS of PEP issuers: fetched by the PDP (A) or published through the log (B) | Through the log. The writer polls the JWKS and writes the key sets into PostgreSQL (stream `pep`). | The PDP needs no outbound access to the IdP, and a cold region without a link to the IdP does not reject every PEP. The IdP must publish new keys in advance. |
| Admin write lock: one global lock, or one per service | One `admin_write` lock in v1. | Correctness is easy to prove, and the admin write load is small. Sharding per service can come later if contention is measured. |
| Checkpoints: all at once every 10k changes or 1 h (B), or per stream (A) | Per stream and incremental. A stream is checkpointed after N changes, and at least every 7 days if it changed. The log TTL is 14 days. `checkpoint_chunk` and `log_chunk` carry `part = n / 8` in the partition key (at most 8 MiB per partition; P0.5 may change the divisor). Superseded checkpoints older than the log TTL are deleted. | A type with 1M resources is not re-checkpointed every hour, and no partition grows to gigabytes. |
| Branch: a new `epic/regional-pdp` (A) or the existing one (C) | The existing `epic/production-load-readiness`, plus the tag `archive/pg-replica-design`. #40 becomes a draft. Branches are counted as absorbed by content (`git diff --quiet`, patch-id, or the merged-PR status), not by `git branch -r --merged`. `origin/loadtest-results` is kept. | Less movement, and the review history stays. Branches were merged by squash, so `--merged` lists none of them. `loadtest.yml` publishes into `origin/loadtest-results`. |
| PDP "without pgx" (A) and the PostgreSQL mode (C) | depguard forbids `pgx`, `core/db` and `dbos` in `core/pdp`, `core/applier`, `core/decide` and `core/policy`. PostgreSQL access goes through the `ReadModel` and `LogSource` ports, implemented in `pgstore`. The binary `licetum-pdp` links `pgstore`. In CQL mode, OPS05 checks that PostgreSQL is not reached. | One binary serves both modes. Isolation is checked by behaviour, not by the dependency graph. |
| Reference to the service in the AuthZEN path: id or slug | Slug only. A slug cannot change after creation and is never reused. | The identifier of a PDP must be stable and unambiguous. Today a slug can change (`ServiceUpdate.Slug`, `core/model/service.go`), and `uq_service_slug` covers only rows `WHERE deleted_at IS NULL`, so the slug of a deleted service can be taken again. |
| Last owner of a service | The last `service_owner` binding can be removed only by a `platform_admin`. | Cheap, and it leaves no orphaned services. |

### 2.3 Statements of the first draft corrected by the review

1. **"Nothing has been released, so the migrations fold into a new `00001_init.sql`."**
   - Wrong: `v0.1.0` is released, and its `00001_init.sql` equals the current one. On a database installed from it, goose would find version 1 already applied and silently skip the new `00001`.
   - Schema v2 therefore uses its own goose version table, and `migrate` refuses a `v0.1.x` database with a clear message.
   - The release notes say that `v0.1.0` does not upgrade to v2 and that the public API of `v0.1.0` (`/permission`, `/check`, `on_behalf`) is removed.
2. **"The `previous-release` job is a no-op until the first tag."**
   - Wrong: the job finds `v0.1.0` and runs that release's integration tests against the new migrations, so it would fail from P3 on.
   - It is removed deliberately in P3, because there is no upgrade path.
3. **"Absorbed branches are found with `git branch -r --merged`."**
   - This does not work after squash merges.
   - Absorption is checked by content (see the branch row in 2.2).
4. **"Publish is enqueued in the same transaction as each commit, including synchronization commits."**
   - Impossible: DBOS refuses `WithEnqueueTransaction` inside a workflow.
   - A workflow per revision would also add a statement to every admin write. With 1 s ticks, `Heartbeat` and `ProjectTick` as workflows would come to about 170,000 workflows a day.
   - Replaced by the single publisher leader woken by `pg_notify`, with a periodic tick.
   - The review also stated that DBOS workflows are never deleted. That is not accurate: `dbosPrune` (`core/engine/sync_dbos.go`) deletes completed synchronization workflows older than max(1 h, 2 × `SYNC__DEMAND_WINDOW`), 500 per pass. Any new DBOS queue needs the same pruning.
5. **"The REV01 budget includes a 100 ms queue poll."**
   - DBOS polls a queue every 1 s by default (`DefaultBasePollingInterval`, `dbos/internal/models/queue.go:12`). On polling errors it backs off to 120 s (`_DEFAULT_MAX_POLLING_INTERVAL`, `dbos/queue.go:17`).
   - Publication no longer goes through a queue.
   - Any latency budget that does depend on a queue must set `WithQueueBasePollingInterval` explicitly and fix it with a contract.
6. **"The check path writes `known_subject` on a cache miss."**
   - Imprecise: while `SUBJECTS__ENABLED` is on (it is off by default), `NoteSubject` runs after every token verification, at most once per ten minutes per subject and process, and is buffered.
   - `delegated_call` is written on every delegated call.
   - Contract OPS06 lists every path, not only these.
7. **"`get_resource` and refresh ignore the service in the path."**
   - Imprecise: the resource list, refresh and `GET /type_resource` have no service in their path at all (`admin-api/api/admin.go:267,296,314`).
   - `get_resource` is under `/service/{service_id}/…` (`:332`).
   - P4 lists these routes explicitly.
8. **"Repair is needed for durability and does not affect correctness."**
   - Withdrawn (see 2.2, deleting rows).
9. **"`freeze` serves the last P."**
   - This conflicts with the MVCC GC: after two newer versions of a key, the row at a pinned P is gone.
   - `freeze` disables only the staleness gate, and the position keeps advancing while the log arrives.
10. **"Two Deployments, `sync` and `project`", next to "a StatefulSet with a stable `DBOS__VMID`".**
    - Resolved as two StatefulSets.
11. **"Publish runs with concurrency 1", next to "several positions in flight".**
    - Resolved by the single leader.
    - The leader publishes positions strictly in order, one at a time (4.9.2). The applier still checks continuity, so a former leader that is still writing does no harm.
12. **"depguard keeps pgx out of `core/pdp`", next to a PostgreSQL mode in which the PDP polls `revision_state` and folds the outbox.**
    - Resolved by the `LogSource` port, implemented in `pgstore`.
13. **"PostgreSQL failover within S", next to "a synchronous standby is only recommended".**
    - With an asynchronous standby, `epoch_start` can take longer than S and close every region.
    - Either the standby is synchronous, or S is derived from the measured failover time.
14. **"Search pages are served at the P of the first page, with policy versions kept for `MVCC__RETENTION`."**
    - Not possible in the PostgreSQL mode, where the tables have no versions, and a later page may land on a pod that does not hold policy at that P.
    - Each page is computed at the current position.
    - Ordering by key keeps the "no skips, no repeats" property, which is all the specification requires.
15. **Phase order.**
    - P2: step 2d (admin OIDC, with the seed and `restore-admin` off `PLATFORM__*`) moves before 2c, `subject_directory` moves into P2, and 2e becomes its own PR.
    - P3: the outbox check covers only the policy tables. Platform self-synchronizers, the inline `resolve()` and the synchronization worker still write resource tables until P4 and P5.
    - P1: contracts and `refeval` use external terms from the first day.
    - P0.5: a CQL spike before the outbox and payload formats are frozen in P3. It measures cold misses at LOCAL_QUORUM, applier throughput, the payload of a type with 1M resources and range-tombstone behaviour, and settles the license and the driver.
    - P1: the fail-closed fix is measured on the loadtest medium profile before it merges.
    - P6: starts append-only.
    - OPS02 and POL05 are marked `planned` until P7.

## 3. Architecture

PostgreSQL is the source of truth in every topology. Every write that changes something a decision reads also commits, in the same transaction, one `outbox` row. That row carries the next value of a single global revision. A region decides from a read model of that state, reached through the port `ReadModel` (`core/readmodel`). The port has two adapters:

- `postgres` is the default. The PDP reads the canonical tables, either on the primary or on a streaming replica in its own region, and follows the outbox to keep its copy of the policy current.
- `cql`: the PDP reads a regional copy, which an applier in the same region builds from a log replicated by a CQL store. The store is ScyllaDB or Apache Cassandra 5. Phase P0.5 chooses between them after a spike and after the license question is settled: from 2025.1 ScyllaDB is source-available, free up to 50 vCPU and 10 TB per organization, and its last AGPL release is 6.2. Whichever is chosen, the CQL model stays Cassandra-compatible: no lightweight transactions, no materialized views, no secondary indexes, and nothing that needs tablets.

Both adapters answer the same question: what was the state at position P. The shared `storetest` suite and the differential contract REF01 keep their answers equal. Multi-region works on either adapter. Streaming replicas give every region a consistent snapshot without a CQL store. The CQL store is for installations whose regions must not hold PostgreSQL credentials or receive its WAL.

The consistency section defines these terms exactly; this section uses them as follows:
- P is the revision a PDP's view has reached.
- T(P) is a PostgreSQL time such that every commit made before it is reflected at P.
- S is `POLICY__MAX_STALENESS`. It defaults to 60 s, and `0` is refused. Today the default is `0`, which means no limit (`core/config/config.go:314`).

Compared with today, the architecture drops:
- the `check` process, which routes its reads to a replica (`DB__READ_URI`, `App.reader`) and writes to the primary from the request path: `ensure` queues synchronization jobs, `delegated_call` rows are written for every delegated call (at once in the strict audit mode, buffered in the async one), and `NoteSubject` records subjects;
- the table synchronization engine (`sync_job`);
- Redis in every process;
- the platform self-service that synchronizes Licetum's own tables.

### 3.1 Processes

| Process | Module, license | Where | Stores | What it does |
|---|---|---|---|---|
| `licetum-pdp` (`core/cmd/licetum-pdp`) | `core`, MIT | Every region. Stateless, scaled on CPU and on requests in flight. | `postgres`: the canonical tables, as the role `licetum_pdp` (SELECT only), on the primary or on the region's replica. `cql`: the keyspaces of its own DC only. | Serves AuthZEN only, on the public port `PDP__ADDR` (:8001). `/healthz`, `/readyz` and `/metrics` are on the ops port `PDP__OPS_ADDR` (:9090). Writes to no store. |
| `licetum-admin` (`admin-api/cmd/licetum-admin`: `serve`, `bootstrap`, `restore-admin`, `openapi`) | `admin-api`, AGPL-3.0-only | Home region, 2 or more replicas. | The PostgreSQL primary. | The write API. It verifies the administrator's OIDC token, evaluates role bindings by scope, and makes each write one PostgreSQL transaction with its outbox row. The admin tools (simulate, who-has-access, preview, lint, policy tests) run `core/decide` over `pgstore`. |
| `licetum-writer run --role sync` (`core/cmd/licetum-writer`) | `core`, MIT | Home region, as a StatefulSet with `DBOS__VMID` set to the pod name. | The primary: `DB__SYNC_POOL_SIZE` connections plus the DBOS pool. The `dbos` schema is in the same database. | Runs DBOS workflows on the queue `licetum-sync`: `SyncSchedule`, `SyncType`, `SyncOne` (which replaces the inline `resolve()` of the admin API) and `RebuildComponent`. Also runs the janitor that takes over workflows of executors removed by a scale-down, and deletes finished workflows (`dbos.DeleteWorkflows`). |
| `licetum-writer run --role project` | `core`, MIT | Home region, 2 replicas, one of them the leader. | The primary, through one direct session. With `cql`, also `licetum_log`. | The leader publishes the outbox to the log (`cql`), writes the heartbeat, takes the per-stream checkpoints (`cql`), and removes old outbox rows. It also fetches the JWKS of the PEP issuers and stores key sets that changed (`IssuerKeys`). It runs no DBOS workflow. |
| `licetum-writer fetch --region <r>` | `core`, MIT | Optional, in region `r`. | None. | When the home `SyncType` asks over mTLS, runs the SQL or HTTP adapter of a synchronizer whose `regions` name `r` and returns the normalized answer. Holds no credentials for PostgreSQL or the CQL store. |
| `licetum-writer apply --dc <dc>` | `core`, MIT | Every region with the `cql` store. 2 replicas; a Kubernetes Lease picks the active one. | Reads `licetum_log`. Writes `licetum_r_<dc>` and its generation keyspaces. No PostgreSQL. | The regional applier of the log. Two active replicas would write identical rows, so the Lease only saves work; correctness does not depend on it. |
| `licetum-writer migrate`, `rebuild`, `reconcile`, `checkpoint`, `failover`, `freeze`, `unfreeze` | `core`, MIT | Job, CronJob or run by an operator. | PostgreSQL and the CQL store; `freeze` and `unfreeze` only the region's own keyspace (5.4). | `migrate` runs the goose migrations of schema v2 (with their own version table; it refuses a v0.1.x database), then the CQL migrations, all under one PostgreSQL advisory lock. `reconcile` is the nightly comparison of every region with PostgreSQL. `rebuild`, `checkpoint` and `failover` are the procedures in the consistency section. `freeze` and `unfreeze` are the break-glass switch of a region. |
| `web` | `web`, AGPL-3.0-only | Home region, optionally others. | None. | The Next.js BFF and OIDC client of the admin IdP (code flow with PKCE; code exchange and refresh happen on the server). It talks only to `licetum-admin`. |

- **Images.** There are three images, as today: `core` contains both `licetum-pdp` and `licetum-writer`, `admin-api` contains `licetum-admin`, and `web` contains the interface.
- **No Redis.** The PEP keys, the verified PEP tokens, the answer cache and the row cache are kept in the PDP's memory. The PKCE `code_verifier`, the `state` and the `nonce` of an administrator's sign-in are kept in a short-lived encrypted cookie of the BFF. Today they are in Redis under `authz:pkce:` (`core/engine/oauth.go:35`).
- **Isolation by ports.**
  - The PDP reaches storage only through `ReadModel` and `LogSource` (the stream of changes after a position), both in `core/readmodel`.
  - `pgstore` implements them for `postgres`, `cqlstore` for `cql`, and `memstore` for tests.
  - depguard forbids `pgx`, `core/db` and DBOS in `core/pdp`, `core/applier`, `core/decide` and `core/policy`.
  - The `licetum-pdp` binary links `pgstore` for the PostgreSQL mode. With `cql`, contract OPS05 checks the running pod rather than the import graph: no PostgreSQL credentials, no connection to port 5432, and no connection to another DC.
- **Why the writer has two roles.** A first synchronization of a type with a million resources, or a storm of closure rebuilds, must not delay publication or the heartbeat, because those two bound the staleness of every region.
  - `sync` runs DBOS workflows. DBOS Go v1.6.0 recovers only the workflows of its own executor. That is why `sync` is a StatefulSet with a stable `DBOS__VMID`, and why a janitor is needed for executors removed by a scale-down.
  - `project` runs no workflow. Its replicas elect a leader with a session-level advisory lock, held on the same connection that runs `LISTEN`. Neither the lock nor `LISTEN` survives PgBouncer transaction pooling, so this one connection goes to PostgreSQL directly.
  - A leader that lost its lock but still writes to the log does no harm: every CQL write is deterministic, with the same bytes under the same key and the same `USING TIMESTAMP`.
- **Who writes what.**
  - `licetum-admin` writes the policy, the bindings, the PEP clients and credentials, the subject directory and the log settings. When it creates or deletes a synchronizer, it also clears `synced_at` of the type and resets its `sync_state` (4.2).
  - `licetum-writer` writes:
    - the resource graph (`resource_cache`, `resource_edge`, `resource_ancestor`, `resource_unresolved`);
    - the effective columns of `type_resource_parent` (`RebuildComponent`) and `type_resource.synced_at` (the first applied synchronization, 4.2);
    - `sync_state`;
    - the issuer key sets;
    - `publish_state`;
    - the heartbeat row;
    - everything in the CQL store.
  - The PDP writes nothing: no synchronization job, no audit row, no subject note.

### 3.2 Topologies and regions

| Topology | `READMODEL__STORE` | Home region | Each other region |
|---|---|---|---|
| One region (default) | `postgres` | PostgreSQL (HA as the operator chooses), `pdp ×n`, `admin ×2`, `writer` (`sync`, `project`), `web`. | — |
| Regions on PostgreSQL replicas | `postgres` | A fenced PostgreSQL primary with a synchronous standby (or S derived from the measured failover time, 6.8), plus the same processes as above. | An asynchronous streaming replica (hot standby, cascading allowed) and `pdp ×n` reading it as `licetum_pdp`. |
| Regions on the CQL store | `cql` | A fenced PostgreSQL primary with a synchronous standby (or S derived from the measured failover time, 6.8), `admin`, `writer` (`sync`, `project`), `apply --dc home`, `pdp ×n`, `web`, and the CQL DC `home`. | A CQL DC, `apply --dc <dc>` and `pdp ×n`. No PostgreSQL credentials. |

- **Common to all topologies.** `licetum-admin`, `licetum-writer run`, the migration Job and usually `web` run only in the home region. A PDP reads only storage in its own region. Nothing needs Redis.
- **Sources reachable from one region only.**
  - `licetum-writer fetch --region <r>` runs in that region and fetches for the home writer. The transaction that applies the answer still runs in the home region, next to the primary.
  - This replaces the earlier idea of a DBOS executor in region `r` working against the primary over the WAN. That design would put PostgreSQL credentials in the region and send DBOS steps, queue polling and `LISTEN` across the long link.
- **Regions on PostgreSQL replicas.** Each region runs a streaming replica of the primary, and its PDPs use that replica as their only database.
  - There is no routing to the primary and no lag action. The current `DB__READ_URI`, `DB__MAX_REPLICA_LAG`, `DB__LAG_ACTION` and `App.reader` are removed in P2e.
  - The replica's `REPEATABLE READ` snapshot is a consistent prefix of the primary's history (see 3.7). The guarantees are therefore the same as on the CQL store: one position per decision, and fail-closed after S.
  - The costs:
    - every region receives the whole WAL of the database (the resource graph, the outbox and its chunks, `change_log`, the `dbos` schema), not just what a decision reads;
    - each region holds the credentials of a read-only role;
    - promotions and timeline changes are PostgreSQL operations that the operator runs;
    - the number of replicas is limited by how many streams the primary can serve, though cascading helps.
- **Regions on the CQL store.**
  - **Replication.** `licetum_log` is replicated with RF 3 to every DC. `licetum_r_<dc>` and its generation keyspaces `licetum_r_<dc>_g<gen>` are replicated only within their own DC (`{<dc>: 3}`), so each DC needs at least three nodes. The cluster runs outside the Helm chart, under an operator.
  - **Generation keyspaces.** The migration Job creates them in advance, from the home region. Creating a keyspace is a schema change that needs schema agreement across the whole cluster, which a cut-off region cannot reach. The applier therefore only writes.
  - **Freshness.** T(P) is `region_state.fresh_pg_time`. Every second the leader runs the heartbeat statement of 4.9.3, which reads `statement_timestamp()`, the epoch and the revision in one snapshot, and writes them to `log_heartbeat`. The applier moves `fresh_pg_time` forward once it has applied that revision.
  - **MVCC.** The newest version of a key is never deleted. P6 starts append-only, with manual rotation of generations. The garbage collection of superseded versions is switched on only after the APP and ASOF contracts pass under kill tests.
  - **Repair is required for correctness.** This means repair itself, or repair-based node operations after a node replacement. If two of the three replicas in a DC lose the newest version of a key, a `LOCAL_QUORUM` read at P returns an older version, for example a resource that is still active or an old lineage. That is fail-open. The nightly `reconcile` of every region is mandatory.
  - **Freeze.** `freeze --dc X` writes `region_freeze` and `freeze_log` into the region's keyspace, using an operator CQL role of its own. It disables only the staleness gate; the applier keeps applying the log as it arrives.
- **Home region in multi-region.**
  - Fencing of the old primary is required. S must exceed the measured failover time (FO01): a synchronous standby keeps it short; with an asynchronous one, S is derived from the failover time measured with it. An asynchronous standby can lose commits that were already published, and a failover that lasts longer than S closes every region.
  - Clocks are synchronized everywhere. The skew ε comes from the clock synchronization metrics (`node_timex_*`), not from the heartbeat, because the heartbeat's age mixes skew and lag.
- **Cell** (D4, for example China): a separate installation with its own PostgreSQL and its own read model. Policy moves between cells only as an exported and imported policy document.
- **Upgrade order:**
  1. PostgreSQL migrations.
  2. CQL migrations.
  3. `apply` and `pdp`, which read both formats N and N+1.
  4. `writer`.
  5. `admin`.
  6. `web`.

  With `postgres`, the CQL steps are skipped.

### 3.3 Diagrams

Regions on the CQL store:

```
                      +==================================== home region ===================================+
 admin's browser -----| web (BFF, OIDC client) ---> licetum-admin x2                                       |
                      |                              |  one PG transaction: lock(admin_write),             |
                      |                              |  authz, write, change_log, outbox                   |
                      |                              v  (CTE bump), NOTIFY                                 |
 sources (SQL/HTTP) <-| writer[sync] (DBOS) ----> PostgreSQL primary ==sync==> standby (fenced)            |
                      |   SyncType: fetch -> apply,  |  NOTIFY on commit;                                  |
                      |   closure, outbox_chunk,     |  outbox WHERE revision > last                       |
                      |   CTE bump, NOTIFY           v                                                     |
                      | writer[project], one leader: publish, heartbeat (pg_time), checkpoints             |
                      |                              |  LOCAL_QUORUM, USING TIMESTAMP(position)            |
                      |                              v                                                     |
                      | CQL DC home:                 licetum_log --> apply --dc home --> licetum_r_home*   |
                      |                              |                 reads at P, LOCAL_QUORUM ^          |
                      | PEP --AuthZEN--> licetum-pdp xN ----------------------------------------+          |
                      +==============================|=====================================================+
                                                     |  asynchronous replication of licetum_log, RF 3 in every DC
                      +==============================v===== region B: no PostgreSQL credentials ===========+
                      | CQL DC b:                    licetum_log --> apply --dc b --> licetum_r_b*         |
                      |                                           reads at P, LOCAL_QUORUM ^               |
                      | PEP --AuthZEN--> licetum-pdp xN -----------------------------------+               |
 sources of B <-------| writer fetch --region b <-- mTLS -- writer[sync] of the home region                |
                      +====================================================================================+
```

One region, and regions on PostgreSQL replicas:

```
 +=== one region =======================================================+
 | web ---> licetum-admin x2 ----------+                                |
 | writer[sync], writer[project] ------+--> PostgreSQL primary (HA)     |
 |                                          ^                           |
 | PEP --AuthZEN--> licetum-pdp xN ---------+ SELECT as licetum_pdp,    |
 |                                            revision poll 100 ms      |
 +======================================================================+

 +=== home region ==============================+                +=== region B =============================+
 | web ---> licetum-admin x2                    |                |                                          |
 | writer[sync], writer[project]                |                |                                          |
 |   ---> PostgreSQL primary ==sync==> standby  |                |                                          |
 |          ^      |                  (fenced)  |                |                                          |
 |          |      +---------------------------------- WAL ------> PostgreSQL hot standby (asynchronous,    |
 |          | SELECT as licetum_pdp,            |                |      ^          cascading allowed)       |
 |          | revision poll 100 ms              |                |      | SELECT as licetum_pdp,            |
 | PEP --AuthZEN--> licetum-pdp xN              |                |      | revision poll 100 ms              |
 |   (on the primary or a local replica)        |                | PEP --AuthZEN--> licetum-pdp xN          |
 +==============================================+                +==========================================+
```

### 3.4 Write flow (example: revoking a rule of the service `orders`)

1. The BFF sends `PATCH /service/orders/rules/{id}` with the administrator's access token, which it keeps in its encrypted session cookie.
2. `licetum-admin` verifies the token: `iss`, a mandatory `aud` equal to `ADMIN_AUTH__AUDIENCE`, and the signature against the admin IdP's JWKS cached in memory. It then loads the caller's role bindings. If the caller has no `policy_editor` role or higher on `orders`, the answer is 403, before the body is parsed. The `withPolicy`/`platformAuth` mechanism stays; only the policies change.
3. Everything happens in one PostgreSQL transaction, `core/db.PolicyTx`:
   1. `BEGIN` (READ COMMITTED), then one `pgx.Batch` with `pg_advisory_xact_lock(admin_write)` and a second read of the caller's bindings. A binding revoked in the meantime is seen here.
   2. Validation and the write. The write returns `change{entity, id, service_id, action, before, after, streams}` with **full** canonical images. These are built by `core/outbox.Project`, the same function the checkpoints, `rebuild` and `reconcile` use.
   3. The invariants, checked under the lock:
      - the last `platform_admin`;
      - the last `service_owner`;
      - cycles in the type graph;
      - `blocked_by`;
      - unique names;
      - links to exported types only within one identity domain.

      The lock removes the write skew these checks allow today.
   4. The final batch takes the revision in its last statement:
      ```sql
      WITH r AS (UPDATE revision_state SET revision = revision + 1 WHERE scope = 'global'
                 RETURNING epoch, revision)
      INSERT INTO outbox (...) SELECT ... FROM r RETURNING epoch, revision
      ```
      The same batch inserts the `change_log` rows stamped with that revision and sends `pg_notify` with the revision. The lock on the `revision_state` row is held until COMMIT. As a result, revisions have no gaps (a rollback uses up no number) and follow the commit order.
   5. `COMMIT`. A deferred constraint trigger refuses the commit if the transaction changed rows of a table the check covers (4.7) and no `outbox` row carries its `xid`.
      - Statement-level triggers mark the transaction only when a statement actually changed tuples (4.7), so a statement that changed nothing does not count. An `AFTER TRUNCATE` trigger covers `TRUNCATE`.
      - A table owner or superuser can bypass it (a disabled or dropped trigger, a restore with triggers off). `session_replication_role = replica` alone does not, because the triggers are `ENABLE ALWAYS`. `reconcile` finds the result; the schema does not claim to prevent it.
      - PostgreSQL delivers the NOTIFY only if the transaction commits.
4. The response is 200 with `Licetum-Revision: <epoch>.<r>`.
5. Propagation depends on the read model:
   - **`postgres`.** Every PDP polls `revision_state` every 100 ms on its own server: the primary, or its replica once the WAL has been replayed. It reads the new outbox rows and folds them into its view.
   - **`cql`.**
     1. The NOTIFY wakes the `project` leader. A 1 s tick covers a notification lost while the leader was reconnecting.
     2. The leader reads `outbox WHERE revision > last_published` in revision order. For each position it writes the `log_chunk` rows and then the `log_entry`, with `USING TIMESTAMP` derived from the position.
     3. It advances `publish_state` to each position once its chunks and entry are written, one position at a time (4.9.2). If a `log_entry` already exists and differs from the outbox row (its header, or the sha256 of a stream), that is a divergence: publication stops and the epoch procedure starts.
     4. The CQL store replicates the log to every DC. Each applier materializes the entry and advances `region_state`.
     5. Each PDP polls `region_state` every 100 ms, pulls the deltas of its streams, swaps its view atomically, and drops the cached answers the change affects.

   No queue sits on this path. REV01 bounds it at p99 ≤ 1 s in the home region, and REV02 at p99 ≤ 5 s elsewhere.

Some writes differ from an ordinary policy write:

- **Role bindings** are not projected. They still take a revision and an outbox row with `streams = {}`, so revisions stay gap-free and the history stays ordered.
- **The subject directory** (`PUT /service/{ref}/subjects`, in batches of 1000) checks no invariant, so it takes only the revision row and not `admin_write`.
  - It is written by the role `directory_writer`, held by a machine client of the admin IdP, under a rate limit.
  - A bulk upload therefore neither serializes the admin writes nor can change the policy.
- The round-trip budget of a write (WR06) is measured, not assumed. The lock and the bindings travel in one batch; the bump, the `change_log` rows and the NOTIFY travel in the final one.
- In P3 the deferred check covers only the policy tables. The resource tables join it in P5, once the last writers outside `licetum-writer` (the platform self-synchronizers and the inline `resolve()`) are gone.

### 3.5 Synchronization flow

1. **Triggers.** All of them run inside `licetum-writer`. No AuthZEN request starts a synchronization or waits for one.
   - `SyncSchedule` queues each synchronized type at its `synchronizer.refresh_interval`. The default is in minutes (between 5 and 15, fixed in P5 against the source-load metric). A source that keeps answering without changes is asked less often.
   - "Refresh now" in the admin API (`Fresh`, at most once per `SYNC__FRESH_MIN_INTERVAL` per type) returns the revision of the synchronization once it has been applied.
   - Creating a synchronizer queues its first synchronization.
   - A structural change (a parent link or its `inherit`) changes only the desired link, and the admin write answers `{revision, effective: false, job}`. `RebuildComponent` then switches the effective links and the closure in one transaction.
   - Deleting a synchronizer switches its type from `sync` to `not_sync` in the admin's own transaction: it clears `synced_at` and resets the type's `sync_state`, together with its outbox row (4.2). A type is `sync` while `synced_at IS NOT NULL` and a live synchronizer exists.
2. **Fetch.** `SyncType` asks the source. It is deduplicated per type; the `:fresh` variant is deduplicated separately.
   - The SQL or HTTP adapter does the fetch, through `licetum-writer fetch` when the synchronizer names `regions`. Then come `NormalizeResources` and `dataDigest`.
   - A resource reported with `*` as its external id is dropped and counted, because `*` means the whole type in AuthZEN.
   - If the digest, the graph signature and the number of active resources are all unchanged, nothing is written and no revision is used.
3. **Apply.** This is one PostgreSQL transaction on the sync pool, run by `dbos.RunAsTransaction`:
   1. Set `lock_timeout` to 5 s and `statement_timeout`, then take `pg_advisory_xact_lock` on every key of `ComponentKeys`.
   2. Take the fences: the synchronizer row and the workflow's row are read `FOR SHARE` (4.9.1), so an executor that has lost the workflow, or a fetch made before the synchronizer changed, cannot commit. They come before the revision row, which is taken last (4.5); today the fence runs last (`settle`, `core/engine/sync_worker.go:412`).
   3. Run `applyType`, `syncEdges`, and `rebuildClosure` together with `resource_unresolved`.
   4. Copy the full state of every affected resource into `outbox_chunk` (COPY, zstd, at most 1 MiB per chunk). Affected resources are the changed ones, their descendants, and descendants whose `unresolved_up` changed.
   5. Run `markApplied` in `sync_state`.
   6. As the last statements, run the CTE bump with the outbox header, and `pg_notify`.

   If `applyType` reports that no row changed, the transaction skips the payload and the bump. It changed no tuple of a projected table, so the deferred check lets it commit without an outbox row and no revision is used. Today the same case skips `bumpDataRevisions` (`core/engine/sync_worker.go:563-580`).
4. **Failures.**
   - A source that does not answer leaves the data as it is. `sync_state` records `attempts + 1`, `last_error`, and `next_due_at = now() + min(30 s · 2^attempts, 10 min)`; the backoff applies across the whole cluster.
   - A component lock held by another transaction defers the job by 5 s, and does not count as a failure.
5. **Lock order.** A synchronization takes the component advisory locks, then its entity rows, then the revision row. An admin transaction takes `admin_write`, then its entity rows, then the revision row, and never takes a component lock. The rows both can touch (`type_resource_parent`, `synchronizer`, `type_resource`, `sync_state`) are taken in one fixed order (4.5). There is no cycle, so there is no deadlock.
6. **Publication.**
   - DBOS Go v1.6.0 refuses `WithEnqueueTransaction` inside a workflow (`dbos/workflow.go:2126` in `dbos-transact-golang` v1.6.0). The apply step runs inside `SyncType`, `SyncOne` or `RebuildComponent`, so a synchronization could not enqueue a publication in its own transaction anyway.
   - It does not need to. The NOTIFY in its transaction wakes the `project` leader just as an admin write does, and the leader's tick covers a lost notification.
7. **Revocation through data** (a resource switched off or moved) takes the publication path of the write flow plus the type's polling interval: `refresh_interval`, or up to `refresh_interval_max` for a quiet source (4.9.1). After "refresh now", it takes only the publication path.

### 3.6 Decision flow (`POST /service/orders/access/v1/evaluation`)

1. **Admission.** Three limits apply: `HTTP__MAX_INFLIGHT` for the pod, a concurrency quota per PEP client, and a separate pool for search. One client or one search therefore cannot take the slots of the others.
2. **Authentication of the PEP.** The PEP presents one of two credentials:
   - a client-credentials JWT, checked for `iss` among the `pep_issuer` rows, `aud` containing `PDP__AUDIENCE`, `exp`/`nbf` with 60 s leeway, and `alg` from the issuer's allowlist;
   - an API key (`lct_<id>_<secret>`), whose sha256 is compared in constant time.

   The keys come from the `pep` stream of the view, so the PDP never calls an IdP.
3. **Authorization of the PEP.**
   - `orders` is looked up by slug in the `catalog` stream. A slug is immutable after creation and never reused, so `PDP__PUBLIC_URL + "/service/" + slug` is a stable PDP identifier.
   - The client must be a `pep_client` of `orders` with the operation `evaluate`. An unknown service, an unknown client and a missing operation all give the same `403 authzen_forbidden`.
   - The optional token bucket (`PEP__RATE_LIMIT`) answers 429 with `Retry-After`. It counts per pod; a global limit belongs to the ingress.
4. **Validation.**
   - A missing `subject.type`, `subject.id`, `resource.type`, `resource.id` or `action.name` is a 400.
   - `resource.id = "*"` asks about the type as a whole.
   - `context.licetum` (`min_revision`, `explain`) is removed from the request attributes. In `/evaluations` it is read only from the top-level `context`.
5. **Staleness gate.** This comes before any cache. When `now − T(P) > S` and the region is not frozen, the answer is `503 policy_unavailable` with `Retry-After`.
6. **Read-your-writes.** When `min_revision` is above P, the request waits for the view to reach it, for at most `min(PDP__FRESHNESS_WAIT, the request deadline)`. If the view does not get there in time, the answer is `503 revision_not_reached`. An answer below a requested revision is never computed and never cached.
7. **Prepare.** The pure function `decide.Prepare(policy@P, service, subject, env)` computes:
   - the identity set: the subject and the groups it belongs to in every service of U(orders), since an upstream rule may name a group of its own service;
   - the applicable rules, with roles expanded;
   - the TTL and the hold;
   - the attributes the rules read.

   The rules come from U(orders): `orders` itself and the services that own the exported ancestor types its types link to. All of them are in the identity domain of `orders`, because links across domains are refused.
8. **Answer cache.** The L1 key contains:
   - the service;
   - the positions of `catalog` and of `svc:x` for every x in U(orders);
   - the subject's type and id;
   - the fingerprint of the attributes read;
   - the digest of the question;
   - `explain`.

   The entry records the position of `data:<type>` for every type the decision read, including ancestor types owned by other services, and is served only while those positions are unchanged (5.12).
9. **On a cache miss, reads at P.**
   - **`cql`.** Rows come from the row cache, keyed `(key, ver)` because a version never changes. Its "newest version ≤ P" index follows `applied_keys`, so a warm decision reads nothing from the store. A missing row is read with `SELECT … WHERE pk AND ver <= P LIMIT 1` at `LOCAL_QUORUM`.
   - **`postgres`.** Rows are read in one `REPEATABLE READ READ ONLY` transaction at its own revision Q, as described in 3.7.
   - Then come the pure `CoversOf` (the lineage plus the transitive `unresolved_up`, failing closed) and `decide`.
   - An unknown type or resource gives `decision: false`, not an error.
   - The explanation is added when it was asked for. A client without the operation `explain` that asks for it gets 403 `authzen_forbidden` (7.7, AZ12). Rules of an upstream service appear in the explanation only as `upstream: <slug>` with the level.
10. **Response.** The response is `{decision, context?}` with the header `Licetum-Revision`. One decision-log line records the client, subject, resource, action, decision, P and request id.

The other endpoints follow the same steps, with these differences:

- `/evaluations` decides all its items at one P. Short-circuit semantics are lazy: nothing after the stopping item is computed. Items with the same `(subject, properties, context)` share one `Prepare`.
- A search page is computed at the current P of the PDP that serves it.
  - The cursor is `(bucket, last key, request digest)`, not a position. Pages of one search may therefore come from different positions and different pods.
  - A key that stays in the result throughout is returned exactly once.
  - A changed request gives `400 authzen_page_invalid`. There is no expired page.

### 3.7 PostgreSQL mode

With `READMODEL__STORE=postgres`, the PDP reads PostgreSQL through `pgstore`, as the role `licetum_pdp`.
- It has SELECT on the tables a decision reads, and on `revision_state`, `outbox` and the heartbeat row.
- It has nothing on the bindings or the `dbos` schema. Column privileges keep `synchronizer.config` out of its reach.

1. **Load.** One `REPEATABLE READ READ ONLY` transaction reads, in a single snapshot:
   - the policy streams the PDP serves: `catalog`, `pep`, `settings`, and `svc:<id>` for its services (`PDP__SERVICES`, all by default) and their upstreams;
   - `revision_state`, which gives the epoch and P;
   - T(P).

   Today a snapshot is labeled with a revision read before its transaction, outside it (`core/engine/localstate.go:299` passes that revision to `loadSnapshot`). Here the revision is read inside the snapshot.
2. **Follow.**
   - Every 100 ms the PDP reads `revision_state` through `LogSource`.
   - When the revision has moved, it reads the outbox rows after P in one snapshot and folds them with `policy.Fold`, the same function the applier uses. The new view replaces the old one atomically.
   - A data row only advances the position of its `data:<type>` stream. Since data are read from the tables, that is enough to invalidate the answers and cached rows of that type.
   - The PDP polls even on the primary, because a hot standby refuses `LISTEN`.
3. **Reload instead of fold.** The PDP reloads its view from a fresh snapshot, and drops its answer cache, in two cases:
   - the epoch changed (a failover);
   - the outbox rows after P have already been removed.

   A revision below P within the same epoch (a pooled address that spans standbys, or a store fault) is not loaded, because a view never goes back (CONS02). The PDP keeps its view without confirming it and counts the read in `licetum_revision_regressions_total`; if the fault persists, T(P) stops moving and the PDP refuses requests after S (5.11).
4. **Data at the decision's position.**
   - On a cache miss, the resource rows (`resource_cache`, `resource_ancestor`, `resource_unresolved`, `subject_directory`) are read in one `REPEATABLE READ READ ONLY` transaction, which also reads its own revision Q.
   - The decision then runs at Q, using:
     - the policy at Q, folded first when Q is above the view. Q below the view happens only behind a pooled address that spans standbys; the read is then retried for at most `PDP__FRESHNESS_WAIT` and refused with 503 (5.9);
     - the rows read at Q;
     - cached rows whose `data:<type>` position did not move up to Q.
   - Policy and data always come from one position.
5. **T(P).**
   - **On the primary**, T(P) is the `statement_timestamp()` of the latest poll that read P from `revision_state`, once the view has folded up to that revision (6.2), or the `transaction_timestamp()` of the load that read P. Every commit acknowledged before that moment is visible to that statement or snapshot. The 100 ms poll keeps T(P) moving while nothing is written.
   - **On a replica** (`pg_is_in_recovery()`), T(P) is the `pg_time` of the heartbeat row read in the same snapshot.
     - The `project` leader commits this row on the primary every second.
     - Any commit acknowledged before that `pg_time` has its commit record earlier in the WAL. A snapshot that sees the heartbeat therefore also sees that commit.
     - The row belongs to no projected table and takes no revision.
     - `pg_last_xact_replay_timestamp()` is not used, because it stops moving when nothing is written.
6. **Why a replica gives a consistent snapshot.**
   - A transaction that takes a revision holds the lock on the `revision_state` row until it commits. The commit records of such transactions therefore appear in the WAL in revision order.
   - A hot standby makes transactions visible in the order it replays their commit records. So a snapshot on the replica holds the revisions of its epoch up to P with no gap, which is the state the primary had at P.
   - Every change to a projected table has its outbox row, so that state is everything a decision reads.
7. **Replica specifics.**
   - Replay can cancel a long snapshot on a standby (`canceling statement due to conflict with recovery`). To avoid this, set `hot_standby_feedback = on`, or set `max_standby_streaming_delay` above the time a policy load takes.
   - Lag shows up as staleness. Past S the region answers 503 and `/readyz` fails. `min_revision` waits for the replay for at most `PDP__FRESHNESS_WAIT`.
   - A replica may have replayed past the point where a promoted standby forked. This can happen to an asynchronous replica even when the home standby is synchronous. Such a replica cannot follow the new timeline: its heartbeat stops, and the region closes after S until the replica is rewound (`pg_rewind`) or cloned again.
8. **Freeze.**
   - A replica accepts no writes, so on PostgreSQL replicas `freeze` is a setting of the region's PDPs rather than a row. The setting is `PDP__FREEZE_REASON`; a non-empty value means frozen.
   - Each PDP logs it once a minute and exports `licetum_frozen`.
   - As with `cql`, freezing disables only the staleness gate. The PDP keeps following the replica.
9. **No publication.**
   - `publish_state` is not used.
   - The write path does not depend on the read model: the outbox rows and their chunks are written in both modes. In this mode the `project` leader removes them by age.
   - A PDP whose P is older than the oldest kept row reloads.
10. **Switching read models.**
    - Moving to the CQL store means running `licetum-writer rebuild --all`. It writes checkpoints of every stream from one snapshot, and the log continues from there. The switch has a contract of its own.
    - Moving back needs nothing in PostgreSQL: the PDPs change `READMODEL__STORE`.

## 4. Data model, outbox and the writer

PostgreSQL remains the source of truth. This section covers three things:

- the v2 schema;
- the rule that a transaction which changes anything a decision reads also writes an outbox row, checked at `COMMIT`;
- the process, `licetum-writer`, that keeps the resource graph current and turns the outbox into the read model.

How the read model is stored and read (the `postgres` adapter, the CQL adapter, the applier) is described in the read model section. Here it appears only where the writer depends on it.

### 4.1 Installing schema v2

- **v0.1.0 is released.** The tag `v0.1.0` (2026-10-12) ships the same `core/db/migrations/00001_init.sql` as the current one. `00002`–`00005` came after it. A new `00001` written into the same goose version table would be skipped on a v0.1.0 database, because version 1 is already applied there, and the schema would be left broken.
- **v2 is a separate chain.** It starts again at `00001_init.sql` and records its versions in its own table, `goose_db_version_v2`.
- **`migrate` refuses a v0.1.x database.** Before applying anything it looks for `goose_db_version`. If that table exists, `migrate` stops with a message that names the installed version and says v2 is installed into a new database. There is no upgrade path and no data migration. An empty database, or one that has only `goose_db_version_v2`, proceeds.
- `db.SchemaVersion` (`core/db/migrate.go:78`, which `/readyz` uses) reads the v2 table.
- The release notes say two things: v0.1.0 does not upgrade to v2, and the published `/permission`, `/check` and `on_behalf` endpoints are gone.
- The `previous-release` job of `.github/workflows/ci.yml` (line 153) runs the v0.1.0 integration tests against the new migrations. It is removed in P3 together with the old chain.
- After the landing, the CLAUDE.md rule applies again: a schema change is a new `0000N_name.sql` in the v2 chain, and an applied file is never rewritten.

### 4.2 Policy tables

A service owns everything under it, and composite foreign keys enforce this. A rule, role item, membership or guard that points at another service's type, group or role cannot be stored.

- **`identity_domain(id, name UNIQUE)`** (new). A namespace of subject identifiers: services in one domain agree that one `subject.id` is one subject. The migration inserts a `default` row. Only `platform_admin` manages domains.
- **`service(id, slug, name, url, icon, identity_domain_id, deleted_at, created_at)`**, with `UNIQUE (id, identity_domain_id)`.
  - The OAuth columns and `trusted_clients` are gone: P2c stops using them, and schema v2 does not have them (11.1).
  - `slug` is unique over **all** rows, deleted ones included. Today `uq_service_slug` covers only live rows (`WHERE deleted_at IS NULL`, `00001_init.sql:422`), and `PATCH` can change the slug (`core/model/service.go:90`).
  - A `BEFORE UPDATE` trigger refuses any change of `slug` or `identity_domain_id`.
  - Service rows are soft-deleted and never removed, so a slug is never reused.
- **`type_resource(id, service_id, identity_domain_id, name, access_implies, buckets smallint DEFAULT 16, exported bool DEFAULT false, synced_at, deleted_at)`**.
  - Constraints: `UNIQUE (id, service_id)`, `UNIQUE (id, identity_domain_id)`, and a foreign key `(service_id, identity_domain_id)` to `service`.
  - `uq_type_resource_service_name` stays: a live name is unique within its service.
- **`type_resource_parent(type_resource_id, parent_id, identity_domain_id, inherit, live, eff_inherit, eff_live)`**.
  - The row holds two states: desired (`inherit`, `live`) and effective (`eff_*`). The admin API writes the desired state. `RebuildComponent` switches the effective state together with the closure, in one transaction.
  - Both ends reference `type_resource(id, identity_domain_id)`, so the schema cannot store a link that crosses identity domains.
  - A link to another service's type also needs `exported` on the parent. This is checked under `admin_write`. Unexporting or deleting a type that has such links is `409 blocked_by type_link`.
- **`synchronizer(…, regions text[], refresh_interval interval DEFAULT '5 min', refresh_interval_max interval NULL, deleted_at)`**.
  - A type still has at most one live synchronizer.
  - The configuration and its secrets stay in PostgreSQL. They are never put into the outbox, and the API returns them masked.
  - `cache_ttl` (`core/model/synchronizer.go:41`) and `SYNC__FETCH_TTL` (`core/config/config.go:345`) are removed in P2e together with Redis: from the model, the settings and the UI. A stored configuration that still has `cache_ttl` is read without it.
- **The mode of a type** is: `synced_at IS NOT NULL` and a live synchronizer exists. One SQL helper defines it and replaces `liveSynchronizerExists` (`admin-api/service/synchronizers.go:21`). `checkRuleTarget` (`admin-api/service/assignment_rules.go:34`) and the catalog projection both use it.
  - The first applied synchronization sets `synced_at` in the same transaction as its data. A type therefore becomes `sync` together with its first data.
  - Creating or deleting a synchronizer clears `synced_at` and resets the type's `sync_state`, in the admin's own transaction. That transaction writes one outbox entry on the `catalog` stream.
    - The mode turns `not_sync` exactly at the revision of the deletion.
    - A new synchronizer starts from "never synchronized" and does not inherit the old source's data.
  - An apply is fenced on its synchronizer. The apply transaction reads the synchronizer row `FOR SHARE` and applies nothing if the row was deleted or its configuration changed since the fetch. A fetch made before a deletion cannot set `synced_at` after it.
  - The review proposed a writer workflow for this transition. The admin's transaction does it instead, because a workflow that commits later would leave a window in which a newly created synchronizer finds the old `synced_at`. The review's requirement holds: the transition happens in one transaction together with its outbox entry. ASOF02 adds this case.
- **`access_group(…, service_id)`**: foreign key `(type_resource_id, service_id)`.
- **Groups.** `user_group(id, service_id, name, UNIQUE (service_id, name), UNIQUE (id, service_id))`. `group_member(…, service_id)` has foreign keys `(group_id, service_id)` and `(member_group_id, service_id)`.
- **Roles.** `role(id, service_id, name, UNIQUE (service_id, name), UNIQUE (id, service_id))`. `role_permission(…, service_id)` has foreign keys `(role_id, service_id)` and `(type_resource_id, service_id)`.
- **`assignment_rule(…, service_id NOT NULL, subject_type, subject, subject_group_id)`**.
  - Foreign keys: `(type_resource_id, service_id)`, `(role_id, service_id)`, `(member_of_group_id, service_id)`, `(requires_group_id, service_id)` and `(subject_group_id, service_id)`.
  - `subject_group_id` replaces the literal `group:<uuid>`.
  - The existing CHECK constraints stay.
  - `resource_from_property` is the former `resource_from_claim`. Its lookup type must be the rule's own type or an exported ancestor; the application checks this.
- **Admin model and PEP tables:** `admin_binding` and `platform_state` (see the admin model section); `pep_client`, `pep_credential`, `pep_issuer` and `pep_issuer_keys` (see the PEP section).
- **`subject_directory(service_id, subject_type, subject_id, display_name, properties jsonb, updated_at)`**.
- **`log_settings` and `policy_scenario` stay.** Scenarios are fixtures and are not projected.

### 4.3 Revision, outbox and history tables

| Table | Shape | Notes |
|---|---|---|
| `revision_state` | `(scope PK, epoch int, revision bigint)` | One row, `'global'`. The scopes `rules`, `data:<service>` and `log_settings` go. `failover` raises `epoch` (see the failover section). |
| `outbox` | `(revision bigint PK, epoch int, xid xid8 NOT NULL DEFAULT pg_current_xact_id(), kind text, committed_at timestamptz, services uuid[], streams text[], policy jsonb, payload uuid, chunk_count int, payload_sha256 bytea, published_at timestamptz)` | Index on `xid`; partial index `WHERE published_at IS NULL`. `kind` is `change` or `epoch_start`. `committed_at` is `clock_timestamp()` of the bump, in the final batch before `COMMIT` (4.6). |
| `outbox_chunk` | `(payload uuid, stream text, n int, body bytea, PRIMARY KEY (payload, stream, n))` | zstd, at most 1 MiB per chunk. |
| `change_log` | `(revision, seq, at, actor, service_id, entity, entity_id, action, before, after, PRIMARY KEY (revision, seq))` | Index `(service_id, revision)`. Replaces `authz_change`. `service_id` is required except for platform-scope records. |
| `heartbeat` | `(id = 1, epoch, position, pg_time)` | See 4.9.3. |
| `publish_state` | `(id = 1, epoch, last_published)` | CQL mode only. |
| `stream_state` | `(stream PK, head, checkpoint, changes_since, bytes_since)` | CQL mode only. Holds the `prev` pointer of each stream's chain and the counters that trigger checkpoints. |
| `tx_dirty` | `(xid xid8 PRIMARY KEY)`, `UNLOGGED` | See 4.7. |

`change_log` is ordered by `(revision, seq)`. Revisions have no gaps and follow commit order (4.5). A follower that reads `revision > last` (`ListChanges`, `WaitChanges`, `PlanRevert`, `PlanRollback`) therefore cannot pass a record that commits later. This removes the known limitation of the feed ordered by `id`.

### 4.4 Resource graph tables (written only by the writer)

- **`resource_cache`, `resource_edge` and `resource_ancestor` stay.** `rebuildClosureSQL` (`core/engine/resource_cache.go:505`) remains the definition of the closure.
- **`resource_unresolved(descendant_id, parent_type_id, inherit, PRIMARY KEY (descendant_id, parent_type_id))`** (new). It is the transitive set used for fail-closed and is rebuilt together with the closure. Its mask is defined as follows:
  - take every ancestor `a` of a resource, the resource itself included;
  - for each edge of `a` with `parent_id IS NULL` to a type `U`, the mask is `inherit(a.type → U) ∩ closure_mask(a → resource)`.
- **`sync_state` keeps `invalidated_at` until P4.** `invalidatePlatformSync` writes it until P4 removes the platform self-synchronizers; the column goes with them.
- **The DBOS system tables live in schema `dbos` of the same database.** The admin API enqueues workflows in its own transaction (4.6), and `WithEnqueueTransaction` requires a shared database.
- **`sync_executor` stays** for the janitor (4.9.1).
- **Removed:** `delegated_call` with `delegated_call_id_seq`, `known_subject`, `sync_job`, `sync_job_token_seq` and `authz_change`. `sync_demand` stays until P5, which removes `ensure`, its writer (11.9).

### 4.5 One revision, in commit order

Every transaction that changes something a decision reads takes its revision in its final batch, just before `COMMIT`; after the bump it only inserts and calls `pg_notify` (see the lock order below):

```sql
WITH r AS (UPDATE revision_state SET revision = revision + 1 WHERE scope = 'global'
           RETURNING epoch, revision)
INSERT INTO outbox (revision, epoch, kind, committed_at, services, streams, policy, payload, chunk_count, payload_sha256)
SELECT r.revision, r.epoch, $1, clock_timestamp(), $2, $3, $4, $5, $6, $7 FROM r
RETURNING epoch, revision
```

The row lock on `revision_state` is held until `COMMIT`. That gives three properties:

- **No gaps.** A rollback gives its number back.
- **Revision order is commit order.** The next writer can read the row only after the previous transaction has committed.
- **Every snapshot sees a contiguous prefix of revisions.** This holds on the primary and on a streaming replica, which replays commits in WAL order.

Today `TestF2_2` (`admin-api/tests/integration/cluster/f2_test.go:84`) checks that 20 concurrent writers get distinct revisions with no gaps; it does not check that revision order is commit order or that every snapshot sees a prefix. In v2 the whole property carries the design, and WR01 is its contract.

**Lock order.** Every writer takes its locks in this order:

1. `admin_write` (admin writes only) or the component advisory locks from `ComponentKeys` (synchronization only);
2. the entity rows. Admin and synchronization transactions share a few of them: `type_resource_parent`, `synchronizer`, `type_resource` (`synced_at`) and `sync_state`. Every writer takes these in this table order, and the rows of one table in key order; for `SyncType` the fence on the `synchronizer` row (4.9.1) is the first of them;
3. the revision row.

While a transaction holds the revision row, it only inserts (`outbox`, `change_log`), calls `pg_notify` and commits. It never waits for another lock, so the revision row is never part of a wait cycle. A synchronization waits for an admin transaction no longer than that transaction's commit (WR07): only for its final batch and commit when they share no row, and from the admin's write of a shared row to its commit when they do.

**Throughput.** The revision row serializes the commits of decision-changing transactions. At most one commits per commit latency, and that latency includes the synchronous standby's acknowledgement. Admin writes and synchronization commits arrive at tens per second, well within this. WR06 and the load budgets measure it. Sharding `admin_write` by service is not part of v1.

### 4.6 The admin write (`core/db.PolicyTx`)

`PolicyTx` replaces `mutate` (`admin-api/service/tx.go:60`). Today `mutate` writes the change record and bumps the `rules` scope at the end of the transaction (`recordChangeAndBump`, `tx.go:82`, `:121`). In v2 the steps are:

1. `BEGIN` (READ COMMITTED).
2. One batch: `pg_advisory_xact_lock(admin_write)` and a re-check of the caller's binding.
3. Validation and the entity writes. The write function returns `change{entity, id, service_id, action, before, after, streams}` with **full** canonical images.
4. The invariants, checked under the lock: last `platform_admin`, last owner, cycles in the type graph (`checkParents`), `blocked_by`, and name uniqueness. Because all of them run under `admin_write`, write skew is not possible.
5. One final batch:
   - the CTE of 4.5;
   - `INSERT INTO change_log`, stamped with `(SELECT revision FROM revision_state WHERE scope = 'global')` (the transaction sees its own update);
   - `SELECT pg_notify('licetum_outbox', <revision>)`;
   - for a structural change (links, `inherit`), right after the final batch and in one more round trip (its id needs the revision the batch returns; it only inserts, so 4.5 holds), the enqueue of `RebuildComponent` with `WithEnqueueTransaction`, under the workflow id `rebuild:<epoch>.<revision>`. The admin API is not inside a workflow, so DBOS allows this. Duplicates are excluded by the deterministic id, not by a deduplication policy: `ReturnExisting` cannot be combined with `WithEnqueueTransaction` (`dbos/workflow.go:2129`).
6. `COMMIT`. The deferred check of 4.7 runs, and the notification is delivered.
7. The response carries `Licetum-Revision: <epoch>.<revision>`.

**Batches and bindings.** A `POST …/batch` is one `PolicyTx`: one revision, one outbox entry, and `change_log` rows with `seq` 1…n. A change of an admin binding is an admin write (the last-admin invariant applies). Bindings are not projected, so the outbox entry has `streams = {}`.

**Round trips.** Today an admin write costs at most 6 (`TestF6_7`, `admin-api/tests/integration/chaos/f6_test.go:193`). v2 adds one, the batch of step 2. `change_log` and `NOTIFY` ride in the final batch. About 7 is expected; WR06 measures the count of every write route and the budget is set from that measurement. A structural write pays one more round trip for the enqueue, and WR06 counts it separately.

**Writes that do not take `admin_write`.** These have no invariants, so they take only the revision row, as their last statement:

- **`subject_directory`** (arrives in P2, see 4.10).
  - In P4 it is written by the role `directory_writer`, held by a machine client of the admin IdP. The role grants no policy rights and is rate-limited per client.
  - One `PUT` of up to 1000 entries is one transaction and one revision on stream `dir:<service id>`.
  - Unchanged entries are not rewritten. A `PUT` that changes nothing spends no revision.
  - Deletions by the directory's retention take the same path.
- **`pep_issuer_keys`**, written by the writer's `IssuerKeys` job (4.9.5).
- **Synchronization commits** (4.9.1).

### 4.7 The outbox is mandatory

The schema checks the rule at `COMMIT`; it does not rely on convention.

- **Marking a transaction.** Every covered table has four triggers:
  - three statement-level triggers, `AFTER INSERT`, `AFTER UPDATE` and `AFTER DELETE`, each with a transition table (PostgreSQL allows a transition table only on a single-event trigger). Each inserts `pg_current_xact_id()` into `tx_dirty` (`ON CONFLICT DO NOTHING`) **only when its transition table is not empty**;
  - an `AFTER TRUNCATE` trigger, which always inserts.
- **Checking at `COMMIT`.** `tx_dirty` carries a `CONSTRAINT TRIGGER … AFTER INSERT DEFERRABLE INITIALLY DEFERRED FOR EACH ROW`. At `COMMIT` it raises an exception unless `outbox` has a row with this `xid`, and then deletes its own `tx_dirty` row. A rolled-back transaction takes its `tx_dirty` row with it. `SET CONSTRAINTS … IMMEDIATE` only makes the check fire before the outbox row exists, so the commit fails; it cannot skip the check.
- **Only tuples actually written count**.
  - A statement that writes no tuple marks nothing. One example is a synchronization whose digest changed while the rows did not: writers guard updates with `IS DISTINCT FROM`, as `applyType` already does (`core/engine/resource_cache.go:297`).
  - So SYN07 holds. Today a synchronization bumps only when rows moved (`core/engine/sync_worker.go:566-580`), and the trigger keeps that behaviour.
  - An `UPDATE` that rewrites equal values does count. Writers must not issue one.
  - The counters `pg_stat_get_xact_tuples_inserted/updated/deleted` are not used as the test: they count attempted changes, including those of rolled-back subtransactions.
- **What the triggers cover.**
  - They are created `ENABLE ALWAYS`, so `session_replication_role = replica` does not switch them off.
  - Foreign key cascades run as statements on the referencing table and fire the triggers.
  - Seed, `restore-admin` and raw SQL run by the application roles are caught.
- **What is not claimed.** A table owner or a superuser can `DISABLE TRIGGER`, drop a trigger, or restore data with triggers off.
  - The application roles (admin, writer, PDP) do not own tables; migrations run as the owner (OPS06).
  - Such bypasses are found by `reconcile` (see the read model section), not prevented.
- **Coverage by phase.** The full list of covered tables and the streams they feed is in 4.8.
  - P3: the policy tables (`identity_domain`, `service`, `type_resource`, `type_resource_parent`, `synchronizer`, `access_group`, `user_group`, `group_member`, `role`, `role_permission`, `assignment_rule`), the PEP tables, `log_settings` and `subject_directory`.
  - P3 as well: `admin_binding`, which schema v2 creates (8.3) though nothing writes it before P4.
  - P5: `resource_cache`, `resource_edge`, `resource_ancestor` and `resource_unresolved`, once the writer is their only writer (4.10).
  - Never: `outbox`, `outbox_chunk`, `change_log`, `revision_state`, `heartbeat`, `publish_state`, `stream_state`, `sync_state`, `sync_executor`, `policy_scenario`, `subject_directory_seen` (8.8) and the `dbos` schema.

### 4.8 What an outbox entry carries

| Stream | Tables |
|---|---|
| `catalog` | `service` (without secrets), `type_resource` (including `exported`, `buckets` and the mode), effective columns of `type_resource_parent`, `synchronizer` (the mode inputs and `regions` only, never the configuration) |
| `svc:<service id>` | `access_group`, `user_group`, `group_member`, `role`, `role_permission`, `assignment_rule` |
| `dir:<service id>` | `subject_directory` |
| `pep` | `pep_client`, `pep_credential`, `pep_issuer`, `pep_issuer_keys` |
| `settings` | `log_settings` |
| `data:<type id>` | `resource_cache`, `resource_edge`, `resource_ancestor`, `resource_unresolved` |
| none | `admin_binding`, `identity_domain`, desired-only columns of `type_resource_parent` |

- **Canonical images.** One function, `core/outbox.Project` (SQL plus Go), builds them. The writing transactions, checkpoints, `rebuild` and `reconcile` all use it, so the fold equation of POL01 is about one definition.
  - Policy streams carry post-images; a deletion is `{id, deleted: true}`.
  - An entry up to 1 MiB is stored inline in `outbox.policy`; a larger one goes to chunks.
- **Data streams.** A data entry carries the full state of every touched resource: name, meta, `active`, lineage with masks, and `unresolved_up`. "Touched" means:
  - the changed resources;
  - their descendants;
  - the descendants whose `unresolved_up` changed.

  The state is written to `outbox_chunk` with `COPY`, zstd-compressed, at most 1 MiB per chunk.
- **`services`** names the services whose decisions may change.
  - For policy, it is the owner plus the services below it through exported links in the type graph.
  - For data, it is the services of every resource whose lineage changed, descendants in other services included. Today `bumpDataRevisions` attributes a data change only to the services that own the synchronized types (`dataServices`, `core/engine/cache.go:338`, `:353`).
- **Entries with no streams** (binding changes, desired-only structural changes) are still published. The log has no holes (PUB02).
- **Partitions in the CQL log**. A first synchronization of a type with a million resources is gigabytes, so it cannot sit in one partition.
  - The chunk key carries `part = n / K`: `log_chunk((epoch, position, stream, part), n)`. `checkpoint_chunk` does the same.
  - K is fixed by the P0.5 spike so that a partition stays within what both ScyllaDB and Cassandra 5 handle well. The starting value is 8, which bounds a partition to 8 MiB (5.1).
  - Each chunk is a single write of at most 1 MiB.
  - The applier records the changed keys of each `(position, stream)` in `applied_keys` rows of at most 4096 keys (5.3). When one position changes more than `APPLY__CHANGED_KEYS_MAX` keys of a stream (65 536, that is 16 rows), it records only a whole-stream marker (`-1`), and a PDP that sees the marker drops its cached rows of that type.

### 4.9 The writer

| Process | Kubernetes | DBOS | PostgreSQL | Does |
|---|---|---|---|---|
| `licetum-writer run --role sync` | StatefulSet, 2+ replicas, `DBOS__VMID` = pod name | yes | yes | Synchronization workflows, janitor |
| `licetum-writer run --role project` | StatefulSet, 2 replicas, one leader | no | yes (direct session) | Publication, heartbeat, checkpoints, retention, issuer keys |
| `licetum-writer fetch --region <r>` | Deployment in region `r` | no | no | Asks the sources that only region `r` can reach |
| `licetum-writer migrate`, `rebuild`, `reconcile`, `checkpoint`, `failover` | Job / CronJob | no | yes | Operations (see the read model and failover sections) |
| `licetum-writer freeze`, `unfreeze` (CQL mode) | Run by an operator inside the region | no | no | Break-glass switch of the region's staleness gate, with the operator CQL role (5.4, 6.5) |

Both roles are StatefulSets:

- `sync` needs a stable executor id (`DBOS__VMID`) for DBOS recovery.
- `project` runs no DBOS workflow (4.9.2) and needs no `DBOS__VMID`; one workload kind keeps the chart, the RBAC and the runbooks uniform.

#### 4.9.1 Synchronization (DBOS)

- **Workflows** on queue `licetum-sync`:
  - `SyncSchedule`, scheduled every `SYNC__SCHEDULE_TICK`. It queues the types with `next_due_at ≤ now()`.
  - `SyncType`, deduplicated per type, with a separate `:fresh` id for "refresh now".
  - `SyncOne`, which replaces the admin API's inline `resolve()` (`core/engine/resource_cache.go:969`).
  - `RebuildComponent`. It moves desired links to effective and rebuilds the closure without asking any source (SYN08). `structureOf` no longer includes the service name, so renaming a service invalidates nothing.
- **Fetch step.** It runs the adapter, `NormalizeResources` and `dataDigest`. If the data digest, the graph signature and the number of active resources are all unchanged, nothing is written and no revision is spent.
- **Apply step.** One `RunAsTransaction`, on the synchronization pool:
  1. Set `lock_timeout` (5 s) and `statement_timeout`, and take the advisory locks of `ComponentKeys` (`core/engine/sync_state.go:131`).
  2. Fence, in two parts:
     - read the synchronizer row `FOR SHARE` (4.2);
     - read the DBOS workflow row `FOR SHARE`, as today (`core/engine/sync_dbos.go:288`). This fence stays until SYN04 shows that the completion row alone is enough.
  3. Run `applyType`, `syncEdges`, and `rebuildClosure` together with `resource_unresolved`.
  4. `COPY` the payload into `outbox_chunk`.
  5. Run `markApplied`.
  6. Last: the CTE of 4.5 and `pg_notify`.

  If no projected tuple changed, the transaction writes neither an outbox row nor a revision.
- **Backoff stays as today.** A source that does not answer gets `attempts + 1` and `next_due_at = now() + min(30 s · 2^attempts, 10 min)`, for the whole cluster. A busy component lock postpones the type by 5 s without backoff (`markBusy`).
- **Queue polling**.
  - The base interval is set explicitly with `WithQueueBasePollingInterval(100 ms)`. The DBOS default is 1 s (`dbos/internal/models/queue.go:12`).
  - After polling errors the interval grows up to 120 s (`dbos/queue.go:17`), and v1.6.0 has no option to lower that cap.
  - For synchronization this is acceptable: it delays the start of a fetch, not a published revocation. It is one of the reasons publication does not go through a DBOS queue.
- **Recovery.** DBOS v1.6.0 recovers only the workflows of its own executor (`dbos/dbos.go:894`). The executor id comes from `DBOS__VMID` (`dbos/dbos.go:165`), which the StatefulSet keeps stable across restarts. The janitor is kept for scale-down:
  - it is generalized from today's `sync_executor` heartbeat (`sync_dbos.go:346`);
  - it keeps the takeover under `<id>+r` (`sync_dbos.go:374`).
- **Workflow retention.** `dbosPrune` (`sync_dbos.go:450`) already deletes ended synchronization workflows. The review said workflows are never deleted; that is not the case on this branch. `dbosPrune` is kept, with a fixed retention instead of one derived from `SYNC__DEMAND_WINDOW`.
- **Schedule without demand**.
  - The PDP writes nothing, so it cannot record demand. `sync_demand` goes, and every type with a live synchronizer is polled on its schedule.
  - `refresh_interval` has a minimum of 10 s. Its default starts at 5 minutes (4.2) and is set in P5 from the metric of calls to sources (13.3 #4).
  - `refresh_interval_max` optionally stretches the interval for quiet sources: each unchanged answer doubles the interval up to the maximum, and any change resets it.
  - A revocation that arrives through data takes effect within `refresh_interval_max`, or within `refresh_interval` when no maximum is set.
  - The number of requests per source is exported as a metric.
- **Region-limited sources**.
  - `synchronizer.regions` stays.
  - When the home region is not in a type's list, the fetch step of `SyncType` calls a `licetum-writer fetch` process in an allowed region, over mTLS. It sends the configuration, secrets included, and receives the normalized answer. The fetcher keeps no state and holds no PostgreSQL credentials.
  - The apply step stays in the home region.
  - This replaces an earlier draft's `licetum-sync-<region>` executors, which would have run DBOS over the WAN. SYN09 becomes: "the source of a type limited to region X is asked only by fetchers in region X".

#### 4.9.2 Publication

- **Leader.**
  - One `project` replica holds a session-level advisory lock on a direct connection to the primary. This cannot go through PgBouncer in transaction mode, which supports neither `LISTEN` nor session locks.
  - On the same connection the leader runs `LISTEN licetum_outbox`.
  - When the connection drops, the lock is released, and the other replica takes over within one tick.
- **Wake-ups.**
  - Every transaction that writes an outbox row calls `pg_notify`, and PostgreSQL delivers the notification only when that transaction commits. This works from synchronization transactions inside DBOS steps too, where `WithEnqueueTransaction` is refused (`dbos/workflow.go:2126`).
  - A tick, `PUBLISH__TICK` (1 s), covers lost notifications and a newly elected leader.
  - PostgreSQL serializes the commits of transactions that issued `NOTIFY` on one global lock. Here those transactions are already serialized by the revision row, so this adds no contention of its own.
- **Loop.** `SELECT … FROM outbox WHERE revision > $last_published ORDER BY revision LIMIT 256`. The prefix property of 4.5 guarantees that no later commit can bring a smaller revision.
- **Order** (one choice). Positions are published strictly in revision order, one at a time:
  1. Check `log_entry(epoch, r)`. If it exists and differs from the outbox row (its header, or the sha256 of a stream), the histories have diverged: publication stops and the epoch procedure starts (see the failover section).
  2. Write the chunks of the position, in parallel up to a bound.
  3. Write the header after all of them.
  4. In one PostgreSQL transaction, advance `publish_state.last_published` (`WHERE last_published < r`), `stream_state.head` and `outbox.published_at`.

  A duplicate publisher, for example a former leader that is still writing, is harmless. The outbox fixes the content, the key is the position, and the write timestamp (`USING TIMESTAMP`) is derived from the position.
- **Cost.** There is no DBOS workflow per revision. The admin write does not pay an extra statement for an enqueue. Heartbeat and tick do not create about 170,000 workflows a day.
- **Revocation budget (REV01).** Commit, then `NOTIFY` (milliseconds), then publication (about 50 ms), then the applier's poll (100 ms), then the PDP's poll (100 ms).
- **Head-of-line blocking.** A large synchronization payload delays the entries after it. The risk is accepted and measured.
- **`postgres` mode**.
  - Nothing is published, and `published_at` stays NULL.
  - Outbox rows and their chunks are deleted by age (`OUTBOX__RETENTION`, default 1 hour). A PDP that falls further behind reloads its view from the tables.
  - Turning on the CQL store for an existing installation is a documented `licetum-writer rebuild --all`, with its own contract.

#### 4.9.3 Heartbeat

- **Statement.** Every second the leader runs:

  ```sql
  UPDATE heartbeat SET pg_time = statement_timestamp(), epoch = g.epoch, position = g.revision
  FROM (SELECT epoch, revision FROM revision_state WHERE scope = 'global') g
  RETURNING pg_time, epoch, position
  ```

- **Why it holds.** The statement's snapshot is taken after `statement_timestamp()`. Every commit visible before `pg_time` therefore has a revision ≤ `position`, and PUB03 checks this.
- **CQL mode.** The leader also writes `log_heartbeat(epoch, day, pg_time) → position`. The heartbeat does not depend on publication progress: the applier uses it only after it has applied up to `position`.
- **`postgres` mode.** A PDP reads `revision_state` and `heartbeat` in one snapshot.
  - On a streaming replica, T(P) is the heartbeat's `pg_time`. The replica replays commits in order, so seeing the heartbeat's commit implies seeing every revision up to `position`.
  - On the primary, the start of the PDP's own read of `revision_state` (`statement_timestamp()` of its poll, 6.2) is a later proof and is used instead. A stopped writer then does not close a single-region PDP that reads the primary.

#### 4.9.4 Checkpoints (CQL mode)

- **Per stream.** Each checkpoint runs in one `REPEATABLE READ READ ONLY` transaction, and the revision is read **inside** that snapshot.
  - Today `loadSnapshot` takes its revision from a statement run before its transaction starts (`core/engine/localstate.go:299` passes it to `core/engine/snapshot.go:129-133`). A snapshot can then hold writes newer than its label.
- **Write order.** Chunks first, then the head.
- **When.**
  - Policy streams: every 256 changes or 4 MiB.
  - Data streams: when the delta exceeds 20% of the type's size.
  - Any stream that changed: at least once every 7 days, since the log TTL is 14 days.
  - `stream_state` holds the counters.

#### 4.9.5 Other leader jobs

- **Retention.**
  - Outbox rows and chunks: one hour after publication (CQL mode) or by age (`postgres` mode).
  - `change_log`: according to the log settings.
  - `subject_directory`: by each service's retention, through the outbox.
  - DBOS workflows are pruned by the `sync` role (4.9.1).
- **`IssuerKeys`.** Polls the JWKS of each `pep_issuer` with the mechanics of `jwks.go`: singleflight, and transport errors are not cached. It writes `pep_issuer_keys` only when the key-set digest changes, on stream `pep`. The PDP never contacts an IdP.
- **Reconcile** is a CronJob (`licetum-writer reconcile`), not a leader job.

### 4.10 Phasing

- **P2**
  - `subject_directory` arrives together with the removal of `known_subject`. It starts as a plain table, governed by the platform policy that governs `/subject` today (`group_member`).
  - P2e removes `cache_ttl` and `SYNC__FETCH_TTL`.
- **P3**
  - The v2 chain with its own version table, and the `migrate` refusal.
  - `PolicyTx` for admin writes, batches, `SeedPlatform` and `restore-admin`.
  - The global revision, `outbox` and `change_log`.
  - The deferred check on the policy tables, the PEP tables, `log_settings`, `subject_directory` and `admin_binding` (4.7).
  - Synchronization commits bump the global revision and write header-only entries: `streams = {data:<type id>}`, the corrected `services`, no payload yet.
  - Until P5 the current engine keys its answers by the global revision.
  - Nothing publishes the outbox yet. It is retained by age.
  - The `previous-release` CI job is removed.
- **P4**
  - Authorization starts reading `admin_binding` (created with its trigger in P3).
  - The platform self-synchronizers go. Today `seedPlatformType` (`core/db/migrate.go:273`) stores `DB__URI` in their configuration.
- **P5**
  - The writer process, `RebuildComponent` and `SyncOne`.
  - The data payload, the filling of `resource_unresolved` together with the closure (the table comes with schema v2 in P3, 11.7), and the heartbeat.
  - The resource tables get the deferred check: by now the self-synchronizers are gone (P4) and inline `resolve()` is replaced, so the writer is their only writer.
  - The PDP runs in `postgres` mode.
- **P6**
  - Publication to the CQL log, `stream_state`, checkpoints and divergence detection.

### 4.11 Contracts

| ID | Statement in v2 |
|---|---|
| WR01 | A commit spends exactly one revision, a rollback none, a batch one. With 20 concurrent writers there are no gaps, and every snapshot sees a prefix. |
| WR02 | A transaction that writes a tuple of a covered table without an outbox row fails at `COMMIT`. This includes raw SQL, cascades, seed, `restore-admin`, `TRUNCATE` and `session_replication_role = replica`. A transaction whose statements write no tuple commits without an outbox row. |
| WR03 | The outbox images reproduce exactly the changed rows. |
| WR05 | Every write route produces exactly its streams and `services`. |
| WR06 | An admin write stays within the round-trip budget measured per route (about 7 expected). A structural write is counted separately. |
| WR07 | A synchronization waits for an admin transaction no longer than that transaction's commit, and never in a cycle (the lock order of 4.5). |
| SYN07 | A repeat with the same data writes nothing and spends no revision. |
| SYN09 | The source of a type limited to region X is asked only by fetchers in region X. |
| ASOF02 | Adds the cases of 4.2: deleting a synchronizer turns the type `not_sync` at that revision, and a new synchronizer makes the type `sync` only with its first applied data. |
| PUB02 | Positions have no gaps, entries without streams included. |
| PUB03 | Heartbeat property of 4.9.3. |
| PUB04 | Reworded. The leader is woken by `NOTIFY` from admin and synchronization commits. A lost notification is picked up by the tick. No DBOS enqueue is on the publication path. The write path is also run through PgBouncer nightly. |

## 5. The CQL read model, the applier and the PDP memory

This section covers the `cql` adapter of the `ReadModel` port. It describes the replicated log, the regional keyspaces that the applier fills from that log, how versions and generations are kept and removed, and what a PDP holds in memory and reads for each decision. The `postgres` adapter stays the default. A region served by a PostgreSQL streaming replica needs none of the machinery below; 5.9 says what such a PDP reads and how an installation moves from one adapter to the other.

The CQL engine is ScyllaDB or Apache Cassandra 5, chosen in phase P0.5 after a spike and after the license question is settled. ScyllaDB from 2025.1 is source-available and free up to 50 vCPU and 10 TB per organization; its last AGPL release is 6.2. Everything in this section is written so that either engine can carry it.

### 5.1 Rules for every CQL table

- **The portable subset.**
  - The schema uses no lightweight transactions, counters, materialized views or secondary indexes (neither ScyllaDB's nor Cassandra's SAI).
  - It uses nothing that needs tablets.
  - Correctness relies on no table option that only one engine has, for example ScyllaDB's `tombstone_gc`.
  - Batches are unlogged and touch one partition. Keyspaces use `NetworkTopologyStrategy`.
  - Some table options differ only in name between the engines (compaction); the CQL migration chooses them per engine. Otherwise there is one schema, and `API01` keeps a snapshot of it per engine.
- **Names.**
  - The adapter is `cql` (`READMODEL__STORE=postgres|cql`) and its package is `core/readmodel/cqlstore`.
  - Settings use the prefix `CQL__`: `CQL__LOCAL_DC`, `CQL__DCS`, `CQL__LISTING_CONSISTENCY`.
  - The code uses the gocql API. The ScyllaDB fork of the driver replaces it through a `replace` directive. P0.5 pins the driver version together with the engine.
- **Both engines in CI.** The P0.5 spike runs the model on both engines. From P6, `storetest` and the contracts of level `cql` run against the chosen engine on every pull request and against the other one nightly (10.2, ST09), so the schema cannot drift out of the portable subset.
- **Every write carries its timestamp.**
  - Writes go through the helpers of `cqlstore`. A helper takes a position and a phase and sets `USING TIMESTAMP ts(position, phase) = 2·position + phase`. Phase 0 deletes, phase 1 upserts.
  - A statement without an explicit timestamp is refused (`ST07`). A server timestamp is microseconds since 1970, about 1.7·10^15, so it would beat every position-stamped value of its cell forever.
  - A few cells are not stamped with positions: `current_generation`, `fresh_pg_time`, `log_epoch` (the epoch), the rows of `log_heartbeat` (their `pg_time`) and `schema_migration` (their `applied_at`), and the freeze cells. Each is named below with its own time domain.
- **No time-window compaction.** Time-window compaction groups SSTables by write timestamp, and here the write timestamps are positions, not times. Tables with a TTL use the engine's default strategy. A TTL counts from the local time of the write, so it works as usual.
- **Bounded partitions.** No partition grows with the size of one change.
  - A chunked body keeps at most 8 chunks of at most 1 MiB per partition (`part = n / 8`). The same rule applies to the log, the checkpoints and the policy snapshots.
  - Listing tables are bucketed per type.

### 5.2 The log keyspace `licetum_log`

The keyspace uses `NetworkTopologyStrategy` with RF 3 in every DC. Log rows are immutable and expire. Checkpoints do not expire; they are pruned.

```
log_entry        ((epoch int, bucket bigint), position bigint)
                   → kind text, committed_at timestamp, services set<uuid>,
                     streams map<text, frozen<tuple<bigint, int, blob>>>,  -- per stream: prev position, chunks, sha256
                     base_position bigint, format smallint
                   -- bucket = position >> 16; TTL 14 d
log_chunk        ((epoch int, position bigint, stream text, part int), n int) → body blob
                   -- part = n / 8; body ≤ 1 MiB zstd, the outbox chunk byte for byte; TTL 14 d
log_heartbeat    ((epoch int, day int), pg_time timestamp) → position bigint
                   -- CLUSTERING ORDER BY (pg_time DESC); TTL 2 d; USING TIMESTAMP = pg_time in microseconds
log_epoch        (k text PRIMARY KEY) → epoch int, start_position bigint, base_position bigint
                   -- USING TIMESTAMP = epoch
checkpoint_head  ((stream text), position bigint) → chunks int, sha256 blob, format smallint
                   -- CLUSTERING ORDER BY (position DESC)
checkpoint_chunk ((stream text, position bigint, part int), n int) → body blob    -- part = n / 8
schema_migration ((keyspace_name text), version int) → applied_at timestamp
                   -- USING TIMESTAMP = applied_at in microseconds
```

- **Entry kinds and payloads.**
  - `kind` is `change` or `epoch_start`.
  - A data stream `data:<type>` carries the full state of each resource it touches: `active`, name, meta, lineage and `unresolved_up`.
  - A policy stream carries canonical post-images of entities. A deletion is written as `{id, deleted: true}`.
- **Write order and arrival order.** The writer writes the chunks of a position before its entry. Replication to other DCs is asynchronous and per partition, so an entry can reach a DC before its chunks do. The applier therefore never trusts arrival order (5.6).
- **Chunk partitions are bounded.** Without `part`, the first synchronization of a type with a million resources would put gigabytes into one partition. With `part`, a partition holds at most 8 MiB.
- **Checkpoint schedule.**
  - Checkpoints are taken per stream: policy streams every 256 changes or 4 MiB, data streams when the delta exceeds 20 % of the type.
  - Any stream that changed is checkpointed at least every 7 days. With a log TTL of 14 days, every entry after the newest checkpoint of a stream is still in the log.
- **Checkpoint retention.**
  - The newest checkpoint of a stream is never deleted. An unchanged stream is not checkpointed again, and its newest checkpoint is all an applier needs.
  - Any other checkpoint is deleted once it is older than the log TTL (14 days), when the entries right after it may have expired; the head row and the chunk partitions go together.
- **CQL migrations.** They are recorded in `schema_migration`. `licetum-writer migrate` applies them under the same PostgreSQL advisory lock as goose (`OPS04`).

### 5.3 The regional keyspaces and generations

Every DC has one state keyspace and several generation keyspaces. All of them use `NetworkTopologyStrategy {<dc>: 3}`.

```
-- licetum_r_<dc>
region_state  ((k text), generation int) → current_generation int STATIC, epoch int, applied_position bigint,
                applied_hash blob, fresh_pg_time timestamp
              -- current_generation: USING TIMESTAMP = generation
              -- epoch, applied_position, applied_hash: ts(position, 1)
              -- fresh_pg_time: USING TIMESTAMP = the heartbeat's pg_time in microseconds
region_freeze (k text PRIMARY KEY) → frozen boolean, frozen_at timestamp, frozen_by text, reason text
              -- written with the operator's wall clock
freeze_log    ((dc text), at timestamp) → by text, reason text, action text
```

One read of the `region_state` partition gives a PDP the current generation and the position of every generation. Reading `region_freeze` is one more read.

```
-- licetum_r_<dc>_g<N>
applied        ((bucket bigint), position bigint) → committed_at timestamp, streams map<text, int>
                 -- per changed stream: the number of applied_keys rows, -1 when its keys are not listed
                 -- bucket = position >> 16
applied_keys   ((position bigint, stream text), n int) → keys blob           -- ≤ 4096 keys a row, ≤ 16 rows
policy_delta   ((stream text, bucket bigint), position bigint) → prev bigint, ops blob, sha256 blob, snapshot boolean
policy_snapshot_head ((stream text), position bigint) → chunks int, sha256 blob   -- CLUSTERING ORDER BY (position DESC)
policy_snapshot ((stream text, position bigint, part int), n int) → body blob    -- part = n / 8
resource_v     ((type_id uuid, ext text), ver bigint)
                 → active boolean, name text, meta text, lineage blob, unresolved_up blob, committed_at timestamp
                 -- CLUSTERING ORDER BY (ver DESC); leveled compaction
descendant_v   ((anchor_type uuid, anchor_ext text, desc_type uuid, bucket smallint), desc_ext text, ver bigint)
                 → mask frozen<set<text>>, name text, gone boolean
type_member_v  ((type_id uuid, bucket smallint), ext text, ver bigint) → name text, gone boolean
attr_v         ((type_id uuid, attr text, value text, bucket smallint), ext text, ver bigint) → gone boolean
subject_v      ((service_id uuid, bucket smallint), subject_type text, subject_id text, ver bigint)
                 → display_name text, properties text, gone boolean
-- descendant_v, type_member_v, attr_v, subject_v: CLUSTERING ORDER BY (<key columns> ASC, ver DESC)
```

- **`resource_v`** is a resource as of one version.
  - `meta` is canonical JSON.
  - `lineage` lists every ancestor, the resource itself included, as `(type, external id, mask)`. A null mask means all levels.
  - `unresolved_up` is the transitive set of `(missing parent type, mask)` that PostgreSQL keeps in `resource_unresolved`.
  - `active = false` is a switched-off resource, as today.
- **Listing tables.**
  - `descendant_v` is the closure arranged for listing: one row per (anchor, descendant) pair, with the mask that flows between them.
  - `type_member_v` lists the members of a type.
  - `attr_v` indexes `name` and every `meta.<k>`, with one row per scalar and one per list element. One lookup therefore finds both forms that PostgreSQL finds with two containment conditions.
  - `gone = true` marks the version at which a row stopped belonging to the index.
- **Buckets.**
  - `bucket = fnv32a(ext) mod buckets(type)`. The count is a power of two: 16 by default (`type_resource.buckets`) and 8 for `attr_v`.
  - The `catalog` stream carries the counts. A change of a count takes effect in the next generation.
  - Listings come in `(bucket, ext)` order. This replaces today's global order by id.
- **Changed keys are split.**
  - Keeping all changed keys of a position in one cell would make one huge cell after a large synchronization, and every PDP reads it at every position.
  - So the keys are split per stream into `applied_keys` rows of 4096 keys each.
  - Above 65 536 keys of one stream at one position, the applier lists none and writes `-1`. A PDP then drops what it caches of that type (5.12).
- **Large policy deltas become snapshots.** If the operations of one position exceed 1 MiB, they are written as a `policy_snapshot` at that position, and the delta row only says so (`snapshot = true`). An import of ten thousand rules therefore does not become one giant cell.
- **Generations are created ahead of time.**
  - Creating or dropping a keyspace is a schema change, and a schema change needs the agreement of the whole cluster. A region cut off from home cannot make one, and every new keyspace needs its grants again.
  - So `licetum-writer migrate` runs in the home region with the migration role and `CQL__DCS`. For every DC it creates `licetum_r_<dc>`, the first generation and one spare generation, and grants on each.
  - The applier never runs DDL. A rotation fills the spare generation and switches to it (5.7).
  - After a rotation, the home job drops the old generation and creates the next spare. If the home region is unreachable, the drop waits; nothing in the region depends on it.

### 5.4 CQL roles

Each job gets one role with the least privilege it needs (`OPS06`). CQL grants are per table at the finest. That is why the freeze cells have a table of their own: the applier cannot unfreeze a region, and the operator cannot move its position.

| role | used by | privileges |
|---|---|---|
| `licetum_migrate` | `licetum-writer migrate` and the generation job, home region | creates, alters and drops the Licetum keyspaces and grants on them; the only role that runs DDL |
| `licetum_writer` | publisher, heartbeat, checkpoints, `rebuild`, `failover` (`log_epoch`, `epoch_start`) | `SELECT`, `MODIFY` on `licetum_log` |
| `licetum_apply_<dc>` | `licetum-writer apply --dc <dc>` | `SELECT` on `licetum_log`; `SELECT`, `MODIFY` on `licetum_r_<dc>.region_state` and on the generation keyspaces of `<dc>` |
| `licetum_pdp_<dc>` | `licetum-pdp` in `<dc>` | `SELECT` on `licetum_r_<dc>` and on the generation keyspaces of `<dc>` |
| `licetum_reconcile` | `licetum-writer reconcile` | `SELECT` on `licetum_log` and on the regional keyspaces |
| `licetum_operator_<dc>` | `licetum-writer freeze` / `unfreeze --dc <dc>` | `SELECT` on `licetum_r_<dc>`; `MODIFY` on `region_freeze` and `freeze_log` only |
| none | `licetum-admin` | nothing in the CQL store |

- `freeze` runs with the operator role from inside the region, because the home region may be what was lost.
- Every DC must be able to read the authentication data, or a cut-off region cannot log in. In Cassandra this means `system_auth` is replicated with `NetworkTopologyStrategy` to every DC.

### 5.5 Consistency levels

| operation | consistency |
|---|---|
| writer → `licetum_log` | `LOCAL_QUORUM` in the home DC. When the home DC is unavailable, a session pinned to another DC writes at that DC's `LOCAL_QUORUM`. This is safe because the content is immutable and checked by hashes. |
| applier ← `licetum_log` | `LOCAL_QUORUM`. After `APPLY__REMOTE_FETCH_AFTER` (5 s), a missing entry or chunk is read from another DC. |
| applier → regional keyspaces | `LOCAL_QUORUM` |
| PDP ← `region_state`, `region_freeze`, `applied`, `applied_keys`, `policy_*`, `resource_v`, `subject_v`, and `attr_v` when it finds the anchors of rules | `LOCAL_QUORUM`. A write and a read at `LOCAL_QUORUM` in one DC always overlap; `LOCAL_ONE` could return a version older than the newest one at or below P. |
| PDP ← `descendant_v`, `type_member_v`, `attr_v` when it lists candidates for search | `CQL__LISTING_CONSISTENCY`: `LOCAL_QUORUM` by default, `LOCAL_ONE` allowed |

- **Anchor lookups always use quorum.**
  - A `resource_from_property` lookup finds the anchors of a rule. Missing the anchor of a denial would let a request through that should be denied (fail-open).
  - Only reads that produce search candidates may use the weaker level. Every candidate is decided again from `resource_v` at `LOCAL_QUORUM`, so a stale listing can miss a candidate but cannot grant access.
- **Sessions stay in their DC.** The session is DC-aware and token-aware, with a host filter on `CQL__LOCAL_DC`, so a PDP opens no connection to another DC (`OPS05`). The applier and `reconcile` may open such connections, for the remote reads above.

### 5.6 The applier

`licetum-writer apply --dc <dc>` runs as two replicas per region. A Kubernetes Lease picks the active one only to save work: two appliers running at once do no harm.

For each position the applier does the following:

1. **Find the next entry.**
   - It takes `next = applied_position + 1` of the generation it fills, or the start position of the next epoch that `log_epoch` names. Epochs and `epoch_start` belong to the failover procedure.
   - It reads `log_entry(epoch, next)`. If there is no entry yet, it polls every 100 ms.
2. **Check the entry.**
   - It reads every chunk of every stream the entry names and checks each stream's sha256.
   - It checks that each stream's `prev` equals the position of that stream it already holds (`POL02`).
   - It waits for a missing chunk and reads it from another DC after 5 s. It never applies a partial entry (`APP01`).
3. **Apply each stream.**
   - A policy stream (`catalog`, `svc:<id>`, `pep`, `settings`) gets a `policy_delta` row, plus a `policy_snapshot` every 256 changes or once a day.
   - A data stream `data:<type>` gets a `resource_v` row with `ver = position` for every key in the payload. The previous version of the key (the newest `ver < position`, fixed by the log) gives the difference for `descendant_v`, `type_member_v` and `attr_v`: new rows, plus `gone = true` rows for whatever left.
   - A directory stream `dir:<svc>` gets `subject_v` rows.
4. **Record the position.**
   - It writes `applied` and `applied_keys` for the position.
   - Then it writes the generation's `region_state` row: `applied_position`, `epoch` and `applied_hash` (sha256 over the previous hash and the entry), all at `ts(position, 1)`.
   - If a heartbeat with `position ≤ applied_position` is newer than `fresh_pg_time`, it writes `fresh_pg_time` after the position, using that heartbeat's `pg_time` as the timestamp. A `LOCAL_QUORUM` read that sees a given `fresh_pg_time` therefore also sees a position at or above that heartbeat's position.

Everything the applier writes is a function of the log. Two appliers running at once write the same cells with the same timestamps and converge (`APP02`). `region_state` never goes back, because its cells are stamped with positions.

The P0.5 spike measures, before P3 freezes the outbox and payload formats:
- the throughput of the applier;
- cold misses at `LOCAL_QUORUM`;
- the payload of a type with a million resources;
- the behaviour of range tombstones.

### 5.7 Versions, garbage collection and generations

- **The newest version of a key is never deleted.** A switched-off resource is a new version with `active = false`. A row that leaves an index is a new version with `gone = true` (`ST08`).
- **P6 starts append-only.**
  - The applier deletes nothing except superseded policy snapshots and whole generations. Policy snapshots follow the retention rule of the checkpoints in 5.2, and deltas older than the second newest snapshot of their stream go with it.
  - Generations are rotated by hand (`licetum-writer apply --rebuild --dc <dc>`).
  - The garbage collection below exists behind `MVCC__GC`, off by default. It is switched on only after `APP01`–`APP03` and `ASOF01`–`ASOF03` pass under the kill tests.
- **How garbage collection works.**
  - When the applier writes version r of a key whose previous version is v1, and v1 is at or below the horizon H, it deletes the older versions: `DELETE … USING TIMESTAMP ts(r, 0) WHERE <key> AND ver < v1`.
  - H is the `applied_position` this applier observed `MVCC__RETENTION` (5 min) ago. The applier keeps samples of its position in memory. After a start it issues no deletes until `MVCC__RETENTION` has passed.
- **Why the horizon is measured by the region's own progress.**
  - The age of v1 is not measured by its PostgreSQL commit time. After a partition, the applier applies an hour of commits within seconds, and a PDP whose view is two seconds old would lose the version it reads.
  - Measured by the region's own progress, the rule holds. A PDP serves only from a view it confirmed against `region_state` within S (5.11). Its P is therefore at least the position the region had S ago, and that is at least H as long as `MVCC__RETENTION` exceeds S plus the request timeout.
  - Configuration validation refuses an `MVCC__RETENTION` below `2 × (POLICY__MAX_STALENESS + HTTP__REQUEST_TIMEOUT)`. The chart sets these three values from the same source for the applier and the PDP.
- **Resurrected versions and late writes are harmless.**
  - A replica that missed a range tombstone may bring back a superseded version after `gc_grace_seconds` if it is not repaired. No read at a P at or above H changes: the newest version at or below P is v1 or newer, and v1 is never deleted.
  - A late write of an old version by a lagging second applier is shadowed by the tombstone. The tombstone's timestamp `ts(r, 0)` is higher than `ts(v, 1)` for every v < r.
- **Generations.**
  - `apply --rebuild` fills the spare generation from the newest checkpoint of every stream plus the tail of the log, while the current generation keeps serving.
  - When the new generation reaches the current generation's position, the rebuild writes `current_generation` (timestamp = generation).
  - A PDP moves to the new generation only once that generation's position is at or above the PDP's own P, so no view goes back.
  - The old generation keeps serving the remaining PDPs for `GENERATION__DRAIN` (1 h). Then the home job drops it and creates the next spare.
  - A rotation starts from a metric (the share of `gone` and superseded rows, partition sizes), from a change of format, or by hand; by default at most once a week.
  - `ASOF03` requires a rebuilt generation to equal one applied incrementally.
- **Dropping a keyspace frees nothing by default.** With `auto_snapshot: true`, the default of both engines, `DROP KEYSPACE` leaves a snapshot on every node. The deployment requires `auto_snapshot: false`, or else the rotation runbook clears the snapshot of the dropped keyspace on every node (`nodetool clearsnapshot`).

### 5.8 Repair and reconcile are correctness requirements

Repair is needed for correctness, not only for durability. An example:

1. Version r is written at `LOCAL_QUORUM` to replicas a and b, while replica c is down and its hints expire.
2. Then b is replaced without repair. Now only a holds r.
3. A `LOCAL_QUORUM` read that reaches b and c returns an older version. A switched-off resource reads as active, or a lineage from before a move reads as outside a new denial.

The request is then allowed when it should be denied (fail-open). Hence:

- after a node replacement, the DC is repaired before another node of the same DC is replaced or taken down. Alternatively, the replacement uses repair-based node operations, where the engine has them;
- after a node was down longer than the hint window, its DC is repaired;
- the regional keyspaces and `licetum_log` get a full repair at least once within `gc_grace_seconds`;
- `licetum-writer reconcile --dc <dc>` runs nightly for every region, as a CronJob in the home region, and on demand.

How `reconcile` works:

- **One position.** It compares PostgreSQL and the region at one position c. It reads PostgreSQL in one `REPEATABLE READ READ ONLY` transaction with c read inside it, waits until the region has applied c, and reads the region as of c.
- **What it compares.** The canonical hash of every policy stream, and, per type, an XOR hash of `(ext, active, name, meta, lineage, unresolved_up)`.
- **Keys changed after c.** These are left out of the run and checked by the next one. Their newer versions came from PostgreSQL after c, and leaving them out keeps a long scan from being confused by garbage collection.
- **On a difference.** It counts `licetum_projection_drift_total{dc, stream}` and raises an alert. `reconcile` only reads (5.4); the repair, `rebuild --stream`, runs with the role `licetum_writer` (5.4).

`REC02` injects the scenario above, the newest version lost on two replicas, and requires `reconcile` to report it. The PDP adds a cheap detector of its own (5.12).

### 5.9 The `postgres` adapter beside the CQL one

In PostgreSQL mode the PDP reads the canonical tables as `licetum_pdp`, which has SELECT only. It reads the primary or a streaming replica in its region.

- **A replica is a consistent source.**
  - A standby replays commits in WAL order.
  - Every transaction that changes the projected tables takes the `revision_state` row lock in its last statement and holds it until COMMIT. So the WAL order of these commits equals the revision order.
  - A `REPEATABLE READ` snapshot on the standby therefore sees a prefix of the revisions, with all the data of each.
  - A standby cancels a query that conflicts with replay. The PDP's loads need `hot_standby_feedback = on`, or a `max_standby_streaming_delay` longer than a load. A cancelled load is retried and counted.
- **T(P) in PostgreSQL mode.**
  - The writer's leader updates one row every second with the statement of 4.9.3: `pg_time = statement_timestamp()`, and `epoch` and `position` from `revision_state`.
  - Every commit acknowledged before `statement_timestamp()`, the start of that statement, is visible to it. So `PUB03` holds for this row just as for `log_heartbeat`, which publishes the same values.
  - The PDP reads the row in the same snapshot in which it reads P. T(P) is that row's `pg_time`.
  - On the primary, the PDP also counts the start of its own read of `revision_state` (`statement_timestamp()` of its poll, 6.2), for the same reason. A single-region installation then does not close when the writer stops.
  - On a replica, only the row counts: the replica's own clock says nothing about its replay lag.
- **Policy and data at one position.**
  - Resource reads run in one `REPEATABLE READ READ ONLY` snapshot, with its revision Q read inside it.
  - If Q is ahead of the PDP's policy, the PDP first folds the outbox up to Q.
  - If Q is behind (another replica behind a pooled address), the read is retried for at most `PDP__FRESHNESS_WAIT`, then refused.
- **No publication.**
  - Without a CQL store the publisher does not run, `publish_state` is not used and `outbox.published_at` stays empty.
  - The writer deletes outbox rows older than `OUTBOX__RETENTION` (1 h).
  - A PDP that falls further behind than that, or that is starting, loads a snapshot instead of folding.
- **Switching an installation to CQL** follows a documented procedure:
  1. `licetum-writer migrate` with the CQL settings creates the keyspaces, the roles, the grants, and the first and spare generation of every DC.
  2. `licetum-writer rebuild --all` writes a checkpoint of every stream at a revision c, read inside its snapshot, as the start of every chain. It then sets `publish_state.last_published = c`. From then on, outbox rows are kept until they are published, and the publisher continues at c + 1.
  3. `apply --dc` starts in every region.
  4. Once a region has reached the head and its staleness is within S, its PDPs are restarted with `READMODEL__STORE=cql`.

  The new contract `ASOF04` requires that no position is skipped and that the PDPs answer as the `postgres` adapter did at equal positions (`REF02` on the switched installation). Switching back means restarting the PDPs with `postgres`.

### 5.10 Adding a DC and restoring PostgreSQL from a backup

Both cases have runbooks. P0.5 and P6 measure the recovery time of a full rebuild on the large profile.

- **Adding a DC.** `ALTER KEYSPACE licetum_log` does not copy data that is already written. Without a rebuild, the new applier would read every checkpoint chunk from another DC, each after a 5 s wait. The steps are:
  1. the nodes of the new DC join the cluster;
  2. `licetum_log` is replicated to the new DC, and so is the authentication data where the engine needs it;
  3. `nodetool rebuild -- <existing dc>` runs on every new node;
  4. `licetum-writer migrate`, with the new DC in `CQL__DCS`, creates `licetum_r_<dc>`, its first and spare generation, the roles and the grants;
  5. `apply --dc <dc>` builds the first generation from the checkpoints and the tail of the log;
  6. the PDPs of the region become ready once their staleness is within S.
- **Restoring PostgreSQL from a backup.** A restore loses the commits made after the backup, like a failover that loses commits.
  - If the log still covers the restored revision, that is, the revision is newer than the log's oldest entry (14 days), the epoch procedure applies.
  - If the restored revision is older, keys that only the lost history touched may have expired from the log, so `epoch_start` cannot name them. `failover` then refuses. The runbook is `licetum-writer rebuild --all` in a new epoch, with a new generation in every DC.
  - The old generations cannot continue into the new epoch, so their heartbeat stops. The regions refuse requests after S until their new generation is ready.
  - Freezing a region instead would serve the lost history. That is an explicit choice of the operator.

### 5.11 The PDP view

- **The view.**
  - The view is `{P, epoch, generation, fresh_pg_time, frozen, readAt}`. It is immutable and replaced as a whole, following the pattern of `core/engine/localstate.go`.
  - A singleflight poll reads `region_state` and `region_freeze` every 100 ms. `readAt` is the time of the last successful read.
- **No view goes back.**
  - A read that shows a lower position than the view holds is a store fault. The PDP keeps its view without confirming it and counts the read in `licetum_revision_regressions_total`. If the fault persists, the PDP refuses requests after S (`CONS02`).
  - Neither an epoch change (positions jump up) nor a generation switch (5.7) lowers a position.
- **One P per request.**
  - A request takes the view once. All evaluations of one `/evaluations` call, and one search page, use that view.
  - `context.licetum` is read only from the top-level `context` of `/evaluations`. The defaults are merged as whole objects, so an item with its own `context` would otherwise lose the token.
  - A `licetum` key inside an item's `context` is ignored. Because the name is reserved, it is never an attribute.
- **Gates.** These run before any cache:
  1. *Confirmation*: `now − readAt ≤ S`. This gate always applies, also under `freeze`. It keeps garbage collection safe (5.7) and catches an unreachable local store.
  2. *Staleness*: `now − T(P) ≤ S`, where `T(P) = fresh_pg_time`. A frozen region skips this gate.
  3. *`min_revision`*: the request waits until P reaches the asked revision, for at most `PDP__FRESHNESS_WAIT`.
- **`freeze` does not pin P.**
  - It turns off gate 2 only. The applier keeps applying whatever log arrives, and the view keeps moving.
  - If the home region is lost, nothing new arrives and P stays where it is, with all its rows.
  - A pinned P would lose its rows once garbage collection runs.

### 5.12 The PDP memory

- **The policy at P** (`core/policy`) is copy-on-write per stream:
  - `catalog`: the services and the slug index; the type graph, with the closures of `computeFlow` (`core/engine/typegraph.go`) computed ahead of time; the exported flags; the buckets; which types are synchronized;
  - `pep`: the clients, the key hashes and the key sets of the issuers;
  - `settings`;
  - the `svc:<id>` streams of the served services (`PDP__SERVICES`, all by default) and of their upstream sets U(S): rules indexed by literal `(subject_type, subject)` and by `subject_group_id`, rules without a literal subject (scanned), memberships, roles and items, access groups, the request attributes the rules read, and validity boundaries.

  Old versions of the policy are not kept. A search page is computed at the P of the request that asks for it, and its cursor is `(bucket, last key, digest of the request)`, so there is no `page_expired`.
- **The answer cache** (`lru.go`).
  - The key is made of: the service; the positions of `catalog` and of `svc:x` for every x in U(S); the subject `(type, id)`; the fingerprint of the attributes the applicable rules read; the sha256 of the questions; and `explain`.
  - Each entry records the position of every `data:<type>` stream whose rows it read. This covers every type read, including ancestor types of other services reached through exported links.
  - An entry is served only while every recorded position is unchanged in the current view. Today `Deps` of `cachedEntry` (`core/engine/cache.go`) does this with one data revision per service; the streams make it per type.
  - `Exp` and `Hold` keep their meaning (`hold_property_test.go`). An answer that reads the clock lives until the next minute (`untilNextMinute`).
  - Search pages are not cached. Their candidates go through the cached evaluation path.
- **The row cache.**
  - It maps `(key, ver) → row`. The rows of a version never change.
  - Beside it is an index from each key to its newest version at or below P. The index moves forward from `applied_keys` of every position the view passes. A stream marked `-1` drops the index entries of its type.
  - A warm decision reads nothing from the store (`CONS05`).
  - If a read returns a version older than the one the index names, the store has lost a newest version (5.8). The request is refused with 503, and `licetum_cql_version_regressions_total` counts it.
- **Verified PEP tokens** stay in memory until `exp`, and at most 60 s.

### 5.13 What a decision reads

Every read is as of the request's P: `SELECT … WHERE <key> AND ver <= P LIMIT 1`, through the row cache. Reading every partition at one position is what rules out torn states, where a newer lineage of one resource sits next to an older lineage of another (`ASOF01`). In PostgreSQL mode the same reads go to `resource_cache`, `resource_ancestor` and `resource_unresolved` in one `REPEATABLE READ READ ONLY` snapshot.

| question | reads on a miss |
|---|---|
| a type as a whole (`resource.id = "*"`) | none |
| one resource R | Round 1: `resource_v(R)`, giving existence, `active`, lineage, `unresolved_up` and meta. Round 2, in parallel: (a) `resource_v(E)` for each point anchor E of an applicable exception, when a point denial D lies in R's lineage; it shows whether D is in E's lineage and replaces `pairMasks`; (b) `resource_v` of R's ancestors whose type a `resource_where` rule names, for their meta, decided by `resourceMatches`; (c) for each `resource_from_property` rule, `attr_v` of the lookup type (its `buckets(type)` buckets, ADR 0002), or `resource_v(lookup type, value)` when `match_by = id`. Round 3: `resource_v` of the matches; their lineage names the anchors of the rule's type. |
| search/action | the reads of one resource; every level is decided from them in memory |
| search/resource | Only the asked type. When a whole-type allow reaches the type: pages of `type_member_v`. Otherwise: a k-way merge of `descendant_v` of the allow anchors, in `(bucket, ext)` order. Each candidate is decided again with the reads of one resource, in batches. At most `PDP__SEARCH_SCAN_BUDGET` (10 000) rows per page; a short page carries `next_token` (`AZ13`). |
| search/subject | pages of `subject_v`; each candidate goes through the cached evaluation path; at most 1000 subjects per page |

**The owner pattern.** Sometimes the lookup type of a `resource_from_property` rule is the rule's own type. Then the anchors are the matches themselves, and only matches in R's lineage matter. Instead of reading `attr_v`, the PDP reads `resource_v` of R's ancestors of that type and compares their name or attribute in Go.

**The read bound of `CONS05`.** A miss on one resource reads at most

```
1 + e + w + Σ over property rules (b + m)        partitions, in at most 3 rounds
```

where:
- e is the number of point exception anchors checked;
- w is the number of R's ancestors of the types that `resource_where` rules name;
- b is the number of `attr_v` buckets of the lookup type: `buckets(type)`, which ADR 0002 sizes by the size of the type; 1 with `match_by = id`, where the match is the resource itself and m is 0; or, for the owner pattern, the number of R's ancestors of that type;
- m is the number of matches of the lookup.

Only rules that apply to the caller and reach R's type count. Neither the size of the policy nor the size of the resource graph enters the bound. A lookup with more than `PDP__PROPERTY_MATCH_LIMIT` (1000) matches is not decided: the evaluation answers `decision: false` with `context.error`, and a metric counts it. `CONS05` computes the bound from the test's policy and graph and compares it with the counted reads.

**Contracts this section adds.** They go into `contracts.yaml` as planned, phase P6:
- `APP03`: with garbage collection on, a PDP that confirmed its view within S finds, for every key it reads, the newest version at or below its P. This must hold under kill tests and during a burst of catch-up after a partition.
- `REC02` (10.4): `reconcile` reports a region that lost the newest version of a key on two of three replicas.
- `ASOF04`: an installation switched from `postgres` to `cql` skips no position and answers as before at equal positions.

`OPS06` gains the roles of 5.4: the operator role, and an applier that runs no DDL.

## 6. Consistency and failure behaviour

This section defines how fresh the data behind a decision must be, how far back that data may lag, and what each process does when a dependency fails. It covers both adapters of the read model:

- `postgres` is the default. The PDP reads the primary, or a streaming standby in its own region.
- The CQL adapter uses a ScyllaDB or Apache Cassandra 5 cluster. P0.5 decides which one.

Where the adapters behave differently, the text says so. Each guarantee is held by a contract, named in parentheses.

This replaces three parts of today's behaviour:

- `POLICY__MAX_STALENESS` defaults to `0`, which means no limit (`core/config/config.go:314`). A process cut off from PostgreSQL keeps answering from its copy for as long as the cut lasts.
- The staleness of a replica is measured by comparing its revisions with the primary's (`measureReplica`, `core/engine/replica.go:138`). Every region therefore needs credentials for the primary.
- Read-your-writes falls back to the primary (`at_least_as_fresh`, `REVISION__WAIT_MAX`, `consistency=higher`).

### 6.1 Definitions

- **Revision `r`.** One global position per committed transaction that changes data a decision reads. Revisions have no gaps and follow commit order within an epoch. The jump of the failover procedure (6.8) keeps them increasing from one epoch to the next. The token a client sees is `<epoch>.<r>`.
- **`W_X`.** The largest `r` such that every change up to `r` is applied in region X.
  - CQL adapter: the `region_state.applied_position` of the region.
  - `postgres` adapter: the revision visible in the server that the region's PDPs read.
- **`P`.** The position of one PDP's view, with `P ≤ W_X`.
  - All the evaluations of one request use one `P`. So does one page of a search.
  - Successive pages of a search may use different positions (6.4).
- **`T(P)`.** The freshness time of `P`, on PostgreSQL's clock. Every commit acknowledged before `T(P)` has a revision `≤ P`, so the state at `P` reflects it. Section 6.2 says where `T(P)` comes from.
- **Staleness.** `now − T(P)`, where `now` is the PDP's clock.
- **`S`.** `POLICY__MAX_STALENESS`, 60 s by default.
  - Loading the configuration refuses `0` and negative values (FC05). Exceeding `S` is allowed only through the audited `freeze` (6.5).
  - The chart's `policyMaxStaleness` is empty today, which means no limit. It now defaults to `60s`.
- **`ε`.** The largest difference between the clocks of the PDPs and the clock of the PostgreSQL primary.

### 6.2 The heartbeat and `T(P)`

The revision alone does not give `T(P)`. When nothing is written the revision stays the same. A region that stopped receiving changes then looks exactly like a region that has nothing new to receive. The heartbeat removes that ambiguity.

**CQL adapter**

- Every second, the publication leader of `licetum-writer` (the `project` role) runs the statement of 4.9.3, which updates the `heartbeat` row and returns `pg_time` (`statement_timestamp()`), `epoch` and `position`. It writes the result to `log_heartbeat((epoch, day), pg_time) → position`.
- Because one statement reads both values under one snapshot, every commit acknowledged before `pg_time` has a revision `≤ position` (PUB03).
- The property does not depend on how far publication has got. A heartbeat whose position is not yet published is not used until the applier reaches that position.
- After applying a position, the applier sets `region_state.fresh_pg_time` to the newest `pg_time` whose position it has applied.
- The applier writes `fresh_pg_time` after the position it covers, with the heartbeat's `pg_time` as the write timestamp (5.3, 5.6). A `LOCAL_QUORUM` read that sees a given `fresh_pg_time` therefore also sees a position at or above that heartbeat's position, and `fresh_pg_time` keeps moving while no revision is written.
- The PDP takes `P` and `T(P)` from the same read of `region_state`.

**`postgres` adapter on the primary**

- The poll that reads `(epoch, revision)` from `revision_state` also reads `statement_timestamp()`, in the same statement.
- Once the view has folded the outbox up to that revision, `T(P)` is that timestamp. Every commit acknowledged before the statement began is visible to it.
- A PDP that reads the primary therefore needs no heartbeat, and stopping the writer does not close it.

**`postgres` adapter on a standby**

- A standby's clock says nothing about what it has replayed.
- Every second, the publication leader updates the one-row table `heartbeat(id = 1, epoch, position, pg_time)` (4.3) with the statement of 4.9.3. The row reaches the standby through WAL like any other commit.
- The PDP reads `revision_state` and `heartbeat` in one statement. A snapshot that contains the heartbeat's commit contains every commit before it, so `T(P)` is the `pg_time` of that row.
- `heartbeat` is not a projected table. Updating it takes no revision and writes no outbox row.
- A standby replays commits in the primary's commit order, and revisions follow commit order. A standby snapshot is therefore a prefix of the revisions, just as a snapshot on the primary is.

**Both adapters.** The PDP reads the revision inside the snapshot it builds its view from. Today `loadSnapshot` is given a revision that was read before its transaction began (`core/engine/localstate.go:299`), and a writer can commit in between.

### 6.3 The hard bound and the revocation SLO

**The hard bound**

A PDP serves an answer at its own time `τ` only while `τ − T(P) ≤ S`. Take a revocation committed at PostgreSQL time `t`:

- If `t ≤ T(P)`, the revocation is in the state at `P`.
- Otherwise the answer was served before `T(P) + S < t + S` on the PDP's clock. That is before `t + S + ε` on PostgreSQL's clock.

So from `t + S + ε` on, no region serves an answer that ignores the revocation, whatever has failed (REV03).

**Typical times**

| What is revoked | When every PDP enforces it |
|---|---|
| Any change, any region, any failure | `t + S + ε`, the hard bound above. |
| A policy change, home region | p99 ≤ 1 s (REV01).<br>CQL adapter: the `pg_notify` sent in the writing transaction is delivered on commit and wakes the publication leader. The leader reads `outbox WHERE revision > last` and writes the log (about 50 ms). Then the applier poll (100 ms) and the PDP poll (100 ms) follow. If the leader was reconnecting and missed the notification, its periodic tick (1 s) picks the change up.<br>`postgres` adapter: the PDP poll (100 ms) and the fold of the outbox row. |
| A policy change, other regions | p99 ≤ 5 s (REV02, decision D3). About 2 s is expected: the replication of `licetum_log` or of WAL, then the same polls. |
| A resource switched off or moved at its source | The same path, counted from the commit of the synchronization, plus up to the source's polling interval (`refresh_interval`, or `refresh_interval_max` for a quiet source, 4.9.1). A refresh from the admin API starts the synchronization at once. The hard bound counts from the commit in PostgreSQL, not from the change at the source. |
| A narrower `inherit`, or a removed type link | At the commit of `RebuildComponent`, which applies the new structure together with the closure. The admin write answers `{revision, effective: false, job}`, and the job reports the revision at which the change is in force. |
| An administrator's role binding | On the next admin request. Bindings are read from PostgreSQL in the request and checked again in the write's transaction. |
| A PEP client or API key | Like a policy change, through the `pep` stream (PEP02). |

The bound needs clock discipline (NTP or chrony) on the nodes that run PDPs and PostgreSQL. An alert fires when `ε` may exceed 1 s (6.11).

The bound also makes availability depend on one shared signal. If no heartbeat is applied for `S`, every region that depends on it closes. A PostgreSQL failover therefore has to finish within `S` (6.8), and the publication leader runs with a standby replica.

### 6.4 Read-your-writes and search paging

**The token**

- Every admin write answers with `Licetum-Revision: <epoch>.<r>`.
- A refresh of a type's resources answers with the revision of its synchronization, once that synchronization is applied.
- Every PDP answer also carries `Licetum-Revision`: the position the answer was computed at.

**Passing the token**

- The PEP passes the token as `context.licetum.min_revision`, a string `"<epoch>.<r>"`. A bare number means the current epoch.
- In `/evaluations`, `context.licetum` (the token and `explain`) is read only from the top-level `context`. An evaluation's own `context` replaces the default as a whole object, so a token in the defaults would be lost for that evaluation. One request has one `P` anyway.
- A `licetum` key in an evaluation's own `context` is ignored (5.11, 7.4).
- `licetum` is the only reserved context name. `model.ValidContextName` refuses it as an attribute name, and rule conditions cannot read it.

**Waiting for the token**

- When `P` is below the token, the request waits for the view to reach it. The wait lasts at most `PDP__FRESHNESS_WAIT` (1 s) or until the request's deadline, whichever comes first.
- Waiting requests share the view's refresh: one process has one waiter, however many requests are waiting.
- If the position is not reached in time:
  - `/evaluation` and the searches answer `503` with `Retry-After: 1` and `Licetum-Error-Code: revision_not_reached`.
  - `/evaluations` answers `decision: false` with `context.error {status: 503, message}` for each evaluation.
- No answer below the token is ever computed or served from a cache. There is no fallback to another store (CONS01).
- `consistency=higher` is removed, because a region cannot read the primary.

**Tokens from an older epoch**

- A token from an older epoch is compared by `r` alone. The new epoch starts above every revision of the old one, so the token is satisfied as soon as the PDP has moved to the new epoch.
- A write lost in a failover is therefore "satisfied" by a state that does not contain it. The integration guide says so.

**Position of an answer in the `postgres` adapter**

The position of an answer is the position of the data it read:

- A cache miss reads the resources in one `REPEATABLE READ READ ONLY` transaction and reads the revision `Q` inside it. The decision then uses the view's policy at `Q`.
- When `Q > P`, the decision waits for the fold to reach `Q`.
- When `Q < P` (a pooled address that spans standbys at different positions), the read is retried for at most `PDP__FRESHNESS_WAIT` and then refused with 503 (5.9). The view keeps no older policy versions (5.12).
- `Q` is what the token and the gate are checked against.

**Search paging**

- Each page is computed at the `P` of the PDP that serves it.
- The cursor is `(bucket, last key, digest of the request)`. There is no snapshot across pages, no stored policy version for paging, and no `page_expired` error.
- Pages follow key order. A resource that is in the result for the whole paging is returned exactly once. A resource added or removed during the paging may or may not appear.
- A later page can be computed at a lower position than an earlier one if another PDP serves it. A client that needs its write reflected in every page passes `min_revision` with each page.

### 6.5 The staleness gate, fail-closed and freeze

**The gate**

- The gate runs before the answer cache. While staleness exceeds `S`, no answer is served, whether cached or computed.
- Within `S`, an in-memory answer may be served after its `Exp` until its `Hold`, the next validity boundary. Today `cachedEntry.stale` (`core/engine/cache.go:143`) allows this only while the view is degraded; in v2 it applies within `S`, and the gate still applies to it (CONS03).

**Beyond `S`**

- `/evaluation` and the searches answer `503`, `Licetum-Error-Code: policy_unavailable`, `Retry-After: 1` (FC01, FC02).
- `/evaluations` answers `decision: false` with `context.error {status: 503, message}` for every evaluation.
- `/readyz` on the ops port answers 503, and the global load balancer moves the traffic to another region.
- A PEP must treat an error as a deny. The integration guide says so.

**Recovery**

- Recovery needs no operator. The log (or WAL) is applied in order and without gaps. Every revocation made while a region was cut off is therefore applied before any grant made after it (FC03).
- A view never goes back to an older position (CONS02). Two resets are accounted for: a new epoch and a new generation. A read that shows a lower position outside them is counted in `licetum_revision_regressions_total` (kept from today) and drops the answer caches; the view keeps its position unconfirmed, and the PDP refuses requests after `S` if the fault persists (5.11).

**Freeze**

Freeze is the operator's answer to losing the home region. It lifts the gate in one region so that the region keeps serving past `S`.

- **What freeze changes.** Freeze turns off only the gate. Nothing else stops:
  - the applier keeps applying whatever log arrives;
  - the PDPs keep moving `P` forward.

  A frozen region therefore still takes every change it receives. A frozen view never pins a position whose versions the MVCC GC may remove (6.6).
- **CQL adapter.**
  - `licetum-writer freeze --dc X --reason …` writes `region_freeze` (`frozen`, `frozen_at`, `frozen_by`, `reason`), plus a `freeze_log` row. It writes them in the region's own keyspace, because the home region may be gone.
  - The command runs with an operator CQL role. The writer's role only writes the log.
  - `frozen` is written with the operator's wall-clock timestamp. `unfreeze` clears it.
- **`postgres` adapter.** A standby cannot be written, so the freeze is a setting of the region's PDP deployment, applied by a rollout and logged in the same way.
- **While a region is frozen:**
  - `licetum_frozen` is 1;
  - each PDP logs one line a minute;
  - `/readyz` does not fail on staleness;
  - an alert keeps firing (FC04).

  Cache misses still need the store: when the store cannot be read, they answer 503.
- **Default.** Without a freeze, a region fails closed (decision D2). Only an operator freezes a region; nothing does it automatically.

### 6.6 Correctness requirements on the CQL store

The CQL read model is correct only if a read at `LOCAL_QUORUM` returns the newest version `≤ P` of every key.

- The applier writes, and the PDP reads `region_state`, `policy_*`, `resource_v` and `subject_v`, at `LOCAL_QUORUM` with RF 3 in the DC. Every read therefore overlaps every acknowledged write, provided no two replicas of a partition have lost that write.
- The listing tables (`descendant_v`, `type_member_v`, and `attr_v` when it lists search candidates) may be read at `LOCAL_ONE`. Every candidate is decided again from `resource_v` at `LOCAL_QUORUM`, so a stale listing can miss a resource but cannot grant one. `attr_v` is read at `LOCAL_QUORUM` when it finds the anchors of rules (5.5).

The proviso fails in two cases:

- a node is replaced without repair;
- a node was down longer than `max_hint_window` and its hints expired.

If two of three replicas then lack the newest version, `LOCAL_QUORUM` returns an older one: a resource that is still active, or an old lineage. The decision can then allow what is denied. Repair is therefore part of correctness, not only of durability:

- **Repair is required.**
  - A replaced node joins through repair-based node operations where the store has them. Otherwise a repair of the region's keyspaces follows the replacement.
  - A node that was down longer than `max_hint_window` is repaired when it returns.
  - Scheduled repair of `licetum_log` and of every region keyspace runs at least weekly, and within the tables' tombstone grace period.
  - The runbooks (P7) cover all three cases.
- **A nightly reconcile in every region is required.**
  1. `licetum-writer reconcile --dc X` runs nightly for every region X as a CronJob in the home region, with the role `licetum_reconcile` (5.4). It reads PostgreSQL in one `REPEATABLE READ READ ONLY` transaction at a revision c read inside it, waits until X has applied c, and reads X as of c at `LOCAL_QUORUM`, as a PDP reads it. No region needs PostgreSQL credentials for this.
  2. It compares the canonical hash of every policy stream and, per type, an XOR hash of `(ext, active, name, meta, lineage, unresolved_up)`. Keys changed after c are left to the next run (5.8).
  3. A difference increments `licetum_projection_drift_total{dc, stream}`, raises an alert, and is repaired with `rebuild --stream`.

  Reconcile also runs on demand, for example after a node replacement. A region with no successful reconcile for 26 h raises an alert.
- **The MVCC GC is off at first.**
  - P6 ships the read model append-only. No version is deleted, and generations are rotated by hand (`apply --rebuild`, 6.7).
  - The GC of superseded versions is enabled only after the APP and ASOF contracts pass under kill tests.
  - Once enabled, the GC never deletes the newest version of a key.
  - Configuration validation refuses an `MVCC__RETENTION` (5 min) below `2 × (POLICY__MAX_STALENESS + HTTP__REQUEST_TIMEOUT)` (5.7). This keeps the versions at `P` available to any PDP that is allowed to serve.
  - A superseded version brought back by a lost tombstone changes no read at `P`. For `P` at or above the next version, the newest version `≤ P` is still the next one. For lower `P`, the version brought back is the correct answer at `P`.
  - A view whose versions were collected finds no version of the key. It answers as for an unknown resource, which is a deny, and never with an older version.

### 6.7 Rebuilding the read model

- **A new region.**
  - CQL adapter:
    1. The DC is added to the replication of `licetum_log`. `ALTER KEYSPACE` does not copy data already written, so the new DC's nodes are rebuilt from an existing DC (`nodetool rebuild`) before its applier starts.
    2. The region keyspaces are created in advance by the migrate job of the home region. A schema change needs agreement across the whole cluster, which a cut-off region cannot get.
    3. `apply --dc <dc>` starts from the stream checkpoints and the tail of the log.
  - `postgres` adapter: a new standby is added. Its PDPs build their views from one snapshot each.
- **Loss of a region keyspace, or a change of format.**
  1. `apply --rebuild` builds generation `g+1` from the checkpoints and the tail of the log while `g` keeps serving.
  2. It writes `region_state.current_generation`.
  3. After `GENERATION__DRAIN` (1 h) the home migrate job drops `g` and creates the next spare; the applier runs no DDL (5.3, OPS06).

  The migrate job creates the next generation's keyspace in advance. With `auto_snapshot` on (the default), a drop leaves a snapshot behind, so the job clears it. ASOF03 holds that a generation built this way equals one applied incrementally.
- **Loss of `licetum_log`.**
  1. `licetum-writer rebuild --all` reads every stream in one `REPEATABLE READ` transaction, with the revision read inside it.
  2. It writes a checkpoint of every stream at that revision, as a reset of the chains.
  3. Publication continues from the next revision.

  Writers are not blocked: the revision read inside the snapshot is exact, and later rows are published as usual.
- **Point-in-time recovery of PostgreSQL from a backup older than the log's TTL (14 days).** An `epoch_start` (6.8) cannot reconcile this case, because the keys of the other history have expired from the log. The runbook is `rebuild --all`, then a new generation in every region.
- **Moving an installation from `postgres` to CQL.** This is a documented `rebuild --all` with its own contract. In the `postgres` adapter nothing publishes outbox rows, so they are removed by age.

### 6.8 PostgreSQL failover and epochs

A failover of the primary is the only failure that can take back changes the regions have already applied. A promoted standby may lack the last commits of the old primary, including revisions that were already published.

**Procedure**

1. **Detection.** At start and while it publishes, the publication leader compares the log with the outbox. Either of the following means divergence:
   - a `log_entry(epoch, r)` that differs from outbox row `r` (its header, or the sha256 of a stream);
   - a revision in PostgreSQL below the head of the log.

   Publication stops and never overwrites the log. The failover then runs, automatically or through `licetum-writer failover`.
2. **Jump.** `p0 = max(revision in PostgreSQL, head of the log) + 1 000 000` is written to `revision_state` with `epoch + 1`. The jump is today's `db.DefaultFailoverJump` (`core/db/revision.go:51`). Then `log_epoch` is written `USING TIMESTAMP = epoch`.
3. **`epoch_start`.** An `epoch_start` entry is published at `p0`, with `base_position` set to the last position the two histories share. It carries the current PostgreSQL state of every key changed after `base_position` in either history: in the old entries of the log or in the new outbox.
4. **Convergence.** Appliers apply the entry over their state and converge on the new primary's state. FO01 checks that no later answer reflects the lost write.

**`postgres` adapter**

- The PDPs see the epoch change at their next poll. They rebuild the view from a new snapshot instead of folding, because their folded state may hold commits the new primary lost. Answers from the old epoch are dropped.
- A regional standby may have replayed WAL from the old primary past the point where the new primary's timeline begins. It cannot follow the new timeline until it is rewound (`pg_rewind`) or cloned again. Until then its heartbeat does not move, and its region closes after `S`.

**Preconditions**

- **Fencing.** The old primary is fenced (Patroni or equivalent). Without fencing, its later writes conflict with the log by hash and stop publication. They are never written over silently.
- **A synchronous standby in the home region, or S derived from the failover time measured without one.** A synchronous standby means `synchronous_commit = on`, with the HA standby listed in `synchronous_standby_names`.
  - With only an asynchronous standby, `epoch_start` must carry every key the lost commits touched. After a large synchronization that can be whole types, which can take longer than `S` and close every region.
  - A synchronous standby makes the loss rare but not impossible. A commit whose wait for the standby is cancelled stays committed on the old primary only, so the epoch procedure remains necessary.
- **`S` must exceed the measured failover time.** That time covers promotion, the writer's leader reconnecting, and the first heartbeat applied in every region. The failover drill in the runbook, and FO01 nightly, measure it. A deployment where it exceeds `S` raises `S`; otherwise every failover would close every region.

### 6.9 Time

- Validity windows of rules and memberships, and the `hour` and `weekday` conditions, are evaluated on the PDP's clock.
- The `Hold` of an answer is the exact distance to the next boundary; its TTL is that distance capped at `ResultCacheTTL` (60 s), with a floor of 1 s (`validityUntil`, `core/engine/permission.go:494`; DEC14).
- Clock skew between regions moves a boundary by the size of the skew. This is documented. The same clock discipline that bounds `ε` bounds it.

### 6.10 Failure matrix

| Failure | What happens | What the operator does |
|---|---|---|
| A region is cut off from the home region (CQL: no log arrives; `postgres`: the standby gets no WAL) | `T(P)` stops moving. The region serves for up to `S`, then fails closed. Other regions are not affected. | Restore the link. If the home region is lost, `freeze`. Recovery is automatic. |
| The home DC of the CQL store is unavailable | The publication leader writes through a session in another DC; entries are immutable and checked by hash. Home PDPs fail `/readyz`, serve warm answers for up to `S`, and answer cold misses with 503. | Restore the DC, then repair it (6.6). |
| The PostgreSQL primary is down for longer than `S` | Nothing commits and no heartbeat moves. Every region closes, in both adapters. | Fail over within `S` (6.8). In a disaster, `freeze`. |
| The publication leader is stopped for longer than `S` and no replica takes over | CQL: every region closes. `postgres`: regions on standbys close; PDPs on the primary are not affected. | Run at least 2 replicas of the `project` role. The heartbeat-age alert fires at 10 s. |
| The applier of region X is stopped | Region X closes after `S`. Other regions are not affected. | A second applier replica (Kubernetes Lease) takes over. Duplicate appliers are harmless. |
| A PDP cannot read its local store | `/readyz` fails. Warm answers for up to `S`, cold misses 503 (FC01). | Restore the store. |
| A standby lags or its replay is paused (`postgres`) | The heartbeat ages with it, and the region closes after `S`. A replay conflict cancels a PDP read, which is retried. | Set `hot_standby_feedback = on` on the standbys the PDPs read. |
| A source does not answer | Its data stays. `attempts` goes up by 1, and a cluster-wide backoff applies. The gate is not affected. Revocations made at the source wait for the source to answer. | Fix the source. |
| A log entry cannot be applied (malformed image) | It is retried up to 10 times, then `licetum_projection_stuck` is set and an alert fires. Regions close after `S`; none serves a policy with a hole in it. | `rebuild --stream`. |
| Two replicas of a CQL partition lost the newest version | `LOCAL_QUORUM` may return an older version, which fails open. | Prevented by repair (6.6). Found by the nightly reconcile. |
| The PostgreSQL primary fails over | See 6.8. | Fence the old primary, check the epoch alerts, and run the drill to measure the time. |
| Clocks drift | The hard bound and the validity boundaries move by the drift. | Fix time sync. An alert fires at 1 s. |

### 6.11 Metrics and alerts

The hard bound is only as good as the signals that show it holds. The chart gets a `PrometheusRule` template in `deploy/helm/licetum`, next to `servicemonitor.yaml`, which today is its only monitoring object. Its thresholds are derived from `policyMaxStaleness`.

`ε` is not measured from the heartbeat, because the heartbeat's age mixes clock skew with publication and apply lag. It is read from the nodes' clock discipline instead: node_exporter's timex collector, or chrony's own exporter.

**Metrics**

| Metric | Process | Meaning |
|---|---|---|
| `licetum_policy_staleness_seconds` | pdp | `now − T(P)`. Replaces `licetum_snapshot_age_seconds`. |
| `licetum_view_position`, `licetum_view_epoch` | pdp | `P` and its epoch. |
| `licetum_gate_refused_total{reason}` | pdp | Answers refused with `policy_unavailable` or `revision_not_reached`. |
| `licetum_frozen` | pdp | 1 while the region is frozen. |
| `licetum_revision_regressions_total` | pdp | Kept from today: a position that went backwards outside an accounted reset. |
| `licetum_cql_version_regressions_total` | pdp | Reads that returned a version older than the one the row cache's index names: the store lost a newest version (5.12). |
| `licetum_heartbeat_age_seconds` | writer | Time since the publication leader last wrote a heartbeat. |
| `licetum_outbox_rows{state}`, `licetum_outbox_oldest_pending_seconds` | writer | Outbox rows that are `pending` or `published` (the latter awaiting retention), and the age of the oldest pending row (the publication lag). |
| `licetum_publish_conflict` | writer | 1 while publication is stopped on a hash conflict. |
| `licetum_sync_queue_depth{queue}` | writer | Synchronizations waiting in each queue. |
| `licetum_apply_lag_revisions{dc}`, `licetum_apply_lag_seconds{dc}` | applier | Head of the log minus the applied position, and how long the oldest published entry not yet applied has waited. |
| `licetum_projection_stuck{dc}` | applier | 1 while an entry has failed 10 times. |
| `licetum_mvcc_versions_total{table}`, `licetum_mvcc_range_deletes_total{table}`, `licetum_generation_superseded_ratio{dc}` | applier | Versions and range tombstones written, and the share of `gone` and superseded rows that drives generation rotation. |
| `licetum_projection_drift_total{dc, stream}`, `licetum_reconcile_last_success_timestamp_seconds{dc}` | reconcile | Differences found, and the time of the last successful reconcile. |
| Partition sizes, tombstones per read | CQL store | The store's own large-partition and tombstone metrics. |
| `node_timex_sync_status`, `node_timex_maxerror_seconds`, `node_timex_offset_seconds` | node | The clock discipline that bounds `ε`. |

**Alerts**

| Alert | Condition | Severity |
|---|---|---|
| `LicetumStalenessHigh` | `licetum_policy_staleness_seconds > S/2` for 30 s | warning |
| `LicetumFailingClosed` | `rate(licetum_gate_refused_total{reason="policy_unavailable"}[1m]) > 0` | critical |
| `LicetumHeartbeatStale` | `licetum_heartbeat_age_seconds > 10` | critical: every region closes at `S` |
| `LicetumPublishLag` | `licetum_outbox_oldest_pending_seconds > 5` for 1 min | critical |
| `LicetumApplyLag` | `licetum_apply_lag_seconds > 5` for 2 min | warning (REV02) |
| `LicetumProjectionStuck` | `licetum_projection_stuck == 1` | critical |
| `LicetumPublishConflict` | `licetum_publish_conflict == 1` | critical: a failover is in progress |
| `LicetumProjectionDrift` | `increase(licetum_projection_drift_total[1d]) > 0` | critical |
| `LicetumReconcileMissing` | `time() − licetum_reconcile_last_success_timestamp_seconds > 26h` | warning |
| `LicetumRegionFrozen` | `licetum_frozen == 1` | warning, firing for as long as the freeze lasts |
| `LicetumRevisionRegression` | `increase(licetum_revision_regressions_total[10m]) > 0` | warning |
| `LicetumClockUnsynchronised` | `node_timex_sync_status == 0` for 5 min, or `node_timex_maxerror_seconds > 1` | critical: `ε` is part of the hard bound |
| `LicetumSyncBacklog` | `licetum_sync_queue_depth` above the chart value for 10 min | warning |
| Store alerts | The CQL store's own thresholds for large partitions and tombstones | warning |

## 7. Public API: AuthZEN 1.0 only

The public surface of Licetum is the OpenID AuthZEN Authorization API 1.0. `licetum-pdp` serves it, one service per path. A policy enforcement point (PEP) authenticates as itself, and the person it asks about is the `subject` of the request. Licetum extensions go only into the places the specification leaves open: `context` in requests and responses, the `page` object, and response headers. Licetum adds no endpoints, query parameters or request headers.

This breaks the public API of the released v0.1.0, and v2 offers no compatibility layer.

- **Removed endpoints.** The check endpoints of v0.1.0 are removed: `GET /service/{id}/permission`, `POST /service/{id}/check`, `POST /service/{id}/on_behalf/check` and `POST /service/{id}/on_behalf/permission`.
- **Changed meaning of the bearer.** The two AuthZEN endpoints of v0.1.0, `/service/{id}/access/v1/evaluation` and `/evaluations`, keep their paths, but the bearer token means something else:
  - In v0.1.0 the token was the person's own, checked against the IdP of the service (`authenticateCaller`).
  - In v2 the token is the PEP's credential.
- **Release notes.** The v2 release notes state both breaks, next to the fact that a v0.1.x database is not upgraded.

### 7.1 Routes and ports

The public port `PDP__ADDR` (default `:8001`) serves exactly these routes:

```
GET  /.well-known/authzen-configuration/service/{slug}
POST /service/{slug}/access/v1/evaluation
POST /service/{slug}/access/v1/evaluations
POST /service/{slug}/access/v1/search/subject
POST /service/{slug}/access/v1/search/resource
POST /service/{slug}/access/v1/search/action
```

- **Probes and metrics.** `/healthz`, `/readyz` and `/metrics` move to the ops port `PDP__OPS_ADDR` (default `:9090`). Today they share the port with the API (`server.NewServer`).
- **API docs.** The PDP does not serve `/docs` or `/openapi.json`.
  - `licetum-pdp openapi` prints the schema.
  - The schema stays committed, as `core/schema/check.yaml` is today (renamed with the process), and is checked for drift (API01).
  - The admin API keeps its docs.
- **Route table.** AZ10 compares the route table of the public port with a snapshot. A route added to the public port fails CI.

### 7.2 The service is named by its slug, which never changes

The path names the service by its slug only. The uuid form is removed; today `App.ServiceIDByRef` accepts either.

The PDP identifier is `PDP__PUBLIC_URL + "/service/" + slug`. PEPs keep it in their configuration, and the specification requires the `policy_decision_point` in the metadata to be byte-identical to it. The slug therefore stays fixed once the service exists and is never given to another service:

- **No rename.** `PATCH` of a service no longer accepts `slug`. Today `model.ServiceUpdate` has a `Slug` field (`core/model/service.go`).
- **No reuse.** Uniqueness also covers deleted services.
  - Today `uq_service_slug` is a partial index (`WHERE deleted_at IS NULL`, `core/db/migrations/00001_init.sql`), so the slug of a deleted service can be taken again.
  - Schema v2 makes it a plain unique constraint.
- **Renaming a service** means creating a new service, then exporting and importing the policy document.

### 7.3 Metadata

- **Location.** The document is served at `GET /.well-known/authzen-configuration/service/{slug}`. This is the multi-tenant form of the specification: the well-known string goes between the host and the path of the PDP identifier.
  - It needs no credential.
  - It sends `Cache-Control: public, max-age=300`, as today.
- **`policy_decision_point`** is `PDP__PUBLIC_URL + "/service/" + slug`.
  - `PDP__PUBLIC_URL` is required. It must use `https` and have no path, query or fragment.
  - It must be byte-identical to the URL the PEPs use, usually the global GeoDNS name.
  - Today `HTTP__PUBLIC_URL` is optional (without it the metadata is a 404 `authzen_metadata_disabled`) and accepts `http`.
- **Endpoints.** The document lists the five endpoints of §7.1 (AZ05).
- **Capabilities.** `capabilities` lists `urn:licetum:whole-type-resource-id`, `urn:licetum:context:min-revision` and `urn:licetum:context:explain`.
  - The specification defines `capabilities` as a list of registered IANA URNs, and these three are not registered.
  - This is a documented deviation. A PEP that does not know them is not affected.
- **Unknown slug.** It gets a 404. This lets anyone list slugs through the metadata, which is an accepted risk: a slug is not a secret, and the access routes answer an unknown service with the same 403 as any other refusal (§7.8).

### 7.4 Requests

Required fields depend on the endpoint, as the specification defines them (AZ01):

| Endpoint | `subject.type` | `subject.id` | `resource.type` | `resource.id` | `action.name` |
|---|---|---|---|---|---|
| `evaluation`; each item of `evaluations` after defaults | required | required | required | required (`"*"` = the type as a whole) | required |
| `search/subject` | required | ignored | required | required | required |
| `search/resource` | required | required | required | ignored | required |
| `search/action` | required | required | required | required | `action` is ignored |

A missing required field is a 400. The 422 that Huma produces is still mapped to 400 for the `AuthZEN` tag (`core/server/server.go`). An ignored field may be present, and it has no effect, including on the page digest (§7.6).

**Fields**
- **`subject.type`** has 1–64 characters. `group` is reserved and is a 400.
- **`subject.id`** has 1–256 characters, no control characters, and no `group:` prefix (the internal identity string of a group).
  - Today these checks run in `engine.PrepareDelegated`, only for trusted clients.
  - From v2 they apply to every request.
- **`resource.type`** is the name of a type of the service in the path.
- **`resource.id`** is the external id of a resource.
  - `"*"` means the type as a whole. This covers whole-type questions and capability types without a synchronizer.
  - A source that reports `*` as an external id has that resource dropped, and the drop is counted in the synchronization metrics.
- **`action.name`** is an access level.
- **Other properties.** `resource.properties` and `action.properties` are accepted and ignored in 1.0.
- **Unknown fields** are ignored, as the specification requires.
- **Body size.** The body is limited to 1 MiB.
- **Unknown type or resource.** The answer is `decision: false`, not an error (AZ11). With explain, `reason_admin.decision` is `type_not_found` or `resource_not_found`.

**`context`**
- `context` is any JSON object.
- **Attributes.** A top-level key becomes a request attribute when all of these hold:
  - the key is not `licetum`;
  - the value is a scalar or a list of scalars;
  - the name is valid for an attribute (`model.ValidContextName`).
- **Ignored keys.** Other keys are ignored, and a condition that reads one sees a missing attribute (`on_missing_attribute` then decides). This covers objects, lists that contain objects or lists, and names no condition can use.
  - Today such a context is refused with 400 `context_value_not_scalar` (`RequestContext.Validate`).
  - AZ06 covers the new behavior.
- **Bounds.** The bounds stay as today and are a 400: at most 64 attributes, strings of at most 1024 characters, lists of at most 64 items.
- **Reserved name.** `licetum` is the only reserved name. `ValidContextName` refuses it in rule conditions, so no rule can read it.
- **`explain` is no longer reserved.** Today `authzenAttributes` drops it; in v2 it is an ordinary attribute.

**`/evaluations`**
- **Size.** At most 100 items.
- **Defaults.** The top-level defaults are merged one whole object at a time: an item's own `context` replaces the top-level one completely, as the specification says ("overrides the corresponding top-level default").
- **`context.licetum`** is read from the top-level `context` only and is ignored inside an item. It governs the whole request, which is answered at one position.
- **Semantics.** `options.evaluations_semantic` is `execute_all` (the default), `deny_on_first_deny` or `permit_on_first_permit`.
  - The answers have the shapes the specification gives.
  - The evaluation that stops a `deny_on_first_deny` batch carries `context {code: "200", reason: "deny_on_first_deny"}`, as in the specification's example (`applySemantic` and `responseOf` are kept).
- **Short-circuit is lazy.**
  - Today `evaluate` decides every item and `applySemantic` cuts the answer afterwards (`core/server/authzen.go`).
  - From P2, under a short-circuit semantic, the items are decided in order, and nothing after the stopping item is computed (AZ03).
  - `execute_all` may decide items in parallel.
- **Shared preparation.** Items with the same `subject`, `subject.properties` and `context` share one preparation (identity, applicable rules), as the current grouping does.
- **Undecidable items.** An item that cannot be decided (a field missing after defaults, an invalid subject) gets `decision: false` with `context.error {status, message}`.
- **Whole-request failures.** The request fails as a whole only for errors that concern the whole payload: malformed JSON, more than 100 items, authentication, authorization.
- **Without `evaluations`.** If `evaluations` is absent or empty, the request behaves as `/evaluation` and the answer is `{decision, context?}`.

### 7.5 Who the subject is

**Trust model.** A PEP bound to service S may assert any `subject` and any `subject.properties` for S.
- This is the specification's trust model ("the PDP must trust the PEP"), and it replaces the verified end-user token.
- As a result, a predicate written for claims an IdP had verified now reads what the PEP sends.
- The binding is narrow: one service and a list of operations per PEP client (§7.10).

**Properties.** `subject.properties` are the subject's attributes on every request, not only for trusted clients as today.
- Renames land in P3 with schema v2:
  - conditions with `source: claim` become `source: subject`;
  - `resource_from_claim` becomes `resource_from_property`, with the field `property`.
- **Bounds.** At most 100 keys and 16 KiB encoded, as in today's `PrepareDelegated`; beyond that is a 400.
- **Comparison.** Values are compared as `PyStr` renders them, as claims are today.

**`subject.id` and `subject.type` are not properties.**
- A rule matches them only through its literal target: `subject_type` (NULL matches any type) and `subject`.
- Conditions read `subject.properties` only.
- One compatibility default stays. If `subject.properties` has no truthy `preferred_username`, it is set to `subject.id`, as `PrepareDelegated` does today. Rules written for v0.1 read `preferred_username` to identify a person, because `SubjectFromClaims` took it before `sub`.

**Identity domains.** Every service declares an identity domain: `service.identity_domain`, a short name, `default` unless set. The domain is the namespace in which the service's PEPs name subjects, and it fixes the meaning of the properties its rules read.
- **Why it matters.** Within one service, the rules and the PEP agree by construction. Across services they need not:
  - Through an exported type, the rules of service A decide on the resources of service B (see the section on cross-service type links).
  - A's rule for `alice`, or for `department eq sales`, must then mean the same person in B's requests.
- **The rule.** A type of A may be the parent of a type of B only when both services are in the same identity domain. A link across domains is refused with 409 (ADM09).
- **Changing it.** The domain is set when the service is created. Changing it later is a `platform_admin` action, and it is refused while the service has cross-service links.
- **What it does not prove.** Licetum cannot check that the PEPs of one domain name people the same way. The domain records that the people who run those services say so.
- **Rejected alternative.** Normalizing every subject to `(namespace, type, id)` was considered and not taken for v1. It would change the key of every literal rule and of every request, for the benefit of cross-domain links that v1 does not allow anyway.

### 7.6 Search

**Paging**
- **Request and response.** A request carries `page {token?, limit?}`, with `limit` at most 1000 (1000 when absent).
  - The response carries `page {next_token, count}`.
  - `next_token` is `""` on the last page.
  - `total` is given only by the action search. The resource and subject searches would have to read the whole result to know it.
- **Each page at the current position.** Each page is computed at the position P of the PDP that serves it, and nothing is pinned between pages.
  - The cursor is base64url of `{bucket, last key, digest of the request}`. Today `pageCursor` holds the last key and the digest (`searchSum`).
  - **Order of results:**
    - resources by `(bucket, external id)`;
    - subjects by `(bucket, subject type, subject id)`;
    - actions by level name.
  - The bucket is the one the read model uses, and both adapters compute it the same way, so the order does not depend on the store.
  - P2 keeps today's order by id; the bucket order arrives with the read-model port in P5.
- **What this guarantees (AZ04):**
  - Keys strictly increase from page to page, so no result is repeated.
  - A result that stays allowed while the pages are read appears exactly once.
  - A result whose access changes in between appears or not, depending on the position of the page whose key range covers it.
  - The specification allows exactly this: "Pagination does not guarantee an atomic snapshot of the result set … results MAY be repeated or omitted between pages".
- **Why pages are not pinned to one position:**
  - The PostgreSQL adapter has no history of resource rows.
  - The next page may reach a pod that never held the earlier position.
  - Keeping old policy versions for paging would cost memory in every PDP.
- **No expiry.** There is no `authzen_page_expired`. A token stays valid across pods, restarts and epochs.
- **Changed request.** If any entity, the `context` or the `limit` differs from the request that produced the token, the answer is 400 `authzen_page_invalid`, as the specification recommends. Ignored fields are left out of the digest.
- **`min_revision`** in a later request applies to that page.

**Resource search**
- **Results.** `{type, id, properties: {name}}`.
- **Scope.** Only the requested type is read.
  - Today the handler computes the whole permission result of the service and then filters it (`searchResources` in `core/server/authzen.go`).
  - In v2 the candidates are:
    - the members of the type, under a whole-type allow;
    - otherwise the descendants of the allow anchors.
  - Each candidate is decided again at the page's position.
- **Scan budget.** One request reads at most `PDP__SEARCH_SCAN_BUDGET` (10 000) candidate rows.
  - When the budget stops it, a page shorter than `limit`, even an empty one, still carries a `next_token` (AZ13).
- **Types without resources.** A `not_sync` type has no resources and gives an empty result.

**Action search**
- **Candidates.** The candidate levels are:
  - the levels of the rules;
  - the levels of the role items;
  - the access groups;
  - the keys of the type's `access_implies`.
- **Decision.** Each candidate is decided for the resource, or for the type as a whole with `"*"`. It is one read, decided in memory.
- **`total`** is given.

**Subject search**
- **Access.** The PEP client needs the operation `search_subject`.
- **Candidates.** The service's `subject_directory` entries of the requested `subject.type`. The directory is filled through the admin API (see the section on the people of a service).
- **Decision.** Each candidate is decided with its stored properties and the request's `context`, through the same cached path as an evaluation. Group membership is therefore followed, whether direct, nested or by predicate. This is the transitive search the specification recommends.
- **Limits.** One request goes through at most 1000 directory entries, as `authzenSubjectScan` does today. `next_token` continues from there, and there is no `total`.
- **Results.** `{type, id, properties: {name}}`, with the stored display name.
- **Unknown subjects.** A subject that is not in the directory is never found. An empty directory gives an empty result, not an error. Today's `context.subjects_registry: "disabled"` is removed.

### 7.7 Licetum extensions

The request extension is `context.licetum = {min_revision?, explain?}`, read only from the top-level `context`.

**`min_revision`: read your writes**
- **Source.** It takes the `<epoch>.<r>` from the `Licetum-Revision` header of an admin write, or of a resource refresh. A bare number means the current epoch.
- **Waiting.** The PDP waits until its position reaches the revision, for at most `min(PDP__FRESHNESS_WAIT (1 s), request deadline)`, with one waiter per process.
- **If the revision is not reached:**
  - `/evaluation` and the searches answer 503 `revision_not_reached` with `Retry-After: 1`.
  - `/evaluations` answers every evaluation with `decision: false` and `context.error {status: 503, message}`. A PEP that reads only `decision` still denies.
- **No stale answers.** An answer below the requested revision is never computed or cached.
- **Replacements.**
  - `min_revision` replaces the query parameter `at_least_as_fresh`. In P2 it is mapped onto that parameter until the position model exists.
  - `consistency=higher` is removed.

**`explain: true`**
- **Content.** It adds `context.reason_admin`, a `model.Explanation`: `decision`, `granted_by`, and `denials` with `lifted_by`, `unresolved_parent` and `missing_attribute`.
- **Access.** It requires the client's `explain` operation; without it the answer is 403 `authzen_forbidden` (AZ12).
- **In a batch.** In `/evaluations` it applies to every evaluation.

**Upstream rules are redacted**
- **What is shown.** A rule of another service that reaches this one through an exported type appears in `reason_admin` only as `{upstream: "<slug>", level: "<level>"}`, in the place where it took part (`granted_by`, a denial, `lifted_by`).
- **What is withheld.** The rule id, anchor, access list and `applies_to`.
- **What stays.** The denial's `unresolved_parent` and `missing_attribute` flags, because they describe this request and this resource.
- **Admin view.** B's admin UI shows the same rules read-only, marked `upstream`. B's PEP sees less than B's admins (AZ16, new).

**When a response carries `context`**
A response carries `context` only when there is something to say:
- `reason_admin` when explain was asked;
- `error` on an evaluation that could not be decided;
- `code` and `reason` on the evaluation that stopped a short-circuited batch.

A `decision: true` carries a `context` only when explain was asked, because the specification lets a PEP reject a decision whose context it does not understand.

**Response headers**
- **`Licetum-Revision: <epoch>.<r>`** is sent on every 200. It gives the position P that the answer, or the page, was computed at.
- **`Licetum-Error-Code`** is sent on every error. It carries the catalog key.
- `X-Authz-Revision` is replaced, and `X-Authz-Sync` is removed.

### 7.8 Errors

**Body.** The body of an error is a JSON string with the localized message, as in the specification's table.
- The language comes from `Accept-Language` as today, and `Content-Language` is set.
- The machine-readable code is in the `Licetum-Error-Code` header.
- The admin API keeps `{detail, extra}`.

| Status | When |
|---|---|
| 400 | Malformed body, missing required field, exceeded bound, paging request changed (`authzen_page_invalid`) |
| 401 | No credential, a rejected credential, or a scheme other than Bearer |
| 403 | `authzen_forbidden`: unknown service, client not registered or disabled for the service, operation not granted, explain without the right |
| 404 | Metadata of an unknown slug only |
| 429 | Per-client rate or concurrency quota exhausted (§7.11), with `Retry-After` |
| 500 | Internal error |
| 503 | `policy_unavailable` (the view is older than the staleness limit S and the region is not frozen), `revision_not_reached`, `overloaded`, `request_timeout`; always with `Retry-After` |

Inside `/evaluations`, the same statuses go into `context.error.status` of each evaluation.

**401 responses (AZ08)**
- **Header.** A 401 always carries `WWW-Authenticate: Bearer realm="<PDP identifier>"`. When a credential was presented and rejected, the header also has `error="invalid_token"` (RFC 6750).
- **Bearer is case-insensitive.** The scheme name is matched without regard to case, and any other scheme is a 401.
- **Today:**
  - `verifiedCaller` (`core/server/check.go`) uses `strings.CutPrefix(authorization, "Bearer ")`, which is case-sensitive.
  - Any other scheme is a 400 `auth_header_required`.

**Order of checks.** Authentication and the check of the route's operation run before the body is parsed; the `explain` operation is checked once `context.licetum` has been read. A caller that is not authenticated, or not bound to the service, gets a 401 or 403, never a 400 that reveals the expected shape. The service is resolved only after the credential is verified, and every failure to find the client in the service is the same 403, so the access routes do not reveal which slugs exist (AZ09).

**`X-Request-ID` (AZ07).** The specification requires the PDP to return the same identifier the PEP sent.
- The header is returned verbatim. Any value `net/http` accepted is a valid field value.
- When it is absent, a UUID is generated and returned.
- The access log gets an escaped form of bounded length.
- Today `requestID` (`core/server/server.go`) replaces any value that is not 1–128 characters of `[A-Za-z0-9._-]` with a new UUID, which breaks the echo.

**PEP behavior.** The integration guide requires a PEP to treat any error as a deny.

### 7.9 PEP authentication

A PEP sends `Authorization: Bearer <credential>`. Two kinds of credential are accepted.

1. **JWT, client credentials.**
   - **Issuers** are listed in `pep_issuer(issuer, jwks_url, audiences, algs, require_typ)`. Issuers have platform scope and are managed by `platform_admin`.
   - **Checks:**
     - `iss` is in the list;
     - `aud` contains `PDP__AUDIENCE` (default `PDP__PUBLIC_URL`), always required;
     - `exp` and `nbf` hold, with a leeway of 60 s;
     - `alg` is in the issuer's allowlist (RS256, ES256, EdDSA). `none` and HMAC are never allowed;
     - `typ` is `at+jwt` (RFC 9068) when `require_typ` is set.
   - **Client.** The client is taken from `azp`, then `client_id`, then `sub`.
   - **Keys.** The keys reach the PDP through the replicated state, in the `pep` stream.
     - The writer polls each issuer's JWKS with `jwks.go`: one fetch per URL, shared by concurrent callers, transport errors never cached.
     - The writer stores the key set when its digest changes.
     - The PDP never calls an IdP (PEP04). A region cut off from the IdP still verifies tokens. The IdP must publish new keys before it signs with them.
   - **Cache.** Verified tokens are kept in memory until `exp`, and for at most 60 s.
   - **Today** `fetchAndVerify` (`core/engine/oauth.go`) checks the signature, `iss` and `iat`, but not `aud`. It accepts the JWK's `alg` (RS256 when absent) plus ES256 (PEP01).
2. **API key** of the form `lct_<16 hex id>_<43 base64url secret>`, a 256-bit secret.
   - **Storage.** It is stored as `sha256(secret)` in `pep_credential(key_id, client_id, sha256, expires_at, revoked_at)` and compared in constant time.
   - **No slow hash.** This is the hot path and the secret has high entropy.
   - **Rotation.** At most two active keys per client.
   - **Revocation** travels through the `pep` stream, with the same SLO as a policy change (PEP02).
   - **Without an IdP.** It works where no IdP is available.
3. **Later.** mTLS/SPIFFE and `private_key_jwt`.

### 7.10 PEP authorization

- **The client table.** `pep_client(id, service_id, name, kind jwt|api_key, issuer, client_id, operations text[], enabled, UNIQUE(service_id, issuer, client_id))`.
  - `operations` is a subset of `{evaluate, search_resource, search_action, search_subject, explain}`.
  - `explain` and the three searches are off by default.
- **Ownership.**
  - A PEP client belongs to one service and is managed by that service's `service_owner`.
  - The same IdP client used by two services is two rows.
- **Uniform refusal.** An unknown service, an unregistered or disabled client and a missing operation all get the same 403 `authzen_forbidden`, with the same body (AZ09).
- **What it replaces.** It replaces the service's `trusted_clients` and its per-service OAuth configuration.

### 7.11 Load isolation

- **Per process.**
  - `HTTP__MAX_INFLIGHT` admission refuses the excess with 503 `overloaded` and `Retry-After`.
  - `COMPUTE__CONCURRENCY` bounds the computations of cache misses.
  - Both work as today.
- **Per client, per process.**
  - `PEP__RATE_LIMIT` is a token bucket; when it is empty the answer is 429 with `Retry-After`.
  - `PEP__MAX_INFLIGHT` limits the in-flight requests of one client; beyond it the answer is 429. One PEP's burst cannot take the slots of the others.
  - AZ14 and AZ15 (new) cover these limits.
- **Search has its own pool.** Searches run on their own pool of slots, `PDP__SEARCH_CONCURRENCY`, separate from the evaluation slots.
  - A resource search may read up to 10 000 rows, and a subject search may decide up to 1000 subjects.
  - They wait for their own pool and do not delay evaluations.
- **No cluster-wide limit.** Without Redis, PDP pods share no state.
  - Per-client limits apply per pod, so the effective limit grows with the number of pods.
  - A global limit per client belongs at the ingress or gateway in front of the PDPs, and the deployment docs say so.
- **Defaults** come from the P2 load test and are documented in `core/README.md`.

### 7.12 Access log, decision log, subject hash

- **Access log.** One line per request, as today.
  - `user` is the PEP client, and `on_behalf_of` is `subject.id`.
  - Today `on_behalf_of` is set only for trusted clients.
- **Decision log** (`LogDecisions`, kept).
  - **Fields:** client, subject, resource type and id, action, decision, position P, request id.
  - **Control:** written according to `log_settings`: the events, the sampling of allowed decisions, and whether the subject is written plain, hashed or omitted.
  - **Today** it records `consistency` instead of the client and the position.
  - **Durability:** it replaces the durable `delegated_call` audit. A log line is only as durable as the operator's log pipeline. Durable audit is a separate feature, for later.
- **The subject hash salt.**
  - **Today** `log_settings.salt` in PostgreSQL keys the subject hash (`LoadLogSettings`, `core/engine/log_runtime.go`).
  - **Problem.** Kept in `log_settings`, the salt would travel with the `settings` stream and copy a secret into the read model of every region.
  - **Decision.** The salt is not part of the replicated state.
    - It becomes a deployment secret, `LOG__SUBJECT_SALT`.
    - Every process that writes subjects into logs gets it: the PDP in every region, the admin API and the writer.
    - The value is the same everywhere, so a subject has the same hash in every region and logs can be joined.
  - **Schema.** Schema v2 drops `log_settings.salt`, and the `settings` stream carries only the settings.
  - **Missing salt.** A process asked to hash without a salt omits the subject, never writes it in plain text, and logs a warning once.
  - **Rotation** is the rollout of a new secret; hashes change from then on (OPS09, new).

### 7.13 Administrator sign-in state

Administrator sign-in is not part of the PDP, but it leaves Redis in the same change.
- **Today:**
  - The web interface starts the sign-in at `/service/<platform service id>/oauth/login` of the admin API.
  - The admin API keeps the PKCE verifier in Redis under `authz:pkce:<nonce>` for 600 s (`core/engine/oauth.go`).
- **New:** the BFF is the OIDC client of the administrator IdP, and the pending sign-in lives in a cookie (WEB01).

1. `/auth/login` creates `state`, `nonce` and a PKCE `code_verifier` (S256).
   - It puts them, together with the return path, into one short-lived encrypted cookie. The return path is kept today in the `licetum_return` cookie.
   - The cookie is a JWE with the session cookie's key.
   - Its attributes:
     - `HttpOnly`;
     - `Secure` when secure cookies are on;
     - `SameSite=Lax`, because it must come back on the top-level redirect from the IdP;
     - path limited to `/auth`;
     - max-age 10 min.
2. `/auth/callback`:
   1. compares `state` in constant time;
   2. exchanges the code with the verifier on the server;
   3. checks the ID token's `nonce`, `iss` and `aud`;
   4. deletes the cookie whatever the outcome;
   5. sets the session cookie.
   - The cookie is used once.
3. **One sign-in per browser.** Only one sign-in can be in progress per browser. A second one, started in another tab, replaces the first, which then fails with "sign-in expired, try again".

No server-side store is involved, so the BFF can run as several replicas and in several regions.

### 7.14 What is removed

**PDP (`core/server/check.go` and related code)**
- **Endpoints:** `GET /service/{id}/permission`, `POST /service/{id}/check`, `POST /service/{id}/on_behalf/check` and `/on_behalf/permission`.
- **Code and models:** `engine.PermissionPage` as an API, the models `CheckInput`, `CheckResponse`, `DelegatedCheckInput` and `DelegatedPermissionInput`, and the OpenAPI tags `Permission`, `Check` and `Delegated`.
- **Request options:** the query parameters `consistency` and `at_least_as_fresh` (`ConsistencyParam`), and the headers `X-Authz-Sync` and `X-Authz-Revision`.
- **Old behaviors:**
  - `context.subjects_registry`;
  - `explain` as a reserved context name;
  - questions without `resource.id`;
  - the uuid form of the path.
- **Per-service verification of end-user tokens:** `VerifyToken` per service, `invalidTokens`, the Redis keys `authz:token:*`, the `jwks:<service id>` breaker, `GetServiceForCheck` and `ServiceCheckColumns`.
- **Delegation and the subject registry:**
  - `trusted_clients` and `AuthorizeDelegate`;
  - the `speaker` logic of `authzenCaller`;
  - `delegated_call` and its retention;
  - `NoteSubject` and `known_subject`.

**Admin API**
- **Removed:** `oauth_login`, `oauth_callback`, `oauth_refresh` and `list_delegated_calls`.
- **`policy_claims`** becomes `GET /service/{ref}/policy/properties`. It lists the `subject.properties` and `context` keys a PEP must send.
- **`simulate_check` and `simulate_permission`** become `/service/{ref}/simulate/access/v1/{evaluation,evaluations,search/*}`.
  - They take AuthZEN shapes and always explain.
  - They use no PEP authentication; they need the admin's `simulate` right.

**Settings**
- `HTTP__PUBLIC_URL` is replaced by `PDP__PUBLIC_URL`.
- `SUBJECTS__*` and `DELEGATION__*` are removed.

### 7.15 Contracts and phases

- **P2.** Most of the API work lands here, with these contracts:
  - AZ01–AZ14 (AZ04 has no expired-token case);
  - PEP01–PEP03;
  - API01–API03;
  - WEB01.
  - **2b:** PEP clients and credentials, the conformance fixes of §7.4–§7.8, `context.licetum`, the ops port, and the rename of `check` to `pdp`.
  - **2c:** the removals of §7.14.
  - **2d:** the administrator sign-in of §7.13.
- **P3.** Schema v2:
  - `service.identity_domain`;
  - the plain unique slug constraint, with the slug no longer accepted in `PATCH` (ADM11, new);
  - `log_settings` without the salt.
- **P4.** ADM09: links only to an exported type of the same identity domain.
- **P5.** The `(bucket, key)` order of search results, with the read-model port.
- **P6.** PEP04.
- **New contracts.** AZ15, AZ16, ADM11 and OPS09 are added to `contracts.yaml` as `planned` in their phases:
  - AZ15: one client over its quota gets 429 while other clients are served, and searches do not take evaluation slots (P5).
  - AZ16: upstream rules in an explanation show only the slug and the level (P4).
  - ADM11: a slug cannot be changed or reused.
  - OPS09: the salt is never in the replicated state, and it gives the same hash in every region.

## 8. Service-scoped administration

Administrators' rights over Licetum become fixed roles that are bound to scopes. This section replaces the self-service described in `CLAUDE.md` under "Self-service: access policies for Licetum itself".

### 8.1 Principle

- An administrator's rights are **bindings**. Each binding gives a fixed role in one scope: the platform, one service, or one group of a service. `licetum-admin` reads the bindings from PostgreSQL on every request. It checks them again inside the write transaction, after `pg_advisory_xact_lock(admin_write)`.
- Bindings are not rules on Licetum's own resource types. The current dogfooding is removed completely: the platform `service` row, the six policy types (`model.PlatformTypes`) with their SQL synchronizers, and the admin group with its rules (`db.SeedPlatform`). That design has three defects that a role model does not have:
  - The synchronizers of the policy types store Licetum's own `DB__URI` in `synchronizer.config`, password included (`seedPlatformType`, `core/db/migrate.go:273`).
  - An admin write marks the policy types due (`invalidatePlatformSync`). The next admin request then waits for a synchronization.
  - If the policy went through the read model, revoking an administrator would become eventually consistent.
- The PDP, the CQL store and the writer are not on an administrator's path. Authorization and admin writes keep working with all three stopped. A revoked binding takes effect on the next request (ADM06).

```
platform
└── service S
    ├── settings of S, PEP clients and their keys, bindings of S
    ├── types ── links, access groups, source, resources, refresh, `exported`
    ├── groups ── members
    ├── roles ── items
    ├── rules, scenarios, subject directory, history of S
    └── group G: one group ── its members (delegated management of people)
```

### 8.2 Roles

| Role | Scope | Allows |
|---|---|---|
| `platform_admin` | platform | Everything: creating and deleting services, platform bindings, identity domains, PEP issuers, log settings, the platform change log. |
| `platform_viewer` | platform | Reading everything, with secrets masked, and the platform change log. |
| `service_creator` | platform | Creating services. The creator gets `service_owner` of the new service in the same transaction. |
| `service_owner` | service S | Everything in S: the bindings of S and of its groups (up to `service_owner`), the PEP clients of S and their keys. It cannot delete S; only `platform_admin` can. |
| `service_editor` | service S | Settings of S, types and links, the `exported` flag, sources (secrets are write-only), refresh, access groups, groups, members, roles, items, rules, scenarios, the subject directory, and import, revert and rollback in S. |
| `policy_editor` | service S | Groups, members, roles, items, rules, scenarios. No types and no sources. |
| `member_manager` | service S | Groups and members of S. |
| `service_viewer` | service S | Reading everything in S with secrets masked, simulate, policy tests, lint, export, who-has-access, preview, history. |
| `directory_writer` | service S | Writing the subject directory of S, and nothing else (section 8.8). |
| `group_manager` | group G | Reading G and managing its members. |

The roles a caller holds in a scope are computed in two steps. First, take the roles of the bindings that are in force and match the caller. A binding is in force while `enabled` is true and `valid_from ≤ now < expires_at`. Then add every role these imply:

- `platform_admin` implies `platform_viewer`, `service_creator`, and `service_owner` of every service.
- `platform_viewer` implies `service_viewer` of every service.
- `service_owner` implies `service_editor`, which implies `policy_editor`, which implies `member_manager`, which implies `service_viewer`.
- `service_editor` implies `directory_writer`.
- `member_manager` of S implies `group_manager` of every group of S.

The same closure decides what a caller may grant: a caller may grant a role in a scope only if its own closure in that scope contains the role.

The matrix `(scope, kind, action) → minimal role` lives in `admin-api/authz/matrix.go`. It is the only place that says which role a route needs.

### 8.3 Tables

- `admin_binding(id uuid PK, scope_kind text CHECK (scope_kind IN ('platform','service','group')), service_id uuid NULL, group_id uuid NULL, role text, subject text NULL, predicate jsonb NULL, enabled bool, valid_from, expires_at, created_by, created_at)`:
  - CHECK constraints: exactly one of `subject` and `predicate` is set. The scope columns match `scope_kind`: for platform both are NULL; for service, `service_id` is set; for group, `group_id` is set together with the group's `service_id`, under FK `(group_id, service_id)`. The role must be one of the roles of the scope.
  - `subject` is compared with the subject of the admin token (`ADMIN_AUTH__SUBJECT_CLAIM`, section 8.12).
  - `predicate` holds conditions over the claims of the admin token. It uses the condition language of `core/decide` and is evaluated by it, with one addition for bindings: a list-valued claim is matched element by element, as resource attributes are matched today (`matchElements`). So `groups in [licetum-admins]` holds for a member of that IdP group. Today claims are compared as their `PyStr` rendering, and under that rendering a list never equals one of its elements. With this addition, IdP groups serve as the global groups of administration.
- `platform_state(id = 1, bootstrapped_at, bootstrapped_by)` (section 8.11).
- `identity_domain(id, name UNIQUE, description, created_at)` and `service.identity_domain_id NOT NULL` (section 8.9).
- A change to a binding or to an identity domain goes through `PolicyTx`. It takes `admin_write`, writes a `change_log` record and takes a revision, with an outbox header whose `streams` are empty. Neither table is projected, because decisions do not read them. The revision sequence stays gapless (PUB02).

The tables arrive with schema v2 in P3. Authorization starts reading them in P4.

### 8.4 Authorization of a request

- Every route declares `withPolicy(op, scope, kind, action)`. The scope is the platform, the path parameter `service_ref`, or the path parameter `group_id`. The current mechanism stays:
  - The policy is metadata of the Huma operation (`server.PolicyMetadataKey`).
  - A middleware (`platformAuth` today) verifies the token and checks the policy before the body is parsed, so an outsider gets 403, not 422.
  - A route without a policy fails the build (ADM02).
- One statement reads every binding that can match the caller: the bindings with its literal subject, plus all predicate bindings (a small table, read in full). Nothing is cached between requests.
- In a write, `PolicyTx` takes `admin_write` and checks the binding again, both in one `pgx.Batch`. A binding revoked between the request check and the write therefore cannot be used. The number of round trips of an admin write is measured, not assumed (WR06).
- A caller with no role in S, and none in any group of S, gets 403 on every route under `/service/{ref}/`, whether or not S exists. A caller with a platform role gets 404 for an unknown `ref`.
- A child entity named in the path must belong to S; otherwise the answer is 404 (ADM10). Child entities are types, groups, roles, rules, access groups, sources, resources, PEP clients and scenarios. Today this does not hold:
  - `ListAccessGroups` and `CreateAccessGroup` look up the type with `GetExistingTypeResourceAnyService` (`admin-api/service/access_groups.go:89,118`) and ignore the service in the path.
  - `GET /type_resource` (`admin-api/api/admin.go:267`), `GET /type_resource/{type_resource_id}/resource` (`:296`) and `POST /type_resource/{type_resource_id}/resource/refresh` (`:314`) have no service in the path at all.
  - `get_resource` (`:332`) has `{service_id}` in its path but does not pass it to `GetResource`.

  P4 moves all of these under `/service/{ref}/`.
- A list checks the scope, filters in SQL (`WHERE service_id = $S`) and pages by keyset. `FilterAllowed` (`admin-api/service/platform.go:186`) goes away. So does `Access.Can`, which parses the id of every access entry for every listed item.

### 8.5 Routes

- `GET /me` returns `{subject, platform: {roles, actions}}`.
- `GET /services?q=&limit=&after=` returns the services the caller can see, paged by slug (`limit` ≤ 200):
  - for a platform role, every live service;
  - otherwise, the services its bindings name, group bindings included.

  Each item is `{id, slug, name, roles, actions}`. `POST /services` creates a service.
- `GET /service/{ref}/me` returns `{roles, actions, groups: [{id, name, roles, actions}]}` for one service.
- These three routes replace `GET /platform` and `GET /platform/me`. None of them grows with the number of services: in an installation with thousands of services, a `platform_admin` pages through `GET /services` instead of receiving all of them in `/me`.
- Platform scope: `/platform/{bindings, identity-domains, pep-issuers, log-settings, changes}`.
- Under `/service/{ref}/`:
  - `types` (with `parents`, `access-groups`, `exported`, `dependents`), `sources`, `resources`, `refresh`;
  - `groups/{id}/members`, `roles/{id}/items`, `rules` (with `preview`);
  - `pep-clients` (with `keys`), `bindings`, `subjects` (bulk `PUT`, in the style of SCIM);
  - `batch`, `changes`, `changes/{revision}/revert`, `changes/rollback`;
  - `policy/{test, scenarios, lint, export, import, properties}`, `simulate/access/v1/*`, `who-has-access`.
- `{ref}` is the slug or the id of the service. Both name the same service for its whole life, because the slug cannot change (section 8.10).

### 8.6 Creation and escalation

- Creating anything in S needs the matching action in S. Today, creating needs write on a whole platform policy, which a write on one entity does not give. With roles bound to scopes, that distinction disappears. Creating a service needs `platform_admin` or `service_creator`.
- **The schema keeps references inside one service.** Referenced tables carry `UNIQUE (id, service_id)`. References are composite foreign keys: `(type_resource_id, service_id)`, `(role_id, service_id)`, `(group_id, service_id)`, `(member_group_id, service_id)`, `(member_of_group_id, service_id)`, `(requires_group_id, service_id)`, `(subject_group_id, service_id)`. A rule, role item, membership or guard that names another service's type, group or role cannot be stored, whatever the application does.
- **The application checks what a foreign key cannot express:**
  - The lookup type of a `resource_from_property` target must be a type of S or an exported ancestor of one (section 8.9).
  - A binding may grant only a role in the caller's own closure for that scope. `service_owner` manages the bindings of S and of its groups. Only `platform_admin` manages platform bindings.
  - Batch, import, revert and rollback act on one service. Every operation must name entities of S, and a rollback reads only the `change_log` records of S. Today `PlanRollback` (`admin-api/service/history.go:37`) undoes every change after the position, in every service (`changesAfter`, `admin-api/service/changelog.go:131`).
  - Retargeting a rule (its type, resource, property, `resource_where` or role) needs the same rights as creating it.
  - A rule that grants a role is checked against every item of that role.
- The current escalation tests (`admin-api/tests/integration/api/escalation_test.go`, and the platform checks in `admin-api/api/admin_targets.go`) are restated as ADM04.

### 8.7 Groups, roles, the policy document and history

- **Groups and roles belong to a service.**
  - Names are unique within the service (`UNIQUE (service_id, name)`). This replaces the global `uq_role_name` and `uq_user_group_name` (`core/db/migrations/00001_init.sql:299,302`).
  - Groups nest only within their service.
  - A rule names its group with `subject_group_id`, a foreign key, instead of the literal `group:<uuid>`. The internal identity string `group:<uuid>` stays.
- **There are no global policy groups in v1.** A set of people across the organization is a predicate over `subject.properties`. Reusing policy across services means exporting and importing the policy document.
- **The policy document becomes `licetum.policy/v2`**, the complete unit of one service. It holds:
  - types: name, `access_implies`, parents with `inherit`, `exported`, and links to upstream exported types named `<slug>/<type>`;
  - access groups, groups and roles;
  - rules, with `property`, `subject_type` and `subject_group`;
  - optionally, sources without secrets.

  Rules about the format:
  - The meaning of the format changes (`property`, `subject_type`, `subject_group`, types and exported links), so its version changes too. Today the format is `licetum.policy/v1` (`core/model/policy.go:97`). A `licetum.policy/v1` document is refused with 422 and a message saying that v1 is not imported.
  - An import into S reads and writes only S. Today `loadPolicyNames` resolves group and role names across every service (`admin-api/service/policy_document.go:42,46`), so an import into one service can rewrite another service's groups and roles.
  - Secret fields of a source are exported as the mask `********`. On import, the mask keeps the secret of an existing source, as `SynchronizerConfig.RestoreSecrets` does now. A new source with a masked secret is refused.
  - Rules of upstream services appear in an export as read-only references. An import ignores them.
- **History.** `GET /service/{ref}/changes` reads the `change_log` records of S in `(revision, seq)` order. `/platform/changes` reads all records. `changes/{revision}/revert` undoes the records of S at that revision.
- **Rollback and the batch kinds.** The batch knows six kinds of entity (`core/model/batch.go:7-12`). `undoOperations` refuses any other record with 409 `change_not_revertible` (`admin-api/service/history.go:55`). A service rollback would therefore fail on the first record about a type, a source or a link after the position. v2 adds these kinds to the batch:
  - `type`: name, `access_implies`, `exported`;
  - `type_link`: a parent link with its `inherit`, including a link to an exported type of another service;
  - `source`: the configuration with its secrets masked; the mask keeps the stored secret;
  - `service_settings`: name, `url`, `icon`.

  How the new kinds are undone:
  - Types and sources are deleted softly (`deleted_at`). Undoing a deletion clears `deleted_at` on the same row, so a source's stored secrets come back with it.
  - The change log never holds secrets: a source's configuration is recorded masked (`old.Config.Masked()`, `admin-api/service/synchronizers.go:144,165`). So undoing a source update that changed a secret keeps the current secret. The v2 record lists the names of the secret fields a change touched, never their values, so the plan can report this.
  - The plan lists such operations as `partial` with the reason.
  - A plan with partial operations is applied only with `allow_partial: true`. Without it, the answer stays 409 `change_not_revertible` and names the records.
  - A rollback never includes records of bindings, of PEP clients and keys, or of the subject directory. A rollback restores policy, not administrative access, credentials or people.
  - Undoing a type or a link is a structural write. Like any other structural write, it takes effect when its `RebuildComponent` commits.

### 8.8 People of a service: the subject directory

- **Table.** `subject_directory(service_id, subject_type, subject_id, display_name, properties jsonb, updated_at, PRIMARY KEY (service_id, subject_type, subject_id))` replaces `known_subject`. `known_subject` is filled by every token verification while `SUBJECTS__ENABLED` is on (`NoteSubject`; off by default) and is global: anyone with read on the whole `group_member` policy can read `/subject` (`admin-api/api/admin_subjects.go:29,48`), whatever the service.
- **Who fills it.** The editors of S or the application itself, through `PUT /service/{ref}/subjects` (an upsert of up to 1000 entries per call) and `DELETE` of single entries. The PDP never writes it.
- **What it feeds.** AuthZEN subject search, the list of people a rule reaches in who-has-access and preview, and the person page.
- **Who reads it.** Only the roles of S, including `group_manager` of a group of S, who picks members from it.
- **Retention** is a setting of the service.
- **Write path.**
  - A directory write does not take `admin_write`. No invariant depends on the directory, and a bulk `PUT` from an application would otherwise make every admin write wait behind it.
  - It takes only the revision row, as the last statement of its transaction. From P3 it also writes an outbox header for the stream `dir:<svc>`.
  - The upsert changes only rows whose content differs. An unchanged batch writes no projected row and takes no revision.
  - The time an entry was last sent is kept separately, in `subject_directory_seen`, which is not projected. Retention reads it and deletes entries that were not sent within the service's retention period. It deletes in chunks; each chunk is a transaction with its own outbox header. Retention runs in the `worker` until P5, then in the writer.
- **`directory_writer`.**
  - An application that fills the directory authenticates as a machine client of the admin IdP: a client-credentials token whose audience is `ADMIN_AUTH__AUDIENCE`.
  - It holds `directory_writer` on S through a binding on its subject. The role allows writing the directory and nothing else. Giving the application `service_editor` instead would let it change the policy.
  - Its writes are rate-limited per client and per process, with a token bucket like `PEP__RATE_LIMIT`.
- **Timing.**
  - The directory arrives in P2, in the same step that removes `known_subject`, so subject search, the people screens and `peopleReached` are never empty in between.
  - Until P4, the platform policies govern its routes, as they govern `/subject` today: read and write on the whole `group_member` policy.
  - P4 moves the routes to the roles of S and adds `directory_writer`.

### 8.9 Cross-service type links and identity domains

- **Exporting and linking.** An editor or owner of service A sets `exported` on a type `A.P`. An editor of service B can then make `A.P` a parent of a type of B, provided A and B are in the same identity domain. B needs no role on A: the flag is A's consent, given once to every service of the domain.
- **Ownership of the link.**
  - The link belongs to B.
  - Its change record is written for both services at the same revision, so the history of each shows it.
  - U(B), the set of services whose policy reaches B's decisions, includes A.
- **Protection of linked types.**
  - While `A.P` is linked, A cannot clear `exported`, delete the type, or be deleted. The answer is 409 `blocked_by` with `type_link` and the services that link to it.
  - A's owners see the dependent services (`types/{id}/dependents`).
- **Upstream rules.**
  - A's rules on `A.P` cascade to B's resources.
  - In B's admin API, who-has-access, lint, export (as references) and explanations show these rules read-only, marked `upstream: <slug of A>`. The predicates of A's groups are not disclosed.
  - The PDP applies the same rule to a PEP of B: in `context.reason_admin`, an upstream rule shows only `upstream: <slug>` and its level.
- **Cycles and locks.** Cycles are checked under `admin_write`. `ComponentKeys` still spans the connected component of the type graph across both services, so synchronizations of A and of B take the same component lock.

**Identity domains.** There is no IdP per service, so each PEP names subjects in its own namespace. A link lets A's rules (literal subjects and predicates over `subject.properties`) decide B's resources. If B's PEP uses the name `alice` for a different person than A's PEP does, a grant from A to its `alice` reaches B's `alice`.

- An identity domain is the operator's statement that the PEPs of its services name the same person with the same `(subject.type, subject.id)` and send `subject.properties` with the same vocabulary. Licetum cannot verify this statement; it only keeps links inside a domain.
- `identity_domain(id, name UNIQUE, description, created_at)` is a platform object. Every service belongs to exactly one domain (`service.identity_domain_id NOT NULL`). The schema creates the domain `default`, and a new service goes into it unless its creator names another existing domain.
- Only `platform_admin` creates domains and moves services between them.
  - A move is refused with 409 `blocked_by` (`type_link`) while a link joins the service to a service that would end up in a different domain.
  - A link across domains is refused with 409 `type_link_cross_domain`.
  - The same rule applies to a `resource_from_property` lookup type in another service, since that type must be an exported ancestor.
- Decisions do not read the domain, so it is not projected.
- **Rejected alternative:** normalizing every subject as `(namespace, type, id)`. It would rewrite every literal rule and change what every PEP sends, for installations that mostly have a single IdP.

### 8.10 Slugs

- The slug is how PEPs name the service: the PDP identifier is `<PDP__PUBLIC_URL>/service/<slug>`.
- **Today:**
  - The slug can be changed (`ServiceUpdate.Slug`, `core/model/service.go:90`).
  - It is unique only among live services (`uq_service_slug … WHERE (deleted_at IS NULL)`, `core/db/migrations/00001_init.sql:422`).
  - So a new service can take the slug of a deleted one, and a PEP still configured for the old service then asks the new one.
- **In v2:**
  - The slug is set at creation and never changes. A `PATCH` with a different `slug` is refused with 422.
  - The unique index covers every row, deleted rows included, and a deleted service keeps its row (`deleted_at`). A slug is therefore never reused.
  - To rename a service, change its `name`.

### 8.11 Bootstrap, the last administrator, break-glass

- **Bootstrap.** `licetum-admin bootstrap` runs as a Job after `migrate`, and also at the start of the admin process.
  - It runs under an advisory lock, and only while `platform_state.bootstrapped_at IS NULL`.
  - Through `PolicyTx`, it creates `platform_admin` bindings for `ADMIN_AUTH__BOOTSTRAP_SUBJECTS` and/or the predicate `ADMIN_AUTH__BOOTSTRAP_CLAIM` in `ADMIN_AUTH__BOOTSTRAP_CLAIM_VALUES`, with the ids `uuid5('licetum:bootstrap')` for the predicate binding and `uuid5('licetum:bootstrap:<subject>')` for each subject. Then it sets the mark.
  - It never runs again, so a deploy does not restore revoked access (ADM08).
- **Last administrator.**
  - After any write, at least one `platform_admin` binding must be in force, enabled and with a non-empty principal. A predicate counts as non-empty. Otherwise the write is refused with 409 `last_platform_admin`.
  - A service must keep a `service_owner` in force, unless the caller is `platform_admin`. Otherwise the write is refused with 409 `last_service_owner`.
  - Both checks run under `admin_write`, so two concurrent revocations cannot together remove the last one (ADM05, WR04).
  - The checks count bindings in force at the time of the write. An `expires_at` that passes later can still leave the platform or a service without one; `restore-admin` or a `platform_admin` repairs that.
- **Break-glass.** `licetum-admin restore-admin [-user name]… [-claim k=v]…` moves from `core/db/restore.go` (MIT) to `admin-api/bootstrap` (AGPL).
  - It runs through `PolicyTx` with the actor `restore-admin@<host>`.
  - It upserts the `platform_admin` binding `uuid5('licetum:restore')` over the `-claim` conditions, when they are given, and one binding per `-user` with the id `uuid5('licetum:restore:<user>')`. Re-running it is therefore safe.
  - It restores nothing else: there is no longer a platform service, policy type, synchronizer, rule or denial to restore.
  - The run is recorded in the change log (ADM07). It needs only database credentials.

### 8.12 Authentication of administrators

- **Token check.** `licetum-admin` verifies the administrator's JWT against one static issuer, configured by:
  - `ADMIN_AUTH__ISSUER`;
  - `ADMIN_AUTH__AUDIENCE` (required; the token's `aud` must contain it);
  - `ADMIN_AUTH__JWKS_URL`;
  - `ADMIN_AUTH__SUBJECT_CLAIM` (default `preferred_username`, then `sub`).

  It also checks `exp` and `nbf`, and `alg` against an allowlist. Keys are fetched through `jwks.go`. A request with no usable token, an invalid token, or a token for another audience gets 401 with `WWW-Authenticate` (ADM01).
- **The web BFF is the OIDC client.**
  - Login, callback and refresh happen at the IdP, from the server.
  - The tokens stay in the existing encrypted httpOnly session cookie.
  - `state`, `nonce` and the PKCE `code_verifier` live in a short-lived encrypted cookie. This replaces the Redis keys `authz:pkce:*`.
  - The Dex redirect points to the web app (`/auth/callback`).
- **Removed settings.** There is no platform `service` row and no OAuth `PLATFORM__*` setting any more. `AUTH__STATE_*` and `CORS__*` go too, because the browser talks only to the BFF.

### 8.13 Admin tools and the primary

- The read-only tools build a policy view with `core/decide` over `pgstore`. They are simulate (in AuthZEN forms), who-has-access, preview, lint, policy tests and export. P2 removes today's replica routing, so without a change all of them would read the primary.
- By default they read the primary.
- If the P5 measurement calls for it (9.9, 13.3 #6), an optional `DB__TOOLS_READ_URI` points them at a streaming replica:
  - They use the same `pgstore` as the PDP in the postgres read model, in one `REPEATABLE READ READ ONLY` transaction with the revision read inside it.
  - The answer carries `Licetum-Revision`.
  - The BFF sends `min_revision`, the revision of the session's last write. If the replica has not reached it, the tool reads the primary instead.
- Authorization, lists, invariants and writes always use the primary.
- The nightly load test measures the statements and the primary's CPU per tool call. The default is revisited from those numbers.

### 8.14 Phases

- **P2, step 2d before step 2c.** 2d brings `ADMIN_AUTH__*` and the BFF as an OIDC client; authorization still runs on the platform policies.
  - In the same step, `SeedPlatform` and `restore-admin` check `ADMIN_AUTH__*` instead of `Platform.IsConfigured()`. That function (`core/config/config.go:145`, called at `core/db/migrate.go:218` and `core/db/restore.go:36`) requires all six OAuth `PLATFORM__*` variables.
  - If 2c came first, it would remove those variables. The seed would then be skipped silently, the admin API would answer 503 `platform_not_seeded`, and break-glass would not work until P4.
- **P2.** `subject_directory` replaces `known_subject` (section 8.8).
- **P3.** Schema v2 brings:
  - `admin_binding`, `platform_state` and `identity_domain`;
  - the composite foreign keys and `subject_group_id`;
  - `exported`;
  - names unique per service;
  - the immutable, never-reused slug.

  Authorization still runs on the platform policies, and the existing admin tests keep passing.
- **P4.** Adds:
  - `admin-api/authz` (matrix, bindings, scopes, last administrator) and `admin-api/bootstrap`;
  - the routes under `/service/{ref}/`, plus `/me` and `/services`, with lists filtered in SQL;
  - batch, import, export, revert and rollback per service, with the new batch kinds;
  - exported links and identity domains;
  - the directory under the roles of S, with `directory_writer`;
  - PEP client management;
  - the web: `access.tsx` on capabilities, the service switcher, routes under `/services/[slug]/…`, and binding screens.

  Removes:
  - the platform `service` row, `core/model/platform.go` and its uuid5 ids;
  - the policy types and their synchronizers, and `invalidatePlatformSync`;
  - `service.Access` and `FilterAllowed`;
  - the platform checks of `admin_targets.go`;
  - `/platform` and `/platform/me`.

### 8.15 Contracts

- ADM01–ADM10 in `contracts.yaml` cover this section. ADM02's matrix is generated from the route table and includes `directory_writer` and a machine client. HIS02 (revert and rollback stay within one service) and HIS03 (export followed by import is the identity) cover the document and the history. WR04 covers concurrent revocations.
- Two statements of this section have no contract yet. They are added as `planned`:
  - ADM11: a slug cannot be changed and is never reused, including after the service is deleted (P3).
  - ADM12: a directory write takes no `admin_write`; an unchanged batch takes no revision; `directory_writer` allows nothing beyond the directory (P4).

## 9. What is kept, moved, rewritten and deleted

This section lists what happens to each part of the current code, grouped by area. The words in the **Decision** column mean:

- **keep**: the code stays as it is and may get small edits.
- **move**: the same code goes to a new package. The move is its own `git mv` commit with no other change, and the behaviour changes only in later commits.
- **split**: the parts of the file go to different places.
- **rewrite**: the purpose stays and the code is new.
- **replace**: another mechanism takes over.
- **delete**: the code is removed.
- **add**: new code.

**Phase** is the phase whose PR makes the change. Inside P2 the order is 2a, 2b, 2d, 2c, and 2e is a PR of its own. A deletion lands only after the contracts that replace the tests it removes are active.

Every PR keeps these green: `task check`, the web check, `docker compose up` and the Helm golden files. So a change to the chart, to Compose or to the interface goes into the same PR as the change that needs it, and P7 only finishes the topology.

The multi-region store is called the CQL store here: package `core/readmodel/cqlstore`, settings `CQL__*`, `READMODEL__STORE=postgres|cql`. ScyllaDB or Apache Cassandra 5 is chosen in P0.5.

Some of P0 and P1 is already done (P0 in the epic at `229c4ed`; the P1 items on `refactor/p1-decision-contracts`, not yet merged):

- `contracts.yaml` and its check `core/internal/tools/contractscheck`;
- the transitive fail-closed fix in `UnresolvedParents`, `Prepared.CoversOf` and `coverNodes` (DEC11);
- the fixture format `core/testsupport/policyfix`.

### 9.1 Decision semantics (`core/engine`, the pure part)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/engine/levels.go` | move | `core/decide` | P5 |
| `core/engine/decision.go` | move | Goes to `core/decide`. The semantics do not change. Anchors change from `resource_cache.id` to `(type_id, external_id)`. Its first direct tests are the DEC tables; `decision_test.go` does not exist today. | P1 (tests), P5 |
| `core/engine/predicate.go` | move | Goes to `core/decide`. `PyStr` stays the canonical form of a value. The condition source `claim` becomes `subject`, and `resource_from_claim` becomes `resource_from_property`. | P3 (rename), P5 |
| `core/engine/typegraph.go` | split | `TypeGraph`, `computeFlow`, `DescendantsOf` and `assumedMask` (from the P1 fix) go to `core/decide`. `loadTypeGraph` becomes the loader of `pgstore` and the catalog fold of `core/policy`. `signature` drops `ServiceName`, so renaming a service changes no structure signature (SYN08). | P5 |
| `core/engine/conditions.go` | split | The pure helpers (`predicateUsesNow`, `untilNextMinute`, `expandAccess`) go to `core/decide`. The SQL code (`ResourcesMatching`, `containmentClauses`, `App.accessGroupLevels`) is deleted. `resource_where` is then decided on the `meta` of the ancestors read at P. | P5 |
| `core/engine/check.go` | split | `Prepared.CoversOf`, `answerCheck` and `explanation` go to `core/decide`. The reads of `decideChecks` are rewritten as reads at P in `core/pdp`. An explanation sent to a PEP names a rule of an upstream service only as `upstream: <slug>` with its level. | P5 |
| `core/engine/permission.go` | split | **To `core/decide`:** the pure part of `App.prepare` (it becomes `decide.Prepare`), `IdentityOf`, `RuleApplies`, `validityUntil`, `fingerprint`, and passes A, B and C of `coverNodes`.<br>**Deleted:** `applicableRules`, `App.ExpandRoles` (the query version; `snapshot.expandRoles` is the pure one), `LiveMemberships`, `cacheTTL`, `TypesWithSynchronizer`, `RequestAttributes` and the call of `ensure`.<br>`GetPermission` goes with `/permission`. | P2c, P5 |
| `core/engine/hold_property_test.go` | move | Goes to `core/decide` as contract DEC14. | P5 |

### 9.2 Read path, view and caches (`core/engine`)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/engine/snapshot.go` | split | **To `core/policy`, as the model of the policy at P:** the structures and their pure methods (`applicable`, `expandRoles`, `liveMembers`, `validity`, `serviceID`, `accessGroupLevels`, `ruleActive`, `memberActive`).<br>**To `pgstore`:** `loadSnapshot`. It reads the revision inside its own `REPEATABLE READ READ ONLY` transaction; today the revision comes from an earlier statement (`localstate.go:299`).<br>**Deleted:** the size estimate (`ruleBytes`, `itemBytes`), `errSnapshotTooLarge`, `oversized`, `logSnapshotFallback` and `licetum_snapshot_fallback_total`. `ServiceCheckColumns` goes with the per-service OAuth. | P2c, P5 |
| `core/engine/localstate.go` | split | **To `core/pdp/view`:** the immutable view, singleflight renewal, waiting for a revision, and the regression detector (`licetum_revision_regressions_total`).<br>**To `pgstore`, behind the `LogSource` port:** the PostgreSQL side (`readView`, `WatchRevision` over `revision_state`), for PG mode.<br>In CQL mode the view follows `region_state` and opens no PostgreSQL connection. | P5, P6 |
| `core/engine/cache.go` | split | **Kept and moved to `core/pdp/cache`:** the `cachedAnswer` mechanics (`DoChan`, `computeSlots`, `cachedEntry` with `Exp`/`Hold`/`stale`).<br>**Deleted in P2e:** the Redis level, `dataVersionsKey`, `recordDataChange` and `liveWithoutRedis`.<br>**Deleted in P5:** `attrsMemo` and `forConsistency`.<br>`bumpDataRevisions` is replaced by the bump at the end of the sync transaction (P3). The answer key holds the positions of `catalog` and of `svc:<x>` for x in U(S); each entry records the positions of `data:<type>` for every type read, foreign ancestor types included, and is served only while they are unchanged (5.12). | P2e, P3, P5 |
| `core/engine/lru.go` | move | `core/pdp/cache` | P5 |
| `core/engine/store.go` | split | The column lists and scanners (`AssignmentRuleColumns`, `ScanAssignmentRule`, `GroupMemberColumns`, `RolePermissionColumns`, `SynchronizerColumns`, `ActiveSynchronizer`) go to `pgstore` for reads and to `admin-api/service` for writes. `RuleActiveSQL`, which only the query path uses, is deleted. | P5 |
| `core/engine/permission_page.go` | delete | Goes with `/permission`. Search pages use the cursor of `core/pdp/authzen`. | P2c |
| `core/engine/simulate.go` | rewrite | `SimulateCheck` and `PermissionPage` become the simulate routes in AuthZEN form in `admin-api/service`, on `core/decide` + `pgstore` from P5. | P2c, P5 |
| `core/engine/replica.go`, `replica_test.go` | delete | Replica routing goes: `reader(ctx)`, `withPrimary`, `replicaGate`, `WatchReplica`, `DB__LAG_ACTION`. In PG mode the PDP reads one DSN, either the primary or a streaming replica. The staleness gate on T(P) replaces the lag measurement; the heartbeat is written by the writer and replayed by a replica. | P2e |
| `core/engine/app.go` | rewrite | `App` splits into `pdp.Server`, the writer and the admin services. The SQL fallback of `ServiceIDByRef` goes: the slug resolves from the catalog only. `GetExistingTypeResourceAnyService` goes to the admin code, where it is bound to the service in the path. | P4, P5 |
| `core/engine/admission.go`, `background.go` | move | `core/httpx` | P5 |
| `core/engine/ready.go` | rewrite | Readiness per role:<br>- PDP: staleness, schema (newer is fine), store, draining;<br>- writer and admin: PostgreSQL and schema.<br>`redisDown` and `snapshotReady` are deleted. | P2e, P5 |
| `core/engine/metrics.go`, `trace.go`, `tuning.go`, `observe.go`, `logging.go` | rewrite | Per process and without Redis. The metric catalog adds: publication lag, apply lag per DC, `now − T(P)`, outbox size, queue depth, versions and tombstones, partition sizes, and clock skew. | P2e, P5, P6 |

### 9.3 Synchronization and the resource graph

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/engine/sync_jobs.go` | delete | **P2e:** the table engine (`pgBackend`).<br>**P5:** `ensure`, `waitJobs`, `SyncResult.Pending` and `X-Authz-Sync`. From then on a request never starts a synchronization; the writer schedules them. | P2e, P5 |
| `core/engine/sync_worker.go` | split | **Deleted in P2e:** the table engine (`claimJob`, the `LISTEN`/`NOTIFY` dispatch, the region filter, `importLegacyState`, `sync_job`, `WORKER__REGION`).<br>**To `core/writer/sync`:** `runSync` (fetch, digest comparison, apply), `markApplied`, `markUnanswered` and `markBusy`. | P2e, P5 |
| `core/engine/sync_dbos.go` | move | **What moves to `core/writer/sync`:**<br>- one workflow per type, with the dedup id and `:fresh`;<br>- the fence on the workflow row (`FOR SHARE`), until SYN04 shows that the completion row is enough;<br>- small step outputs;<br>- `dbosPrune`, whose window no longer comes from `SYNC__DEMAND_WINDOW`.<br>**What changes:**<br>- steps run as `RunAsTransaction`;<br>- the janitor (`sync_executor` heartbeat, `dbosTakeOver`) is generalized for scale-down;<br>- `dbosBackend.enqueue`/`poll` on the request side goes with `ensure`.<br>DBOS runs synchronization only. | P5 |
| `core/engine/sync_state.go` | split | `ComponentKeys` and `syncTx` go to `core/writer/sync` (P5). `invalidatePlatformSync` and `sync_state.invalidated_at` go with the platform types (P4). | P4, P5 |
| `core/engine/resource_cache.go` | split | **Writer half, to `core/writer/graph`:** `NormalizeResources`, `dataDigest`, `applyType`, `syncEdges`, `rebuildClosure` together with `resource_unresolved`, and `structureOf`. It also writes the data payload of the outbox. `NormalizeResources` drops (and counts) a resource whose external id is `*`.<br>**Reader half, to `pgstore`:** `UnresolvedParents` and the closure and edge reads.<br>**To `admin-api/service` over `pgstore`:** `GetResource`, `IsCovered`, `ListResources` and `RefreshResources`. The inline `resolve` is replaced by the `SyncOne` workflow. | P5 |
| `core/synchronizer/adapter.go` | keep | The Redis breaker goes (`redisx.RecordFailure`, `BreakerOpen`, `OnBreakerOpen`); the cluster-wide backoff in `sync_state` replaces it. The `authz:fetch_all` cache goes (`cachedRows`, `storeRows`, `fetchTTL`). The SQL and HTTP adapters, `BindIDParam` and secret masking stay. | P2e |
| `synchronizer.cache_ttl`, `SYNC__FETCH_TTL` | delete | Removed from the model, the schema, the OpenAPI and the web form. | P2e |
| `synchronizer.regions` | keep | The home executor asks a region-local fetcher (`licetum-writer fetch --region <r>`, no PostgreSQL credentials) over mTLS. | P5 |
| `synchronizer.refresh_interval` | add | Drives the schedule of `SyncSchedule`. The default is minutes (5–15 min), not 60 s. Sources that answer without a change back off, and a metric counts the load on the sources. | P3 (column), P5 |

### 9.4 Authentication, delegation, audit, logs (`core/engine`)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/engine/jwks.go` | move | Goes to `core/pepauth`: one key set per URL, singleflight, a refetch for an unknown `kid`, transport failures not cached. The writer uses it to fetch the issuer keys of the PEPs into `pep_issuer_keys` (stream `pep`), and the admin API uses it for the admin IdP. From P5 the PDP never fetches JWKS; until then it fetches them through this package (11.6). | P2b, P5 |
| `core/engine/oauth.go` | split | **To `core/pepauth`:** `fetchAndVerify`, now with `aud` required, an `alg` allowlist, `exp`/`nbf` with 60 s leeway and an optional `typ=at+jwt`.<br>**Deleted:** `StartLogin`, `FinishLogin`, `RefreshTokens`, `CreateState`/`VerifyState`, PKCE in Redis, the per-service `VerifyToken`, `invalidTokens`, `authz:token:*`, the `jwks:<svc>` breaker and `GetServiceForCheck`. | P2b, P2c |
| `core/engine/token_cache_test.go` | rewrite | Becomes the test of the verified-token L1 of `core/pepauth` (PEP03). | P2b |
| `core/engine/delegation_runtime.go` | delete | The checks of `PrepareDelegated` are applied to every AuthZEN `subject` in `core/pdp/authzen`: at most 100 properties and 16 KiB, no `group:` prefix, and `preferred_username` defaults to `subject.id`. `AuthorizeDelegate`, `ClientOfClaims` and `RecordDelegatedCall` are deleted. | P2b, P2c |
| `core/engine/audit.go`, `subjects_runtime.go`, `flusher.go` | delete | `flusher` serves only the other two. `LoadKnownSubjectsAfter` is replaced by reads of `subject_directory`. | P2c |
| `core/engine/decisions.go` | move | `LogDecisions` goes to `core/pdp/decisionlog`. It logs client, subject, resource, action, decision, P and `request_id`, as `log_settings` says. | P5 |
| `core/engine/log_runtime.go` | rewrite | `LogOptions` stays. The PDP takes the settings from the `settings` stream of its view, not from PostgreSQL. The subject-hash salt (`log_settings.salt`) is a secret and stays out of the stream: the processes read it from a deployment secret, with one value per installation so hashes agree across regions. | P5 |
| `core/engine/retention.go` | rewrite | The Redis lock goes. Retention becomes a task of the writer's leader:<br>- the outbox after publication (by age in PG mode);<br>- `change_log`, as the settings say;<br>- `subject_directory`, per service, through the outbox.<br>DBOS workflows are pruned by the `sync` role (`dbosPrune`, 4.9.1).<br>The pruning of `delegated_call` and `known_subject` goes with those tables. | P2c, P2e, P5 |

### 9.5 HTTP surface (`core/server`)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/server/check.go` | delete | Deletes:<br>- the routes `GET /permission`, `POST /check` and `POST /on_behalf/{check,permission}`;<br>- the request types `ConsistencyParam` (`consistency`, `at_least_as_fresh`) and `PermissionQuery`;<br>- `verifiedCaller`, whose `Bearer ` prefix is case-sensitive (`check.go:67`);<br>- `authenticateDelegate` and `requestEnv`;<br>- the OpenAPI tags `Permission`, `Check` and `Delegated`. | P2c |
| `core/server/authzen.go` | split | **Kept:**<br>- `prepare`, which merges defaults as whole objects;<br>- `evaluate`, now lazy: nothing is computed after the stopping evaluation; today everything is computed and `applySemantic` cuts it afterwards (`authzen.go:294`);<br>- `applySemantic`, `responseOf`, `parseSearch` and `searchSum`;<br>- `encodeCursor`/`decodeCursor` and `pageOf`;<br>- `authzenMetadata`, with https and `PDP__PUBLIC_URL`.<br>The cursor is (bucket, last key, digest of the request). Each page is computed at the PDP's current position; `authzen_page_expired` does not exist.<br>**Rewritten:**<br>- `authzenCaller` (`trusted`, `speaker`, `note`, `flush`) becomes the check of the PEP client and its operations;<br>- `searchSubjects` and `decideKnown` read `subject_directory`.<br>Only the slug is accepted in the path. `min_revision` is read only from the top-level `context.licetum`; in P2b it maps to `at_least_as_fresh`. | P2b, P2c, P5 (`core/pdp/authzen`) |
| `core/server/server.go` | move | Goes to `core/httpx`, with `LocalizeError`, `logAndRecover`, `withAdmission` and `NewServer`. `LocalizeError` also maps the errors of the read-model store. `X-Request-ID` is returned exactly as received when it is safe in a header; the log gets a sanitized copy. The PDP serves its probes on the ops port. `withCORS` is deleted, because the browser reaches the admin API only through the BFF. | P2b, P2c, P5 |
| `core/server/bearer.go`, `gzip.go` | move | Go to `core/httpx`. The Bearer scheme is matched case-insensitively everywhere, and any other scheme gets a 401. | P2b |
| `core/server/docs.go`, `core/server/locales/` | keep | Kept for the admin API only. The PDP does not serve `/docs`; `licetum-pdp openapi` prints the committed spec. | P2b |

### 9.6 Database, revisions, migrations (`core/db`)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/db/db.go`, `tracer.go` | keep | | |
| `core/db/revision.go` | rewrite | One `global` row holds `(epoch, revision)`. `PolicyTx` runs in this order:<br>- `pg_advisory_xact_lock(admin_write)`;<br>- writes;<br>- as the last statement, the CTE bump and `INSERT outbox`;<br>- `NOTIFY` in the same transaction, which wakes the publisher on commit.<br>`subject_directory` takes the revision row only, not `admin_write`.<br>Deleted: the scopes `rules`, `data:<svc>` and `log_settings`, plus `DataScope`, `ServiceOfDataScope`, `QueueBumpRevision` and `BumpAllRevisions`. The epoch procedure of `licetum-writer failover` replaces `BumpAllRevisions`. | P3, P6 |
| `core/db/migrate.go` | rewrite | The goose session locker and `seedLockKey` stay. CQL migrations run under the same advisory lock and also create, ahead of time:<br>- the generation keyspaces of every DC;<br>- the CQL roles and their grants.<br>Migrations move to `licetum-writer migrate`.<br>`SeedPlatform` and `seedAdminGroup` check `ADMIN_AUTH__*` instead of `PLATFORM__*` in P2d. In P4 they are deleted together with `seedPlatformType`, so no synchronizer keeps Licetum's own `DB__URI`. `SchemaVersion` reads the v2 version table. | P2d, P3, P4, P5, P6 |
| `core/db/restore.go` | move | Goes to `admin-api/bootstrap` (AGPL) and becomes an upsert of `platform_admin` bindings through `PolicyTx`, actor `restore-admin@<host>`. Its `IsConfigured` check moves to `ADMIN_AUTH__*` in P2d. | P2d, P4 |
| `core/db/migrations/0000[1-5]*.sql` | delete | `00001_init.sql` stays at tag `v0.1.0`, which ships only that file and equals the current one; `00002`–`00005` stay at `archive/pg-replica-design`. Schema v2 is a new `00001_init.sql` recorded in its own goose version table (`goose_db_version_v2`). `migrate` refuses a database that has `goose_db_version` and says that v0.1.x cannot be upgraded. There is no upgrade path, and the release notes say so. After v2 is released, a schema change is again a new file. | P3 |

### 9.7 Models, configuration, binaries and the other core packages

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `core/model/oauth.go`, `delegation.go`, `subject.go` | delete | `PolicyClaim` lives on as the answer of `GET /service/{ref}/policy/properties`. `subject.go` is replaced by the model of the directory. | P2c |
| `core/model/platform.go` | delete | The uuid5 ids, `PlatformType`, `Action` and `PlatformAccess` go with the platform self-service. | P4 |
| `core/model/check.go` | split | **Kept:** `Explanation`, `RuleRef`, `DenialRef`, `Anchor`, `CheckRequest` and `CheckResult` (internal types).<br>**Deleted:** `CheckInput` and `CheckResponse`.<br>**Changed:** `RequestContext.Validate` ignores objects and nested lists (conditions see them as missing) instead of answering 400. `ValidContextName` refuses `licetum`. | P2b, P2c |
| `core/model/authzen.go` | rewrite | Required fields are set per endpoint: search ignores the ids the spec says to omit. Adds `resource.id = "*"` and `context.licetum`. | P2b |
| `core/model/service.go` | rewrite | The OAuth fields, `client_secret`, `trusted_clients` and `NormalizeTrustedClients` are deleted. The slug is set at creation and never changes: `ServiceUpdate.Slug` (`service.go:90`) goes, and a slug is never reused after its service is deleted. A service belongs to an identity domain. | P2c, P3 |
| `core/model/{assignment_rule,role,group,type_resource,synchronizer,policy}.go` | rewrite | Adds `subject_type`, `subject_group_id` (instead of the literal `group:<uuid>`), `source: subject`, `resource_from_property`, `exported`, desired and effective parents and `refresh_interval`; drops `cache_ttl`. `PolicyDocumentFormat` becomes `licetum.policy/v2`, and a v1 document is refused. | P2e, P3, P4 |
| `core/config/config.go` | rewrite | **New sections, per role:** `PDP__`, `READMODEL__` (the store, and for PG mode a DSN that may point at a streaming replica), `CQL__`, `PEP__`, `ADMIN_AUTH__`, `WRITER__`, `PUBLISH__`, `OUTBOX__`, `APPLY__`, `MVCC__`, `GENERATION__`, `CHECKPOINT__`, `LOG__` (`LOG__SUBJECT_SALT`).<br>**Changed:** `POLICY__MAX_STALENESS` defaults to 60 s, and 0 is refused.<br>**Renamed:**<br>- `HTTP__PUBLIC_URL` → `PDP__PUBLIC_URL`;<br>- `REVISION__POLL_INTERVAL` and `REVISION__WAIT_MAX` → the poll interval of `PDP__` and `PDP__FRESHNESS_WAIT`.<br>**Deleted:**<br>- Redis and the old sign-in: `REDIS__*`, `AUTH__STATE_*`, `AUTH__INVALID_TOKEN_TTL`;<br>- `PLATFORM__*`: the OAuth half in P2d, the rest in P4; bootstrap uses `ADMIN_AUTH__BOOTSTRAP_*`;<br>- removed features: `DELEGATION__*`, `SUBJECTS__*`, `CORS__*`;<br>- replicas and the snapshot: `DB__READ_URI`, `DB__MAX_REPLICA_LAG`, `DB__LAG_*`, `SNAPSHOT__*`;<br>- the request-driven sync and the table engine: `SYNC__ENGINE`, `SYNC__REQUEST_WAIT`, `SYNC__DEMAND_WINDOW`, `SYNC__KEEP_WARM`, `SYNC__FETCH_TTL`, `SYNC__LOCK_*`, `WORKER__REGION`.<br>**Kept:** `SYNC__LEASE`, which times the executor heartbeat and the janitor's takeover of DBOS workflows (`dbosHeartbeat`, `dbosJanitor`, 4.9.1). | P2–P6 |
| `core/cli/cli.go` | rewrite | `Serve`, `StopServing` and `connectionBudget` stay, per process. `Worker` is deleted. `Failover` becomes the epoch procedure. `roleOf` knows the roles `pdp`, `writer` and `apply`. | P2b, P5, P6 |
| `core/cmd/licetum` | split | `licetum check` becomes `licetum pdp` in P2b. Later there are two binaries in the same image:<br>- `core/cmd/licetum-pdp`: `serve`, `openapi`;<br>- `core/cmd/licetum-writer`: `run`, `fetch`, `apply`, `migrate`, `rebuild`, `reconcile`, `checkpoint`, `failover`, `freeze`, `unfreeze`.<br>`licetum worker` is deleted. | P2b, P5, P6 |
| `core/redisx/*` | delete | Goes with every use of Redis: answer and token L2, breakers, `authz:fetch_all`, PKCE, the counters kept for the previous release, and the retention lock. The exit check: `go list -deps` of both modules shows no go-redis. | P2e |
| `core/schema/check.yaml` | rewrite | Becomes `core/schema/pdp.yaml` with the AuthZEN routes only, next to the snapshot of the CQL schema (API01). | P2b, P6 |
| `core/apperr`, `core/logx` | keep | | |
| `core/i18n/messages.go` | keep | Keys of deleted features go, because `TestCatalogComplete` fails on unused keys. New keys are added for AuthZEN, PEP and the admin roles. A key's value stays the public `extra.code`. | each phase |

### 9.8 New packages

| Package | Module | Contents | Phase |
|---|---|---|---|
| `core/decide` | core (MIT) | The pure engine: levels, type graph, predicates, `Prepare`, `CoversOf`, explanation, TTL and Hold. depguard forbids pgx, DBOS, the CQL driver and `net/http` here. | P5 (DEC tables from P1) |
| `core/policy` | core | The policy at P (streams `catalog`, `pep`, `settings`, `svc:<id>`), `Fold`, the codec. | P5 |
| `core/readmodel` | core | The ports `ReadModel` and `LogSource`. | P5 |
| `core/readmodel/pgstore` | core | The PostgreSQL adapter, against the primary or a streaming replica, under a SELECT-only role:<br>- reads the policy in one `REPEATABLE READ READ ONLY` transaction, with the revision read inside it;<br>- reads resources at one position;<br>- follows the outbox. | P5 |
| `core/readmodel/cqlstore` | core | The CQL adapter:<br>- CQL migrations;<br>- write helpers that refuse a statement without `USING TIMESTAMP`;<br>- reads at P with LOCAL_QUORUM.<br>The model stays Cassandra-compatible: no LWT, no materialized views, no secondary indexes, nothing tablets-only. The driver is pinned after P0.5. | P6 |
| `core/readmodel/memstore`, `storetest` | core | The in-memory adapter, and the ST suite that runs against every adapter. | P5 |
| `core/outbox` | core | `Project`: the canonical images of entities and resources. The write path, checkpoints, `rebuild` and `reconcile` all use it. | P3 |
| `core/logfmt` | core | Log entry, chunk and checkpoint formats, hashes, reading formats N and N−1. | P6 |
| `core/writer/sync`, `graph`, `fetch` | core | DBOS synchronization, graph apply and closure, the region-local fetcher. | P5 |
| `core/writer/publish` | core | The publisher. A single leader, woken by `NOTIFY` and a periodic tick, reads `outbox WHERE revision > last` and writes the log. The leader also writes the heartbeat, runs retention and polls the issuer keys. | P5 (the leader: heartbeat, outbox retention, issuer keys), P6 (publication to the log) |
| `core/writer/checkpoint`, `reconcile`, `failover` | core | Checkpoints per stream; `reconcile` against the outbox (P5) and of every region, nightly and mandatory (P6); epochs. | P5 (`reconcile` against the outbox), P6 (checkpoints, `reconcile` of the regions, epochs) |
| `core/applier` | core | The regional applier: contiguity, hashes, MVCC rows. It starts append-only with manual generation rotation. The MVCC garbage collection is switched on only after APP and ASOF pass under kill tests. | P6 |
| `core/pdp/view`, `cache`, `authzen`, `decisionlog` | core | The view (position, generation, T(P), frozen), the staleness gate in front of the caches, the answer L1 and the row cache, the AuthZEN handlers, the decision log. | P5, P6 |
| `core/pepauth` | core | Verification of PEP JWTs and API keys, and JWKS fetching for the writer and the admin API. | P2b |
| `core/httpx` | core | The server plumbing of `core/server`. `bearer.go` and `gzip.go` move in P2b, the rest by P5 (9.2, 9.5). | P2b, P5 |
| `core/testsupport/refeval` | core | The reference evaluator, in external terms (type names, external ids). It shares no code with `decision.go` or `check.go`, and production code does not import it. It is not under `core/internal`: DECP runs the engine through the `admin-api` tests, and Go does not let another module import an `internal` package. | P1 |
| `core/testsupport/policygen`, `crash` | core | `policygen`: the generator taken from `snapshot_differential_test.go`. `crash`: kill -9 by re-exec. `policyfix` already exists. | P1, P5 |
| `admin-api/authz` | admin-api (AGPL) | The role matrix (`matrix.go`), bindings, scopes, the last `platform_admin` and the last `service_owner`. | P4 |
| `admin-api/bootstrap` | admin-api | `bootstrap`, which runs once (`platform_state`), and `restore-admin`. | P4 |

### 9.9 Admin API (`admin-api`)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `admin-api/api/auth.go` | keep | The mechanism stays: `withPolicy`, `platformAuth`, `server.PolicyMetadataKey`. Authorization runs before the body is parsed, so a stranger gets 403, not 422. In P2d the token is checked against `ADMIN_AUTH__*`, with authorization still by the platform policies. In P4 a policy becomes `(scope, kind, action)` from `admin-api/authz`, and `filterAllowed` gives way to `WHERE service_id = $S`. | P2d, P4 |
| `admin-api/api/admin_targets.go` | rewrite | Checks scopes, backed by the composite foreign keys. `authorizePlatformTargets` goes, and so does its wrapper for role items, `checkPlatformTargets` (`admin-api/api/admin.go:784`). Retargeting a rule needs what creating it needs. A rule that grants a role is checked against every item of the role. The lookup type of `resource_from_property` must be the service's own type or an exported ancestor. | P4 |
| `admin-api/api/admin.go` | rewrite | All routes move under `/service/{ref}/`, including those that have no service in the path today:<br>- `GET /type_resource` (`admin.go:267`);<br>- `/type_resource/{id}/resource` (`:296`) and `…/resource/refresh` (`:314`);<br>- `/group…`, `/role…`, `/assignment_rule…`.<br>A child entity of another service is a 404. `registerOAuth` is deleted after P2d. `registerPlatform` is replaced by `GET /me` and `/platform/*`. | P2c, P4 |
| `admin-api/api/admin_batch.go`, `admin_policy.go`, `admin_access_groups.go` | keep | Each is limited to one service. The batch also covers types, synchronizers (without secrets) and type links, so revert and rollback no longer fail with `change_not_revertible` because of the kind of a record (8.7). | P4 |
| `admin-api/api/admin_ops.go` | rewrite | Simulation becomes `/service/{ref}/simulate/access/v1/{evaluation,evaluations,search/*}`. Explain is always on, and the admin right `simulate` is required. `claimsOf` becomes `properties`. | P2c |
| `admin-api/api/admin_subjects.go` | replace | `/service/{ref}/subjects` on `subject_directory`. | P2c |
| `admin-api/service/platform.go` | delete | `Authenticate` becomes admin OIDC verification (P2d) and the bindings (P4). `Access`, `FilterAllowed` and `GetPlatform` go in P4. | P2d, P4 |
| `admin-api/service/admin_access.go` | rewrite | Moves into `admin-api/authz`. Under `admin_write`, a write is refused with 409 `last_platform_admin` or 409 `last_service_owner` when it would leave none. | P4 |
| `admin-api/service/app.go` | rewrite | No longer embeds `engine.App`. It holds the PostgreSQL pool, the admin token verifier, `pgstore`, and the DBOS client for `SyncOne` and fresh syncs. | P5 |
| `admin-api/service/tx.go` | rewrite | `mutate` becomes `core/db.PolicyTx`:<br>- the lock, then a recheck of the caller's binding;<br>- the write, then the invariants;<br>- the bump, the outbox and `change_log` sent in one batch, then `NOTIFY`.<br>The round-trip count is measured (WR06), not assumed. | P3 |
| `admin-api/service/batch.go`, `history.go`, `changelog.go` | keep | The ideas stay. `change_log` is ordered by `(revision, seq)` and replaces `authz_change` with its order by `id`. `PlanRollback` covers only the records of the service in the path; today `changesAfter` reads every service (`changelog.go:131`). | P3, P4 |
| `admin-api/service/policy_document.go` | rewrite | The document becomes the whole unit of one service: types, access groups, groups, roles, rules, sources without secrets, and exported links by slug. Format `licetum.policy/v2`. | P4 |
| `admin-api/service/delegation.go` | delete | Only `PolicyClaims` survives, as `GET /service/{ref}/policy/properties`. | P2c |
| `admin-api/service/subjects.go` | replace | `subject_directory` per service, written in bulk PUTs of up to 1000. It is written by editors or by the application's machine client with the `directory_writer` role (P4), rate-limited, with retention per service. | P2c, P4 |
| `admin-api/service/{access_lookup,grant_preview,policy_tests,lint}.go` | rewrite | Run on `core/decide` + `pgstore`. Upstream rules are shown read-only. These tools read the primary; that load is measured, and a read URI for these tools only is added if the measurement calls for it. | P5 |
| `admin-api/service/{services,type_resources,groups,roles,assignment_rules,access_groups,synchronizers}.go` | rewrite | **Ownership and images:** entities are owned by a service through composite foreign keys, and the outbox gets their full canonical images.<br>**Type links:** a type may link only to an exported type of the same identity domain. While it is linked, the exported type cannot lose the flag or be deleted (409 `blocked_by`).<br>**Services:** the slug is immutable and never reused; only `platform_admin` deletes a service.<br>**Access groups:** they check the service in the path; today they use `GetExistingTypeResourceAnyService`. | P2e, P3, P4 |
| `admin-api/service/{source_check,log_settings,policy_scenarios}.go`, `batch_check_test.go` | keep | Log settings are at platform scope and are published as the `settings` stream. | P5 |
| `admin-api/cmd/licetum-admin` | rewrite | The subcommands become `serve` (was `admin`), `bootstrap`, `restore-admin` (from `admin-api/bootstrap`) and `openapi`. | P4 |

### 9.10 Tests, test support, load test

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `admin-api/tests/integration/api/{graph,hierarchy,conditions,roles,gaps,escalation}_test.go` | split | Their scenarios are extracted first: they become DEC tables (`policyfix` YAML), ADM04 and a thin AuthZEN smoke test. Then the files are deleted. | P1, P4, P5 |
| `…/api/snapshot_differential_test.go` | rewrite | Its generator goes to `core/testsupport/policygen`. The comparison becomes DECP (the engine, `core/decide` from P5, ≡ `refeval`) and REF01. | P1 |
| `…/api/{sync_test,sync_crash_test,regions_test}.go` | rewrite | SYN01–SYN09 | P5 |
| `…/api/{delegation_test,audit_test,f4_test,f4_kill_test}.go` | delete | They go with their features. | P2c |
| `…/api/authzen_test.go` | rewrite | AZ01–AZ14 | P2b |
| The rest of `…/api/` | rewrite | ADM, WR, HIS, API, CONS | P3–P6 |
| `admin-api/tests/integration/{cluster,chaos,harness}` | replace | First, `Replica.Permission`/`Fresh` move to evaluation and resource search (P2a). Then the suites are replaced by `admin-api/tests/e2e`, with one topology builder (PostgreSQL, CQL, writer, admin, N PDPs, fake IdP and source), and by `admin-api/tests/chaos`. | P2a, P5–P7 |
| `admin-api/tests/multiregion` | rewrite | Becomes `admin-api/tests/multidc`, with two topologies: two CQL data centers behind Toxiproxy, and regions on PostgreSQL streaming replicas. | P7 |
| `core/tests/integration/redisx`, `core/engine/replica_test.go` | delete | | P2e |
| `core/tests/integration/db/migrate_test.go` | rewrite | OPS04, and the refusal of a v0.1.x database. | P3 |
| `core/tests/unit/service/{oauth_test,permission_page_test}.go` | split | The verifier cases become PEP01 in `core/pepauth`; the rest is deleted with its code. | P2c |
| `core/tests/unit/service/predicate_test.go` | move | DEC08 | P5 |
| `core/engine/{jwks,lru,admission,background,logging}_test.go` | move | They follow their packages. | P5 |
| `core/engine/snapshot_test.go` | rewrite | Tests of the `pgstore` loader (revision inside the snapshot) and of `core/policy`. | P5 |
| `core/testsupport/{containers,fault,idp,source}.go` | keep | `containers.go` adds a CQL node (one keyspace per package) and drops Redis. The fake IdP issues PEP client-credentials and admin tokens. | P2e, P6 |
| `admin-api/loadtest` | keep | `oracle.go` (`expectedAllowed`), `budget.go` and `profile.go` stay. The mix (`mix.go`, `check.go`, `k6/check.js`) moves to AuthZEN in P2a. The Redis measurements and Redis faults go in P2e. In P7 the budgets add store reads per evaluation and are recalibrated on a fixed runner without `-race`. | P2a, P2e, P7 |

### 9.11 Web interface (`web/`)

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `web/src/app/auth/*`, `web/src/lib/admin-api.ts` | rewrite | The BFF becomes the OIDC client of the admin IdP: code flow with PKCE, with the exchange and refresh at the IdP on the server. `code_verifier`, `state` and `nonce` live in a short-lived encrypted cookie. The `/platform` lookup and `/service/{id}/oauth/*` go. | P2d |
| `web/src/features/services/connect-wizard.tsx` (OIDC step), `delegation-section.tsx`, `service-overview.tsx` (sign-in card); `web/src/app/api/discover`, `web/src/app/api/check` | delete | | P2c |
| `web/src/features/services/integration-tab.tsx`, `web/src/features/check/check-workspace.tsx` | rewrite | They send AuthZEN requests (simulate and the integration snippets) and manage PEP clients and keys. | P2c, P4 |
| `web/src/lib/access.tsx` | rewrite | Becomes `can(serviceId, kind, action)` and `canPlatform`: `canPlatform` from `GET /me`, `can` from `GET /service/{ref}/me` (8.5). | P4 |
| `web/src/features/shell/*` | rewrite | A service switcher, with routes `/services/[slug]/{rules,people,groups,roles,check,history,integration,sources,types,policy,access}`. | P4 |
| `web/src/features/settings/settings-admins.tsx` | split | Split into platform bindings and service bindings. | P4 |
| `web/src/features/data/queries.ts` | rewrite | Uses paged, service-scoped endpoints, with no global lists and no N+1 queries. | P4 |
| `web/src/features/people/*` | rewrite | Reads the service's `subject_directory`. | P2c, P4 |
| `web/src/features/{grant,roles,access,history}/*` | rewrite | `claim` becomes `subject`/`property`, with `subject_type` and `subject_group_id`. History is by revision and per service. | P3, P4 |
| `web/src/features/services/sources-tab.tsx` | rewrite | `cache_ttl` goes in P2e; `refresh_interval` comes in P5. | P2e, P5 |
| `web/src/lib/{session.ts,safe-return.ts,env.ts,schemas.ts}`, `web/src/proxy.ts`, `web/src/app/api/admin/[...path]` | keep | `env` and `schemas` add the OIDC settings and the IdP token answers. | P2d |
| `web/src/i18n/locales` | keep | Keys follow the screens in en, ru and zh. | each phase |

### 9.12 Deployment, Compose, CI, documentation

| Path | Decision | Where and how | Phase |
|---|---|---|---|
| `deploy/helm/licetum/templates/check.yaml` | rewrite | Becomes `pdp.yaml`: in every region, with an ops port. Egress goes only to the CQL store (9042/9142), or to PostgreSQL in PG mode. There are no PostgreSQL secrets in CQL mode. | P2b, P7 |
| `templates/worker.yaml` | rewrite | Becomes `writer`: two StatefulSets, `sync` (with a stable `DBOS__VMID`) and `project` (no DBOS), and egress to the sources and to the JWKS of the PEP issuers. | P5, P7 |
| `templates/` for `apply`, `fetch`, `reconcile` | add | `apply` runs in every region as a Deployment of 2, with a Lease and its RBAC, and no PostgreSQL. `fetch` runs only in regions that have regional sources. `reconcile` is a CronJob. | P6, P7 |
| `templates/migrate.yaml` | rewrite | Runs the PostgreSQL and CQL migrations, creates the generation keyspaces, and sets up the CQL roles and grants. | P6 |
| `templates/{admin,web,configmap,secret,serviceaccount}.yaml` | rewrite | New settings; no Redis, no `PLATFORM__*`, no `AUTH__STATE_*`. | each phase |
| `templates/{networkpolicy,servicemonitor}.yaml` | rewrite | One of each per component, plus a PrometheusRule with the alerts of the metric catalog. ε comes from `node_timex_*`. | P7 |
| `values-cell.yaml`, `ci/*.yaml`, `tests/golden` | rewrite | A cell has its own PostgreSQL and its own CQL cluster. The CQL cluster lives outside the chart (an operator), with 3 nodes per region at RF 3. Golden files are regenerated in every PR that changes the chart. | P7 |
| `deploy/helm/kind-test.sh`, `test.sh` | rewrite | The rolling upgrade runs under AuthZEN evaluation load; today it loads only `/healthz`. | P7 |
| `docker-compose.yml`, `infra/redis.compose.yml` | delete | Redis is removed. | P2e |
| `infra/dex.compose.yml`, `infra/dex/` | rewrite | The redirect goes to the web `/auth/callback`, plus a client-credentials client for PEP tests. | P2d |
| `infra/cql.compose.yml` | add | One node, in developer mode, of the store chosen in P0.5. | P6 |
| `infra/postgres.compose.yml`, `scripts/pg-replica.sh`, `scripts/hooks` | keep | `pg-replica.sh` serves PG mode on a streaming replica. | |
| `.github/workflows/ci.yml` job `previous-release` | delete | v0.1.x is not upgraded. The rule "Migrations must work with the previous release" (`CONTRIBUTING.md`) applies again from the first v2 release. | P3 |
| `ci.yml` job `go-test-replica` | delete | The routing tests go in P2e, and the job with them (11.6). From P5 the PG-mode contracts run against a streaming replica in the nightly suite (`scripts/pg-replica.sh`, `task go:test:integration:replica`, 10.6). | P2e, P5 |
| `ci.yml` job `go-test` | rewrite | One integration run on PostgreSQL plus one CQL node. | P6 |
| `pgbouncer.yml`, `nightly-suite.yml`, `chaos.yml`, `loadtest.yml`, `helm.yml`, `codeql.yml`, `release.yml` | keep | PgBouncer covers the write path only. Releases ship two core binaries in one image. | |
| `multiregion.yml`, `Taskfile.yaml` `go:test:multiregion` | rewrite | Become `multidc` and `go:test:multidc`. | P7 |
| `docs/deployment.md`, `core/README.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `admin-api/README.md`, `web/README.md`, `admin-api/tests/integration/README.md`, `docs/README.ru.md`, `docs/README.zh-CN.md` | rewrite | `docs/deployment.md` was ignored by `docs/*` and never committed; P0 adds the existing guide unchanged, and P7 rewrites it.<br>**Content:**<br>- the topologies: one region on PostgreSQL, regions on streaming replicas, regions on the CQL store;<br>- the SLOs, the failure matrix, sizing and the upgrade order.<br>**Runbooks:**<br>- repair after a node replacement (a correctness requirement);<br>- rebuilding a DC, generation rotation;<br>- failover and epochs, `freeze`, PITR.<br>Each phase updates the parts it changes. | P7 |
| `docs/adr/0001-regional-pdp.md`, `.gitignore` | add | This document. `.gitignore` ignores `docs/*` today, so P0 adds `!docs/adr/` and `!docs/deployment.md`. | P0 |

## 10. Tests as contracts

The acceptance criteria of the epic are contracts. They replace the F-criteria of #31. A contract states one property of the system that a test can refute. It names what the answer is compared with (its oracle) and the environment it runs in (its level). The contracts are listed in `contracts.yaml` at the root of the repository, and each one is checked by tests that carry its ID in their name. A phase is done when the contracts it makes active pass. Pull requests and the tracking issue name contract IDs where they used to name F-IDs.

### 10.1 The index and the check

`contracts.yaml` holds one entry per contract:

| Field | Meaning |
|---|---|
| `id` | 3 to 8 capital letters and digits (`^[A-Z][A-Z0-9]{2,7}$`), unique. This is why the property test of the decision engine is `DECP`, not `DEC-P`. |
| `statement` | The property in one or two sentences, in external terms: type names, external ids, routes, status codes. |
| `oracle` | What the answer is compared with. It never shares code with what it checks (10.3). |
| `level` | The environment the contract runs in once the epic lands (10.2). DEC01 to DEC13 and DECP are the exception until P5: the index gives them `pg`. |
| `phase` | The phase that makes the contract active (P1 to P7). |
| `status` | `planned`, `active` or `retired`. A retired entry stays in the index, so its ID is never reused. |

A Go test that checks a contract is named `TestContract_<ID>_<What>`, for example `TestContract_DEC02_ExceptionLiftsOnlyStrictlyBelow`. A Jest test of the web interface has a name that starts with `TestContract_<ID>_`. Several tests may check one contract. A contract whose cases live in fixtures has one test function that runs every fixture listing it.

`core/internal/tools/contractscheck` keeps the index and the tests in step. It reads `contracts.yaml` and validates every entry: the id format, no duplicate id, a statement, an oracle and a phase, a known level and a known status. It then scans every `*_test.go`, `*.test.ts` and `*.test.tsx` under the root for `TestContract_<ID>_` names. It skips `.git`, `.claude`, `node_modules`, `.next`, `vendor`, `reports` and `dist`. It fails when:

- a test names a contract that the index does not list;
- a `planned` contract already has a test (it must be marked `active` in the same pull request);
- a `retired` contract is still tested;
- an `active` contract has no test.

It runs as `task contracts:check`. That task is part of `task go:lint`, so it also runs in `task lint`, in `task check` and in the job "Go lint and schema checks" of `ci.yml`.

The check reads names only. It does not look at build tags or results, so it cannot prove that a test runs at the contract's level or that it asserts anything. Review enforces three rules that cover this gap:

- **Only the code under test makes a contract active.** A test of an oracle alone is not a contract test, for example `refeval` answering the decision tables or the generator checking its own guards. It is named after what it checks of the oracle, so `active` always means that the product passes.
- **No silent skips.** Once its contract is active, a table-driven contract test fails when no fixture lists the contract; it does not skip. Each fixture lists the contracts it checks (`contracts:` of a `policyfix` fixture), and the fixture tests refuse a fixture that lists none.
- **No latency figures under `-race`.** A contract with a timing figure runs only in a job without `-race` (10.6).

New tests do not use:

- F-identifiers (`TestF3_4_…`). Today `admin-api/tests/integration/README.md` asks for them.
- The tier letters U/I/M/L/C/R of #31 (the tier column of that README uses I, M and C).
- Constants that scale a figure under `-race`: `raceFactor` (`admin-api/tests/integration/cluster/race_test.go`), `slowdown` and `loadDivisor` (`admin-api/tests/integration/chaos/{race,norace}_test.go`). A latency is either a contract measured without `-race` or not asserted.

The existing F-tests stay green until the contracts that replace them are active (10.8). P7 removes what is left of them, together with the tier tables and these constants.

### 10.2 Levels

| Level | What runs | Build tag | When |
|---|---|---|---|
| `unit` | Pure code and components with fakes, no database, under `-race`. | none | every pull request |
| `pg` | One PostgreSQL: Testcontainers, or the server named by `LICETUM_TEST_POSTGRES_URI`. Each test gets its own database (`Infrastructure.Isolate`, `core/testsupport/containers.go`), so the tests run with `t.Parallel`. | `pg` | every pull request |
| `cql` | One CQL node in developer mode: ScyllaDB with `--smp 1 --memory 1G --overprovisioned`, or Apache Cassandra 5. One keyspace per test package, created once. Tests within a package are separated by a namespace column in the partition key, or each takes a keyspace from a pool created up front. No test runs DDL of its own, because concurrent schema changes are slow and flaky. | `cql` | every pull request against the store chosen in P0.5; nightly against the other one |
| `system` | The processes of a topology inside one test binary: PostgreSQL, the CQL store when used, the writer, the admin API, N PDPs, the fake IdP and the fake source. | `system` | a smoke run without `-race` on every pull request, the full set nightly |
| `fault` | Toxiproxy (`core/testsupport/fault.go`), and `kill -9` of a process started by re-executing the test binary. | `fault` | one crash point per contract as a smoke run on pull requests, every point nightly |
| `multidc` | Two CQL data centers with Toxiproxy between them, in Compose (`admin-api/tests/multidc`, which replaces `admin-api/tests/multiregion`). | `multidc` | nightly |
| `load` | k6 against a running stack (`admin-api/loadtest`). | none | smoke on every pull request, medium and large nightly |
| `helm` | `deploy/helm/test.sh` (lint, golden files, kubeconform) and `deploy/helm/kind-test.sh` (install, upgrade). | none | `helm.yml` |
| `web` | Jest (`web/tests`) with the API mocked. | none | every pull request |

The tags `integration`, `chaos` and `multiregion` belong to the current suites. A contract test uses the tag of its level. The old tags are removed with the suites they carry, in P7 at the latest.

`level` is the level the contract runs at after the epic. Before the code it needs exists, a contract may be checked at a heavier level. In P1 to P4 the DEC tables run against the current engine through the admin API and PostgreSQL (`pg`), because `core/engine` reads its inputs from the database. In P5 they move to `core/decide` and run as `unit`. Until then the index gives DEC01 to DEC13 and DECP the level they run at, `pg`, and P5 changes it to `unit` (10.9). The target of under 30 seconds without a database applies from then on.

### 10.3 Oracles

An oracle never shares code with what it checks. The oracles of the epic are:

- **Decision tables.** These are the fixtures of `core/testsupport/policyfix` (`testdata/decisions/*.yaml`). Each holds a policy, a resource graph, and questions with their expected answers and explanations, written from the semantics.
  - They use external terms from the start: service slugs, type names, external ids, and the names of groups, roles and rules. The same files therefore still hold after P5 moves the anchors from `resource_cache.id` to `(type_id, external_id)`.
  - The first fixture is the worked example of the decision semantics (`worked_example.yaml`).
  - P1 extracts the scenarios of `admin-api/tests/integration/api/{graph,hierarchy,conditions,escalation,gaps,roles}_test.go` into fixtures, one table row per scenario.
- **`core/testsupport/refeval`.** An in-memory evaluator of the same semantics, written apart from the engine. It builds lineages and the type flow by walking the declared links. It imports only the standard library, `core/model` and `policyfix`. depguard keeps it from importing `core/engine`, `core/server` and `core/db`; the rule that no production package imports it comes with the new packages (10.6).
- **`core/testsupport/policygen`.** The generator of random policies and graphs. It starts from `policyGenerator` of `admin-api/tests/integration/api/snapshot_differential_test.go`.
  - A run fails when its policies are degenerate. It needs at least 20 non-empty policies, 100 allowed answers and 20 denied answers.
  - Every run prints its seed, and the seed repeats the run.
- **PostgreSQL at an exact revision.** The canonical loader reads the state in one `REPEATABLE READ` transaction and reads the revision inside it. POL01, ASOF and REF compare with it.
- **A second read-model adapter.** It is a secondary oracle only (REF02): two adapters can share a bug of the code above them.
- **Counting fakes.** The fake source (`core/testsupport/source.go`) counts calls per type. The fake IdP (`core/testsupport/idp.go`) counts key set fetches.
- **The specification.** The examples of the AuthZEN Authorization API 1.0 are kept as vectors (`core/pdp/authzen/testdata/spec`). For tokens the oracles are RFC 6750, RFC 7519 and RFC 9068.
- **The load test oracle.** `expectedAllowed` of `admin-api/loadtest/oracle.go` decides each generated request without the server.
- **Clocks.**
  - A manual clock (today `App.clock`) for validity boundaries and the lifetime of cached answers.
  - The wall clock of a runner without `-race` for latency contracts.
  - For REV03, the clock skew ε measured from the clock discipline of the hosts (chrony, `node_timex_*`). It is not measured from the heartbeat, which mixes skew with lag.

### 10.4 Catalogue

**DEC: decision semantics.** From P5 these run in `core/decide`. Before that they run against the current engine through the admin API at `pg` (10.2).

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| DEC01 | A denial beats any allow, wherever the allow lies. | decision tables | unit | P1 |
| DEC02 | An exception lifts a denial only strictly below it. The deepest of the denials and exceptions above a resource decides, and a tie goes to the denial. | decision tables | unit | P1 |
| DEC03 | "Below" is decided per level: a point below a point by the lineage, a whole type below a higher type, a point below the whole of its own type. | decision tables | unit | P1 |
| DEC04 | `access_implies` works both ways: granting `write` grants `read`, and denying `read` denies `write`. The denial level `*` denies every level. | decision tables | unit | P1 |
| DEC05 | The `inherit` masks of type links limit what flows down. With several parents, the levels are the union over the routes of the intersection along each route. | decision tables | unit | P1 |
| DEC06 | A `not_sync` type is decided by whole-type rules only, with no cascade from ancestors. A whole-type rule on it still cascades to the resources of `sync` descendant types. | decision tables | unit | P1 |
| DEC07 | Validity windows of rules and memberships are honoured at the time of the decision. | decision tables with a manual clock | unit | P1 |
| DEC08 | The condition operators are `eq ne in not_in regex gt ge lt le in_cidr`. A missing attribute never satisfies a condition, `ne` and `not_in` included. | decision tables | unit | P1 |
| DEC09 | `on_missing_attribute` decides whether a denial applies on an unknown condition. A membership whose predicate reads a missing attribute is unknown, so a denial on the group applies. | decision tables | unit | P1 |
| DEC10 | `requires_predicate` and `requires_group_id` guard any rule. `resource_where` matches scalars and list elements. `resource_from_claim` (`resource_from_property` from P3) matches by `name`, `id` and `meta.<attribute>`. | decision tables | unit | P1 |
| DEC11 | A resource with an unresolved parent link, or below an ancestor that has one, is treated as possibly under every denial that could cover the missing parent (fail closed, transitively). The release notes announce this change of behaviour. | decision tables | unit | P1 |
| DEC12 | A role assignment expands to its items with the effect and the exception of the rule. Nested groups resolve up to 16 levels (`MaxGroupDepth`). `@name` access groups expand to their levels. | decision tables | unit | P1 |
| DEC13 | An explanation names the granting rules, every denial with the exception that lifted it, and the denials assumed for an unresolved parent. | decision tables | unit | P1 |
| DEC14 | The time an answer is kept and the time it stays correct (`Hold`) both end exactly at the next validity boundary. | property test with a manual clock (`hold_property_test.go` moves here) | unit | P1 |
| DECP | The engine and `refeval` give the same decisions on generated policies and graphs: from P5, when the decision core is pure, 2000 fixed seeds on every pull request and random seeds nightly; until then seeds 1–20 on every pull request and 21–520 nightly. | `refeval`, `policygen` | unit | P1 |

**ST: read-model stores.** `core/readmodel/storetest` runs against `memstore` (`unit`), `pgstore` (`pg`) and the CQL adapter (`cql`).

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| ST01 | The lineage of every resource, the resource itself included, with its masks equals the definition of the closure. | a recursive walk of the edges in the test; `rebuildClosureSQL` | pg | P5 |
| ST02 | The descendants of an anchor by type, read page by page, equal a range scan of the PostgreSQL closure. | the PostgreSQL closure | pg | P5 |
| ST03 | Listing a type gives no gaps and no repeats while the type is written concurrently. | the set written | pg | P5 |
| ST04 | A lookup by attribute finds scalars and list elements. A lookup by a non-unique name works. | the rows written | pg | P5 |
| ST05 | A resource that was switched off is absent from a read at a position after the switch and present at a position before it. | the history written | cql | P6 |
| ST06 | `unresolved_up` is transitive and equals `resource_unresolved` of PostgreSQL. | PostgreSQL | pg | P5 |
| ST07 | Every CQL row is written with the timestamp `ts(position, phase)` of its position, or with the time domain 5.1 names for the cells that are not positions. The write helper refuses a statement without `USING TIMESTAMP`. | `WRITETIME` of the rows | cql | P6 |
| ST08 | The garbage collection of versions never deletes the newest version of a key. Table options (compaction) match the schema. The collection is switched on only after APP01 to APP03 and ASOF01 to ASOF03 pass under the kill tests. | the versions written | cql | P6 |
| ST09 | The CQL schema and the statements of the CQL adapter use no lightweight transaction, materialized view, secondary index or tablets-only option. `storetest` passes on ScyllaDB and on Apache Cassandra 5. | the schema snapshot; both stores | cql | P6 |
| ST10 | The applier refuses to write a version older than the GC horizon of its key, and the PDP refuses a read position older than the GC horizon (ADR 0002). | writes and read positions injected below the horizon, which the test derives from the applied positions and `MVCC__RETENTION` | cql | P6 |

**POL, PUB, APP, ASOF, REC: the log, publication, application, reads as of a position, reconciliation.**

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| POL01 | `fold(checkpoint at a, entries (a, b])` equals the canonical state of PostgreSQL at `b`, for random histories of admin writes and synchronizations. | the PostgreSQL loader at the exact revision | pg | P5 |
| POL02 | A broken `prev` chain is not applied. | an injected log | unit | P5 |
| POL03 | An incomplete checkpoint or a digest mismatch is not applied. | an injected log | unit | P5 |
| POL04 | The codec is deterministic. | bytes | unit | P5 |
| POL05 | The PDP and the applier read formats N and N−1. | fixtures of both formats | unit | P7 |
| PUB01 | With `kill -9` at every point of a publication, the log equals the outbox, with the same hash at every position. The points are: after the commit, among the chunks, after the chunks, after the header, before the mark. Two publishers running during a change of leader give the same result. | hashes of the outbox | fault | P6 |
| PUB02 | Positions are continuous. Empty entries count too: a write of an admin binding takes a revision and an empty header. | the outbox | pg | P3 |
| PUB03 | Heartbeat: every commit before `pg_time` has a revision no higher than the heartbeat's position. | snapshots of PostgreSQL | pg | P5 |
| PUB04 | The `pg_notify` of the writing transaction, delivered on commit, wakes the publisher, which reads `outbox WHERE revision > last`. Its periodic tick catches a lost notification, also when the writes go through PgBouncer in transaction mode. | the log | pg | P6 |
| PUB05 | Every `log_chunk.body` is at most 1 MiB of zstd and holds whole records (ADR 0002). | the records encoded, decoded again chunk by chunk | unit | P3 |
| APP01 | The applier never moves past a missing or mismatching entry. Late, duplicate or reordered headers and chunks never produce a torn application. A missing chunk is read from another data center. | an injected log | cql | P6 |
| APP02 | Two appliers running at once converge byte for byte, and the regional position never goes back. | the regional state | cql | P6 |
| APP03 | With the garbage collection of versions on, a PDP that confirmed its view within S finds, for every key it reads, the newest version at or below its P, also under kill tests and during catch-up after a partition. | the versions written | fault | P6 |
| ASOF01 | A decision at position P reads every partition as of P. With the applier stopped in the middle of a write, the decisions equal the reference at P. This includes the counter-example of an exception, a resource and a denial whose rows are read at different positions. | `refeval` over PostgreSQL at P | system | P6 |
| ASOF02 | A change of a type link or of `inherit` never coexists with lineages of the old structure. A type becomes `sync` together with its first data. It becomes `not_sync` when its last live synchronizer is deleted, in one transaction with its outbox entry. The mode is `sync` when `synced_at` is set and a live synchronizer exists. | the reference over PostgreSQL | system | P6 |
| ASOF03 | A generation built from checkpoints and the tail of the log equals the generation applied incrementally. | an incrementally applied region | cql | P6 |
| ASOF04 | An installation switched from the postgres read model to the CQL store with `rebuild --all` skips no position, its PDPs answer as the `postgres` adapter did at equal positions, and it ends with regions equal to those of an installation that used the CQL store from the start (5.9). | a region applied from the start; the outbox positions; the `postgres` adapter at equal positions (secondary oracle) | system | P6 |
| REC01 | Reconcile reports a write that bypassed the outbox check (a trigger disabled by the table owner): the canonical state of PostgreSQL at revision r differs from the fold of the outbox entries up to r. | the canonical loader | pg | P5 |
| REC02 | The nightly reconcile of a region reports a regional state that differs from PostgreSQL at the same position, and `rebuild --stream` restores it. The divergence is injected as a newest version lost on a quorum of replicas (a node replaced without repair). | hashes of PostgreSQL at the position | fault | P6 |

**REF: end-to-end equivalence.** On pull requests: 5 fixed seeds. Nightly: 200 random seeds.

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| REF01 | The answers and explanations of `licetum-pdp` equal those of `refeval` over the PostgreSQL record. The policies and graphs are random, with mutations and with validity windows that pass during the run. The guards of `policygen` apply. Phase P5 covers the postgres read model, P6 the CQL store. | `refeval` | system | P5 |
| REF02 | The PDP on the CQL store and the PDP on PostgreSQL answer the same at equal positions. | the second adapter (secondary oracle) | system | P6 |

**WR, HIS: the write path and history.**

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| WR01 | A commit takes exactly one revision, a rollback none, a batch one. With 20 concurrent writers there are no gaps and every snapshot sees a prefix. | the outbox and `revision_state` | pg | P3 |
| WR02 | A transaction that changed rows of a covered policy table (4.7), or truncated one, without an outbox row fails at COMMIT. This includes raw SQL, cascades, the seed and `restore-admin`. The count is of tuples actually changed, so a statement that changed no row does not count. The resource tables get the same check in P5. A bypass is not claimed impossible: REC01 reports it. | the commit result | pg | P3 |
| WR03 | The outbox images reproduce exactly the rows that a write changed. | a diff of the tables around the operation | pg | P3 |
| WR04 | Concurrent writes cannot together remove the last admin access, create a cycle of types, or delete an entity in use. | the final state | pg | P3 |
| WR05 | Each write route produces exactly its streams and its affected services. The expectations are generated from the route table. | the route table | pg | P3 |
| WR06 | An admin write stays within its measured budget of round trips to PostgreSQL. The budget is measured, not postulated. The lock and the binding check go in one `pgx.Batch`; the outbox, the revision and the `pg_notify` go in the final one. | the traced round trips | pg | P3 |
| WR07 | A synchronization never waits for an admin transaction longer than its commit (lock order). | timings under contention | pg | P5 |
| HIS01 | A batch is atomic and names the failing operation. | the state after the batch | pg | P3 |
| HIS02 | Revert and rollback stay within one service. Records of the service's types, synchronizers and type links are reverted through the batch; one that cannot be restored exactly (a secret changed) is planned as `partial`, and the plan is applied only with `allow_partial: true`, otherwise 409 `change_not_revertible` names the records (8.7). | the change log of the service | pg | P4 |
| HIS03 | Exporting a service and importing the document again changes nothing. The document is `licetum.policy/v2`. Importing a `v1` document is refused with an error that names the version. | the exported document | pg | P4 |
| HIS04 | A create that is given an id another entity has is refused with 409. | the status code | pg | P3 |
| HIS05 | A reader of the change feed ordered by `(revision, seq)` never skips a record. | the records committed | pg | P3 |

**SYN: synchronization of sources.** The cases start from `admin-api/tests/integration/api/sync_test.go` and `sync_crash_test.go`.

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| SYN01 | A source is asked once per type and period across all executors. | calls counted by the fake source | pg | P5 |
| SYN02 | When a source does not answer, the data is kept and the backoff applies to the whole cluster. | the data and `sync_state` | pg | P5 |
| SYN03 | An answer with no resources switches off every resource of the type. | the data | pg | P5 |
| SYN04 | A late or duplicate executor never commits a second application. | the commits | pg | P5 |
| SYN05 | `kill -9` in the middle of an application leaves it applied exactly once, and the closure stays consistent. | the definition of the closure | fault | P5 |
| SYN06 | Components of the type graph do not block each other. A busy component lock delays the job without starting a backoff. | timings and `sync_state` | pg | P5 |
| SYN07 | A repeat with the same data writes nothing and takes no revision. | the outbox | pg | P5 |
| SYN08 | A change of a type link rebuilds the closure without asking the source. Renaming a service invalidates nothing. | calls counted, the closure | pg | P5 |
| SYN09 | The source of a type limited to region X is asked only by fetchers in region X (`licetum-writer fetch --region X`). | calls counted per region | pg | P5 |
| SYN10 | A source that keeps answering without change is polled less and less often, up to a bound, and the polls of each source are counted. | calls counted by the fake source; the metric | pg | P5 |

**CONS, REV, FC, FO: consistency, revocation, fail closed, failover.**

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| CONS01 | A request carries the `min_revision` of a write in the top-level `context.licetum`. Any region then answers at or above that revision, or refuses: 503 `revision_not_reached` for `/evaluation` and searches, `decision: false` with `context.error` in `/evaluations`. A stale 200 never happens. It is checked with the postgres adapter on a streaming replica and with the CQL adapter. Phase P5 covers the postgres adapter, P6 the CQL adapter. | the revision of each answer | system | P5 |
| CONS02 | A PDP never goes back to an older position, except by an accounted reset (an epoch or a generation change), which drops its cached answers. | the positions observed | unit | P5 |
| CONS03 | No cached answer outlives a validity boundary or a change of the positions it read. Those positions are `catalog`, `svc:x` for every x in U(S), and `data:<type>` for every type read, ancestor types of other services included. | a manual clock and positions | unit | P5 |
| CONS04 | A write in service A invalidates only the answers of services whose upstream set U(·) contains A. | the invalidated keys | unit | P5 |
| CONS05 | A warm decision reads nothing from the store. A miss reads at most 1 + the point exception anchors + the `attr_v` buckets of the lookup type (`buckets(type)`, ADR 0002) and the matches of each property lookup + the ancestors read for `resource_where`. This bound does not depend on the size of the policy. | counted reads | system | P6 |
| REV01 | A revocation is enforced in the home region with a p99 of at most 1 s over 200 revocations. Pull requests run it as a smoke test without the figure. | the wall clock on a runner without `-race` | system | P7 |
| REV02 | A revocation is enforced in every other region with a p99 of at most 5 s over 200 revocations. | the wall clock | multidc | P7 |
| REV03 | For every revocation committed at t, no allowing answer is served at t + S + ε or later. This holds through link cuts, stopped appliers, stopped publishers, stopped PostgreSQL and epoch changes. | the commit time of each revocation; ε from the clock discipline | fault | P7 |
| FC01 | A region whose store has been unreadable for longer than S refuses (503 or `decision: false`), and its `/readyz` fails. The test uses a short S. | status codes and `/readyz` | system | P6 |
| FC02 | A region cut off from home (its heartbeat does not move) refuses after S and is not ready. | status codes and `/readyz` | fault | P7 |
| FC03 | A region recovers by itself, and applies the revocations made during the partition before any later grant. | the order of positions | fault | P7 |
| FC04 | `freeze` disables only the staleness gate: the log is still applied and the position keeps moving. The freeze is accounted in `licetum_frozen`, `freeze_log` and one log line a minute. `unfreeze` restores the gate. | metrics, `freeze_log`, positions | system | P6 |
| FC05 | The configuration refuses `POLICY__MAX_STALENESS=0` (today 0 means no limit). | the configuration error | unit | P5 |
| FO01 | A PostgreSQL failover that loses published commits is detected automatically and starts a new epoch (`epoch_start`). Every region converges to the new primary, and no later answer reflects the lost write. The time from the failover to the first answer of each region is reported, and S must exceed it. | the state of the new primary | fault | P7 |

**AZ, PEP: AuthZEN and the authentication of PEPs.**

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| AZ01 | A missing required field is a 400, endpoint by endpoint. A search does not require `subject.id` or `resource.id` where the specification says to omit or ignore them. | the specification | unit | P2 |
| AZ02 | The defaults of `/evaluations` merge as the specification says, and the three semantics answer as in its examples. `context.licetum` is read from the top-level `context` only. | the examples of the specification | unit | P2 |
| AZ03 | A short-circuiting batch evaluates nothing after the stopping evaluation. Today `applySemantic` computes every evaluation first. | an evaluation counter | unit | P2 |
| AZ04 | Each search page is computed at the current position. The cursor is (bucket, last key, digest of the request); until P5 it has no bucket and keys follow today's order by id (7.6). Paging ends with `next_token` `""` and has no gaps or repeats for the items present throughout. A changed request is a 400 (`authzen_page_invalid`). No page expires. | the full result set | unit | P2 |
| AZ05 | The metadata name an https PDP identifier and only the endpoints that are implemented. | the specification | unit | P2 |
| AZ06 | Unknown fields are ignored. A nested `context` value is ignored by the conditions instead of refused. | status and decision | unit | P2 |
| AZ07 | `X-Request-ID` is returned verbatim when it is safe for a header. | the response header | unit | P2 |
| AZ08 | A 401 carries `WWW-Authenticate`. The `Bearer` scheme is case-insensitive, and any other scheme is a 401. | RFC 6750 | unit | P2 |
| AZ09 | An unregistered client, a missing operation and an unknown service give the same 403. | status and body | unit | P2 |
| AZ10 | The public port serves exactly the AuthZEN routes. | a snapshot of the route table | unit | P2 |
| AZ11 | An unknown type or resource gives `decision: false`, not an error. | the specification | unit | P2 |
| AZ12 | `explain` needs the `explain` operation of the client. | the status code | unit | P2 |
| AZ13 | Resource search is limited to the requested type and to the scan budget (`PDP__SEARCH_SCAN_BUDGET`). A short page carries `next_token`. | the result set | unit | P2 |
| AZ14 | An exhausted rate bucket gives a 429 with `Retry-After`. The limit is per pod; a global limit belongs to the ingress. | status and header | unit | P2 |
| AZ15 | A client holds at most its share of the computation slots and of the requests in flight, and searches run in a pool of their own. A search storm from one client does not delay another client's evaluations. | the evaluations of a second client, counted and timed | system | P5 |
| AZ16 | An explanation given to the PEP of service B names a rule of an upstream service A only as `upstream: <slug of A>` with the level. It never shows the rule's id, anchors or groups. | the explanation | unit | P4 |
| PEP01 | A PEP JWT is accepted only when it comes from an allowed issuer, carries the PDP audience (`aud`, which is not checked today), is within `exp`/`nbf`, and is signed with an allowed algorithm. | RFC 7519, RFC 9068 | unit | P2 |
| PEP02 | API keys are stored as hashes and rotate with two active keys. Every PDP refuses a revoked key within the revocation SLO. | status codes over time | system | P2 |
| PEP03 | The JWKS is fetched once per TTL and fetched again once per unknown `kid`. Junk tokens do not multiply fetches. The cases come from `core/engine/jwks_test.go`. | fetches counted by the fake IdP | unit | P2 |
| PEP04 | The keys of a PEP issuer reach the PDP only through the replicated state (stream `pep`). The PDP never calls the IdP. | the connections of the PDP | system | P6 |

**ADM: administration of Licetum itself.**

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| ADM01 | A missing token, an invalid token, or a token for another audience gives a 401 with `WWW-Authenticate`. A 403 comes before the body is validated. | status codes | pg | P2 |
| ADM02 | The isolation matrix, generated from the route table, holds. It crosses every route with these callers: `platform_admin`, `platform_viewer`, `service_creator`, each of `service_owner`, `service_editor`, `policy_editor`, `member_manager`, `service_viewer` and `directory_writer` (a machine client) of A, `service_owner` of B, `group_manager` of G, and nobody. Roles of A on entities of B give 403 or 404. Lists hold only the caller's scope. A route without a policy fails the build. | the route table and the role matrix | pg | P4 |
| ADM03 | Creating needs a role in the scope. Creating a service needs `platform_admin` or `service_creator`. | status codes | pg | P4 |
| ADM04 | No escalation. Foreign groups, roles and types are refused, also by the composite foreign keys; the only exception is an exported parent type. A binding above one's own role is refused. Batch, import, revert and rollback stay within the service. Retargeting an entity counts as creating it. A role with foreign items is refused. | status codes and the state | pg | P4 |
| ADM05 | The last `platform_admin` and the last owner of a service cannot be removed, also not by two concurrent revocations. | the final state | pg | P4 |
| ADM06 | Admin authorization and writes work while the writer, the appliers and the PDPs are stopped. A revoked binding takes effect on the next request. | status codes | system | P4 |
| ADM07 | `restore-admin` works and is recorded in the change log. | the bindings and the log | pg | P4 |
| ADM08 | Bootstrap never restores revoked access. | the bindings | pg | P4 |
| ADM09 | A cross-service type link is allowed only to an exported type of the same identity domain. Unexporting a linked type, or deleting it, is a 409. | status codes | pg | P4 |
| ADM10 | A child entity must belong to the service in the path. Today `ListAccessGroups` and `CreateAccessGroup` do not check this, and the resource list, `refresh` and `GET /type_resource` have no service in the path. | status codes | pg | P4 |
| ADM11 | A service slug cannot be changed after creation, and it is never given to another service, also after the service is deleted. | status codes | pg | P3 |
| ADM12 | Only callers whose roles in S include `directory_writer` (a machine client of the admin IdP, or an editor of S through the role closure, 8.2) write the directory of S, at a limited rate and without the `admin_write` lock. An unchanged batch takes no revision, `directory_writer` allows nothing else, and a role of one service cannot read the directory of another. | status codes and lock waits | pg | P4 |
| ADM13 | The seed and `restore-admin` need only the `ADMIN_AUTH__*` settings. With no `PLATFORM__*` OAuth variable set, the admin API is seeded and break-glass works. Today both require all six variables (`Platform.IsConfigured`). | status codes | pg | P2 |

**OPS, API, LOAD, WEB.**

| ID | Statement | Oracle | Level | Phase |
|---|---|---|---|---|
| OPS01 | `/healthz` has no dependency. `/readyz` follows the staleness gate, the schema (a newer one is fine), the store and draining. | probe results | unit | P5 |
| OPS02 | A rolling upgrade under a load of AuthZEN evaluations loses no request. Today `kind-test.sh` loads only `/healthz`. | the load generator | helm | P7 |
| OPS03 | Beyond the in-flight limit, admission answers 503 with `Retry-After`. The probes are exempt, and the request timeout applies. | status codes | unit | P5 |
| OPS04 | Migrations of PostgreSQL and of the CQL store, started concurrently, are applied once. | the schema versions | pg | P3 |
| OPS05 | A PDP in CQL mode connects only to the nodes of its data center and has no PostgreSQL credentials. The test uses the `/proc/net/tcp` technique of `cluster/f6_cell_test.go`. | the sockets of the process | system | P6 |
| OPS06 | Least privilege, checked by refused statements and by a list of every write path of each process (retention and the log settings included). <br>• PDP: its CQL role selects in its own region only; in postgres mode its PostgreSQL role only selects. <br>• Applier: writes the tables of its region and reads the log; it never creates or drops keyspaces, because the migrate job creates the generation keyspaces up front. <br>• Writer: writes the log. <br>• `freeze`: uses an operator CQL role. <br>• Admin API: has no CQL role, and its PostgreSQL role does not write the resource tables. | refused statements | system | P6 |
| OPS07 | The Helm chart renders the golden files and passes kubeconform. It does not render without its secrets. | the golden files | helm | P7 |
| OPS08 | `migrate` on a database of v0.1.x refuses with a message that names the release and says there is no upgrade path. Schema v2 keeps its versions in a goose table of its own. | the error and the untouched database | pg | P3 |
| OPS09 | The subject-hash salt is in no outbox entry, checkpoint or regional row, and every process hashes a subject to the same value from the same `LOG__SUBJECT_SALT`. Phase P5 covers the outbox and the decision log, P6 the checkpoints and the regional rows. | the replicated state and the decision log | system | P5 |
| API01 | The committed OpenAPI schemas (admin and PDP) and the CQL schema snapshot match the code. | the generated schemas | unit | P2 |
| API02 | Every message and every API text has its English, Russian and Chinese translations. | the catalogs | unit | P2 |
| API03 | Input bounds are enforced: sizes, counts, names. | status codes | unit | P2 |
| LOAD01 | The load test gives no wrong answer. | `expectedAllowed` | load | P2 |
| LOAD02 | p99, store reads per evaluation and CPU per 1000 requests stay within their budgets on both sides. A run better than its budget by more than the tolerance fails until the budget is set again; today it only asks for that in its report. | `admin-api/loadtest/budgets.yaml` | load | P7 |
| WEB01 | The BFF and the session are safe: Origin check, path sanitizing, and an encrypted session cookie. The PKCE verifier, the state and the nonce of the admin sign-in live in a short-lived encrypted cookie (today they are in Redis, `authz:pkce:*`). | unit tests of the BFF | web | P2 |
| WEB02 | The interface shows only what the caller's capabilities in the service allow. | components rendered with mocked capabilities | web | P4 |
| WEB03 | The AuthZEN request builders of the simulation and integration pages produce valid requests. | the specification | web | P2 |

### 10.5 Test tree

```
contracts.yaml                                     the index
core/internal/tools/contractscheck/                the check (task contracts:check)
core/testsupport/policyfix/ (+ testdata/decisions) decision fixtures in external terms             DEC
core/testsupport/refeval/                          reference evaluator, never imported by production code
core/testsupport/policygen/                        generator of policies and graphs                 DECP, REF
core/testsupport/{containers.go (+ CQL node), fault.go, idp.go, source.go, crash/}
core/decide/*_test.go                              DEC, DECP (from P5; before, the engine runner of the admin API tests)
core/policy/*_test.go, core/logfmt/*_test.go       POL
core/readmodel/storetest/ (memstore, pgstore, cqlstore)                                             ST
core/writer/**/*_test.go, core/applier/*_test.go   PUB, APP, SYN, REC
core/pdp/**/*_test.go                              AZ, PEP, CONS, FC, OPS (component)
admin-api/{authz,service,api}/*_test.go            ADM, WR, HIS, API
admin-api/tests/e2e/      one topology builder (PostgreSQL, CQL store, writer, admin API, N PDPs, fake IdP
                          and source), a typed policy builder, a database or namespace per test, t.Parallel
                                                                                        REF, ASOF, CONS, FC, OPS
admin-api/tests/fault/    Toxiproxy and kill -9 (replaces tests/integration/chaos)
                                                         PUB01, SYN05, APP03, REC02, REV03, FC02, FC03, FO01
admin-api/tests/multidc/  two CQL data centers and Toxiproxy (replaces tests/multiregion)          REV02
admin-api/loadtest/                                                                                 LOAD
deploy/helm/{test.sh, kind-test.sh}                                                         OPS02, OPS07
web/tests/                                                                                          WEB
```

### 10.6 Placement in CI

**Pull requests (`ci.yml`):**

- **Lint.** golangci-lint, with depguard rules for the new packages:
  - `core/decide` and `core/policy` import no pgx, DBOS, CQL driver or `net/http`.
  - `core/pdp` and `core/applier` import no pgx, `core/db` or DBOS. The postgres mode of the PDP reads the outbox through a `LogSource` port next to `ReadModel`, implemented in `pgstore`.
  - No production package imports `refeval`.

  The lint job also runs `go mod tidy -diff`, the OpenAPI drift check (`task go:schema:check`) and `contracts:check`.
- **Unit tests** under `-race`.
- **One integration run** at the `pg` and `cql` levels on Testcontainers. Since P0 this is `task go:coverage`, the only run of the whole integration suite on a pull request; `go-test-replica` (until 2e) and `previous-release` (until P3) run narrower sets.
- **A `system` smoke run without `-race`:** the fixed seeds of REF01, one crash point per `fault` contract, and REV01 without its figure.
- **The load test smoke run** (LOAD01), `web-check`, and Helm when the chart or the settings change (10.7).
- **Measuring decision-path changes.** A change of the decision path that adds work on a cache miss is measured with the medium load test before it is merged. The transitive fail-closed of P1 is the first such change.

**Leaving the pull request:**

- **PgBouncer.** The runs through PgBouncer left in P0 for `pgbouncer.yml`.
- **The `previous-release` job.** It is removed in P3: the integration tests of v0.1.0 cannot run on schema v2, and OPS08 states the refusal instead.
- **The read-replica job.** It goes in 2e together with the routing of reads to a replica. `scripts/pg-replica.sh` stays. It is the environment for the postgres adapter of a regional PDP on a streaming replica (CONS01, REF01), and that run moves to the nightly suite.

**Nightly.** `nightly-suite.yml` of the epic is called by the dispatcher `nightly.yml` on `main` (03:11 UTC) with the ref of the epic.

- The dispatcher only holds the schedule and the ref. The jobs, tags and environment come from the epic's own commit, so the epic changes its nightly suite without touching `main`.
- Today the suite runs the integration tests through PgBouncer, the chaos suite, the multi-region environment and the load test. A scheduled run measures the medium and the large profiles, because `loadtest.yml` reads the caller's `github.event_name`.
- The phases add:
  - the seeds of DECP (21–520 from P1, random seeds from P5) and the random seeds of REF01 (each seed goes in the job summary);
  - every crash point of PUB01, SYN05, APP03, REC02, FC02, FC03, FO01 and REV03;
  - REV01 and REV02 on a runner without `-race`;
  - LOAD02 at the medium and large profiles;
  - `storetest` against the CQL store not chosen in P0.5;
  - the postgres adapter on a streaming replica;
  - the kind upgrade under AuthZEN load (OPS02);
  - the write path through PgBouncer (PUB04).
- The exit of P7 is seven green nightly runs in a row.
- When the epic reaches `main`, the suites move there and the dispatcher is removed.

### 10.7 Every pull request keeps the stack working

Between P2 and P7 the admin schema, the settings and the processes change under the web interface, the chart and Compose. The rule is that nothing is left red.

- **What stays green.** Each pull request of the epic keeps these green:
  - `task check`, which includes the web lint. That lint regenerates the orval client from `admin-api/schema/admin.yaml` and type-checks it.
  - The web tests and `pnpm build`.
  - The Helm golden files.
  - The Compose stack (`task dev`).
- **API changes.** A pull request that changes the admin API changes the web interface in the same pull request.
- **Settings and process changes.** A pull request that removes or renames a setting or a process updates, in the same pull request, the chart, its golden files, `docker-compose.yml` and `infra/*.compose.yml`. Examples: `REDIS__*`, `AUTH__STATE_*`, the OAuth half of `PLATFORM__*`, `CORS__*`, `DB__READ_URI`, the `check` and `worker` processes.
- **When the chart job runs.** `helm.yml` runs on pull requests that touch `deploy/helm/**`, `core/Dockerfile`, `admin-api/Dockerfile` or the workflow itself; `web/Dockerfile` does not start it. Its path filter also gets `core/config/**`, so a change of settings renders the chart.
- **Compose.** The author starts the Compose stack for a pull request that changes settings, processes or images.
- **When a check cannot stay green.** Sometimes a phase cannot keep a check green, for example a page that is rewritten only in P4. The pull request then disables that check explicitly in the workflow, with a comment that names the phase that restores it. The contracts that cover the check stay `planned` with that phase.

### 10.8 What replaces the F-tests

| Today | Becomes |
|---|---|
| F0-1 to F0-4 (`TestF0_*`, self-tests of the harness) | Removed. The fakes are checked by the tests that use them. |
| F1, the fast path and its cost (`TestF1_1` to `_8`), and the JWKS cache | CONS05, LOAD02, PEP03, DEC14 |
| F1-11 (`TestF1_11`, snapshot path ≡ query path) | DECP and REF01. `policyGenerator` moves to `core/testsupport/policygen`. |
| F2 (the revision, atomicity, read-your-writes) | WR01, WR02, CONS01 to CONS03, PUB02 |
| F2-5 and F6-2 (the revocation SLO) | REV01 and REV02 |
| F2-8, F2-9, the Redis chaos of F5, F3-13, F3-14 | Removed with Redis (2e). |
| F3 (`TestSync*` in `api/sync_test.go`, `sync_crash_test.go`) | SYN01 to SYN10 |
| F4 (the delegation audit, known subjects) | Removed with the features (2c). The subject directory is covered by ADM12 and by subject search under AZ. |
| F5 (probes, admission, degraded mode, migrations) | OPS01, OPS03, OPS04, FC01 to FC05 |
| F5-11 (the job `previous-release`) | OPS08. The job is removed in P3. |
| F6 (replicas, regions, cells, Helm) | REV02, FC01 to FC03, FO01, OPS05, OPS07 |
| `TestF6_7_*` (an admin write in at most six round trips) | WR06, with a measured budget. |
| `api/{graph,hierarchy,conditions,roles,gaps,escalation}_test.go` | The DEC fixtures (extracted first, in P1; every scenario maps to a table row), ADM04, and a thin AuthZEN smoke test. |

### 10.9 Changes to `contracts.yaml`

This section adds twelve contracts to the index, all `planned`: APP03, ASOF04, REC01, REC02, SYN10, AZ15, AZ16, ADM11, ADM12, ADM13, OPS08, OPS09. ST09 is already in the index with the same meaning: it came with ADR 0002 (#61), together with ST10 (the GC horizon) and PUB05 (log chunks of at most 1 MiB), which 10.4 lists as ADR 0002 states them.

It also changes these entries:

| Entry | Change |
|---|---|
| DEC oracle path | `core/testsupport/policyfix/testdata/decisions`, not `core/decide/testdata/decisions` (already changed on `refactor/p1-decision-contracts`). |
| DECP oracle path | `core/testsupport/refeval`, not `core/internal/refeval` (already changed on `refactor/p1-decision-contracts`). |
| DEC01 to DEC13, DECP | Level `pg` until P5, as set on `refactor/p1-decision-contracts`; P5 changes it to `unit` (10.2). Until P5, DECP runs seeds 1–20 on every pull request and 21–520 nightly; the 2000 fixed seeds of its row start with `core/decide`. |
| WR02 | Policy tables only, counted by tuples actually changed, `TRUNCATE` included; bypasses are left to REC01. |
| PUB04 | A leader woken by `pg_notify`, with a periodic tick; no transactional enqueue. |
| CONS01, AZ02 | `context.licetum` is read only from the top-level `context`. CONS01 also runs with both adapters. |
| CONS03 | The positions read, ancestor types of other services included. |
| CONS05 | The bound with the `attr_v` buckets and the `resource_where` ancestors. |
| AZ04 | Each page at the current position; no expiry. |
| FC04 | `freeze` disables only the staleness gate. |
| FO01 | Reports the failover time that S must exceed. |
| ASOF02 | The transition to `not_sync`. |
| HIS02 | Types, synchronizers and type links in a rollback. |
| HIS03 | `licetum.policy/v2`. |
| OPS06 | Generation keyspaces created up front, and an operator role for `freeze`. |
| ST08 | The garbage collection waits for APP and ASOF under the kill tests. |
| LOAD02 | A result that is too good fails. |
| WEB01 | The sign-in cookie. |
| PEP01 | `aud`. |
| AZ14 | The limit is per pod. |
| PUB03 | Phase P5, when the `postgres` adapter on a standby starts taking T(P) from the heartbeat. |
| SYN09 | Asked only by the fetchers of region X, not by executors there; no executor runs outside the home region (4.9.1). |

POL05 and OPS02 stay `planned` until P7.

## 11. Phases

The refactor lands on `epic/production-load-readiness` in nine phases: P0 to P7, plus P0.5, which runs beside P1. Each phase is a series of small pull requests into the epic. Its acceptance criteria are contract IDs from `contracts.yaml`. The `phase` field of a contract names the phase that makes it active. Until then the contract is `planned`, and `task contracts:check` (`core/internal/tools/contractscheck`) refuses a test that names it, so no contract can be claimed early.

### 11.1 Branch, pull requests and tracking

- **Integration branch.** The work uses the existing `epic/production-load-readiness`; it is not recreated. The last commit of the PostgreSQL read-replica design is `8989ed4`, the squash of #48. P0 tags it `archive/pg-replica-design` so that design can still be reached by name.
- **Tracking.** Issue #49 supersedes #31. It has one sub-issue per phase. The checklist of #49 is the status of the work; this ADR is the plan and is not updated as phases close.

  | Phase | Issue | Scope |
  |---|---|---|
  | P0 | #50 | Tracking, ADR, contracts skeleton, CI hygiene |
  | P0.5 | #51 | CQL read-store spike and license decision |
  | P1 | #52 | Decision contracts on the current engine and the transitive fail-closed fix |
  | P2 | #53 | AuthZEN-only surface, PEP authentication, admin OIDC |
  | P3 | #54 | Schema v2 and the mandatory outbox |
  | P4 | #55 | Service-scoped administration and the web interface |
  | P5 | #56 | Pure decision core, `licetum-writer`, PDP in PostgreSQL mode |
  | P6 | #57 | CQL log, regional applier, PDP in CQL mode |
  | P7 | #58 | Topology, Helm, runbooks, budgets, landing |

- **#31** is closed as superseded (`not_planned`). Its goals carry over as follows:
  - D1: the audit of delegated calls is removed, and a structured decision log replaces it.
  - D2: a region fails closed after a finite staleness limit S (`POLICY__MAX_STALENESS`, 60 s), with an audited `freeze` as the only exception.
  - D3: revocation stays at p99 ≤ 5 s in every region and becomes ≤ 1 s in the home region.
  - D4: a cell is a separate installation with its own PostgreSQL and its own read store.
  - D5: synchronization runs on DBOS only.
- **#40** (the epic into `main`) is a draft titled "Licetum 1.0: regional AuthZEN PDP, service-scoped administration". It is marked ready after P7 and merged with a merge commit when the owner decides.
- **Rules.** These are kept from #31, with rule 7 changed:
  - every change is a pull request into the epic, never into `main` directly, except the nightly dispatcher (`nightly.yml`, #60), which must live on the default branch;
  - CI is green before a merge;
  - `main` is merged into the epic with a merge commit, and nothing is force-pushed;
  - a pull request names its phase and the contract IDs it makes active, in place of F-IDs.
- **Stale branches.** The candidates are `fix/epic-ci-flakes`, `f6/read-replica` and `tails/close-epic-tails` (remote and local), the local `epic-sync`, and the branches of the two P0 pull requests, `refactor/p0-tracking` (#59) and `ci/nightly-dispatcher` (#60).
  - They were merged by squash, so `git branch -r --merged` does not show them (it lists only the epic). Before a branch is deleted, check that its content is in the epic: an identical tree (`git diff --quiet`), equal patch-ids, or the merged state of its pull request through the REST API.
  - `origin/loadtest-results` stays, because `loadtest.yml` publishes there.
  - Of the `loadtest-results-*` branches, one baseline set stays and the owner deletes the rest.
- **Package moves** are commits that contain only `git mv`. Behaviour changes come in the commits after them, so review and `git log --follow` see the two apart.
- **Green between phases.** Every pull request keeps `task check`, the web check, `docker compose up` and the Helm golden files green.
  - Sometimes a phase cannot. For example, the web client is generated from the admin schema, which P2 and P3 change before P4 rewrites the pages.
  - In that case the pull request switches the check off explicitly and says so in its description. The contract that covers the check stays `planned`.
- **Migrations and the released `v0.1.0`.** The tag `v0.1.0` (`0fc5b0b`, 2026-10-12) shipped `core/db/migrations/00001_init.sql` exactly as it is now; `00002` to `00005` came after it.
  - **Until P3, a migration only adds.** It is a new numbered file with a table, a nullable column or an index. What P2 stops using stays in the schema: `delegated_call`, `known_subject`, the OAuth columns of `service` and `trusted_clients`.
    - The `previous-release` job of `.github/workflows/ci.yml` runs the integration tests of `v0.1.0` on the migrations of the change. It keeps passing until P3.
  - **P3 replaces the schema with schema v2.** Schema v2 has a goose version table of its own.
    - `migrate` recognizes a v0.1.x database by its version table (`goose_db_version`). It stops with a message that the database cannot be upgraded and the installation must be made anew. There is no upgrade path.
    - The `previous-release` job is removed in the same phase, for this reason. Development databases are recreated (`docker compose down -v`).
  - **From schema v2 on,** the rules of `CONTRIBUTING.md` apply again between releases: a new numbered migration for each change, and expand before contract.

### 11.2 Order

```
P0 ─> P1 ─> P2 [2a ─> 2b ─> 2d ─> subject_directory ─> 2c ─> 2e] ─> P3 ─> P4 ─> P5 ─> P6 ─> P7
       └── P0.5, in parallel with P1: its numbers come before P3 fixes the outbox format; its decision gates P6
```

- **P1 before P2.** P2 removes the endpoints that most of today's semantic tests go through. The decision contracts must hold before those endpoints go.
- **Inside P2, 2d comes before 2c.** `SeedPlatform` and `restore-admin` depend on `Platform.IsConfigured()`, which requires all six `PLATFORM__*` OAuth settings (`core/config/config.go:145`, called at `core/db/migrate.go:218` and `core/db/restore.go:36`).
  - If 2c removed those settings first, the seed would be skipped without an error. The admin API would answer 503 `platform_not_seeded`, and the break-glass would not work until P4.
- **`subject_directory` arrives together with the removal of `known_subject`.** Without it, subject search, the people pages and "whom a rule reaches" would be empty until P4.
- **2e is a pull request of its own.** It does not depend on AuthZEN. It lands after 2c, because the per-service OAuth sign-in keeps its PKCE state in Redis (`authz:pkce:*`, `core/engine/oauth.go`) until 2c removes that sign-in.
- **P3 after P2.** Schema v2 drops what P2 stopped using.
- **P4 after P3.** The admin roles are checked against what P3 introduces: service ownership enforced by composite foreign keys, and `PolicyTx`.
- **P5 after P4.** The platform self-service must be gone first: its types with SQL self-synchronizers, `ensure` on the admin path and `invalidatePlatformSync`. Only then can the PDP lose `ensure` and the resource tables get the outbox check.
- **P6 after P5 and P0.5.**
- **P7 last.**

### 11.3 P0: branch, tracking, ADR, contracts skeleton, CI hygiene (#50)

No product change.

- **Lands:**
  - the tracking and branch steps of 11.1;
  - this ADR, `docs/adr/0001-regional-pdp.md`, with the `.gitignore` exceptions `!docs/adr/` and `!docs/deployment.md`;
  - `contracts.yaml` and its check `task contracts:check`, which is part of `task check`.
- **CI:**
  - The integration suites run once per pull request. The PgBouncer variant moves to the nightly run.
  - The nightly suites of the epic become reusable (`nightly-suite.yml`, with a `ref` input).
  - A thin dispatcher, `nightly.yml`, on `main` calls them with the ref of the epic, because a schedule fires only from the default branch.
  - The `previous-release` job stays until P3 (see 11.1).
- **Exit:**
  - CI of the epic is green;
  - one nightly run started through the dispatcher is green;
  - the pull-request CI time before and after is recorded.

### 11.4 P0.5: CQL read-store spike and license decision (#51)

Runs in parallel with P1.

- **Spike.** One node each of ScyllaDB (developer mode) and Apache Cassandra 5, using the CQL model of this ADR. The model uses no LWT, no materialized views, no secondary indexes and no feature that exists only with tablets. The spike measures:
  - cold single-partition reads at `LOCAL_QUORUM`;
  - applier throughput, in rows per second;
  - the payload of a first synchronization of 1M resources with full lineage, through outbox, log and applied rows;
  - the size of `descendant_v` partitions for root anchors;
  - the range tombstones of the MVCC garbage collection.

  It also chooses a driver, pinned by version.
- **License.**
  - ScyllaDB from 2025.1 is source-available: it is free up to 50 vCPU and 10 TB per organization, and the last AGPL release is 6.2.
  - Apache Cassandra is licensed under Apache-2.0.
  - The question is whether an MIT/AGPL product that is self-hosted by enterprises can require or recommend ScyllaDB.
- **Reach of the `postgres` adapter on regional streaming replicas.** The spike records how many regions, what write rate and what revocation latency this setup carries. An operator can then tell when an installation needs the CQL store.
- **Output.** `docs/adr/0002-read-store.md` chooses ScyllaDB or Apache Cassandra 5. Its numbers set the chunk size and the `part` component of the log chunk key, the bucket counts, and the threshold above which the applier lists no `applied_keys` of a stream and a whole type is invalidated.
  - The numbers are needed before P3 fixes the outbox format.
  - The decision gates P6.
- **Exit:** ADR 0002 is merged with measured numbers.

### 11.5 P1: decision contracts on the current engine and the transitive fail-closed fix (#52)

- **Decision tables DEC01 to DEC14** come from the scenarios of `admin-api/tests/integration/api/{graph,hierarchy,conditions,roles,gaps,escalation}_test.go` and from the worked example of the decision semantics. They run against `core/engine` until P5 moves the engine to `core/decide`.
- **External terms from day one.** Fixtures name everything in external terms: service slugs, type names, external ids, and group, role and rule names. They use the fixture format `core/testsupport/policyfix`, with YAML under its `testdata/decisions`.
  - Because of this, the same files serve `core/decide` when P5 moves anchors from `resource_cache.id` to `(type, external id)`.
- **Reference evaluator `core/testsupport/refeval`.**
  - It is written in the same external terms.
  - It builds reachability from the edges itself and shares no code with `decision.go` or `check.go`.
  - Production code never imports it.
- **Generator `core/testsupport/policygen`.** It is the generator of `admin-api/tests/integration/api/snapshot_differential_test.go`, moved. DECP compares the engine with `refeval` on its policies.
- **The fail-closed fix (DEC11).** Today a resource whose own parent links are resolved, but whose ancestor has a missing parent, is not treated as possibly under a denial anchored above that missing parent.
  - After the fix, `UnresolvedParents` returns the unresolved links at or above each resource. Each link's mask is the `inherit` of the missing link intersected with the closure mask down to the resource.
  - `CoversOf`, pass C of `coverNodes` and the listing reads use this set.
  - There is no schema change.
  - The change in behaviour is announced in the release notes.
  - The fix adds a join of the closure with the unresolved edges to every listing miss. It is measured on the medium load-test profile before it is merged.
- **Removed:** nothing.
- **Contracts:** DEC01 to DEC14, DECP.
- **Exit:**
  - DEC01 to DEC14 are green against the current engine (level `pg` until P5, 10.2; the 30 s target without a database applies from P5);
  - the engine equals `refeval` on 2000 seeds, in one run of `decp.yml` over seeds 1–2000 (pull requests run 1–20 and the nightly suite 21–520);
  - every scenario of the six API files is mapped to a table row;
  - all existing suites are green;
  - the medium load-test profile stays within its budget with the fix.

### 11.6 P2: AuthZEN-only surface, PEP authentication, admin OIDC (#53)

The steps are separate pull requests, in this order.

- **2a: oracles to AuthZEN.** Test oracles move to AuthZEN while the old endpoints still exist:
  - `Replica.Permission` and `Replica.Fresh` of the harness go to evaluation and resource search;
  - the k6 mix goes to `evaluation`, `evaluations` and search.
- **2b: PEP clients and conformance.**
  - New tables `pep_client`, `pep_credential` and `pep_issuer`, added by a new numbered migration.
  - JWT client-credentials: allowlisted `iss`, mandatory `aud`, `exp` and `nbf`, an `alg` allowlist. Until the writer collects issuer keys (P5), the PDP fetches JWKS through the existing `jwks.go`.
  - API keys: stored as sha256, two active per client.
  - Authorization per operation. An unknown service, an unregistered client and a missing operation all get the same 403.
  - An optional token bucket per pod answers 429.
  - Conformance fixes:
    - required fields, per endpoint; in search, `subject.id` and `resource.id` are omitted or ignored as the specification says;
    - `resource.id = "*"`;
    - `context.licetum`, read only from the top-level `context` of `/evaluations`; its `min_revision` maps to today's `at_least_as_fresh` until P5;
    - `X-Request-ID` is echoed;
    - the 401 rules: `Bearer` is case-insensitive and another scheme is a 401;
    - a nested context is ignored, not refused;
    - a lazy short-circuit in `/evaluations`;
    - https metadata;
    - the ops port.
  - Only the slug addresses the service in the path.
  - Search pages are computed at the current position of each page. The cursor holds the last key and the request digest, in today's order by id; the bucket joins it with the read-model port in P5 (7.6). There is no `authzen_page_expired`.
  - The `check` role becomes `pdp`.
- **2d: admin OIDC.**
  - `ADMIN_AUTH__*` settings: issuer, a mandatory audience, the JWKS URL and the subject claim.
  - Authorization still uses the platform policies until P4.
  - The web BFF becomes the OIDC client: code flow with PKCE, with the verifier, state and nonce in a short-lived encrypted cookie. The Dex redirect goes to the web interface.
  - `SeedPlatform` and `restore-admin` check `ADMIN_AUTH__*` in place of `Platform.IsConfigured()`.
  - The `PLATFORM__*` OAuth settings are removed. `AUTH__STATE_*` and `CORS__*` go in 2c with `oauth_login` and `oauth_callback`, which use them (`core/engine/oauth.go:57,70,114`).
- **`subject_directory` per service.**
  - Bulk writes through `PUT /service/{ref}/subjects`.
  - The directory feeds AuthZEN subject search, the people pages and `peopleReached`.
  - Until P4 it is governed by the platform policy that governs `/subject` today (`group_member`). The role `directory_writer` comes with the roles in P4.
- **2c: removal of the per-service surface.**
  - `core/server/check.go`: `/permission`, `/check`, `on_behalf/*`.
  - The OAuth half of `core/engine/oauth.go`.
  - `core/engine/{delegation_runtime,audit,subjects_runtime,flusher}.go`.
  - `trusted_clients` and the `oauth_login`, `oauth_callback`, `oauth_refresh` and `list_delegated_calls` routes.
  - In the web interface: the OIDC step of `connect-wizard.tsx`, `delegation-section.tsx`, `app/api/discover` and `app/api/check`. `integration-tab.tsx` is rewritten.
  - The tables and columns stay until P3 (see 11.1).
  - This removes the public API of `v0.1.0`, and the release notes say so.
- **2e: removal of Redis, replica routing and the table engine.**
  - Redis: `core/redisx` and every use of Redis go. That covers the answer and token L2 caches, the breakers, `authz:fetch_all`, PKCE, the legacy `BumpVersion` and `infra/redis.compose.yml`.
  - `synchronizer.cache_ttl` and `SYNC__FETCH_TTL` go explicitly from the model, the schema and the UI.
  - Read-replica routing inside one process goes: `core/engine/replica.go`, `DB__READ_URI`, `DB__MAX_REPLICA_LAG` and `DB__LAG_*`. The replica job of `ci.yml` goes too.
    - `scripts/pg-replica.sh` stays: P5 uses it to test the `postgres` adapter on a regional standby.
  - The table sync engine goes: `claimJob`, its LISTEN/NOTIFY and its regions filter, together with `SYNC__ENGINE`, `importLegacyState`, `recordDataChange` and `SYNC__LOCK_*`.
    - Until P5 brings back regional sources (SYN09), a source with `regions` may be asked by any executor.
- **Kept until later:** the snapshot path and `ensure`, because the platform self-service still lives.
- **Contracts:** AZ01 to AZ14, PEP01 to PEP03, ADM01, ADM13, API01 to API03, LOAD01, WEB01, WEB03.
- **Exit:**
  - `go list -deps` of both modules contains no go-redis;
  - these contracts are green, AZ10 (the route table of the public port) included;
  - sign-in through Dex works in Compose;
  - the load-test smoke gives no wrong answer;
  - the `previous-release` job is green.

### 11.7 P3: schema v2 and the mandatory outbox on policy tables (#54)

- **Schema v2.** A new migration set starting at `00001_init.sql`, with its own goose version table. It refuses a v0.1.x database (see 11.1). It contains:
  - service ownership with composite foreign keys;
  - `subject_type` and `subject_group_id`;
  - the renames `claim` → `subject` / `property` in the API, the UI and i18n;
  - desired and effective type links;
  - `exported`, `identity_domain` and the identity domain of a service;
  - `admin_binding` and `platform_state` (read from P4);
  - `resource_unresolved` and `synchronizer.refresh_interval`;
  - one `revision_state` row, `global`, with an `epoch`;
  - `outbox`, `outbox_chunk`, `change_log` and `publish_state`;
  - the `dbos` schema in the same database.
- **Slugs.** A slug is immutable after creation: `PATCH` refuses `slug`. Uniqueness covers deleted services too, so a slug is never reused.
- **The outbox check covers policy tables only:** `identity_domain`, `service`, `type_resource`, `type_resource_parent`, `synchronizer`, `access_group`, `user_group`, `group_member`, `role`, `role_permission`, `assignment_rule`, `admin_binding`, the `pep_*` tables, `log_settings` and `subject_directory`.
  - Statement-level triggers with transition tables mark a transaction that actually inserted, updated or deleted tuples of these tables, and a deferred constraint trigger checks at COMMIT that a marked transaction wrote its outbox row (4.7). A statement that touched no row marks nothing and needs no outbox.
  - An `AFTER TRUNCATE` trigger covers truncation.
  - The triggers are `ENABLE ALWAYS`, so `session_replication_role = replica` does not bypass them. A `DISABLE TRIGGER` by the table owner is not claimed impossible. Reconciliation catches it.
  - The resource tables follow in P5. Until then their legacy writers are alive: the platform self-synchronizers (until P4), the inline `resolve()` of the admin API and the sync executor.
- **`core/db.PolicyTx`.**
  - The first batch takes `pg_advisory_xact_lock(admin_write)` and rechecks the caller's access inside the transaction.
  - Then come the write and the invariants.
  - The last batch holds the CTE that takes the revision, the outbox row, the `change_log` rows and a `pg_notify`. A NOTIFY is delivered only when the transaction commits.
  - Directory writes take only the revision row, not `admin_write`, because they have no invariants.
  - Seed and `restore-admin` write through `PolicyTx`.
- **Synchronization in P3.** A synchronization that changed data takes the global revision last in its transaction. It writes an outbox header that names the changed types and services, without a resource payload.
- **Revisions and history.**
  - The check path keeps its snapshot and in-memory answers. It reads the global revision in place of the `rules` and `data:<service>` scopes.
  - History and the change feed are ordered by `(revision, seq)`: `ListChanges`, `WaitChanges`, `PlanRevert`, `PlanRollback`. This removes the known defect of the feed ordered by id.
  - The policy document becomes `licetum.policy/v2`. An import of a v1 document is refused with a clear message.
- **Removed:**
  - `authz_change`;
  - the view part of the after-commit `AfterWrite` (the view is renewed by its poll); its call of `invalidatePlatformSync` stays until P4 removes the platform types;
  - the `previous-release` job;
  - the tables and columns P2 stopped using.
- **Contracts:** PUB02, PUB05, WR01 to WR06, HIS01, HIS04, HIS05, ADM11, OPS08, OPS04 (the PostgreSQL half; P6 adds the CQL half).
- **Exit:**
  - these contracts are green;
  - the WR06 budget is measured, not postulated;
  - `migrate` on a `v0.1.0` database stops with its message (a test);
  - the existing admin tests still pass, because platform authorization is not touched yet.

### 11.8 P4: service-scoped administration and the web interface (#55)

- **`admin-api/authz`.**
  - The matrix `(scope, kind, action) → minimal role`.
  - Bindings over the scopes `platform ⊃ service ⊃ group`.
  - The roles `platform_admin`, `platform_viewer`, `service_creator`, `service_owner`, `service_editor`, `policy_editor`, `member_manager`, `service_viewer`, `group_manager` and `directory_writer`. `directory_writer` is a machine client of the admin IdP with a rate limit.
  - The last-platform-admin and last-owner checks, under `admin_write`.
- **`admin-api/bootstrap`.**
  - `bootstrap` runs once, from `ADMIN_AUTH__BOOTSTRAP_*`.
  - `restore-admin` moves from `core/db/restore.go` (MIT) to the admin module (AGPL).
- **Routes.**
  - Routes are nested under `/service/{ref}/`, plus `GET /me`, `GET /services` (paged by slug) and `GET /service/{ref}/me` (8.5).
  - Lists are filtered in SQL.
  - Batch, import, export, revert and rollback are limited to one service. Rollback reads only the `change_log` of that service.
  - The batch learns the kinds of types, synchronizers and links, without their secrets. Until it does, a rollback over them stays a 409 `change_not_revertible`.
  - Exported links are allowed only within one identity domain. Upstream rules appear only to read, marked `upstream: <slug>`. In an explanation sent to a PEP they show only that mark and the level.
  - P4 adds management of PEP clients and the directory API under the service roles.
  - Routes that have no service in the path today get one: `GET /type_resource`, `/type_resource/{type_resource_id}/resource` and `/type_resource/{type_resource_id}/resource/refresh` (`admin-api/api/admin.go`). A child entity must belong to the service of the path.
- **Web interface.**
  - `web/src/lib/access.tsx` becomes capabilities per service.
  - A service switcher, and routes `/services/[slug]/{rules,people,groups,roles,check,history,integration,sources,types,policy,access}`.
  - Binding screens.
  - No N+1 requests in `web/src/features/data/queries.ts`.
- **Removed:**
  - the platform row in `service`, `core/model/platform.go` and its uuid5 ids;
  - the platform types and self-synchronizers of `SeedPlatform`, and with them the `DB__URI` with its password stored in `synchronizer.config`;
  - `invalidatePlatformSync` and the `isPlatformType` branch;
  - `service.Access` and `FilterAllowed`;
  - the platform checks of `admin_targets.go`;
  - `/platform` and `/platform/me`;
  - the current `settings-admins.tsx`.
- **Contracts:** ADM02 to ADM10, ADM12, AZ16, HIS02, HIS03, WEB02.
- **Exit:**
  - these contracts are green, with the ADM02 matrix generated from the route table;
  - no `synchronizer` row points at the Licetum database;
  - `rg PlatformServiceID` finds nothing;
  - build, typecheck, knip and Jest of the web interface are green.

### 11.9 P5: pure core, one decision path, `licetum-writer`, PDP in PostgreSQL mode (#56)

- **Pure core.**
  - `core/decide`: the pure engine, with anchors as `(type, external id)`.
  - `core/policy`: `Fold` and the codec.
  - `core/readmodel`: the `ReadModel` port and, beside it, a `LogSource` port. The `pgstore` adapter implements both, and there is a `memstore` adapter and the `storetest` suite.
  - With `LogSource`, `core/pdp` follows the outbox and depguard still keeps `pgx` out of it.
- **The PostgreSQL loader** reads the revision inside its `REPEATABLE READ READ ONLY` snapshot. Today `loadSnapshot` gets its revision from outside its transaction (`core/engine/localstate.go:299`).
- **PDP in `postgres` mode.**
  - It runs against the primary or against a regional streaming replica. The snapshots of a hot standby are consistent prefixes of the commits.
  - It folds the outbox entries.
  - It reads a decision's resources in one `REPEATABLE READ` snapshot, at the same position as its policy.
  - It has no `ensure`, writes nothing, and runs as a role that has `SELECT` only.
  - T(P), the time up to which all commits are reflected at position P:
    - on the primary, the time of the read;
    - on a standby, the newest heartbeat that the writer commits to PostgreSQL every second, so an idle primary does not make a replica look stale.
  - `POLICY__MAX_STALENESS` defaults to 60 s, and `0` is refused.
  - A long snapshot load on a standby must survive recovery conflicts (`hot_standby_feedback` or `max_standby_streaming_delay`, documented).
  - `min_revision` is native, and `consistency=higher` is removed.
- **`licetum-writer`.**
  - DBOS only. Steps run on `RunAsTransaction`.
  - A region-local fetcher (`licetum-writer fetch --region <r>`) without PostgreSQL credentials passes the source's answer to the executor in the home region; no DBOS executor runs outside the home region (4.9.1).
  - A schedule from `refresh_interval`, with a default of minutes, not 60 s. Sources that answer without changes get a backoff, and a metric counts calls to sources.
  - `RebuildComponent` (desired → effective links) runs apart from fetching.
  - `SyncOne` replaces the inline `resolve()`.
  - `structureOf` does not include the service name.
  - Two StatefulSets, `sync` (with a stable `DBOS__VMID`) and `project` (no DBOS), and a generalized janitor for scale-down.
  - Workflow retention through `dbos.DeleteWorkflows`, and an explicit `WithQueueBasePollingInterval` where queue latency matters.
  - The publisher is a single leader. `pg_notify` wakes it, it reads `outbox WHERE revision > last`, and a periodic tick catches a lost wake-up. The heartbeat is a ticker of the same leader. There is no DBOS workflow per revision.
  - In `postgres` mode nothing consumes the log, so the outbox is kept by age.
  - `reconcile` in its PostgreSQL form: it folds the outbox entries (a, r] onto the canonical state at a and compares the result with the canonical state at r, so a write that bypassed the outbox check shows (REC01). The comparison of the regions with PostgreSQL comes in P6 (REC02).
  - The writer collects the JWKS of PEP issuers into `pep_issuer_keys`.
  - `migrate` moves to the writer.
  - The binaries `core/cmd/licetum-pdp` and `core/cmd/licetum-writer` replace `core/cmd/licetum`.
- **Data payload.**
  - A synchronization writes the full state of every touched resource into `outbox_chunk` with COPY. Touched means changed, below a changed resource, or below a change of `unresolved_up`.
  - The outbox check now covers `resource_cache`, `resource_edge`, `resource_ancestor` and `resource_unresolved`.
  - The `services` of a data change are the services of every resource whose lineage changed, including descendants in other services. Today `bumpDataRevisions` names only the services that own the synchronized types.
- **Type mode.** A type is `sync` when `synced_at IS NOT NULL` and it has a live synchronizer. The admin transaction that creates or deletes a synchronizer clears `synced_at` and resets `sync_state`, in one transaction with its outbox entry (4.2).
- **Admin tools.** Simulate (in AuthZEN forms), who-has-access and preview run on `core/decide` with `pgstore`. Their load on the primary is measured. If needed, they read through the `postgres` adapter on a standby.
- **Removed:**
  - the query path: `applicableRules`, `ExpandRoles` (DB), `LiveMemberships`, `cacheTTL`, `TypesWithSynchronizer`, `RequestAttributes`, `attrsMemo`, and the SQL fallback of `ServiceIDByRef`;
  - the snapshot size guard;
  - `ensure`, `SYNC__REQUEST_WAIT`, `Pending`, `X-Authz-Sync` and `sync_demand`;
  - the `licetum worker` subcommand.
- **Contracts:** ST01 to ST04, ST06, POL01 to POL04, PUB03, REF01, WR07, SYN01 to SYN10, CONS01 to CONS04 (CONS01 with the postgres adapter; P6 adds the CQL adapter), FC05, AZ15, REC01, OPS01, OPS03, OPS09 (the outbox and the decision log; P6 adds the checkpoints and the regional rows).
- **Exit:**
  - depguard keeps `pgx`, `dbos`, the CQL driver and `net/http` out of `core/decide` and `core/policy`, and `pgx`, `core/db` and `dbos` out of `core/pdp`;
  - these contracts are green;
  - REF01 also passes with the PDP on a streaming replica (`scripts/pg-replica.sh`);
  - the load-test smoke gives no wrong answer.

### 11.10 P6: CQL log, regional applier, PDP in CQL mode (#57)

The CQL store is the one ADR 0002 chose. The model stays compatible with Apache Cassandra 5 whichever store is chosen. `storetest` also runs nightly against the store that was not chosen (10.2, ST09).

- **6a: log and publication.**
  - `core/logfmt`.
  - The CQL adapter, with CQL migrations and write helpers that require `USING TIMESTAMP`.
  - The publisher writes the outbox into `licetum_log`. Log chunks are keyed with a `part` component.
  - The heartbeat goes into the log.
  - Checkpoints are taken per stream. A superseded checkpoint is deleted once it is older than the log TTL.
  - `IssuerKeys` feeds the `pep` stream.
  - Epochs, with automatic detection of a divergence.
  - The commands `rebuild`, `reconcile --dc` (P5 brought its PostgreSQL form), `failover`, `freeze` and `unfreeze`.
    - `reconcile --dc X` runs nightly for every region X, as a CronJob in the home region (5.8), and that run is mandatory.
    - `freeze` uses an operator CQL role. It disables only the staleness gate; the log goes on being applied.
  - The migrate job of the home region creates the keyspaces or tables of generations ahead of time. The applier only writes.
  - A CQL container in `core/testsupport`, with one keyspace per package.
- **6b: applier and PDP.**
  - `core/applier` checks continuity and hashes and writes MVCC rows append-only. It also writes `policy_snapshot`, manages generations with manual rotation, and reads a missing chunk from another data center.
  - `core/pdp` serves from the regional view. It applies the staleness gate before the answer cache, waits for `min_revision`, and keeps a row cache. The row cache is invalidated by `applied_keys`, split per stream into rows of at most 4096 keys; above the threshold, a whole type is invalidated.
  - Search uses `descendant_v`, `type_member_v`, `attr_v` and `subject_v`, and computes each page at the current position.
- **MVCC garbage collection.**
  - The code and ST08 land in this phase, with the collection off by default.
  - A separate pull request turns it on by default, and only after APP01 to APP03 and ASOF01 to ASOF03 have passed under the kill tests of the nightly suite.
- **Removed:** the PostgreSQL poll of `revision_state` from the CQL mode of the PDP, and the PostgreSQL half of `localstate.go`.
- **Contracts:** ST05, ST07 to ST10, PUB01, PUB04, APP01 to APP03, ASOF01 to ASOF04, REC02, REF02, CONS01 (the CQL adapter), CONS05, FC01, FC04, PEP04, OPS04 (the CQL half), OPS05, OPS06, OPS09 (the checkpoints and the regional rows).
- **Exit:**
  - these contracts are green on fixed seeds;
  - REF01 passes in CQL mode;
  - an end-to-end test with two PDPs is green;
  - the load-test smoke gives no wrong answer and does no store read on a warm answer.

### 11.11 P7: topology, operations, nightly, landing (#58)

- **Helm.**
  - `pdp` in every region, with egress only to its read store.
  - `apply` in every region, with RBAC on its Lease.
  - `admin`.
  - `writer`, as two StatefulSets (`sync` and `project`), with egress to the sources and to the issuers' JWKS.
  - A `migrate` Job for PostgreSQL and CQL, which also creates the CQL roles, their GRANTs and the generation keyspaces.
  - A `reconcile` CronJob per region.
  - `web`.
  - A NetworkPolicy, a ServiceMonitor and a PrometheusRule for every component.
  - `values-cell.yaml` gives a cell its own PostgreSQL and read store.
  - The CQL cluster is outside the chart (run by an operator). It needs at least three nodes per region at RF 3, and that cost is documented.
  - The golden files are regenerated.
- **Metrics and alerts.**
  - Publication lag, application lag per data center, `now − T(P)`, outbox size, queue depth, numbers of versions and tombstones, and partition sizes.
  - The clock skew ε is taken from chrony or `node_timex_*`, never from the heartbeat, which mixes in lag.
- **Compose:** `infra/` gets a single-node CQL service, and `admin-api/tests/multidc` runs two data centers with Toxiproxy.
- **Documentation.**
  - `docs/deployment.md` is rewritten: the three topologies (one region on PostgreSQL, regions on PostgreSQL streaming replicas, regions on the CQL store), the SLOs, the failure matrix, sizing and the upgrade order.
  - `core/README.md` and `CLAUDE.md` are rewritten.
  - Runbooks: repair and repair-based node operations, rebuild, adding a data center (`nodetool rebuild` before the applier starts), a restore older than the log TTL (`rebuild --all` and a new generation), generation rotation, failover and epochs, `freeze`.
- **PostgreSQL HA for the CQL topology.** A synchronous standby is required, or S is derived from the measured failover time. Fencing of the old primary is a precondition.
- **Budgets and tests.**
  - The budgets are recalibrated on a fixed runner without `-race`.
  - The kind upgrade runs under AuthZEN load.
  - The remaining F-tests, the tier tables and the `-race` constants are removed.
- **Contracts:** POL05, REV01 to REV03, FC02, FC03, FO01, OPS02, OPS07, LOAD02.
- **Exit:**
  - REV01 p99 ≤ 1 s, REV02 p99 ≤ 5 s;
  - the other contracts of the phase are green;
  - seven nightly runs in a row are green;
  - `contracts.yaml` has no `planned` contract left except accepted gaps;
  - #40 is marked ready and is merged when the owner decides; `v1.0.0-rc.1` is tagged.

## 12. Risks

| Risk | Mitigation | Where |
|---|---|---|
| **Amount of mechanism.** The log, applier, MVCC, epochs and generations are more moving parts than "a store as a replica". | The `postgres` adapter is the default. It covers one region, and several regions on streaming replicas, without any of these parts. The applier is a role of the writer binary, so there are still three images. The garbage collection waits for the kill tests. The fold equation and the POL, APP and ASOF contracts check the parts. | P5, P6 |
| **Availability is coupled across regions.** If PostgreSQL or the publisher leader stops for longer than S, every region closes. A standby that stops replaying ages the same way. | Fenced PostgreSQL with a synchronous standby, or S above the measured failover time. Two or more `project` replicas with one leader. An alert at a heartbeat age of 10 s. `freeze` for a disaster: it turns off only the gate, and the log goes on being applied. | FC02, FO01, REV03 |
| **One ordered publication chain.** A malformed entry blocks every region. | Bounded retries, `licetum_projection_stuck`, an alert on publication lag, and the runbook `rebuild --stream`. Regions close after S instead of serving a policy with a hole. | P6, P7 |
| **Write amplification.** A first load or a structural change carries the full lineage of every touched resource through the outbox, the log and every region. A type of 1M resources means gigabytes. | Chunks written with COPY and compressed with zstd. A `part` component in the chunk key. `applied_keys` split per stream into rows of 4096 keys, with a whole-type invalidation above the threshold. Checkpoints per stream, rebuilds into a new generation, and a memory budget for the writer. Measured in P0.5 before the format is fixed. | P0.5, P5, P6 |
| **License of the CQL store.** ScyllaDB from 2025.1 is source-available: free up to 50 vCPU and 10 TB per organization, last AGPL release 6.2. A larger self-hosted installation would need a commercial license. | The choice is made in P0.5. The model stays Cassandra-compatible (no LWT, no MV, no secondary indexes, no tablets-only features), and `storetest` runs on Cassandra 5 too. The `postgres` adapter on regional standbys gives a multi-region setup without any CQL store. | P0.5, P6 |
| **Durability is part of correctness.** Suppose two of three replicas lose the newest version of a key, because a node was replaced without repair or the hints expired. A `LOCAL_QUORUM` read then returns an older version, such as a resource that is still active or an old lineage. That is fail-open. | Repair, or repair-based node operations after a node replacement, is a correctness requirement with a runbook. The nightly reconcile per region is mandatory; on drift it raises `licetum_projection_drift_total`, and `rebuild --stream` repairs the stream. Alerts fire on drift (`LicetumProjectionDrift`) and on a reconcile missing for 26 h (`LicetumReconcileMissing`, 6.11). | P6, P7 |
| **CQL specifics.** Large `descendant_v` partitions for root anchors, hot `attr_v` values, range tombstones from the garbage collection, and the `LOCAL_QUORUM` tail on cold misses. | Buckets per type, generation rotation, a scan budget for search, and the row cache. The garbage collection is off until the kill tests pass. Measured in P0.5. | P0.5, P6 |
| **The MVCC garbage collection and generation rotation are the main sources of fail-open.** | P6 starts append-only with manual rotation. The collection is turned on only after APP and ASOF pass under the kill tests. ST08 checks that the newest version is never deleted. | P6 |
| **PostgreSQL split brain, or commits lost on failover.** | Fencing is a precondition. A hash mismatch stops publication instead of overwriting. Epochs with `epoch_start`. A synchronous standby, or S above the measured failover time (6.8). | FO01 |
| **A restore older than the log TTL (14 days), or a new data center.** `epoch_start` cannot refer to entries of another history that have already expired. `ALTER KEYSPACE` does not move data that is already written. | Runbooks: a restore needs `rebuild --all` and a new generation; a new data center needs `nodetool rebuild` before its applier starts. The time of a full rebuild is measured on the large profile. | P7 |
| **Trust moves to the PEP.** A bound PEP can assert any subject and any properties, including ones that satisfy predicates written for claims an IdP verified. | Bindings per service and per operation. `explain` and `search_subject` are separate operations, off by default. Identity domains. The trust model is documented. Passing the user's token through can come later. | P2, P4 |
| **Subject namespaces across services.** The same id under two IdPs is two people. An exported cascade could apply the literals or predicates of service A to the subjects of service B. | Cross-service links are allowed only within one identity domain. | ADM09 |
| **Upstream rules leak through explain** to the PEP of a downstream service. | Upstream rules are reported only as `upstream: <slug>` and a level. The group predicates of the upstream service are never shown. | P4 |
| **Limits of DBOS Go v1.6.0.** It recovers only the workflows of its own executor. `WithEnqueueTransaction` is refused inside a workflow. Queue polling defaults to 1 s and grows to 120 s on errors. Prepared statements conflict with PgBouncer in transaction mode. Only the synchronization queue is pruned today (`dbosPrune`), so any new queue needs the same. | Publication does not use DBOS: a leader woken by NOTIFY, plus a tick. Two StatefulSets, `sync` (with a stable `DBOS__VMID`) and `project` (no DBOS), and a generalized janitor. An explicit `WithQueueBasePollingInterval`. A DBOS connection straight to PostgreSQL (`SYNC__DBOS_URI`). Retention through `dbos.DeleteWorkflows`. DBOS Conductor is evaluated before P6. | P5 |
| **Formats evolve.** The log and the CQL tables are a contract between the writer and the regions. | The PDP and the applier read formats N and N−1. The upgrade order is fixed in a runbook: PostgreSQL migrate, CQL migrate, `apply` and `pdp`, `writer`, `admin`, `web`. | POL05, OPS02 |
| **Coverage gaps during the transition.** | P1 comes before P2. 2a moves the oracles before the endpoints go. A contract stays `planned` until its phase. | P1, P2 |
| **The fail-closed fix changes answers.** Resources allowed today become denied. | Release notes, DEC11, and a measurement of the added join on the medium profile. | P1 |
| **No upgrade from v0.1.x.** Schema v2 cannot be applied to a `v0.1.0` database, and the public API of `v0.1.0` (`/permission`, `/check`, `on_behalf`) is removed. A v1 policy document is refused by the v2 import, so such an installation is reinstalled and its policy is re-created. | `migrate` refuses with a clear message, and the release notes say so. This is the owner's decision. | P2, P3 |
| **Regional standbys in `postgres` mode.** Replay can cancel a long snapshot load (a recovery conflict). | `hot_standby_feedback`, or a `max_standby_streaming_delay` above the load time, documented. Loads are retried. REF01 runs on a replica. | P5 |
| **Clocks.** Staleness and validity windows depend on the clock of the PDP. | NTP is required. ε is read from `node_timex_*` or chrony's exporter (6.11), with an alert at 1 s (`LicetumClockUnsynchronised`). S ≫ ε. | P7 |
| **CI and development get heavier.** The CQL store runs in Testcontainers, Compose and kind, and the multi-region SLO can only be measured at night. | One developer-mode node per pull request, one keyspace per package, two data centers only at night. The nightly suites are started by the dispatcher on `main`. | P0, P6 |
| **Artifacts break between phases.** Helm and Compose refer to `check`, `worker`, Redis, `AUTH__STATE_SECRET` and `PLATFORM__*`. The web typecheck runs on an admin schema that keeps changing. | Each pull request keeps them green, or switches the check off explicitly and leaves its contract `planned`. | all |
| **Cross-service links mix administration domains.** A hub service invalidates the answers of many others. | The exported flag, upstream rules shown only to read, `blocked_by`, and identity domains. Invalidation is limited to the services that read the change. | CONS04, ADM09 |
| **Volume of the rewrite.** About 14.8k lines of TypeScript and 1089 keys in each of three languages in `web/src`, about 7.3k lines in `admin-api/api` and `admin-api/service`, and about 17.5k lines of Go tests. The epic will diverge from `main` for months. | Small pull requests per phase, and `main` merged into the epic regularly. | all |
| **The subject directory** is empty unless something fills it, and holds personal data when something does. | Search is honestly empty when the directory is. Retention per service, and visibility only to the roles of the service. `directory_writer` has a rate limit. Directory writes take the revision row but not `admin_write`. | P2, P4 |
| **Services can be enumerated** through the public metadata (404 against 200). | A slug is not a secret. The access routes answer the same 403. The metadata stays public, as the specification requires. | P2 |
| **The audit of calls becomes best-effort,** because the decision log goes to stdout or OTel. | The requirement on the log pipeline is documented. A durable audit is a later feature. | P2 |
| **Admin tools load the primary:** simulate, who-has-access, lint and policy tests. | Measured in P5. If needed, they read through the `postgres` adapter on a standby. | P5 |
| **Load isolation.** `PEP__RATE_LIMIT` works per pod. There is no fair share between clients, and search scans up to 10 000 rows. | Concurrency quotas per client and a separate pool for search in the PDP. A global limit belongs to the ingress. | P2, P5 |
| **Source load without demand.** Every type is polled every `refresh_interval` forever, while today only types asked for within `SYNC__DEMAND_WINDOW` are polled. | A default of minutes, a backoff for unchanged answers, and a metric of calls to sources. | P5 |

## 13. Open questions and the defaults taken

### 13.1 Decided by the owner

These decisions are part of the owner's "implement the plan". They are not reopened.

| # | Question | Decision |
|---|---|---|
| 1 | Read model | A port with a `postgres` adapter (the default) and a CQL adapter for multi-region. The `postgres` adapter works against the primary or a regional streaming replica, whose `REPEATABLE READ` snapshots are consistent, so several regions work without a CQL store. |
| 2 | Which CQL store | ScyllaDB or Apache Cassandra 5, decided in P0.5 after the spike and the license question. The model stays Cassandra-compatible: no LWT, no MV, no secondary indexes, no tablets-only features. |
| 3 | Upgrade from `v0.1.0` | None. Schema v2 has its own goose version table, `migrate` refuses a v0.1.x database with a clear message, and the `previous-release` job is removed in P3. |
| 4 | Publication of the outbox | A single leader woken by `pg_notify` in the writing transaction, reading `outbox WHERE revision > last`, plus a periodic tick. There is no DBOS workflow per revision. DBOS stays for source synchronization. |
| 5 | Order inside P2 | 2d before 2c. `subject_directory` comes with the removal of `known_subject`. 2e is a pull request of its own. |
| 6 | Outbox check in P3 | On policy tables only. It counts the tuples actually changed and adds `AFTER TRUNCATE`. Reconciliation catches bypasses; they are not claimed impossible. Resource tables follow in P5. |
| 7 | Terms of P1 | Contracts and `refeval` use external terms (type names, external ids) from the start. |
| 8 | MVCC in P6 | Append-only with manual generation rotation. The garbage collection is turned on only after APP and ASOF pass under the kill tests. |
| 9 | Repair and `freeze` | Repair, or repair-based node operations after a node replacement, is a correctness requirement. The nightly reconcile per region is mandatory. `freeze` disables only the staleness gate; the log goes on being applied. |
| 10 | Search pagination | Each page is computed at the current position. The cursor holds the bucket, the last key and the request digest. No snapshot spans pages, and there is no `page_expired`. |
| 11 | Cross-service type links | Only to `exported` types, and only within one identity domain. |
| 12 | Slugs | Immutable after creation, never reused. |
| 13 | Branch strategy | The existing epic. #40 is a draft, the archive point is `8989ed4`, and #31 is closed as superseded. |

### 13.2 Defaults taken

These follow the plan with the critique applied. A later ADR, or a pull request that names the contracts it changes, may revisit them.

| # | Question | Default |
|---|---|---|
| 1 | Where the regional applier runs | The `licetum-writer apply` role, as a Deployment of two replicas per region with a Lease leader; duplicates are harmless. An applier embedded in the PDP for small installations may come later. |
| 2 | Staleness limit | `POLICY__MAX_STALENESS` is 60 s and `0` is refused. Beyond the limit: 503, and `/readyz` false. |
| 3 | PostgreSQL HA for the CQL topology | Fencing of the old primary is required. A synchronous standby is required, or S must exceed the measured failover time. |
| 4 | Global directory groups | None in v1. Groups belong to a service. What is common across services is written as predicates over `subject.properties`, and policies are reused through export and import. |
| 5 | Subject identity | `subject.type` is required. Literal rules carry `subject_type` (NULL means any), and `group` is reserved. |
| 6 | Questions about a whole type | `resource.id = "*"`, with the capability `urn:licetum:whole-type-resource-id`. A source may not report `*` as an external id. |
| 7 | AuthZEN error body | A JSON string, as the specification shows, plus a `Licetum-Error-Code` header. |
| 8 | PEP authentication in v1 | JWT client-credentials (mandatory `aud`, an `alg` allowlist, keys through the replicated `pep` stream) and API keys (sha256, two active). mTLS/SPIFFE and `private_key_jwt` come later. |
| 9 | Explain and search for PEPs | Separate operations: `explain`, `search_resource`, `search_action` and `search_subject`, off by default. An explanation shows upstream rules only as `upstream: <slug>` with a level. |
| 10 | Admin roles | `platform_admin`, `platform_viewer`, `service_creator`, `service_owner`, `service_editor`, `policy_editor`, `member_manager`, `service_viewer`, `group_manager`, `directory_writer`. |
| 11 | Who deletes a service | Only `platform_admin`. An owner can switch off the service's PEP clients and empty the service. |
| 12 | Revocation SLO | p99 ≤ 1 s in the home region and ≤ 5 s in every region, with the hard bound S + ε. Measured at night on a runner without `-race`. |
| 13 | What replaces the audit of delegated calls | A structured decision log (stdout or OTel) with the client, subject, resource, action, decision, position and request id. A durable audit is a later feature. |
| 14 | Recovery of workflows of dead executors | Two StatefulSets, `sync` (with a stable `DBOS__VMID`) and `project` (no DBOS), and a generalized janitor for scale-down. |
| 15 | License of the writer and the applier | MIT, in `core`. `bootstrap`, `restore-admin` and the admin role model are AGPL, in `admin-api`. |
| 16 | Retention | The log TTL is 14 days. A stream that changed gets a checkpoint at least every 7 days (policy streams every 256 changes or 4 MiB, data streams at a delta of 20 %). Superseded checkpoints are deleted after the log TTL. Generations rotate by metric, at most once a week. Buckets: 16 per type, 8 for `attr_v`. |
| 17 | Sources reachable from one region only | A region-local fetcher without PostgreSQL credentials passes the source's answer to the executor in the home region. |
| 18 | `resource.properties` and `action.properties` | Accepted and ignored in 1.0. |
| 19 | Renaming `claim` to `subject` / `property` | Yes, in the API, the UI and i18n, in P3. |
| 20 | Sharding `admin_write` per service | Not in v1. One lock, revisited if contention shows in the metrics. |
| 21 | Authentication for AuthZEN metadata | No: the specification makes the metadata public. The access routes answer the same 403. |
| 22 | PKCE and state of the admin sign-in | A short-lived encrypted cookie of the BFF. |
| 23 | Policy document | `licetum.policy/v2`, a full unit of one service. Importing a v1 document is a clear refusal. |
| 24 | Writes to the subject directory | They take the revision row but not `admin_write`. They are done by editors or by a `directory_writer` machine client with a rate limit. Retention is set per service. |
| 25 | Type mode | `sync` means `synced_at IS NOT NULL` and a live synchronizer exists. The admin transaction that creates or deletes a synchronizer clears `synced_at` together with its outbox entry (4.2). |
| 26 | `context.licetum` in `/evaluations` | Read only from the top-level `context`, so one request has one position. |
| 27 | `synchronizer.cache_ttl` and `SYNC__FETCH_TTL` | Removed with Redis in 2e. |
| 28 | Outbox in `postgres` mode | Kept by age, since nothing consumes it there. Moving an installation to the CQL store is a documented `rebuild --all`, covered by a contract added in P6. |
| 29 | Subject-hash salt | A deployment secret, `LOG__SUBJECT_SALT`, the same in every region and never in the replicated state; schema v2 drops `log_settings.salt` (7.12, OPS09). |

### 13.3 Open, with the phase that settles them

| # | Question | Default until then | Settled in |
|---|---|---|---|
| 1 | ScyllaDB or Apache Cassandra 5; the driver and its pinned version | None. P6 does not start without ADR 0002. | P0.5 |
| 2 | How far the `postgres` adapter on regional standbys carries (regions, write rate, revocation latency) | It is the recommended multi-region setup until the CQL store exists. | P0.5, measured again in P5 |
| 4 | The default of `refresh_interval` | Minutes, not 60 s, with a backoff for unchanged answers. The number is set from the metric of calls to sources. | P5 |
| 5 | DBOS Conductor (license, self-hosting) for recovering workflows | StatefulSets and the janitor. | before P6 |
| 6 | Whether the admin tools need a read path of their own | The primary. | P5, after measurement |
| 7 | Rollback over types, synchronizers and links | Until the batch learns these kinds (without secrets), such a rollback stays a 409 `change_not_revertible`. | P4 |
| 8 | The round-trip budget of an admin write (WR06) | Measured, not postulated. The lock and the access recheck share one batch, and the revision, outbox, `change_log` and NOTIFY share the last one. | P3 |

Question 3, the salt for hashing subjects in logs, is settled: see 13.2 #29. The other rows keep their numbers.

## Appendix: defects of the current code found while planning

These defects came up while the plan was written. Each one was checked against the code again on 2026-10-18. Unless a line says otherwise, file and line references point to the tip of the epic `epic/production-load-readiness` (`229c4ed`). "In `v0.1.0`" says whether the released version has the same defect. This ADR does not include a backport to 0.1.x.

| # | Defect | In `v0.1.0` | Closed by |
|---|---|---|---|
| 1 | A resource below an ancestor with an unresolved parent is not treated as possibly under a denial (fail-open) | yes | P1, DEC11 (already fixed on the P1 branch) |
| 2 | Tokens are not checked for `aud`, and `exp` is not required | yes | P2, PEP01, ADM01 |
| 3 | Rollback undoes the changes of every service | yes | P4, HIS02, ADM04 |
| 4 | `DB__URI` is stored in the configuration of the platform synchronizers | yes | P4 (exit criterion) |
| 5 | Admin routes ignore the service in their path, or have none | yes | P4, ADM10, ADM02 |
| 6 | A short-circuiting `/evaluations` decides every evaluation | no (no `evaluations_semantic` yet) | P2, AZ03 |
| 7 | The policy snapshot carries a revision read before its transaction | no (no snapshot yet) | P5 |
| 8 | Slugs can be changed and reused | yes | P3, ADM11 |
| 9 | Deviations from AuthZEN on the check port | partly | P2, AZ01, AZ06–AZ08 |

Items 1–6 are those of the plan. Items 7–9 were confirmed while re-checking them.

### 1. Fail-open below an ancestor whose parent is unresolved

- **Where.** `UnresolvedParents` (`core/engine/resource_cache.go:836`) selected `resource_edge` rows with `parent_id IS NULL` whose child is an active resource of one of the requested types. It returned only a resource's own unresolved links. The check path (`CoversOf`, `core/engine/check.go:189`) and pass C of `coverNodes` in the permission path (`core/engine/permission.go:982`) built their assumed covers from that result.
- **Effect.** Take a resource whose own parent links are resolved but which lies below an ancestor whose parent is not in the cache. It was not treated as possibly lying under a denial anchored above the missing parent. Example (`TestIntegrationOrphanAboveFailsClosed`):
  - Rules: allow `read` on the whole type `branch`, deny `read` on the division `D1`.
  - The rrs `R9` names a division `D9` that the source of divisions does not report yet.
  - The branch `B9` under `R9` was visible, although `D9` may turn out to be `D1`.
- **Status.** Fixed in P1 by `5393b2b` on `refactor/p1-decision-contracts`, not yet merged into the epic.
  - `UnresolvedParents` now starts from the unresolved edges (partial index `ix_resource_edge_unresolved`) and walks down `resource_ancestor`.
  - For each resource it returns every unresolved link at or above it as an `UnresolvedLink`: the type that declares the link, the missing parent type and the levels of the route down.
  - `TypeGraph.assumedMask` turns these links into the mask of an assumed cover, on both paths.
  - Contract DEC11 becomes `active` in the P1 pull request, together with its `TestContract_DEC11_*` tests.
  - The new join runs on every cache miss of a listing. It has to be measured on the loadtest medium profile before P1 merges.
  - The "Fail closed" bullet of `CLAUDE.md` still describes only a resource's own edge and has to say "at or above".

### 2. Tokens are not checked for their audience, and `exp` is not required

- **Where.** `fetchAndVerify` (`core/engine/oauth.go:512-551`) verifies three things:
  - the signature against the service's JWKS, with an allowed algorithm;
  - `iat` (`jwt.WithIssuedAt`);
  - `iss` against `service.issuer`.

  It does not check `aud`. It does not require `exp` either: without `jwt.WithExpirationRequired()`, `golang-jwt/jwt/v5` checks `exp` only when the claim is present. A token without `exp` is therefore accepted on every request; `VerifyToken` only skips caching it (`oauth.go:424`).
- **Effect.** Any token the issuer signed is accepted, whatever client or resource server it was issued for. When several applications share one IdP realm, a token any of them obtained is valid in two places:
  - the check API of every service with that issuer (`core/server/check.go:76`);
  - the admin API (`admin-api/service/platform.go:66`), which verifies against the platform service.

  The rules then decide on the subject and the claims of that token.
- **Status.** Not fixed in place, because per-service token verification is removed in P2 (2c). The replacements must check the audience and require `exp`:
  - PEP01: a PEP JWT needs an allowed issuer, the PDP audience, `exp`/`nbf` and an allowed algorithm. Its test includes a token without `exp`.
  - ADM01: a wrong-audience admin token is a 401.

  Until then, giving a service an issuer (realm) of its own limits the tokens it accepts to that realm's clients.

### 3. Rollback undoes the changes of every service

- **Where.**
  - `PlanRollback` (`admin-api/service/history.go:37`) reads `changesAfter` (`admin-api/service/changelog.go:130-131`): `SELECT … FROM authz_change WHERE id > $1 ORDER BY id DESC`, with no service filter.
  - The route `POST /changes/rollback` (`admin-api/api/admin_batch.go:71`) has no service either.
  - `GET /changes` can filter by service (`changelog.go:67`); the rollback cannot.
- **Effect.**
  - Rolling one service back to a position also undoes every later change of every other service, in one batch.
  - `authorizeOperations` checks each operation against the caller's access, so this is not an escalation. But the first operation the caller may not do refuses the whole batch. A caller who may write only part of the policy therefore cannot roll back at all once another service has changed after the position.
  - `undoOperations` (`history.go:55-58`) refuses with 409 `change_not_revertible` any record without an entity id, or whose entity is not one of the `revertibleEntity` kinds (rules, groups, members, roles, role items, access groups). A single later change anywhere to a service, a resource type, a synchronizer or the log settings refuses the whole rollback.
- **Status.** P4 makes rollback and revert service-scoped (HIS02), and ADM04 refuses a rollback outside the caller's service. The batch learns types, type links, synchronizers (without secrets) and service settings; an operation that cannot be restored exactly is planned as `partial` and applied only with `allow_partial: true` (8.7).

### 4. The password of Licetum's database is stored in the platform synchronizers

- **Where.** `SeedPlatform` passes `cfg.DB.URI` to `seedPlatformType` (`core/db/migrate.go:244`). That function stores it as `dsn` in the `synchronizer.config` of each of the six platform policy types (`migrate.go:284-305`), whose sources read Licetum's own tables.
- **Effect.**
  - `DB__URI` is stored in plain text in a table row, with the password if the URI carries one.
  - The admin API masks it (`SynchronizerConfig.Masked`). It is still in every dump and backup, and any database role with `SELECT` on `synchronizer` can read it.
  - The seed inserts with `ON CONFLICT (id) DO NOTHING` and never updates the row, so a password rotation leaves the old credentials in place. The platform synchronization then fails and keeps the data of its last success.
  - `Access.Can` (`admin-api/service/platform.go:120`) only looks at per-entity entries. An entity created after the failure therefore has none, and even a platform admin is refused on it and does not see it in lists. This lasts until the six configurations are updated by hand.
- **Status.** P4 removes the platform types and their synchronizers. Its exit criterion is that no `synchronizer` row points at Licetum's database. No contract covers it.

### 5. Admin routes ignore the service in their path

- **Where.** Six routes are nested under `/service/{service_id}` but never compare the type with that service:
  - The four access-group routes (`admin-api/api/admin_access_groups.go:27, 42, 64, 86`). They pass only the type to `ListAccessGroups`, `CreateAccessGroup`, `UpdateAccessGroup` and `DeleteAccessGroup` (`admin-api/service/access_groups.go:88, 110, 145, 179`). The first two look the type up with `GetExistingTypeResourceAnyService` (`core/engine/app.go:338`).
  - `get_resource` and `check_resource_covered` (`admin-api/api/admin.go:332, 350`). They call `GetResource` and `IsCovered` (`core/engine/resource_cache.go:1017, 1027`) with the type only.

  Three routes have no service in the path at all:
  - `GET /type_resource` (`admin.go:267`)
  - `GET /type_resource/{type_resource_id}/resource` (`:296`)
  - `POST /type_resource/{type_resource_id}/resource/refresh` (`:314`)

  The synchronizer routes and `who_has_access` do it right: they use `GetExistingTypeResource`, which returns 404 for a type of another service (`core/engine/app.go:325-334`).
- **Effect.** Today the authorization is on the type (`requireResource(model.PlatformTypeResource, …, "type_resource_id")`), so the ignored segment grants nothing extra. `…/service/<A>/type_resource/<type of B>/access_group` acts on B's type under the caller's rights on that type. In P4 rights attach to the service of the path, and this becomes an isolation hole.
- **Status.** P4: every route checks that its child entity belongs to the service of the path (ADM10, isolation matrix ADM02). The three routes without a service move under `/service/{service_id}`.

### 6. A short-circuiting `/evaluations` decides every evaluation

- **Where.**
  - `authzen_evaluations` (`core/server/authzen.go:774-785`) runs `caller.evaluate` over all `evaluations`. Only afterwards does `applySemantic` (`:292-313`) cut the response at the stopping evaluation.
  - `evaluate` (`:225-279`) groups the evaluations by subject, claims and attributes, and decides each group with one `CheckAccess` (`:262`).
  - It logs every decision (`LogDecisions`, `:266`) and counts every question for the audit of delegated calls (`c.note`, `:277`, written by `flush`, `:778`).
- **Effect.**
  - `deny_on_first_deny` and `permit_on_first_permit` cost as much as `execute_all`, for up to 100 evaluations.
  - The decision log and `delegated_call` record evaluations that the response does not contain.
  - If `CheckAccess` fails for a group after the stopping evaluation (for example 503 `policy_unavailable`), the whole request fails.
- **Status.** P2 (2b), AZ03. Evaluations are decided in request order and the work stops at the stopping evaluation. Grouping may only merge evaluations up to that point, for example consecutive runs of one subject.

### 7. The policy snapshot carries a revision read before its transaction

- **Where.**
  1. `readView` reads the revisions with one statement (`readCacheState`, `core/engine/localstate.go:258`; `core/engine/cache.go:58-71`).
  2. It then calls `loadSnapshot(ctx, revision, …)` (`localstate.go:299`).
  3. `loadSnapshot` opens its own `REPEATABLE READ` transaction (`core/engine/snapshot.go:133`) and stores the revision it was given (`snapshot.go:155`).
- **Effect.**
  - A write committed between the two statements is in the snapshot, but labelled with the earlier revision.
  - The content is never older than its label, so no revoked grant is served.
  - But answers kept under one revision key (L1 and Redis, `authz:permission:t7:r<revision>:…`) can come from different policy states in different processes. So a revision is not an exact position of the policy, which the positions of this ADR (ASOF) cannot accept.
- **Status.** P5: the PostgreSQL loader reads the revision inside its snapshot transaction.

### 8. Slugs can be changed and reused

- **Where.** `ServiceUpdate.Slug` (`core/model/service.go:90`) lets `PATCH /service/{id}` change the slug. `uq_service_slug` is unique only among live services (`core/db/migrations/00001_init.sql:422`, `WHERE deleted_at IS NULL`).
- **Effect.** The check API, AuthZEN and its discovery document take the service by id or slug (`App.ServiceIDByRef`).
  - A PEP configured with a slug stops working after a rename.
  - After the service is deleted, a new service can take the slug. The PEP is then answered from that service's policy for whatever types share the names it asks about.
- **Status.** Owner decision: a slug is immutable after creation and never reused. Schema v2 makes the slug unique over all rows and refuses its change (4.2, 8.10), in P3; contract ADM11.

### 9. Deviations from AuthZEN on the check port

- `verifiedCaller` (`core/server/check.go:67-69`) accepts only the exact prefix `Bearer `. `bearer …` or another scheme gets 400 `auth_header_required`. The admin API's `bearerToken` (`admin-api/service/platform.go:105-111`) compares the scheme case-insensitively. Target (AZ08): case-insensitive, and 401 for another scheme.
- `requestID` (`core/server/server.go:293-300`) replaces an `X-Request-ID` that is not `[A-Za-z0-9._-]{1,128}` with a generated UUID. Target (AZ07): returned verbatim when it is safe for a header.
- `RequestContext.Validate` (`core/model/check.go:39-56`) refuses an object, or a list within a list, in the AuthZEN `context`: a 400 for a single evaluation, an error decision in a batch. Target (AZ06): such a value is ignored by conditions.
- `resource.id` is optional, and an empty one means the type as a whole (`core/model/authzen.go:20`). Target (AZ01): it is required; the whole-type question is carried as `resource.id="*"` (7.4).

All four are fixed in P2 (2b).

### Claims checked and not confirmed

- **"DBOS workflows are never deleted"**: wrong. `dbosJanitor` (`core/engine/sync_dbos.go:374`) runs `dbosPrune` (`:450-467`) every `SYNC__LEASE`/3. It deletes, 500 at a time, the workflows of the `licetum-sync` queue completed more than max(1 h, 2 × `SYNC__DEMAND_WINDOW`) ago. What is true: `retention.go` does not touch DBOS, and only that one queue is pruned, so any queue the writer adds needs the same.
- **"`get_resource` is scoped by its path"**: its path does contain `service_id`, but the handler ignores it (item 5).
- **"`bumpDataRevisions` bumps only the owner services"** (`core/engine/cache.go:353-360`): this is true, but it is not a defect of the current cache.
  - An answer records the data revisions of the services of every type it involves: the asked types, the types of the applicable rules with their descendant types, and the lookup types (`core/engine/permission.go:662-701`).
  - The owner of every changed type is bumped.
  - Only the per-type data streams of v2 (2.2, 4.8) need a wider set.

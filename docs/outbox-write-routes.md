# Admin write publications

Each non-GET admin operation declares `withOutbox(op, streams, services)` alongside
its access policy. API registration fails when a declaration is missing. The
OpenAPI generation and API unit tests therefore check declarations during the build.
The metadata is internal; it does not change the public OpenAPI document.

A nil stream list means no publication (simulation, previews, source tests and
stored policy scenarios). An empty, non-nil list means a `change` header with no
streams, as for directory settings. A declared publication describes a successful
execution that changes state: an unchanged directory entry, an unchanged source
refresh or an empty import plan need not take a revision.

The stream names are `catalog`, `pep`, `settings`, `svc:{owner}` and `dir:{owner}`.
`owner` is the immutable business owner, including for group, role and rule routes
whose URL does not name a service. A service owns itself. `data:{moved-types}` is
the set of type data partitions actually moved by a refresh or a source mode
change; it can be empty. `{operations}` means the union for the operations in a
batch, import, revert or rollback. In P3 a type link changes desired and effective
state together and belongs to `catalog`.

Affected services are either `none`, `owner-and-descendants` (the owner plus the
services below it in the effective type graph), or `operations` (the union for a
compound write). PEP, logging and directory writes affect no decision services.

`admin-api/tests/integration/harness/canned_writes.go` contains one successful
request template for every write route, including read tools implemented as POST.
The PostgreSQL fixture supplies owners, dependency rows and synchronized data.
WR05 compares the actual kind, streams and services with route metadata, and
rejects missing, duplicate, obsolete or mismatched catalogue entries. The same
catalogue is the input for canonical-image and round-trip contracts.

WR03 reads every covered table independently around each canned write and compares
its net canonical row diff with the published images. The oracle checks its table
registry against PostgreSQL's covered-table triggers. Soft deletes become
key-only tombstones; private source configuration and the desired half of links
are excluded. Data headers identify partitions and carry no policy-table images.
Seed and restore-admin use the same independent table-diff check.

Compound writes preserve the before state from the first touch and project the
final row once. Creating and then removing a row, or changing it and restoring its
original value, produces no image. A revision can still record the audited write
and its streams and affected services. The same applies to an edit of private
source configuration that changes no canonical field. Writers supply the mutable
before fields from their locked reads; a writer without that information must
leave `BeforeKnown` false. Parent links and PEP rows retain canonical before
images, including hidden fields. Timestamp equivalence applies only to timestamp
columns; literal names and subject IDs always retain exact string semantics.

Deleting a group removes and audits its own membership entries. Rules and incoming
references from other groups still block the deletion. Deleting a type removes
its outgoing parent rows in the same transaction. Issuer-key cache rows removed
by a PEP issuer cascade are included in that issuer's publication.

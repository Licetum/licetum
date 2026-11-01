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

# Schema-v2 fold verification

The full chain at `1116d32` (versions 1–24) and the folded `00001_init.sql`
were installed into two independent empty databases on PostgreSQL 16.15.
`pg_dump --schema-only --no-owner` was run with the same 16.15 client, retaining
function ACLs. Only the random `\restrict` / `\unrestrict` nonce lines were
removed before comparison. The diff is empty, including the provider-owned
version-table DDL.

Both normalized dumps have SHA-256:

```text
2b76696fed0c1a21a2ef56c90936d5833ce371fa33d85b659cc317015464514b
```

The five initial records are checked by
`TestSchemaV2InitializerRoundTripPreservesDefaultsAndNamespace`: global epoch 1
and revision 0, the default identity domain, log settings, unbootstrapped
platform state and publisher progress. Creation timestamps use their defaults;
the test reinstalls the initializer and requires a fresh timestamp. It also
checks all 74 ALWAYS triggers, the PUBLIC execution restrictions, a complete
Down to version 0 and installation in a custom search-path schema.

To reproduce the comparison:

1. Create two empty databases, one for the chain and one for the initializer.
2. Extract the chain sources with `git archive 1116d32 core | tar -x -C <temporary-directory>`.
3. From the extracted `core` directory, run the current checkout's
   `core/tests/integration/db/testdata/schema-install/main.go` with
   `LICETUM_SCHEMA_URI` pointing at the chain database. From the current `core`
   directory run the same tool against the initializer database. It applies
   embedded migrations without environment-dependent platform seed data.
4. Dump both databases with `pg_dump --schema-only --no-owner` using the same
   client. Keep ACLs: `--no-privileges` would hide the security boundary.
5. Remove only lines matching `^\\restrict ` and `^\\unrestrict `, then compare
   the complete dumps with `diff -u`.

The stage verification saved the original dumps, normalized dumps and empty
diff in `/workspace/.licetum-setup/p3-{chain,fold}-schema*.sql` and
`/workspace/.licetum-setup/p3-schema-equivalence.diff`. The application schema is
unchanged; migration history now records one v2 initializer instead of the
intermediate chain. Future changes add new numbered migrations.

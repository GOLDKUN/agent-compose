# SQLite migrations

Migration files are embedded into the agent-compose binary and applied by the
`storage/sqlite` package before the shared database is provided to any store.

- Name files `NNNNNN_description.sql` with a unique, increasing six-digit
  version.
- Migrations are forward-only and must run inside a SQLite transaction.
- Never edit, rename, reorder, or remove a released migration. The runner
  stores and verifies each file's SHA-256 checksum.
- Add schema changes only as a new migration; do not add startup-time schema
  inspection or `CREATE TABLE` statements to store implementations.
- Keep connection PRAGMAs and operations such as `VACUUM` out of migrations.
- Store new business timestamps as Unix milliseconds, with `0` representing an
  optional timestamp that has not been set. Existing second-, millisecond-, and
  nanosecond-based columns retain their released units and are converted at
  their owning storage boundary rather than rewritten in place.

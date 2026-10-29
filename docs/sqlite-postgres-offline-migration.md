# Offline SQLite to PostgreSQL migration

Use a maintenance window when downtime is acceptable. `runnerd-migrate` is a one-shot importer; it starts no HTTP server or background business workers and requires no dual writes or replication service. See the detailed [Chinese runbook](zh/sqlite-postgres-offline-migration.md).

1. Rehearse with a consistent SQLite copy and a disposable PostgreSQL `_test` database. Use the current model schema; missing columns are rejected rather than modifying the source.
2. Pause new workflows and deployments, drain active work, and stop every runnerd writer and restart supervisor. Existing Web Terminals disconnect. Arrange durable webhook intake or record and redeliver failed GitHub deliveries; GitHub does not automatically redeliver them.
3. Preserve the original SQLite, configuration and encryption/session keys. Prepare a protected target config with `database.backend: postgres`, the target `database.dsn`, and no `online_migration`. Preserve relative credential path resolution.
4. Create a dedicated empty PostgreSQL schema/database, keep its service stopped, and run:

   ```sh
   go build -o bin/runnerd-migrate ./cmd/runnerd-migrate
   install -d -m 700 ./var/offline-backups
   bin/runnerd-migrate --config ./runnerd.postgres.yaml \
     --sqlite /absolute/path/runnerd.db \
     --backup "$PWD/var/offline-backups/before-postgres.db" \
     --source-stopped --timeout 30m > ./var/offline-backups/report.json
   ```

The importer holds a SQLite writer lock while creating a private, exclusive `VACUUM INTO` backup (including committed WAL data). The lock does not establish service shutdown or fence future writes after the command exits. The backup directory must be `0700`; the file is `0600`. Existing files are never overwritten; failed artifacts remain for inspection.

It copies the 18 model-owned tables, preserving IDs, nulls, booleans, JSON and ciphertext. Timestamps normalize to microseconds. Legacy extra tables/columns remain in the backup and are counted in the report. Target schema creation, copying, full row verification and sequence alignment happen in one transaction. Historical deleted-ID sequence watermarks are preserved. The report contains table row counts and SHA-256 digests. No source startup backfill or business hook runs.

Require successful exit and `verified: true`. Failures roll back target DDL/data; lost commit acknowledgement is uncertain, so inspect the target instead of automatically retrying or clearing it. Nonempty target schemas are rejected. The import transaction pins `search_path` to the selected target schema, preventing ORM lookup from using same-named tables in fallback schemas. Budget space for the backup and PostgreSQL transaction/WAL; default timeout is 30 minutes.

Start one target runnerd behind maintenance access. Verify health, sign-in, historical records, a real catalog save with audit, and new IDs. Then verify real Runner/Deployment lifecycles and a new Web Terminal. Restore traffic only after acceptance, redeliver missed events, and reconcile task state.

Before formal writes resume, rollback means stopping the PG process and restoring the original SQLite configuration. PG acceptance-test writes are not synchronized back and must be disposable or reconciled; do not duplicate tasks already started there. After formal PG writes resume, returning to the old SQLite is not lossless and requires a separate recovery plan.

An already-active online migration must be finalized through its existing protocol, never by deleting metadata. To rehearse offline from it: verify reverse replication, rollback, finish on SQLite, stop the service, then import into a new empty PG database. Finishing directly on PG is another valid closeout but does not test this importer.

Validation (only dedicated `_test` databases; random per-test schemas):

```sh
RUNNERD_CATALOG_BACKEND_TESTS=1 RUNNERD_POSTGRES_TEST_DSN='<test DSN>' \
  go test -tags development ./internal/state ./internal/server \
  -run 'Test(OfflineMigration|PlatformSpecRevision)' -count=1
```

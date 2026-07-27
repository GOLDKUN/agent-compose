# V2 storage migration

The daemon upgrades databases with a valid `schema_migrations` prefix in place. Migrations `000001` through `000004` remain immutable; `000005` through `000007` replace the business-v1 agent, loader, and event-link storage with native project agent, scheduler, and sandbox associations.

Stop every sandbox with the old CLI, then stop the old daemon and back up its data root before upgrading. Stopping sandboxes is required because an existing Docker container retains bind mounts to the old root; the copy migrator rejects running sandbox metadata rather than allowing writes to split across old and new roots. Stopped legacy containers are safely recreated with target-root mounts when they are resumed after cutover. A normal versioned database containing only project-managed agents and schedulers can be opened directly by the new daemon.

Use the standalone copy migrator when the old root contains standalone agents or loaders, has no migration history but has a recognized legacy shape, or still needs legacy filesystem-layout conversion. Versioned sources must have a known exact migration prefix; a checksum mismatch is rejected rather than guessed:

```bash
agent-compose-migrate --source /old/data-root --target /new/data-root --dry-run
agent-compose-migrate --source /old/data-root --target /new/data-root --json
```

The published daemon image includes this command. When using the supported image deployment, mount the stopped source read-only and mount a writable parent for the new target directory (replace `VERSION` with the version being installed):

```bash
docker run --rm --entrypoint agent-compose-migrate \
  -v /old/data-root:/source:ro \
  -v /new/data-parent:/migration \
  ghcr.io/chaitin/agent-compose:VERSION \
  --source /source --target /migration/data-v2 --runtime-root /data --dry-run
```

Remove `--dry-run` to perform the copy. `--target` is where the migrator writes in its own filesystem namespace; `--runtime-root` is the same target data root as the new daemon will see it. The latter defaults to `--target` for native migrations, but must be `/data` for the Compose deployment above. A resumed migration must use the same runtime root recorded in its journal.

The source is opened read-only. The target must be new or contain the migrator's matching journal; there is no overwrite flag. The migrator fingerprints a consistent SQLite snapshot plus authoritative files, converts standalone and mixed agent/scheduler records into the `legacy-v1-default` project, and appends a new immutable revision when historical managed projections disagree with their recorded revision. It only fills a project run's scheduler-run link when persisted event/sandbox evidence identifies exactly one scheduler run; unresolved or ambiguous links remain `NULL` and are listed in the report.

Filesystem copying does not follow symlinks. Legacy `sessions` and `loaders` trees become `sandboxes` and `schedulers`, old loader directory IDs are mapped to scheduler IDs, and stored database paths plus known sandbox lifecycle, metadata, and mount-manifest path fields are rewritten to the runtime root. Unrelated JSON strings and external volume paths remain unchanged. Conflicting old/new layouts fail instead of overwriting one another. A matching journal makes interrupted copies resumable, while any logical database or authoritative-file change invalidates the resume. The source remains untouched for rollback. After success, explicitly configure the daemon to use the target root.

If the report identifies an unsupported or ambiguous shape, do not point the new daemon at the old root. The daemon intentionally does not guess ownership or migrate unversioned schemas during startup.

# V2 storage migration

The daemon upgrades databases with a valid `schema_migrations` prefix in place. Migrations `000001` through `000004` remain immutable; `000005` through `000007` replace the business-v1 agent, loader, and event-link storage with native project agent, scheduler, and sandbox associations.

Stop every sandbox with the old CLI, then stop the old daemon and back up its data root before upgrading. Stopping sandboxes is required because an existing Docker container retains bind mounts to the old root; the copy migrator rejects running sandbox metadata rather than allowing writes to split across old and new roots. Stopped legacy containers are safely recreated with target-root mounts when they are resumed after cutover. A normal versioned database containing only project-managed agents and schedulers can be opened directly by the new daemon.

Use the standalone migrator when the old root contains standalone agents or loaders, has no migration history but has a recognized legacy shape, or still needs legacy filesystem-layout conversion. Versioned sources must have a known exact migration prefix; a checksum mismatch is rejected rather than guessed. Prefer an in-place migration by passing the same data root to `--source` and `--target`:

```bash
agent-compose-migrate --source /data --target /data --dry-run
agent-compose-migrate --source /data --target /data --json
```

The dry run performs the database conversion in a temporary database and validates every filesystem entry and path rewrite, but does not copy sandbox, workspace, volume, artifact, image, or cache data. In-place execution retains a consistent v1 database and the original rewritten JSON files under `.agent-compose-migrate-backup`, renames `sessions` to `sandboxes`, renames `loaders` and their IDs to native scheduler paths, and atomically activates the converted database last. Large data files stay on the same filesystem and inode.

The published daemon image includes this command. For the supported image deployment, mount the stopped data root read-write at `/data` (replace `VERSION` with the version being installed):

```bash
docker run --rm --entrypoint agent-compose-migrate \
  -v /old/data-root:/data \
  ghcr.io/chaitin/agent-compose:VERSION \
  --source /data --target /data --runtime-root /data --dry-run
```

Remove `--dry-run` only after it succeeds, then run the same command without that flag. Keep the old daemon stopped until migration completes. A matching journal resumes an interrupted database, layout, or activation stage. Do not remove the backup directory until the new daemon and migrated data have been verified.

Copy migration remains available when an operator intentionally needs a separate target: pass different `--source` and `--target` directories and set `--runtime-root` to the path the new daemon will see. That mode preserves the source for rollback but requires enough space to duplicate its files.

The migrator fingerprints a consistent SQLite snapshot plus authoritative files, converts standalone and mixed agent/scheduler records into the `legacy-v1-default` project, and appends a new immutable revision when historical managed projections disagree with their recorded revision. It only fills a project run's scheduler-run link when persisted event/sandbox evidence identifies exactly one scheduler run; unresolved or ambiguous links remain `NULL` and are listed in the report.

Filesystem validation and copy mode do not follow symlinks. Legacy `sessions` and `loaders` trees become `sandboxes` and `schedulers`, old loader directory IDs are mapped to scheduler IDs, and stored database paths plus known sandbox lifecycle, metadata, and mount-manifest path fields are rewritten to the runtime root. Unrelated JSON strings and external volume paths remain unchanged. Conflicting old/new layouts fail before any in-place rename instead of overwriting one another. In copy mode, a matching journal resumes interrupted copying and the source remains untouched. After a separate-target migration succeeds, explicitly configure the daemon to use that target root.

If the report identifies an unsupported or ambiguous shape, do not point the new daemon at the old root. The daemon intentionally does not guess ownership or migrate unversioned schemas during startup.

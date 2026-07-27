# V2 storage migration

The daemon upgrades databases with a valid `schema_migrations` prefix in place. Migrations `000001` through `000004` remain immutable; `000005` through `000007` replace the business-v1 agent, loader, and event-link storage with native project agent, scheduler, and sandbox associations.

Stop the old daemon and back up its data root before upgrading. A normal versioned database containing only project-managed agents and schedulers can be opened directly by the new daemon.

Use the standalone copy migrator when the old root contains standalone agents or loaders, has no migration history but has a recognized legacy shape, or still needs legacy filesystem-layout conversion. Versioned sources must have a known exact migration prefix; a checksum mismatch is rejected rather than guessed:

```bash
agent-compose-migrate --source /old/data-root --target /new/data-root --dry-run
agent-compose-migrate --source /old/data-root --target /new/data-root --json
```

The source is opened read-only. The target must be new or contain the migrator's matching journal; there is no overwrite flag. The migrator fingerprints a consistent SQLite snapshot plus authoritative files, converts standalone and mixed agent/scheduler records into the `legacy-v1-default` project, and appends a new immutable revision when historical managed projections disagree with their recorded revision. It only fills a project run's scheduler-run link when persisted event/sandbox evidence identifies exactly one scheduler run; unresolved or ambiguous links remain `NULL` and are listed in the report.

Filesystem copying does not follow symlinks. Legacy `sessions` and `loaders` trees become `sandboxes` and `schedulers`, old loader directory IDs are mapped to scheduler IDs, and stored paths below the old data root are rewritten to the target. Conflicting old/new layouts fail instead of overwriting one another. A matching journal makes interrupted copies resumable, while any logical database or authoritative-file change invalidates the resume. The source remains untouched for rollback. After success, explicitly configure the daemon to use the target root.

If the report identifies an unsupported or ambiguous shape, do not point the new daemon at the old root. The daemon intentionally does not guess ownership or migrate unversioned schemas during startup.

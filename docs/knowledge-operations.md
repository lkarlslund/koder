# Knowledge operations

Koder stores Knowledge in its own Pebble database, separate from sessions, chats, Android
state, and the main Koder store. Back up and restore it as an independent unit.

## Create a backup

Stop the Koder process that owns the selected data directory, then run:

```sh
systemctl --user stop koder.service
koder knowledge backup /path/to/backups/koder-knowledge-2026-08-22
systemctl --user start koder.service
```

Use `--data-dir` when backing up a non-default installation, and `--json` for automation:

```sh
koder --data-dir /srv/koder knowledge backup --json /srv/backups/knowledge-r1876
```

The destination must not already exist and must be outside the live Knowledge database.
The command opens an existing store only—it will not silently create an empty database
after a typo—creates a consistent Pebble checkpoint with a flushed write-ahead log, and
then validates its tables, Koder metadata, schema, index generation, and keyspace before
reporting success.

Protect the backup directory like the Koder data directory: it may contain personal
knowledge. Copying or deleting a Knowledge backup does not affect chat/session backups,
and a chat/session backup does not substitute for this command.

If the command reports that the database is locked, another Koder process is still using
that data directory. Stop that process; do not copy the live Pebble files manually.

## Restore a backup

Restore is deliberately offline and requires explicit confirmation:

```sh
systemctl --user stop koder.service
koder knowledge restore --confirm /path/to/backups/koder-knowledge-2026-08-22
systemctl --user start koder.service
```

Use the same `--data-dir` that owns the database being replaced. Add `--json` when an
operator or automation needs the source, live, and rollback paths as structured output.

Before changing anything, the command validates the backup and acquires the live Pebble
lock. It refuses to continue if Koder or another process owns the database. The backup is
copied into a sibling staging directory, validated again, and only then swapped into the
live path. The restored database is validated after the swap; a post-swap failure causes
the previous database to be put back automatically.

On success, the prior database is retained beside the live database with a timestamped
`.rollback-...` suffix, and its exact path is printed. Keep it until the restored Knowledge
has been checked in Koder. To roll back, stop Koder and restore that retained path through
the same validated command:

```sh
systemctl --user stop koder.service
koder knowledge restore --confirm /path/printed/by/the/previous/restore
systemctl --user start koder.service
```

Do not rename Pebble directories manually. A successful rollback also retains the database
it replaced, so both directions remain recoverable until an operator deliberately removes
old checkpoints.

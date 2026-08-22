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

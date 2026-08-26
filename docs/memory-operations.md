# Memory operations

Koder stores Memory in its own Pebble database, separate from sessions, chats, Android
state, and the main Koder store. Back up and restore it as an independent unit.

## Create a backup

Stop the Koder process that owns the selected data directory, then run:

```sh
systemctl --user stop koder.service
koder memory backup /path/to/backups/koder-memory-2026-08-22
systemctl --user start koder.service
```

Use `--data-dir` when backing up a non-default installation, and `--json` for automation:

```sh
koder --data-dir /srv/koder memory backup --json /srv/backups/memory-r1876
```

The destination must not already exist and must be outside the live Memory database.
The command opens an existing store only—it will not silently create an empty database
after a typo—creates a consistent Pebble checkpoint with a flushed write-ahead log, and
then validates its tables, Koder metadata, schema, index generation, and keyspace before
reporting success.

Protect the backup directory like the Koder data directory: it may contain personal
memory. Copying or deleting a Memory backup does not affect chat/session backups,
and a chat/session backup does not substitute for this command.

If the command reports that the database is locked, another Koder process is still using
that data directory. Stop that process; do not copy the live Pebble files manually.

## Restore a backup

Restore is deliberately offline and requires explicit confirmation:

```sh
systemctl --user stop koder.service
koder memory restore --confirm /path/to/backups/koder-memory-2026-08-22
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
`.rollback-...` suffix, and its exact path is printed. Keep it until the restored Memory
has been checked in Koder. To roll back, stop Koder and restore that retained path through
the same validated command:

```sh
systemctl --user stop koder.service
koder memory restore --confirm /path/printed/by/the/previous/restore
systemctl --user start koder.service
```

Do not rename Pebble directories manually. A successful rollback also retains the database
it replaced, so both directions remain recoverable until an operator deliberately removes
old checkpoints.

## Migrate between storage backends

Backups preserve Pebble exactly. Migration archives instead contain backend-neutral
canonical records, complete chunk/entry/link revision histories, and package assets. Use a
migration archive when moving Memory into a new data directory or a future replacement
backend.

Export is offline and refuses to overwrite an existing archive. Koder installations contain
the private `About me` chunk by default, so a complete export normally requires the explicit
personal-data acknowledgement:

```sh
systemctl --user stop koder.service
koder memory migrate export --include-personal /srv/backups/memory-migration.gz
systemctl --user start koder.service
```

The output includes a SHA-256 digest, compressed byte size, and content-free counts. Store
the archive as private data. `--json` provides the same result for automation.

Import requires a genuinely empty target Memory store and explicit confirmation. It
validates the gzip/JSON envelope, exact schema, every canonical record, complete contiguous
revision histories, asset digests, references, dependencies, and derived counts before one
atomic transaction makes anything visible:

```sh
koder --data-dir /srv/koder-new memory migrate import \
  --confirm --include-personal /srv/backups/memory-migration.gz
```

The target remains unchanged if validation or any write fails. Import refuses a non-empty
target instead of merging or replacing data; use package import for deliberate content-level
merges and the backup/restore commands for exact Pebble recovery.

Migration excludes backend metadata and derived indexes because the target rebuilds those
from canonical truth. It also excludes transient operation state, retrieval-usage signals,
and saved graph-explorer layouts. Those are not canonical Memory and must never make a
replacement backend understand Pebble-private records.

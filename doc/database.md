# Metadata database, backups and export

All metadata lives in SQLite, and it is the precious part of the system.
Play counts and ratings change constantly, which suits a database better than
jiggle's git repository of TOML files.

## JMAP state

- **State comes from a modseq:** a global counter, with a `modseq` column on
  every row bumped on write.  `/changes` is then a query for rows above the
  client's modseq.
- **Tombstones** for destroyed objects, so deletions still appear in
  `/changes`.
- **Transactions** keep a metadata edit and its state bump atomic.

## Backups

- **History is snapshots,** taken with `VACUUM INTO` or the online backup API
  (`.backup`).  Never `cp` the live file, which can capture an inconsistent
  state in WAL mode.
- **A backup is** the opaque-named originals plus database snapshots.

## Export

Export is an escape hatch: a one-way, full, non-incremental export to a
human-readable tree of tagged files.  It need not be space-efficient, but it
must work, so test it periodically.

A collection-wide "reorganize" job was considered and rejected as overkill.

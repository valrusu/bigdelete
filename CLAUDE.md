# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`bigdelete` is a Go CLI for mass-deleting rows from an Oracle table fully online. ROWIDs to delete are piped to stdin; the tool fans them out to N parallel sessions, each batching ROWIDs through a temp table (`bigdeletetemp`) and committing every `-commit` rows. See README.md for the rationale and version history.

## Build

```bash
cd cmd && ./build_bigdelete.sh
# or directly:
cd cmd && go build -o bigdelete bigdelete.go
```

- The final binary must be built on Linux because of the `godror` (Oracle, CGO-based) dependency. On Windows the build script only does a compile check and deletes the .exe.
- There are no tests in this repository.

## Usage

**Legacy single-table mode** (ROWIDs piped from stdin):
```bash
<rowid-source> | ./bigdelete -connect <conn-string> -table <target-table> [-threads 20] [-commit 1237] [-group 1] [-tnsadmin <dir>] [-prerun <script>] [-postrun <script>] [-debug]
```

**New config-file mode** (multiple tables with automatic ROWID extraction):
```bash
./bigdelete -connect <conn-string> [-configfile bigdelete.json] [-parallel 2] [-extractonly] [-tnsadmin <dir>] [-prerun <script>] [-postrun <script>] [-debug]
```

### Pre/post run scripts
- `-prerun <script>`: executed (directly, no shell, no args) before the run; non-zero exit aborts with code 26.
- `-postrun <script>`: executed after the run, whether it succeeded or failed (but not if a delete worker `os.Exit`ed). Gets the run's exit code in env `BIGDELETE_EXITCODE`. If the run succeeded and the script fails, exit code is 27; if the run failed, its own code wins.
- Scripts get stdout/stderr but not stdin (stdin carries ROWIDs in `-table` mode). Neither runs with `-checkconfig`.

The `-table` and `-configfile` flags are mutually exclusive. If neither is passed, `-configfile` defaults to `bigdelete.json`.

### Operating modes
- **Normal** (default): extract ROWIDs, then run deletes.
- **Extract-only** (`-extractonly` CLI flag): extract ROWIDs to files, do not delete. Useful to pre-generate rowid files.
- **Delete-only** (`"deleteOnly": true` in config): skip extraction, run deletes from existing rowid files. Useful to reuse pre-extracted files.

### Config file format
```json
{
  "parallel": 2,
  "deleteRowidFiles": "/tmp/myrowids",
  "cleanupRowidFiles": false,
  "tables": [
    {
      "table": "SCHEMA.TABLE1",
      "sql": "select rowid from schema.table1 where ... and rownum <= 100",
      "threads": 20,
      "commit": 1237,
      "group": 50,
      "sql-pre": "alter trigger schema.table1_trg disable",
      "sql-post": ["alter trigger schema.table1_trg enable", "alter index schema.idx1 coalesce"],
      "enabled": true
    },
    {
      "table": "SCHEMA.TABLE2",
      "sql": "select rowid from schema.table2 where ...",
      "threads": 10,
      "commit": 500,
      "enabled": false
    }
  ]
}
```
**Config file settings:**
- `parallel` (optional, default 1): number of tables to process concurrently. The CLI flag `-parallel` overrides this.
- `deleteRowidFiles` (optional, default `$TMPDIR` or `/tmp`): directory where to store extracted rowid files.
- `cleanupRowidFiles` (optional, default `false`): if `true`, delete rowid files after each table's delete run. If `false` (default), keep them.
- `deleteOnly` (optional, default `false`): if `true`, skip ROWID extraction; assume rowid files already exist and only run deletes. Incompatible with `-extractonly`.

**Per-table settings:**
- Each table's `threads`, `commit`, `group` are optional and default to 20, 1237, and 1 respectively.
- `enabled` (optional, default `true`): if `false`, skip processing this table.
- `sql-pre` (optional): a string or an array of strings, each one SQL statement or PL/SQL block, executed in order (stopping at the first failure) before the extract (in delete-only mode: before the delete). Failure skips the table, exit code 28.
- `sql-post` (optional): same format as `sql-pre`; executed after a successful delete; not run in extract-only mode. Failure exit code 29.
- Plain SQL must not end with `;` (ORA-00911); PL/SQL blocks (`begin ... end;`) must.

### Temp table prerequisite

Both modes require a temp table (or a synonym named `bigdeletetemp` pointing to one):

```sql
create global temporary table bigdeletetemp (rid rowid) on commit delete rows;
```

## Architecture

Three-file project; everything else under `vendor/` is dependencies.

- `bigdelete.go` — package `bigdelete` (module root), core delete engine. Config is package-level exported vars (`ConnStr`, `TableName`, `Numthreads`, `Rowidspercall`, `Groupsize`, `Tnsadmin`, `DebugFlag`) used by `BigDelete()` for legacy single-table mode. Key functions:
  - `connectDB() (*sql.DB, Progerr)` — opens Oracle connection, pings, runs optional debug query.
  - `BigDelete() Progerr` — legacy entry point; reads globals, uses `os.Stdin` as ROWID source.
  - `runDelete(db *sql.DB, tableName string, numthreads, rowidspercall, groupsize int, in io.Reader) Progerr` — parametrized delete engine used by both legacy and config-file modes; orchestrates concurrent delete goroutines via channel fan-out.
  - `piperowids`, `deletedata`, `countreader` — worker goroutines, now take explicit parameters instead of reading globals to support concurrent multi-table mode.
  - Errors return as `Progerr{Err, Msg, Code}`; worker goroutines call `os.Exit` directly on DB errors (known TODO).

- `configfile.go` — JSON config-file orchestration (new). Provides:
  - `RunConfigFile(configFile string, parallel int, extractOnly bool) Progerr` — loads JSON config, checks mutual exclusion of `-extractonly` and `deleteOnly` setting (error code 21), spawns bounded-concurrency worker pool to handle three operating modes.
  - `loadConfigFile` — parses JSON, validates required fields, applies defaults, creates `deleteRowidFiles` directory if missing (error code 22).
  - `runTablePool` — manages concurrent table processing via work queue; skips disabled tables; supports normal/extract-only/delete-only modes.
  - `runOneTable` — orchestrates per-table work: extraction (normal/extract-only modes) or file lookup (delete-only mode), optional delete phase.
  - `extractRowids` — runs per-table ROWID SQL, writes to rowid file in configured directory (error codes 13-19).
  - `findRowidFile` — locates existing rowid file for delete-only mode; errors if none or multiple found (error codes 23-25).

- `cmd/bigdelete.go` — package `main`; parses flags and dispatches to legacy `BigDelete()` or new `RunConfigFile()` based on which of `-table` or `-configfile` was explicitly passed (mutually exclusive).

## Concurrency model

**Per-table delete phase** (inside `runDelete`):
- `piperowids` goroutine reads from an `io.Reader` (stdin for legacy mode, a temp file for config-file mode) line-by-line and sends `[]string` batches of `groupsize` sequential ROWIDs into channel `chdata`, so ROWIDs from the same block are deleted by the same session (avoids block contention between sessions).
- `numthreads` × `deletedata` goroutines each hold prepared statements and a transaction: insert ROWIDs into `bigdeletetemp`, then `delete from <table> where rowid in (select rid from bigdeletetemp)` and commit every `rowidspercall` rows. The temp table exists so only one delete SQL is ever hard-parsed.
- `countreader` goroutine aggregates per-batch `count` structs from `chcnt` and logs progress; final totals go to stdout.

**Config-file mode overall** (inside `runTablePool`):
- A bounded-concurrency worker pool (size = `-parallel`) pulls table entries off a work queue.
- Each worker runs extraction (ROWID SQL query → temp file) then spawns the per-table delete phase in parallel.
- A buffered error channel collects per-table failures without blocking other workers; all tables' extractions are attempted even if some fail.

## Exit codes

**Connection/setup (1-3):** connect, ping, debug-query failures.
**Config errors (4-5, 9, 20-22):** file open, JSON parse, invalid entry, flag mutual exclusion, rowid directory create.
**Extraction errors (13, 16-19, 23-25):** query, scan, file I/O, read directory, file not found, multiple files found.
**Delete errors (6-8, 10-15):** prepare stmt, transaction, delete, commit (worker goroutine exits).
**Mode conflicts (21):** `-extractonly` with `deleteOnly` config setting.
**Pre/post hooks (26-29):** `-prerun` script, `-postrun` script, per-table `sql-pre`, per-table `sql-post`.

## Error handling and documented limitation

In **config-file mode** with `-parallel > 1`:
- Extraction-phase errors (bad SQL, file I/O) are caught gracefully and reported per-table via `Progerr`; sibling tables in the worker pool continue processing.
- Delete-phase errors inside `deletedata` goroutines still call `os.Exit` directly (pre-existing behavior, not changed here). This means **one table's fatal delete error kills the entire process immediately**, aborting other tables' in-flight delete runs. This is a documented limitation to track separately in `todo.txt`.
- In delete-only mode, if a rowid file is not found or multiple matches exist, that table's error is logged but doesn't abort the pool; other tables continue.

## Vendored godror patch

`conn.go.patch` (repo root) records a local modification to `vendor/github.com/godror/godror/conn.go` (`initTZ`: unconditionally set `c.tzValid = true` to skip the timezone check). The vendor tree may not currently have it applied, and `go mod vendor` will wipe it — check/re-apply the patch if touching dependencies or hitting timezone-related connection issues.

## Known TODOs (todo.txt)

- Fail gracefully when a delete fails (e.g. FK constraint) instead of crashing.
- Finish `-tnsadmin` handling (full path or executable's directory; integrate with dataarchive).

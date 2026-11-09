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

`./bigdelete -exampleconfig` prints an example config file and exits. It prints `bigdelete.json.example`, which is embedded into the binary with `go:embed`, so keep that file up to date and valid (check it with `-checkconfig`).

The `-table` and `-configfile` flags are mutually exclusive. If neither is passed, `-configfile` defaults to `bigdelete.json`.

### Operating modes
- **Normal** (default): extract ROWIDs, then run deletes.
- **Extract-only** (`-extractonly` CLI flag, or `"extractOnly": true` in config; either one turns it on): extract ROWIDs to files, do not delete. Useful to pre-generate rowid files.
- **Delete-only** (`"deleteOnly": true` in config): skip extraction, run deletes from existing rowid files. Useful to reuse pre-extracted files.

### Config file format
```json
{
  "parallelExtract": 2,
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
- `parallelExtract` (optional, default `parallel`): number of tables whose ROWID extract runs at the same time. All extracts finish before any delete starts; deletes then run one table at a time in config order, each with its own `threads`.
- `parallel` (optional, default 1): default for `parallelExtract`. The CLI flag `-parallel` overrides `parallelExtract`.
- `connectString` (optional): used only when `-connect` is not given; `-connect` always wins.
- `deleteRowidFiles` (optional, default `$TMPDIR` or `/tmp`): directory where to store extracted rowid files. Each table has one file with a fixed name, `bigdelete-<DB>-<SCHEMA.TABLE>.rowids`, where `<DB>` is `db_unique_name` (plus `_<con_name>` when connected to a PDB) and an unqualified table name is prefixed with `current_schema`, so runs against different databases do not share files. A new extract replaces the file (written to a `.tmp` file, then renamed); delete-only mode reads it. An enabled table may appear only once in a config (exit code 9).
- `cleanupRowidFiles` (optional, default `false`): if `true`, delete rowid files after each table's delete run. If `false` (default), keep them.
- `purgeRowidFilesOlderThan` (optional, default: never): age in days and/or minutes (`5d`, `78m`, `6d3m`). Before extraction, delete every regular `bigdelete-*.rowids` file in `deleteRowidFiles` older than this (by modification time), for any table, including tables not in this config. Ignored in delete-only mode, because those files are the input. An invalid or zero value is exit code 9.
- `extractOnly` (optional, default `false`): same as the `-extractonly` flag.
- `deleteOnly` (optional, default `false`): if `true`, skip ROWID extraction; assume rowid files already exist and only run deletes. Incompatible with `-extractonly` / `extractOnly` (exit code 21; the config-only conflict is caught by `-checkconfig` too).

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
  - `runTablePool` — select-stage and delete-stage worker pools (see Concurrency model); skips disabled tables; supports normal/extract-only/delete-only modes.
  - `extractTable` — per-table extract phase: `sql-pre`, then extraction (or rowid file check in delete-only mode).
  - `deleteTable` — per-table delete: `runDelete`, then rowid file cleanup on success (`sql-post` is run by `runTablePool`).
  - `getDBIdent` / `dbIdent.rowidFileName` — ask the DB for `db_unique_name`, `con_name`, `current_schema` and build each table's fixed rowid file name.
  - `extractRowids` — runs per-table ROWID SQL, writes to a temp file and renames it to the table's rowid file (error codes 13-19).
  - `checkRowidFile` — checks the table's rowid file exists for delete-only mode (error codes 23-24).

- `cmd/bigdelete.go` — package `main`; parses flags and dispatches to legacy `BigDelete()` or new `RunConfigFile()` based on which of `-table` or `-configfile` was explicitly passed (mutually exclusive).

## Concurrency model

**Per-table delete phase** (inside `runDelete`):
- `piperowids` goroutine reads from an `io.Reader` (stdin for legacy mode, a temp file for config-file mode) line-by-line and sends `[]string` batches of `groupsize` sequential ROWIDs into channel `chdata`, so ROWIDs from the same block are deleted by the same session (avoids block contention between sessions).
- `numthreads` × `deletedata` goroutines each hold prepared statements and a transaction: insert ROWIDs into `bigdeletetemp`, then `delete from <table> where rowid in (select rid from bigdeletetemp)` and commit every `rowidspercall` rows. The temp table exists so only one delete SQL is ever hard-parsed.
- `countreader` goroutine aggregates per-batch `count` structs from `chcnt` and logs progress; final totals go to stdout.

**Config-file mode overall** (inside `runTablePool`):
- Phase 1, extract: `parallelExtract` workers take enabled tables in config order and run `extractTable` (`sql-pre` + extraction; delete-only: just check the rowid file). Every table's extract is attempted even if some fail.
- Phase 2, delete: starts only after **all** extracts finished, and only if **all** succeeded (otherwise no deletes run). Tables are deleted **one at a time in config order** (`deleteTable` = `runDelete`, then `sql-post`), each with its own `threads` sessions. A failed delete (e.g. rowid file can't be opened, code 17) stops the run: later tables are not deleted and are listed in the log. A failed `sql-post` (code 29) is logged and the next table goes on; all failures are reported again at the end. With `cleanupRowidFiles`, a rowid file is removed only after its table's delete succeeded.
- There is no table-level delete parallelism for now (a `parallelDelete` setting was removed; to be reimplemented differently later).

## Exit codes

**Connection/setup (1-3):** connect, ping, debug-query failures.
**Config errors (4-5, 9, 20-22):** file open, JSON parse, invalid entry, flag mutual exclusion, rowid directory create.
**Extraction errors (13, 16-19, 23-24):** query, scan, file I/O, stat/read directory, file not found. (25, multiple files found, is no longer used.)
**Delete errors (6-8, 10-15):** prepare stmt, transaction, delete, commit (worker goroutine exits).
**Mode conflicts (21):** `-extractonly` with `deleteOnly` config setting.
**Pre/post hooks (26-29):** `-prerun` script, `-postrun` script, per-table `sql-pre`, per-table `sql-post`.

## Error handling and documented limitation

In **config-file mode**:
- Extraction-phase errors (bad SQL, file I/O, `sql-pre`, and in delete-only mode a missing rowid file) are caught and reported per table via `Progerr`; the other extracts continue, but then no deletes run.
- Delete-phase errors inside `deletedata` goroutines still call `os.Exit` directly (pre-existing behavior). This means **a fatal delete error kills the whole process immediately**, so later tables are not deleted and `-postrun` does not run. This is a documented limitation tracked in `todo.txt`.

## Vendored godror patch

`conn.go.patch` (repo root) records a local modification to `vendor/github.com/godror/godror/conn.go` (`initTZ`: unconditionally set `c.tzValid = true` to skip the timezone check). The vendor tree may not currently have it applied, and `go mod vendor` will wipe it — check/re-apply the patch if touching dependencies or hitting timezone-related connection issues.

## Known TODOs (todo.txt)

- Fail gracefully when a delete fails (e.g. FK constraint) instead of crashing.
- Finish `-tnsadmin` handling (full path or executable's directory; integrate with dataarchive).

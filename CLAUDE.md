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
<rowid-source> | ./bigdelete -connect <conn-string> -table <target-table> [-threads 20] [-commit 1237] [-group 1] [-tnsadmin <dir>] [-debug]
```

**New config-file mode** (multiple tables with automatic ROWID extraction):
```bash
./bigdelete -connect <conn-string> [-configfile bigdelete.json] [-parallel 2] [-tnsadmin <dir>] [-debug]
```

The `-table` and `-configfile` flags are mutually exclusive. If neither is passed, `-configfile` defaults to `bigdelete.json`.

### Config file format
```json
{
  "parallel": 2,
  "tables": [
    {
      "table": "SCHEMA.TABLE1",
      "sql": "select rowid from schema.table1 where ... and rownum <= 100",
      "threads": 20,
      "commit": 1237,
      "group": 50
    },
    {
      "table": "SCHEMA.TABLE2",
      "sql": "select rowid from schema.table2 where ...",
      "threads": 10,
      "commit": 500
    }
  ]
}
```
- `parallel` (optional, default 1): number of tables to process concurrently. The CLI flag `-parallel` overrides this.
- Each table's `threads`, `commit`, `group` are optional and default to 20, 1237, and 1 respectively.

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
  - `RunConfigFile(configFile string, parallel int) Progerr` — loads JSON config, spawns bounded-concurrency worker pool to run multiple tables' extractions+deletes in parallel.
  - `loadConfigFile` — parses JSON, validates required fields, applies defaults.
  - `runTablePool` — manages concurrent table processing via a work queue.
  - `runOneTable`, `extractRowids` — per-table extraction phase (query ROWID SQL, write to temp file) and temp-file cleanup (deletes file after delete run, unless `-debug` keeps it).

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

## Error handling and documented limitation

In **config-file mode** with `-parallel > 1`:
- Extraction-phase errors (bad SQL, file I/O) are caught gracefully and reported per-table via `Progerr`; sibling tables in the worker pool continue processing.
- Delete-phase errors inside `deletedata` goroutines still call `os.Exit` directly (pre-existing behavior, not changed here). This means **one table's fatal delete error kills the entire process immediately**, aborting other tables' in-flight delete runs. This is a documented limitation to track separately in `todo.txt`.

## Vendored godror patch

`conn.go.patch` (repo root) records a local modification to `vendor/github.com/godror/godror/conn.go` (`initTZ`: unconditionally set `c.tzValid = true` to skip the timezone check). The vendor tree may not currently have it applied, and `go mod vendor` will wipe it — check/re-apply the patch if touching dependencies or hitting timezone-related connection issues.

## Known TODOs (todo.txt)

- Fail gracefully when a delete fails (e.g. FK constraint) instead of crashing.
- Finish `-tnsadmin` handling (full path or executable's directory; integrate with dataarchive).

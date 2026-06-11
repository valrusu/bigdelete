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

```bash
<rowid-source> | ./bigdelete -connect <conn-string> -table <target-table> [-threads 20] [-commit 1237] [-group 1] [-tnsadmin <dir>] [-debug]
```

Requires a temp table (or a synonym named `bigdeletetemp` pointing to one):

```sql
create global temporary table bigdeletetemp (rid rowid) on commit delete rows;
```

## Architecture

Two-file project; everything else under `vendor/` is dependencies.

- `bigdelete.go` — package `bigdelete` (module root), all the logic. Config is package-level exported vars (`ConnStr`, `TableName`, `Numthreads`, `Rowidspercall`, `Groupsize`, `Tnsadmin`, `DebugFlag`) set by the CLI before calling `BigDelete()`. Errors return as `Progerr{Err, Msg, Code}` from `BigDelete()`, but worker goroutines still call `os.Exit` directly on DB errors (improving this is a known TODO).
- `cmd/bigdelete.go` — package `main`; parses flags into the package vars and calls `bigdelete.BigDelete()`.

Concurrency model inside `BigDelete()`:
- `piperowids` goroutine reads stdin line-by-line and sends `[]string` batches of `-group` sequential ROWIDs into channel `chdata`, so ROWIDs from the same block are deleted by the same session (avoids block contention between sessions).
- `Numthreads` × `deletedata` goroutines each hold prepared statements and a transaction: insert ROWIDs into `bigdeletetemp`, then `delete from <table> where rowid in (select rid from bigdeletetemp)` and commit every `Rowidspercall` rows. The temp table exists so only one delete SQL is ever hard-parsed.
- `countreader` goroutine aggregates per-batch `count` structs from `chcnt` and logs progress; final totals go to stdout.

## Vendored godror patch

`conn.go.patch` (repo root) records a local modification to `vendor/github.com/godror/godror/conn.go` (`initTZ`: unconditionally set `c.tzValid = true` to skip the timezone check). The vendor tree may not currently have it applied, and `go mod vendor` will wipe it — check/re-apply the patch if touching dependencies or hitting timezone-related connection issues.

## Known TODOs (todo.txt)

- Fail gracefully when a delete fails (e.g. FK constraint) instead of crashing.
- Finish `-tnsadmin` handling (full path or executable's directory; integrate with dataarchive).

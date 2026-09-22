# PI-05/A SQLite driver decision

Status: selected for the bounded migration adapter; backup implementation remains
in PI-05/B.

## Decision

Pin modernc.org/sqlite v1.59.0. It preserves the project's reproducible,
CGO-free Go build while exposing the SQLite controls required by ADR-0003.
The compared CGO alternative was github.com/mattn/go-sqlite3 v1.14.52.

| Criterion | modernc.org/sqlite v1.59.0 | mattn/go-sqlite3 v1.14.52 |
|---|---|---|
| Build model | Pure Go; selected adapter tests pass with CGO_ENABLED=0 | CGO package; its no-CGO file registers a stub that returns an error |
| License | BSD-3-Clause in the pinned module | MIT in the compared module |
| Local executable evidence | Linux amd64 tests pass; Windows amd64 and Linux arm64 test binaries cross-compile | Module source requires CGO and GCC; not admitted into the product graph |
| SQLite runtime | sqlite_version() reports 3.53.4 on Linux amd64 | Not executed because it would change the product build/toolchain contract |
| Connection controls | Connector DSN applies per-connection pragmas; adapter reads them back | DSN and connection hooks available, but require CGO |
| Backup API | Driver connection exposes NewBackup/NewRestore and incremental backup steps | SQLite connection exposes Backup and incremental steps |
| WAL/checkpoint | WAL is configured and verified; checkpoint remains a later explicit operation | SQLite API/PRAGMA available |
| Busy behavior | Per-connection busy_timeout is configured and verified; bounded application retry belongs to PI-05/B | Busy timeout options available |
| Foreign keys | foreign_keys=ON is applied through every connector-created connection and verified | DSN/hook configuration available |
| Supply chain | Larger generated Pure-Go dependency graph, pinned by go.mod/go.sum | Smaller Go graph plus compiler/system C toolchain variability |

The decision optimizes the already accepted topology rather than claiming that
the selected driver is universally superior. A future change requires repeating
the failure, cross-build, advisory and reproducibility evidence.

## Implemented invariants

- Only internal/adapters/sqlite imports database/sql and the driver.
- The pool has one open and idle connection, which is the PI-05/A serialized
  writer boundary. PI-05/B may add explicit bounded reader infrastructure.
- WAL, synchronous=FULL, foreign_keys=ON and the bounded busy timeout are read
  back before migrations run.
- The strict schema ledger records version, immutable ID, SHA-256 checksum,
  dirty state and application timestamp.
- Migrations are contiguous, ordered and forward-only.
- A dirty marker is committed before each migration transaction. A failed or
  interrupted migration therefore causes the next open to fail closed.
- Unknown newer versions and checksum drift fail closed.
- Reopening the same clean version is idempotent.

Rollback is not a down migration. PI-05/B must use the driver's backup interface
to produce and verify the pre-upgrade snapshot described in the PI plan.

## Evidence

The local evidence used Go 1.27.1 with isolated caches:

~~~text
go test ./backend/internal/adapters/sqlite -v
CGO_ENABLED=0 go test ./backend/internal/adapters/sqlite
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c ./backend/internal/adapters/sqlite
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c ./backend/internal/adapters/sqlite
~~~

The focused tests cover fresh creation, clean reopen, supported upgrade, dirty
state, failed migration recovery, newer schema, checksum drift, invalid version
gaps and connection pragma readback. Runtime metadata recorded SQLite 3.53.4,
THREADSAFE=1, DEFAULT_SYNCHRONOUS=2 and DEFAULT_WAL_SYNCHRONOUS=2 among the
reported compile options.

The race build was not executable locally because this host lacks a C compiler
and Go race instrumentation requires CGO. Remote CI/advisory and full
reproducibility evidence must therefore be recorded before closing issue #14.
No production database was opened or migrated.

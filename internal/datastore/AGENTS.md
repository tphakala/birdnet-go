# Datastore Guidelines

Applies to everything under `internal/datastore/` and to any code that persists
or reads detections, species data or other stored state. Read
`internal/AGENTS.md` first for the general Go rules.

## HARD RULE: the legacy database is deprecated, never expand it

The legacy database is going away and will be removed completely. No PR may add
to it, whatever the feature. This rule has no "just this once" exception; if a
change seems to need the legacy store, stop and ask the maintainer.

What counts as legacy:

| Legacy (frozen)                                                        | Where                                                                                   |
| ---------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| The pre-v2 schema (the `notes` table and the GORM models mapped to it) | `internal/datastore` (for example `Note` in `model.go`)                                 |
| The legacy store implementations                                       | `DataStore` (`interfaces.go`), `SQLiteStore` (`sqlite.go`), `MySQLStore` (`mysql.go`)   |
| The legacy and dual-write modes                                        | the migration mode handling in `internal/datastore/v2/` (`state_manager.go`, `mode.go`) |
| The store API shaped around the legacy schema                          | `datastore.Interface` and the `c.DS` accessor in API code                               |

What a PR must not do:

- Add a column, table, model, field or index that exists only in the legacy
  schema, or a field on the legacy models whose value is stored only there.
- Add a method to `datastore.Interface` or to a legacy store, add an optional
  interface that is satisfied through a type assertion on them, or add mock
  methods for them.
- Add a dual-write path, a legacy-only branch, a shim, an adapter or a fallback
  that keeps a new feature working on the legacy store. A new feature simply
  does not exist on the legacy store.
- Add new callers of `c.DS` or `datastore.Interface` for persistence, or
  copy the pattern of an existing caller. The existing callers are debt to be
  removed, not precedent.

What is still allowed:

- Bug fixes in the legacy code that keep its current behavior.
- Changes that remove legacy code, or move a caller from `c.DS` /
  `datastore.Interface` to the repositories (one PR per domain is welcome).
- The migration code in `internal/datastore/v2/migration`, which reads the legacy
  database into v2, and the compatibility that `internal/datastore/v2only` keeps
  so existing callers of `datastore.Interface` still run on a v2 database.
  `v2only` is not a place to add new `Interface` methods either.

Reviewers: reject any PR that adds behavior to the files listed above. A change
to `model.go`, `interfaces.go` (the `Interface` or `DataStore` definitions),
`sqlite.go` or `mysql.go` that adds anything other than a
removal or a behavior-preserving fix needs the maintainer's explicit approval.

## HARD RULE: reconcile safely on startup, never migrate data on your own

Two rules govern what the code may do to a database it finds at startup or at
runtime. They apply to SQLite and to MySQL/MariaDB alike.

**1. Always try to reconcile database problems, when it is safe.** A database
can be left inconsistent by a failed setup, a crash, a downgrade or a bug in an
older release: leftover empty tables, a half-finished schema, a stale index, a
v2-shaped table under the legacy store, an orphaned sidecar file. The code should
repair that itself and carry on, not refuse to start and not ask the user to run
SQL. Reconciling is safe only when all of these hold:

- No row of user data is lost, overwritten or hidden. Never drop, truncate or
  delete a table or file that holds rows, and never drop a table on the strength
  of a guess about which side is "the real one". Check that a table is empty
  immediately before dropping it. A count or listing that fails is treated as
  non-empty (fail closed).
- The result is idempotent: the same state leads to the same decision on every
  boot, with no flapping and nothing recreated that the next boot has to fix.
- The action is visible: log one clear line saying what was found (with row
  counts), what was chosen and what was dropped, if anything. Use the typed
  logger helpers and include no personal data.
- Prefer choosing a side and leaving the other untouched over dropping it.

Refuse to start, with an error that names the cause, only when no safe automatic
choice exists, for example when both candidate sides hold data. Never destroy
data to avoid that refusal.

**2. Never move data between stores or schemas without the user starting it.**
A data migration reads user data from one place and writes it to another
(SQLite to MySQL, legacy schema to v2, copying or consolidating databases, any
rewrite or re-keying that discards rows). It must never run at startup, in a
background job, on a timer, or as a side effect of a settings change or an
upgrade. It runs only when the user explicitly starts it, from System > Database
or a command, after seeing what will be copied. Startup may detect that a
migration is possible and show a notice; it must not begin one.

The line between the two: reconciling restores a consistent state without moving
user data between stores, so the code does it by itself. Migrating moves or
transforms user data, so only the user starts it. Creating the schema on an empty
database, and additive schema evolution for the running version (new nullable
columns, new indexes, `AutoMigrate` as described below), are neither. Switching
the configured backend to an empty database starts empty; say so visibly instead
of copying anything.

Tests for startup and repair code run against a real database (SQLite in a temp
directory, MySQL and MariaDB through the testcontainer suites), assert that no
row is ever dropped, and boot twice to assert the same result both times.

## How to add persistence (v2 only)

1. Add the column or entity to the v2 schema in `internal/datastore/v2/entities`.
   Additive nullable columns are added by the schema migration at startup
   (`AutoMigrate` in `internal/datastore/v2/manager.go` and
   `mysql_manager.go`); never require a startup scan or backfill of existing
   rows, because that can stall slow systems on startup. Fill old rows lazily on
   first use instead.
2. Add the operation to the repository for that domain
   (`datastore.DetectionRepository` for detections, or the matching v2
   repository), with `context.Context` as the first parameter. Implement it in
   `internal/datastore/v2/repository`. Callers use the repository, never a
   concrete store.
3. Carry the value on the domain model (for example `detection.Result`) and map
   it in the repository conversion code. Do not add it to the legacy `Note` model
   except as a `gorm:"-"` runtime-only field when an unmigrated caller must
   still read it, and say so in a comment.
4. Write tests against the real v2 repository (SQLite in a temp directory); MySQL
   behavior goes through the existing testcontainer suites. Use testify and read
   `TESTING.md`.
5. If an interface changed, regenerate the mocks with `go generate
./internal/datastore`. Never edit generated mocks by hand.

## Rules that apply to all datastore code

- Pass `context.Context` first through every call chain that reaches the
  database, and honor its cancellation and deadlines for writes as well as reads.
- Guard conditional writes in the SQL (for example `WHERE col IS NULL`), not with
  a read followed by a write.
- Keep detection writes idempotent and safe to retry, and never overwrite a
  stored value with an unknown (NULL) one when a sync or migration has no value.
- Do not log secrets or personal data; use the typed helpers in
  `internal/logger`.

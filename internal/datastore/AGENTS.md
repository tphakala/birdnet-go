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

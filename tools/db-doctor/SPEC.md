# BirdNET-Go Database Doctor - Specification

## Purpose

A standalone Python diagnostic and repair tool for BirdNET-Go SQLite databases. Users run it
outside the application to diagnose and fix schema problems that prevent startup after
upgrading between nightly builds.

The script targets end users running BirdNET-Go on Raspberry Pi, NUC, or Docker. It must work
with Python 3.9+ (Debian Bookworm ships 3.11) and have zero third-party dependencies.

## Problem Scope

Database issues fall into five categories, identified from GitHub issues and discussions:

### 1. Schema contamination (Discussion #3210, Issue #3165)

When a v2 database is opened by a legacy-fallback code path (caused by the PR #2165 table
rename mismatch), GORM's AutoMigrate adds columns from the v1 model definitions to v2 tables.
The three affected tables and their contamination columns:

| Table                    | Contamination column(s)                           | V2 correct key          |
|--------------------------|---------------------------------------------------|-------------------------|
| `image_caches`           | `scientific_name` (NOT NULL, no default)          | `label_id` (FK)         |
| `dynamic_thresholds`     | `species_name`, `model_name`, `scientific_name`   | `label_id` (FK, unique) |
| `notification_histories` | `scientific_name`                                 | `label_id` (FK)         |

The contamination columns are NOT NULL with no default, so INSERT fails after the schema is
corrupted. The in-app self-healing code (manager.go `cleanupLegacySchemaContamination`) can
only fix `image_caches`, and only when SQLite >= 3.35.0 or the table is empty. When the table
has data and DROP COLUMN is unavailable, startup fails with `ErrV2SchemaCorrupted`.

### 2. Orphaned label references (Issue #3211)

After upgrade, historical detections exist in the `detections` table but their `label_id`
values don't match any row in the `labels` table. This happens when:
- The model identity (name/version/variant) changed between builds
- Labels were re-seeded with different IDs
- A migration ran partially

Result: detections are invisible in the dashboard (JOIN on labels returns nothing).

### 3. Corrupted indexes (Issue #2461)

SD card corruption or unclean shutdown causes SQLite index trees to become inconsistent with
the underlying table data. `PRAGMA integrity_check` reports errors like:
`wrong # of entries in index idx_notes_date_commonname_confidence`

Fix: `REINDEX` rebuilds all indexes from table data. Harmless on healthy databases.

### 4. Stale migration state (Issues #3165, #2629, #2672)

The `migration_states` table can get stuck in an intermediate state (e.g., `migrating`,
`dual_write`, `failed`) after a crash, preventing the application from starting or showing
migration as incomplete when the data is actually fully migrated. Scenarios:
- Power loss during migration
- OOM kill during dual-write phase
- Application killed during validation

### 5. Stripped clip extensions (Issue #2810)

Audio clip paths stored in the database have trailing dots instead of file extensions (e.g.,
`species_name_87p_20260418T152258Z.` instead of `species_name_87p_20260418T152258Z.m4a`).
The physical files exist with correct extensions on disk. This is a data-level bug, not a
schema bug, but the tool should detect it since it's a common complaint.

### 6. Missing foreign key targets (general integrity)

V2 tables use foreign keys to `labels`, `ai_models`, `label_types`, and `taxonomic_classes`.
If lookup tables are missing rows that other tables reference, queries silently drop data
(inner JOIN behavior) or inserts fail with FK constraint violations.

## Non-goals

- MySQL support (different API surface, different failure modes, different tooling)
- Modifying the application code or config files
- Performing the v1-to-v2 migration itself
- Handling encrypted or WAL-locked databases (user must stop the app first)

## User Interface

### Invocation

```bash
# Diagnose only (default, read-only)
python3 db-doctor.py /path/to/birdnet.db

# Diagnose and fix
python3 db-doctor.py /path/to/birdnet.db --fix

# Verbose output (show SQL queries)
python3 db-doctor.py /path/to/birdnet.db --verbose

# Fix specific issues only (values: schema, indexes, migration, clips, labels)
python3 db-doctor.py /path/to/birdnet.db --fix --only schema,indexes

# Scan a clips directory to detect extension mismatches
python3 db-doctor.py /path/to/birdnet.db --clips-dir /path/to/clips

# Skip backup (for testing or when disk is nearly full)
python3 db-doctor.py /path/to/birdnet.db --fix --no-backup

# JSON output for machine consumption
python3 db-doctor.py /path/to/birdnet.db --json
```

### Exit Codes

| Code | Meaning                                        |
|------|------------------------------------------------|
| 0    | All checks passed (or all fixes applied)       |
| 1    | Issues found (diagnose mode) or fix failed     |
| 2    | Usage error (bad arguments, file not found)    |
| 3    | Database is locked (application still running) |

### Output Format

Human-readable by default. Each check prints a status line:

```text
BirdNET-Go Database Doctor v1.0

Database: /home/birder/birdnet-go-app/data/birdnet.db
Size: 105.2 MB
SQLite version: 3.40.1
Schema: v2 (migration status: completed)

Checks:
  [PASS] SQLite integrity check
  [FAIL] Schema contamination
         image_caches: unexpected column 'scientific_name' (152 rows)
         dynamic_thresholds: unexpected column 'species_name' (47 rows)
  [WARN] Orphaned detections
         1,247 detections reference non-existent labels
  [PASS] Foreign key integrity
  [PASS] Index consistency
  [WARN] Clip path extensions
         3,891 clip paths end with '.' (missing extension)
  [PASS] Migration state

Summary: 1 failure, 2 warnings, 4 passed
Run with --fix to repair issues.
```

With `--json`, output is a JSON object with the same structure for scripting.

## Diagnostic Checks

All checks run in read-only mode by default (database opened with `?mode=ro`).

### Check 1: Database accessibility

- File exists and is readable
- Not currently locked (try `PRAGMA journal_mode` - will fail if WAL lock held)
- Valid SQLite file (check magic bytes: first 16 bytes)
- Report file size, SQLite version, page size, page count, freelist count

### Check 2: Schema version detection

Determine whether the database is:
- **Legacy v1**: Has `notes` table, no `detections` table, no `migration_states` table
- **V2 (completed)**: Has `detections` table and `migration_states` with state=completed
- **V2 (in-progress)**: Has `migration_states` with active state
- **Mixed/unknown**: Some combination that doesn't match expected patterns

Report the detected schema version, migration status, and any anomalies.

### Check 3: Schema contamination scan

For each V2 table, compare actual columns (from `pragma_table_info`) against the expected
column set derived from the Go entity definitions. Report:
- Extra columns (contamination from legacy AutoMigrate)
- Missing columns (incomplete migration or manual tampering)
- Type mismatches (column exists but with wrong type/constraints)

Expected column definitions (from entity structs):

**image_caches (V2)**:
id, provider_name, label_id, source_provider, url, license_name, license_url,
author_name, author_url, cached_at, created_at, updated_at

**dynamic_thresholds (V2)**:
id, label_id, level, current_value, base_threshold, high_conf_count, valid_hours,
expires_at, last_triggered, first_created, trigger_count, created_at, updated_at

**notification_histories (V2)**:
id, label_id, notification_type, last_sent, expires_at, created_at, updated_at

**threshold_events (V2)**:
id, label_id, previous_level, new_level, previous_value, new_value, change_reason,
confidence, created_at

**detections**:
id, model_id, label_id, source_id, detected_at, begin_time, end_time, confidence,
latitude, longitude, clip_name, processing_time_ms, unlikely, legacy_id

**labels**:
id, scientific_name, model_id, label_type_id, taxonomic_class_id, created_at

**ai_models**:
id, name, version, variant, model_type, classifier_path, created_at

**detection_predictions**:
id, detection_id, label_id, confidence, rank

**detection_reviews**:
id, detection_id, verified, created_at, updated_at

**detection_comments**:
id, detection_id, entry, created_at, updated_at

**detection_locks**:
id, detection_id, locked_at

**audio_sources**:
id, source_uri, node_name, source_type, display_name, config_json, created_at

**label_types**:
id, name

**taxonomic_classes**:
id, name

**daily_events**:
id, date, sunrise, sunset, country, city_name, moon_phase, moon_illumination

**hourly_weathers**:
id, daily_events_id, time, temperature, feels_like, temp_min, temp_max, pressure,
humidity, visibility, wind_speed, wind_deg, wind_gust, clouds, weather_main,
weather_desc, weather_icon, created_at

**alert_rules**:
id, name, description, name_key, description_key, enabled, built_in, object_type,
trigger_type, event_name, metric_name, cooldown_sec, escalation_steps, created_at,
updated_at

**alert_conditions**:
id, rule_id, property, operator, value, duration_sec, sort_order

**alert_actions**:
id, rule_id, target, template_title, template_message, sort_order

**alert_histories**:
id, rule_id, fired_at, event_data, actions, created_at

**migration_states**:
id, state, current_phase, phase_number, total_phases, started_at, phase_started_at,
completed_at, last_migrated_id, total_records, migrated_records, error_message,
related_data_error, updated_at

**migration_dirty_ids**:
detection_id, created_at

**detection_model_contributions**:
id, detection_id, model_id, hit_count, max_confidence

**app_metadata**:
key, value

Also check legacy tables if present (for mixed/migration databases):

**notes** (legacy v1):
id, source_node, date, time, begin_time, end_time, species_code, scientific_name,
common_name, confidence, latitude, longitude, threshold, sensitivity, clip_name,
processing_time, unlikely

**results** (legacy v1):
id, note_id, species, confidence

**note_reviews** (legacy v1):
id, note_id, verified, created_at, updated_at

**note_comments** (legacy v1):
id, note_id, entry, created_at, updated_at

**note_locks** (legacy v1):
id, note_id, locked_at

Note: The `migration_states` table may also exist under the pre-PR #2165 name
`migration_state` (singular). The script must check both names. Similarly,
`alert_histories` may exist as `alert_history` (singular).

### Check 4: SQLite integrity

Run `PRAGMA quick_check` (faster than full `integrity_check`). Report any errors.
If `--verbose`, also run `PRAGMA integrity_check` for full index validation.

### Check 5: Foreign key integrity

Run `PRAGMA foreign_key_check` to find rows that reference non-existent parent rows.
Also run targeted queries:
- `SELECT COUNT(*) FROM detections WHERE label_id NOT IN (SELECT id FROM labels)`
- `SELECT COUNT(*) FROM detections WHERE model_id NOT IN (SELECT id FROM ai_models)`
- `SELECT COUNT(*) FROM image_caches WHERE label_id NOT IN (SELECT id FROM labels)`
- `SELECT COUNT(*) FROM dynamic_thresholds WHERE label_id NOT IN (SELECT id FROM labels)`
- `SELECT COUNT(*) FROM notification_histories WHERE label_id NOT IN (SELECT id FROM labels)`
- `SELECT COUNT(*) FROM threshold_events WHERE label_id NOT IN (SELECT id FROM labels)`
- `SELECT COUNT(*) FROM detection_predictions WHERE detection_id NOT IN (SELECT id FROM detections)`
- `SELECT COUNT(*) FROM detection_reviews WHERE detection_id NOT IN (SELECT id FROM detections)`

### Check 6: Migration state consistency

If `migration_states` table exists:
- Check the state value is one of the known states
- If state is `completed`, verify that the `detections` table exists; a completed
  marker with the `detections` table missing entirely is the GitHub #3575 wedge and
  is flagged fixable (treated like an empty table). A healthy fresh install has the
  `detections` table present (even with zero rows), so an empty-but-present table is
  not flagged on its own.
- If state is stuck in an active state (`migrating`, `dual_write`, etc.), report it
- Check for stale `migration_dirty_ids` entries
- If both legacy `notes` and v2 `detections` tables exist, compare record counts
  and report if detections are significantly fewer (suggests incomplete migration)

### Check 7: Clip path analysis

If `--clips-dir` is provided:
- Query clip paths from `detections` (v2) or `notes` (v1)
- Check for paths ending in `.` (stripped extension)
- Cross-reference against actual files on disk when clips directory is provided
- Report count of mismatches and suggest the likely correct extension

### Check 8: Table statistics

Report row counts for all tables (informational, always runs):
- Detection/note count
- Species count (distinct labels or scientific names)
- Date range of detections
- Image cache entries
- Dynamic threshold entries
- Alert rules count

## Repair Operations

When `--fix` is specified, repairs run after diagnostics. Each repair is idempotent.

### Fix 1: Automatic backup

Before any write operation:
1. Check available disk space (need at least 1.1x the database size)
2. Copy the database file to `{filename}.{timestamp}.doctor-backup`
3. Also copy WAL and SHM files if they exist
4. Verify the backup by opening it read-only and running `PRAGMA quick_check`

Skip with `--no-backup` flag.

### Fix 2: Schema contamination repair

For each table with extra columns, use the SQLite table-recreation algorithm:

```sql
-- 1. Begin transaction
BEGIN IMMEDIATE;

-- 2. Create new table with correct schema
CREATE TABLE image_caches_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_name TEXT NOT NULL DEFAULT 'wikimedia',
    label_id INTEGER NOT NULL,
    source_provider TEXT NOT NULL DEFAULT 'wikimedia',
    url TEXT,
    license_name TEXT,
    license_url TEXT,
    author_name TEXT,
    author_url TEXT,
    cached_at DATETIME,
    created_at DATETIME,
    updated_at DATETIME
);

-- 3. Copy data (only columns that exist in both old and new schema)
INSERT INTO image_caches_new (id, provider_name, label_id, source_provider,
    url, license_name, license_url, author_name, author_url, cached_at,
    created_at, updated_at)
SELECT id, provider_name, label_id, source_provider,
    url, license_name, license_url, author_name, author_url, cached_at,
    created_at, updated_at
FROM image_caches;

-- 4. Drop old table
DROP TABLE image_caches;

-- 5. Rename new table
ALTER TABLE image_caches_new RENAME TO image_caches;

-- 6. Recreate indexes
CREATE UNIQUE INDEX idx_image_cache_provider_label
    ON image_caches (provider_name, label_id);
CREATE INDEX idx_image_caches_cached_at ON image_caches (cached_at);

COMMIT;
```

This approach works on all SQLite versions (no DROP COLUMN needed) and preserves data.

**Data recovery during recreation**: When a contaminated table has both the legacy column
(e.g., `species_name`) and the v2 column (`label_id`), and `label_id` is 0/NULL for some
rows while `species_name` is populated, the script should attempt to resolve label_ids by
looking up species in the `labels` table before recreating the table. If the lookup fails,
preserve the rows but report that manual intervention is needed.

For tables where the correct column (`label_id`) is entirely missing, the fix is not safe
to automate. Report the issue and skip.

### Fix 3: REINDEX

If integrity check found index errors:
```sql
REINDEX;
```

### Fix 4: Migration state reset

If migration state is stuck in an active state and the user ran `--fix`:
- If `detections` table has data and `notes` table doesn't exist (or is empty):
  set state to `completed`
- If `detections` table is empty but `notes` has data:
  set state to `idle` (migration never completed)
- If both have data: set state to `completed` and report that some legacy records
  may not have been migrated (informational)

### Fix 5: Clip extension repair

When `--clips-dir` is provided and clip paths have stripped extensions:
1. For each detection with a path ending in `.`:
2. Strip the trailing dot
3. Look for files matching `{path_without_dot}.*` in the clips directory
4. If exactly one match is found, update the path in the database
5. If multiple matches or no matches, skip and report

### Fix 6: Orphaned detection label repair (best-effort)

When detections reference non-existent label IDs:
1. Query orphaned detections to see if the scientific name can be recovered
   (v2 detections don't store scientific_name directly, but legacy_id may map
   to a note that does)
2. If the legacy `notes` table exists and has matching records via `legacy_id`,
   create missing labels from the note's scientific_name
3. If no recovery path exists, report the count and suggest manual intervention

This is inherently best-effort. The tool should clearly report what it did and what
it couldn't fix.

## Internal Architecture

### Module structure

Single file (`db-doctor.py`) for easy distribution. No package dependencies.

### Key classes

```text
DatabaseDoctor
    - __init__(db_path, clips_dir=None, verbose=False)
    - diagnose() -> DiagnosticReport
    - fix(report: DiagnosticReport, only: list[str] = None) -> FixReport

DiagnosticReport
    - schema_version: str  # "v1", "v2", "mixed", "unknown"
    - migration_status: str | None
    - checks: list[CheckResult]
    - table_stats: dict[str, int]

CheckResult
    - name: str
    - status: "pass" | "fail" | "warn" | "skip"
    - message: str
    - details: list[str]
    - fixable: bool

FixReport
    - backup_path: str | None
    - fixes: list[FixResult]

FixResult
    - name: str
    - status: "applied" | "skipped" | "failed"
    - message: str
    - rows_affected: int
```

### Safety constraints

1. **Read-only by default**: The database is opened with `?mode=ro` for diagnostics.
   Write mode is only used when `--fix` is specified.
2. **Backup before write**: A verified backup is created before any modification.
3. **Transactional fixes**: Each fix operation runs inside a transaction. If any
   step fails, the transaction is rolled back.
4. **Idempotent**: Running `--fix` multiple times produces the same result.
5. **No data deletion**: The tool never deletes detection data. It only modifies
   schema structure, indexes, and metadata. The only "deletion" is dropping and
   recreating contaminated tables with the same data.
6. **WAL checkpoint**: Before backup, run `PRAGMA wal_checkpoint(TRUNCATE)` to
   ensure all WAL data is flushed to the main database file.
7. **Locked database detection**: Check for WAL lock before any operation (both
   diagnose and fix modes). In fix mode, attempt to acquire an exclusive lock. If
   the lock fails, exit with code 3 and tell the user to stop the application first.
8. **Operation ordering**: Lock check runs first, then WAL checkpoint (if writable),
   then backup, then fixes. Each step depends on the previous succeeding.

### Schema definition source of truth

The expected column sets are hardcoded in the script, derived from the Go entity
struct definitions in `internal/datastore/v2/entities/`. When entity structs change,
the script's expected schemas must be updated.

A `--check-schema-version` flag prints the schema version the script was built for,
so users can verify compatibility.

## Testing Plan

### Unit tests

- Schema detection (v1 vs v2 vs mixed)
- Column comparison logic (extra, missing, matching)
- Table recreation SQL generation
- Clip path extension detection

### Integration tests

Create test databases with known problems:
1. Clean v2 database (all checks pass)
2. V2 database with `scientific_name` column in `image_caches`
3. V2 database with orphaned detection label_ids
4. V2 database with stuck migration state
5. V2 database with corrupted index (hard to simulate, skip in CI)
6. Legacy v1 database (checks should detect and report, not attempt v2 fixes)

### Manual testing

Test against the actual user database from Discussion #3210 (if available via
support dump) to verify the fix resolves the specific upgrade path from
nightly-20260322 to newer builds.

## Distribution

The script lives at `tools/db-doctor/db-doctor.py` in the BirdNET-Go repository.
Users can download it directly or copy it from their local clone. No installation
step needed.

The script could also be bundled into the Docker image at `/usr/local/bin/db-doctor`
for container users who can exec into the container.

## Version

Script version tracks the schema version it was built for. Include a header:
```python
SCRIPT_VERSION = "1.0.0"
SCHEMA_VERSION = "v2-2026-05-21"  # Date of entity struct snapshot
```

## Edge Cases

### Mixed v1/v2 databases

Some databases have both legacy `notes` and v2 `detections` tables (mid-migration state).
The doctor should detect this and report it without assuming either schema is authoritative.
The `--fix` operations only modify v2 tables; legacy tables are never touched.

### Empty v2 tables with populated legacy tables

This indicates a migration that was started but never completed. The doctor should report
the disparity and suggest the user complete the migration through the web UI rather than
attempting to fix it externally.

### Pre-PR #2165 table names

Tables may use singular names (`migration_state`, `alert_history`) instead of the current
pluralized names (`migration_states`, `alert_histories`). The doctor checks both names.
The `--fix` mode does NOT rename these tables; that's the application's responsibility
during `Initialize()`.

### Databases from very old builds

Databases from builds before the v2 schema existed will only have legacy tables. The doctor
should detect this, report the schema as "v1 (legacy)", and skip all v2 checks. No fixes
are applicable; the user needs to upgrade through the normal migration path.

## Future Considerations

- MySQL support could be added as a separate mode with different connection handling
- A `--export-report` flag could generate a support dump fragment for Sentry
- Integration into the web UI's System > Database page as a one-click health check
- Automated schema definition extraction from Go source (build-time code generation)

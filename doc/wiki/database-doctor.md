# Database Doctor

The Database Doctor is a standalone diagnostic and repair tool for BirdNET-Go SQLite databases. Run it outside the application to identify and fix database issues that prevent startup after upgrading between nightly builds.

**Requirements:** Python 3.9+ (no additional packages needed)

The Database Doctor supports SQLite only. If you use MySQL or MariaDB, BirdNET-Go resolves a database that holds both enhanced and legacy tables by itself when one side is empty, and refuses to start only when it cannot do that safely; see [MySQL and MariaDB: enhanced and legacy tables in one database](#mysql-and-mariadb-enhanced-and-legacy-tables-in-one-database).

## When to Use

Use the Database Doctor when:

- BirdNET-Go fails to start after upgrading to a newer nightly build
- You see errors like `Cannot add a NOT NULL column with default value NULL`
- The dashboard shows no detections after an upgrade, but the database file is large
- The System > Database page shows migration as stuck or incomplete
- You suspect database corruption after an unclean shutdown or SD card issue

## Download

The script is part of the BirdNET-Go repository. Download it directly:

```bash
# Download the latest version
curl -O https://raw.githubusercontent.com/tphakala/birdnet-go/main/tools/db-doctor/db-doctor.py
chmod +x db-doctor.py
```

Or if you have a local clone:

```bash
# The script is at tools/db-doctor/db-doctor.py in the repository
python3 tools/db-doctor/db-doctor.py --version
```

## Quick Start

**Important:** Stop BirdNET-Go before running the doctor with `--fix`. The diagnostic (read-only) mode can run while BirdNET-Go is active.

### Step 1: Find your database file

| Install method        | Typical database path                                                  |
| --------------------- | ---------------------------------------------------------------------- |
| Docker via install.sh | `~/birdnet-go-app/data/birdnet.db`                                     |
| Docker Compose        | Check your volume mount, usually `./data/birdnet.db`                   |
| Manual/binary         | In the data directory specified in your config, usually `./birdnet.db` |

### Step 2: Run diagnosis

```bash
python3 db-doctor.py /path/to/birdnet.db
```

This runs in read-only mode and does not modify anything. It checks:

1. **Database accessibility** - file exists, valid SQLite, reports size and version
2. **Schema version** - detects v1 (legacy), v2 (current), or mixed (mid-migration)
3. **SQLite integrity** - checks for index corruption from unclean shutdowns
4. **Schema contamination** - finds columns that shouldn't be there (the main upgrade issue)
5. **Foreign key integrity** - finds orphaned references between tables
6. **Migration state** - checks if migration is stuck or inconsistent
7. **Clip path extensions** - detects stripped audio file extensions

### Step 3: Review the output

The output looks like this:

```text
BirdNET-Go Database Doctor v1.0.0
Schema definition: v2-2026-05-21
Run at: 2026-05-21 09:32:00

Database: /home/birder/birdnet-go-app/data/birdnet.db
Size:     105.2 MB
SQLite:   3.40.1
Python:   3.11.2
Platform: Linux aarch64
Schema:   v2 (migration: completed)

Database fingerprint:
  detections: 142,891
  labels: 2,347
  ai_models: 1
  audio_sources: 1
  date range: 2024-03-15 to 2026-05-20
  distinct species: 187
  aux: image-caches=1,203, dynamic-thresholds=45, alert-rules=3

Migration state:
  state: completed
  completed_at: 2026-03-01 14:22:33

Checks:
  [PASS] Database accessibility
  [PASS] Schema version
  [PASS] SQLite integrity
  [FAIL] Schema contamination
         Schema issues found
         image_caches: unexpected column(s) ['scientific_name'] (1,203 rows)
           actual columns: [...]
           expected columns: [...]
  [PASS] Foreign key integrity
  [PASS] Migration state
  [PASS] Clip path extensions

Summary: 1 failure(s), 6 passed

Run with --fix to repair fixable issues.

-- Copy everything above when reporting issues --
```

### Step 4: Fix issues (if any)

If the doctor found fixable issues, stop BirdNET-Go first, then run:

```bash
# Stop BirdNET-Go
sudo systemctl stop birdnet-go
# Or for Docker: docker stop birdnet-go

# Run the fix
python3 db-doctor.py /path/to/birdnet.db --fix
```

The script automatically:

1. Creates a verified backup (e.g., `birdnet.db.20260521-093204.doctor-backup`)
2. Applies fixes transactionally (rolls back on failure)
3. Re-runs diagnostics to verify the fix worked

After the fix completes successfully, start BirdNET-Go again:

```bash
sudo systemctl start birdnet-go
# Or for Docker: docker start birdnet-go
```

## What the Doctor Fixes

### Schema contamination

This is the most common issue when upgrading between nightly builds. Some builds accidentally added legacy columns to v2 tables, preventing newer builds from starting.

The doctor uses SQLite's table-recreation algorithm to remove the extra columns while preserving all data. This works on all SQLite versions (no DROP COLUMN support needed).

**Affected tables:** `image_caches`, `notification_histories`

The doctor does not repair `dynamic_thresholds` or `threshold_events`. If they still have the old `label_id` column, BirdNET-Go recreates them itself on its next start and discards their rows. Adding a `label_id` column to a current table would trigger that, so the doctor leaves both tables alone.

### Corrupted indexes

SD card corruption or power loss during writes can cause SQLite index trees to become inconsistent. The doctor rebuilds all indexes with `REINDEX`.

### Stuck migration state

If BirdNET-Go crashes during database migration (power loss, OOM kill), the migration state can get stuck. The doctor resets it based on the actual data present:

- If detections exist, sets state to `completed`
- If only legacy notes exist, resets to `idle` for re-migration

### Clip path extensions

A past nightly build stripped file extensions from audio clip paths in the database. The doctor can repair these by matching against actual files on disk (requires `--clips-dir`).

### Orphaned label references

After some upgrades, detection records may reference label IDs that no longer exist, making those detections invisible in the dashboard. The doctor attempts to recover these using legacy data when available.

## Command Reference

```bash
# Basic diagnosis (read-only)
python3 db-doctor.py /path/to/birdnet.db

# Diagnosis with verbose output (shows SQL queries, full integrity check)
python3 db-doctor.py /path/to/birdnet.db --verbose

# Diagnose and fix all issues
python3 db-doctor.py /path/to/birdnet.db --fix

# Fix only specific issue types
python3 db-doctor.py /path/to/birdnet.db --fix --only schema
python3 db-doctor.py /path/to/birdnet.db --fix --only indexes
python3 db-doctor.py /path/to/birdnet.db --fix --only migration
python3 db-doctor.py /path/to/birdnet.db --fix --only schema,indexes

# Fix with clip extension repair (needs clips directory)
python3 db-doctor.py /path/to/birdnet.db --fix --clips-dir /path/to/clips

# Skip backup (use when disk is nearly full)
python3 db-doctor.py /path/to/birdnet.db --fix --no-backup

# JSON output for scripting
python3 db-doctor.py /path/to/birdnet.db --json

# Check script version
python3 db-doctor.py --version
```

### Exit codes

| Code | Meaning                                               |
| ---- | ----------------------------------------------------- |
| 0    | All checks passed (or all fixes applied successfully) |
| 1    | Issues found (diagnose mode) or a fix failed          |
| 2    | Usage error (bad arguments, file not found)           |
| 3    | Database is locked (BirdNET-Go is still running)      |

## Docker Users

If you're running BirdNET-Go in Docker and don't have Python on the host, you can run the doctor inside any Python container:

```bash
# Stop BirdNET-Go container first
docker stop birdnet-go

# Run the doctor using a Python container, mounting the data volume
docker run --rm -v /path/to/data:/data python:3.11-slim \
  python3 /data/db-doctor.py /data/birdnet.db --fix
```

Make sure to copy `db-doctor.py` into your data directory first, or mount it separately.

## Reporting Issues

If the doctor cannot fix your database, or you encounter an error, please open a [GitHub Discussion](https://github.com/tphakala/birdnet-go/discussions) and include:

1. The **complete output** of `python3 db-doctor.py /path/to/birdnet.db --verbose`
2. Which BirdNET-Go version you were upgrading **from** and **to**
3. Your install method (Docker via install.sh, Docker Compose, manual binary)

The doctor output includes platform info, database fingerprint, migration state, and full schema details, which makes remote debugging possible without follow-up questions.

## Safety

- **Read-only by default.** The `--fix` flag is required for any modification.
- **Automatic backup.** Before any fix, the script creates a verified backup using SQLite's backup API.
- **Transactional fixes.** Each repair runs inside a database transaction. If anything fails, changes are rolled back.
- **Idempotent.** Running `--fix` multiple times produces the same result.
- **No data deletion.** The doctor never deletes detection data. Schema repair preserves all rows.
- **Lock detection.** The script refuses to run fixes if BirdNET-Go is still using the database.

## MySQL and MariaDB: enhanced and legacy tables in one database

A MySQL or MariaDB database can hold the enhanced database (v2) tables and the legacy tables at the same time, for example after a failed setup or a downgrade. BirdNET-Go resolves this by itself when one side holds no data:

- If the legacy tables are empty and the enhanced database is a completed install, it keeps the enhanced database and removes the empty legacy tables.
- If the legacy tables hold data and the enhanced detection tables are empty, it keeps the legacy database and removes only the empty enhanced detection tables. Every other enhanced table stays where it is.
- If both sides are empty, it keeps the enhanced database and marks it as a completed install (when the install has no marker row yet, the first start writes it and the empty legacy tables are removed on the start after that).

BirdNET-Go checks each table for rows again right before it drops it, never drops a table that has rows, and logs one warning that names what it found, kept, removed and left in place.

It refuses to start, and drops no table with rows, only when keeping a side would lose data or the tables cannot be handled safely: both sides hold data, keeping a side would remove rows of a table both schemas use (`notification_histories`, `dynamic_thresholds`, `threshold_events` or `image_caches`), the enhanced side holds detections but is not a completed install, a table cannot be read, or an empty table cannot be removed because another session holds it or the database user lacks the privilege. The error lists the tables that have rows on each side, or the step that failed; in the last case fix the cause (a session that holds the tables, or a missing privilege) and restart. The Database Doctor script does not support MySQL, so the steps below are manual.

Do not downgrade to an older release while the database is in this state: older releases drop the enhanced database tables at startup.

### Step 1: Stop BirdNET-Go

```bash
docker stop birdnet-go
# Or for a systemd install: sudo systemctl stop birdnet-go
```

### Step 2: Back up the database

```bash
mysqldump --single-transaction -h <host> -u <user> -p <database> > birdnet-backup.sql
```

Use `mariadb-dump` instead of `mysqldump` on MariaDB. Check that `birdnet-backup.sql` is not empty before you continue.

### Step 3: Remove one side

Choose the side to keep. Dropping a side loses the rows the error lists for it. Run the SQL in the `mysql` or `mariadb` client connected to your BirdNET-Go database.

**3a: Keep the enhanced database.** Choose this when the legacy side lists no rows you need. First check that the enhanced database is a completed install (if the table does not exist, run the same query on `migration_state`):

```sql
SELECT state FROM migration_states;
```

If the result is not `completed`, do not use this step and ask for help as described in step 3c. Otherwise drop the legacy tables:

```sql
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS note_locks, note_comments, note_reviews, results, notes;
SET FOREIGN_KEY_CHECKS = 1;
```

**3b: Keep the legacy database.** Choose this when the enhanced side lists no rows you need. Drop the bare enhanced database tables:

```sql
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS alert_actions, alert_conditions, alert_history, alert_histories, alert_rules,
  detection_locks, detection_comments, detection_reviews, detection_model_contributions,
  detection_predictions, detections, labels, privacy_filter_hourlies, audio_sources, ai_models,
  taxonomic_classes, label_types, app_events, app_metadata, migration_dirty_ids, migration_state,
  migration_states;
SET FOREIGN_KEY_CHECKS = 1;
```

The enhanced database rows listed in the error are lost with these tables. On the next start the legacy store also recreates any of `notification_histories`, `dynamic_thresholds`, `threshold_events` and `image_caches` that still have the enhanced database shape, and their rows are lost.

**3c: Both sides hold data you need, or the error says tables could not be read, or the enhanced side holds detections but is not a completed install.** Keep the backup and drop nothing. Ask on [GitHub Discussions](https://github.com/tphakala/birdnet-go/discussions) and include the log lines. The table names and row counts in them are safe to share.

### Step 4: Start BirdNET-Go

Start BirdNET-Go again. After step 3a BirdNET-Go starts in enhanced database mode. After step 3b BirdNET-Go starts in legacy mode.

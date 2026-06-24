# Database Doctor

The Database Doctor is a standalone diagnostic and repair tool for BirdNET-Go SQLite databases. Run it outside the application to identify and fix database issues that prevent startup after upgrading between nightly builds.

**Requirements:** Python 3.9+ (no additional packages needed)

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

| Install method | Typical database path |
|---|---|
| Docker via install.sh | `~/birdnet-go-app/data/birdnet.db` |
| Docker Compose | Check your volume mount, usually `./data/birdnet.db` |
| Manual/binary | In the data directory specified in your config, usually `./birdnet.db` |

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

**Affected tables:** `image_caches`, `dynamic_thresholds`, `notification_histories`

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

| Code | Meaning |
|------|---------|
| 0 | All checks passed (or all fixes applied successfully) |
| 1 | Issues found (diagnose mode) or a fix failed |
| 2 | Usage error (bad arguments, file not found) |
| 3 | Database is locked (BirdNET-Go is still running) |

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

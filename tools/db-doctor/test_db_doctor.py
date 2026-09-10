#!/usr/bin/env python3
"""Tests for db-doctor's migration-state consistency check.

Focuses on the GitHub #3575 blind spot: a `completed` migration marker with the
`detections` data table missing entirely must be flagged fixable and reset to
idle, while a healthy fresh install (detections present but empty, no legacy
notes) must NOT be flagged.

Run: python3 tools/db-doctor/test_db_doctor.py
"""

import importlib.util
import os
import sqlite3
import tempfile
import unittest

# db-doctor.py has a hyphen in its name, so load it explicitly as a module.
_HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location(
    "db_doctor", os.path.join(_HERE, "db-doctor.py")
)
db_doctor = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(db_doctor)


_MIGRATION_STATES_DDL = """
CREATE TABLE migration_states (
    id INTEGER PRIMARY KEY,
    state TEXT NOT NULL DEFAULT 'idle',
    current_phase TEXT NOT NULL DEFAULT '',
    phase_number INTEGER DEFAULT 0,
    total_phases INTEGER DEFAULT 0,
    started_at DATETIME,
    phase_started_at DATETIME,
    completed_at DATETIME,
    last_migrated_id INTEGER DEFAULT 0,
    total_records INTEGER DEFAULT 0,
    migrated_records INTEGER DEFAULT 0,
    error_message TEXT,
    related_data_error TEXT,
    updated_at DATETIME
)
"""


_HOURLY_WEATHERS_COLUMNS = [
    "id INTEGER PRIMARY KEY AUTOINCREMENT",
    "daily_events_id INTEGER NOT NULL DEFAULT 0",
    "time DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
    "temperature REAL NOT NULL DEFAULT 0",
    "feels_like REAL NOT NULL DEFAULT 0",
    "temp_min REAL NOT NULL DEFAULT 0",
    "temp_max REAL NOT NULL DEFAULT 0",
    "pressure INTEGER NOT NULL DEFAULT 0",
    "humidity INTEGER NOT NULL DEFAULT 0",
    "visibility INTEGER NOT NULL DEFAULT 0",
    "wind_speed REAL NOT NULL DEFAULT 0",
    "wind_deg INTEGER NOT NULL DEFAULT 0",
    "wind_gust REAL NOT NULL DEFAULT 0",
    "clouds INTEGER NOT NULL DEFAULT 0",
    "weather_main TEXT NOT NULL DEFAULT ''",
    "weather_desc TEXT NOT NULL DEFAULT ''",
    "weather_icon TEXT NOT NULL DEFAULT ''",
    "created_at DATETIME",
]

# Added to entities.HourlyWeather in #3495; absent from pre-#3495 databases.
_HOURLY_WEATHERS_PRECIPITATION_COLUMNS = [
    "precipitation REAL NOT NULL DEFAULT 0",
    "precipitation_type TEXT NOT NULL DEFAULT ''",
]


def _build_hourly_weathers(path, *, with_precipitation):
    """Create an hourly_weathers table with one row, with or without precipitation."""
    columns = list(_HOURLY_WEATHERS_COLUMNS)
    if with_precipitation:
        columns += _HOURLY_WEATHERS_PRECIPITATION_COLUMNS
    conn = sqlite3.connect(path)
    try:
        conn.execute(
            "CREATE TABLE hourly_weathers (\n    "
            + ",\n    ".join(columns)
            + "\n)"
        )
        conn.execute(
            "INSERT INTO hourly_weathers (daily_events_id, time, temperature) "
            "VALUES (1, '2026-07-01 10:00:00', 12.5)"
        )
        conn.commit()
    finally:
        conn.close()


def _build_db(path, *, state, with_detections, detection_rows=0, note_rows=None):
    """Create a minimal SQLite db with a migration_states row and optional tables."""
    conn = sqlite3.connect(path)
    try:
        conn.execute(_MIGRATION_STATES_DDL)
        conn.execute(
            "INSERT INTO migration_states (id, state, last_migrated_id, "
            "total_records, migrated_records, error_message) "
            "VALUES (1, ?, 0, 0, 0, '')",
            (state,),
        )
        if with_detections:
            conn.execute("CREATE TABLE detections (id INTEGER PRIMARY KEY)")
            for i in range(detection_rows):
                conn.execute("INSERT INTO detections (id) VALUES (?)", (i + 1,))
        if note_rows is not None:
            conn.execute("CREATE TABLE notes (id INTEGER PRIMARY KEY)")
            for i in range(note_rows):
                conn.execute("INSERT INTO notes (id) VALUES (?)", (i + 1,))
        conn.commit()
    finally:
        conn.close()


class MigrationStateCheckTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)

    def _run_check(self, **kwargs):
        path = os.path.join(self._tmp.name, "birdnet.db")
        _build_db(path, **kwargs)
        doctor = db_doctor.DatabaseDoctor(path)
        report = db_doctor.DiagnosticReport(db_path=path)
        conn = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        try:
            report.tables_present = doctor._get_tables(conn)
            report.schema_version = "v2"
            return doctor._check_migration_state(conn, report)
        finally:
            conn.close()

    def test_completed_with_detections_table_missing_is_flagged(self):
        # GitHub #3575: completed marker, detections table absent entirely.
        result = self._run_check(state="completed", with_detections=False)
        self.assertEqual(result.status, "warn")
        self.assertTrue(result.fixable)
        self.assertIn("missing", result.message)

    def test_completed_fresh_install_empty_detections_no_notes_passes(self):
        # Healthy fresh install: detections present but empty, no legacy notes.
        # Must NOT be flagged (would otherwise reset a healthy install to idle).
        result = self._run_check(
            state="completed", with_detections=True, detection_rows=0
        )
        self.assertEqual(result.status, "pass")
        self.assertFalse(result.fixable)

    def test_completed_empty_detections_with_legacy_notes_is_flagged(self):
        # Existing behavior: detections empty but legacy notes hold data.
        result = self._run_check(
            state="completed", with_detections=True, detection_rows=0, note_rows=5
        )
        self.assertEqual(result.status, "warn")
        self.assertTrue(result.fixable)
        self.assertIn("empty", result.message)

    def test_completed_with_populated_detections_passes(self):
        result = self._run_check(
            state="completed", with_detections=True, detection_rows=42
        )
        self.assertEqual(result.status, "pass")
        self.assertFalse(result.fixable)


class MigrationStateFixTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)

    def test_fix_resets_completed_missing_detections_to_idle(self):
        # The #3575 condition must be repaired by resetting state to idle so the
        # app can rebuild / re-run the migration instead of staying wedged.
        path = os.path.join(self._tmp.name, "birdnet.db")
        _build_db(path, state="completed", with_detections=False)
        doctor = db_doctor.DatabaseDoctor(path)

        report = db_doctor.DiagnosticReport(db_path=path)
        ro = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
        try:
            report.tables_present = doctor._get_tables(ro)
            report.schema_version = "v2"
            check = doctor._check_migration_state(ro, report)
        finally:
            ro.close()
        report.checks = [check]
        self.assertEqual(check.status, "warn")

        conn = sqlite3.connect(path)
        try:
            fix = doctor._fix_migration_state(conn, report)
        finally:
            conn.close()
        self.assertEqual(fix.status, "applied")

        verify = sqlite3.connect(path)
        try:
            state = verify.execute(
                "SELECT state FROM migration_states WHERE id = 1"
            ).fetchone()[0]
        finally:
            verify.close()
        self.assertEqual(state, "idle")


class HourlyWeatherSchemaTest(unittest.TestCase):
    """Covers GitHub #4052: the precipitation columns added in #3495.

    A v2 database carrying `precipitation` / `precipitation_type` must not be
    reported as contaminated, and a pre-#3495 database missing them must be
    repairable via the ALTER TABLE ADD COLUMN path.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.path = os.path.join(self._tmp.name, "birdnet.db")

    def _run_contamination_check(self, report=None):
        doctor = db_doctor.DatabaseDoctor(self.path)
        report = report or db_doctor.DiagnosticReport(db_path=self.path)
        ro = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True)
        try:
            report.tables_present = doctor._get_tables(ro)
            report.schema_version = "v2"
            check = doctor._check_schema_contamination(ro, report)
        finally:
            ro.close()
        return doctor, report, check

    def test_precipitation_columns_pass_contamination_check(self):
        _build_hourly_weathers(self.path, with_precipitation=True)

        _, _, check = self._run_contamination_check()

        self.assertEqual(check.status, "pass")

    def test_missing_precipitation_columns_are_added_by_fix(self):
        _build_hourly_weathers(self.path, with_precipitation=False)

        doctor, report, check = self._run_contamination_check()
        self.assertEqual(check.status, "fail")
        self.assertTrue(check.fixable)
        report.checks.append(check)

        conn = sqlite3.connect(self.path)
        try:
            fix = doctor._fix_schema_contamination(conn, report)
            columns = {
                row[1]: row for row in conn.execute(
                    "PRAGMA table_info(hourly_weathers)"
                )
            }
            rows = conn.execute("SELECT COUNT(*) FROM hourly_weathers").fetchone()[0]
            values = conn.execute(
                "SELECT precipitation, precipitation_type FROM hourly_weathers"
            ).fetchone()
        finally:
            conn.close()

        self.assertEqual(fix.status, "applied")

        # PRAGMA table_info columns: (cid, name, type, notnull, dflt_value, pk)
        self.assertEqual(columns["precipitation"][2], "REAL")
        self.assertEqual(columns["precipitation"][3], 1)
        self.assertEqual(columns["precipitation"][4], "0")
        self.assertEqual(columns["precipitation_type"][2], "TEXT")
        self.assertEqual(columns["precipitation_type"][3], 1)
        self.assertEqual(columns["precipitation_type"][4], "''")

        # Existing rows survive and pick up the column defaults.
        self.assertEqual(rows, 1)
        self.assertEqual(values, (0.0, ""))

        # The repaired schema is clean on a re-check.
        _, _, recheck = self._run_contamination_check()
        self.assertEqual(recheck.status, "pass")


if __name__ == "__main__":
    unittest.main()

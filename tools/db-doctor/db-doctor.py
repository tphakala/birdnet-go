#!/usr/bin/env python3
"""BirdNET-Go Database Doctor - diagnose and repair SQLite database issues.

A standalone diagnostic and repair tool for BirdNET-Go SQLite databases.
Identifies schema contamination, orphaned references, corrupted indexes,
stale migration state, and other issues that prevent startup after upgrades.

Requires Python 3.9+ and has zero third-party dependencies.

Usage:
    python3 db-doctor.py /path/to/birdnet.db            # diagnose only
    python3 db-doctor.py /path/to/birdnet.db --fix       # diagnose and fix
    python3 db-doctor.py /path/to/birdnet.db --verbose   # verbose output
    python3 db-doctor.py /path/to/birdnet.db --json      # machine-readable
"""

import argparse
import json
import os
import platform
import shutil
import sqlite3
import sys
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Optional

SCRIPT_VERSION = "1.1.0"
SCHEMA_VERSION = "v2-2026-05-21"

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SQLITE_HEADER_SIZE = 16
SQLITE_MAGIC = b"SQLite format 3"
BACKUP_DISK_HEADROOM = 1.1
MAX_INTEGRITY_ERRORS_SHOWN = 20

# ---------------------------------------------------------------------------
# Expected column definitions for V2 tables (source of truth: Go entity structs
# in internal/datastore/v2/entities/)
# ---------------------------------------------------------------------------

V2_EXPECTED_COLUMNS: dict[str, list[str]] = {
    "label_types": ["id", "name"],
    "taxonomic_classes": ["id", "name"],
    "ai_models": [
        "id", "name", "version", "variant", "model_type",
        "classifier_path", "created_at",
    ],
    "labels": [
        "id", "scientific_name", "model_id", "label_type_id",
        "taxonomic_class_id", "created_at",
    ],
    "audio_sources": [
        "id", "source_uri", "node_name", "source_type",
        "display_name", "config_json", "created_at",
    ],
    "detections": [
        "id", "model_id", "label_id", "source_id", "detected_at",
        "begin_time", "end_time", "confidence", "latitude", "longitude",
        "clip_name", "processing_time_ms", "unlikely", "legacy_id",
    ],
    "detection_predictions": [
        "id", "detection_id", "label_id", "confidence", "rank",
    ],
    "detection_model_contributions": [
        "id", "detection_id", "model_id", "hit_count", "max_confidence",
    ],
    "detection_reviews": [
        "id", "detection_id", "verified", "created_at", "updated_at",
    ],
    "detection_comments": [
        "id", "detection_id", "entry", "created_at", "updated_at",
    ],
    "detection_locks": ["id", "detection_id", "locked_at"],
    "image_caches": [
        "id", "provider_name", "label_id", "source_provider", "url",
        "license_name", "license_url", "author_name", "author_url",
        "cached_at", "created_at", "updated_at",
    ],
    "dynamic_thresholds": [
        "id", "label_id", "level", "current_value", "base_threshold",
        "high_conf_count", "valid_hours", "expires_at", "last_triggered",
        "first_created", "trigger_count", "created_at", "updated_at",
    ],
    "threshold_events": [
        "id", "label_id", "previous_level", "new_level", "previous_value",
        "new_value", "change_reason", "confidence", "created_at",
    ],
    "notification_histories": [
        "id", "label_id", "notification_type", "last_sent",
        "expires_at", "created_at", "updated_at",
    ],
    "alert_rules": [
        "id", "name", "description", "name_key", "description_key",
        "enabled", "built_in", "object_type", "trigger_type", "event_name",
        "metric_name", "cooldown_sec", "escalation_steps", "created_at",
        "updated_at",
    ],
    "alert_conditions": [
        "id", "rule_id", "property", "operator", "value",
        "duration_sec", "sort_order",
    ],
    "alert_actions": [
        "id", "rule_id", "target", "template_title",
        "template_message", "sort_order",
    ],
    "alert_histories": [
        "id", "rule_id", "fired_at", "event_data", "actions", "created_at",
    ],
    "migration_states": [
        "id", "state", "current_phase", "phase_number", "total_phases",
        "started_at", "phase_started_at", "completed_at", "last_migrated_id",
        "total_records", "migrated_records", "error_message",
        "related_data_error", "updated_at",
    ],
    "migration_dirty_ids": ["detection_id", "created_at"],
    "daily_events": [
        "id", "date", "sunrise", "sunset", "country", "city_name",
        "moon_phase", "moon_illumination",
    ],
    "hourly_weathers": [
        "id", "daily_events_id", "time", "temperature", "feels_like",
        "temp_min", "temp_max", "pressure", "humidity", "visibility",
        "wind_speed", "wind_deg", "wind_gust", "clouds", "weather_main",
        "weather_desc", "weather_icon", "created_at",
    ],
    "app_metadata": ["key", "value"],
}

# Tables that had schema changes between v1 and v2 (used for contamination detection).
# Maps table name to the set of columns that should NOT exist in a clean v2 schema.
# Includes both legacy contamination (v1 columns leaking into v2 tables) and
# schema evolution leftovers (columns that existed in earlier v2 entity versions).
V2_CONTAMINATION_COLUMNS: dict[str, list[str]] = {
    "image_caches": ["scientific_name"],
    "dynamic_thresholds": ["species_name", "model_name", "scientific_name"],
    "notification_histories": ["scientific_name"],
    # Schema evolution: columns removed from v2 entities between builds
    "ai_models": ["label_count"],
    "labels": ["label_type", "taxonomic_class"],
    "detections": ["created_at", "sensitivity", "threshold"],
    "daily_events": ["created_at", "updated_at"],
}

LEGACY_V1_TABLES = {"notes", "results", "note_reviews", "note_comments", "note_locks"}

V2_ONLY_TABLES = {
    "detections", "detection_predictions", "detection_reviews",
    "detection_comments", "detection_locks", "labels", "ai_models",
    "audio_sources", "label_types", "taxonomic_classes",
    "detection_model_contributions", "alert_rules", "alert_conditions",
    "alert_actions", "alert_histories", "migration_states",
    "migration_dirty_ids", "app_metadata",
}

TABLE_RENAMES = {
    "migration_state": "migration_states",
    "alert_history": "alert_histories",
}

KNOWN_MIGRATION_STATES = {
    "idle", "initializing", "dual_write", "paused",
    "migrating", "validating", "cutover", "completed", "failed",
}

ACTIVE_MIGRATION_STATES = {
    "initializing", "dual_write", "migrating", "validating", "cutover",
}

# V2 table recreation SQL templates for schema contamination fix.
V2_TABLE_SCHEMAS: dict[str, tuple[str, list[str]]] = {
    "image_caches": (
        """CREATE TABLE image_caches_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            provider_name TEXT NOT NULL DEFAULT 'wikimedia',
            label_id INTEGER NOT NULL,
            source_provider TEXT NOT NULL DEFAULT 'wikimedia',
            url TEXT DEFAULT '',
            license_name TEXT DEFAULT '',
            license_url TEXT DEFAULT '',
            author_name TEXT DEFAULT '',
            author_url TEXT DEFAULT '',
            cached_at DATETIME,
            created_at DATETIME,
            updated_at DATETIME,
            FOREIGN KEY(label_id) REFERENCES labels(id)
        )""",
        [
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_image_cache_provider_label "
            "ON image_caches (provider_name, label_id)",
            "CREATE INDEX IF NOT EXISTS idx_image_caches_cached_at "
            "ON image_caches (cached_at)",
        ],
    ),
    "dynamic_thresholds": (
        """CREATE TABLE dynamic_thresholds_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            label_id INTEGER NOT NULL,
            level INTEGER NOT NULL DEFAULT 0,
            current_value REAL NOT NULL,
            base_threshold REAL NOT NULL,
            high_conf_count INTEGER NOT NULL DEFAULT 0,
            valid_hours INTEGER NOT NULL,
            expires_at DATETIME NOT NULL,
            last_triggered DATETIME NOT NULL,
            first_created DATETIME NOT NULL,
            trigger_count INTEGER NOT NULL DEFAULT 0,
            created_at DATETIME,
            updated_at DATETIME,
            CONSTRAINT uni_dynamic_thresholds_label_id UNIQUE (label_id),
            FOREIGN KEY(label_id) REFERENCES labels(id)
        )""",
        [
            "CREATE INDEX IF NOT EXISTS idx_dynamic_thresholds_expires_at "
            "ON dynamic_thresholds (expires_at)",
            "CREATE INDEX IF NOT EXISTS idx_dynamic_thresholds_last_triggered "
            "ON dynamic_thresholds (last_triggered)",
        ],
    ),
    "notification_histories": (
        """CREATE TABLE notification_histories_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            label_id INTEGER NOT NULL,
            notification_type TEXT NOT NULL DEFAULT 'new_species',
            last_sent DATETIME NOT NULL,
            expires_at DATETIME NOT NULL,
            created_at DATETIME,
            updated_at DATETIME,
            FOREIGN KEY(label_id) REFERENCES labels(id)
        )""",
        [
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_label_type "
            "ON notification_histories (label_id, notification_type)",
            "CREATE INDEX IF NOT EXISTS idx_notification_histories_last_sent "
            "ON notification_histories (last_sent)",
            "CREATE INDEX IF NOT EXISTS idx_notification_histories_expires_at "
            "ON notification_histories (expires_at)",
        ],
    ),
    # Schema evolution recreation templates (columns removed between builds)
    "ai_models": (
        """CREATE TABLE ai_models_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name VARCHAR(50) NOT NULL DEFAULT '',
            version VARCHAR(20) NOT NULL DEFAULT '',
            variant VARCHAR(100) NOT NULL DEFAULT 'default',
            model_type VARCHAR(20) NOT NULL DEFAULT '',
            classifier_path VARCHAR(500),
            created_at DATETIME
        )""",
        [
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_model_identity "
            "ON ai_models (name, version, variant)",
        ],
    ),
    "labels": (
        """CREATE TABLE labels_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            scientific_name VARCHAR(200) NOT NULL DEFAULT '',
            model_id INTEGER NOT NULL DEFAULT 0,
            label_type_id INTEGER NOT NULL DEFAULT 0,
            taxonomic_class_id INTEGER,
            created_at DATETIME,
            FOREIGN KEY(model_id) REFERENCES ai_models(id),
            FOREIGN KEY(label_type_id) REFERENCES label_types(id),
            FOREIGN KEY(taxonomic_class_id) REFERENCES taxonomic_classes(id)
        )""",
        [
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_label_identity "
            "ON labels (scientific_name, model_id)",
            "CREATE INDEX IF NOT EXISTS idx_labels_model_id "
            "ON labels (model_id)",
            "CREATE INDEX IF NOT EXISTS idx_labels_label_type_id "
            "ON labels (label_type_id)",
            "CREATE INDEX IF NOT EXISTS idx_labels_taxonomic_class_id "
            "ON labels (taxonomic_class_id)",
        ],
    ),
    "detections": (
        """CREATE TABLE detections_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            model_id INTEGER NOT NULL DEFAULT 0,
            label_id INTEGER NOT NULL DEFAULT 0,
            source_id INTEGER,
            detected_at INTEGER NOT NULL DEFAULT 0,
            begin_time INTEGER,
            end_time INTEGER,
            confidence REAL NOT NULL DEFAULT 0,
            latitude REAL,
            longitude REAL,
            clip_name VARCHAR(500),
            processing_time_ms INTEGER,
            unlikely NUMERIC NOT NULL DEFAULT 0,
            legacy_id INTEGER,
            FOREIGN KEY(model_id) REFERENCES ai_models(id),
            FOREIGN KEY(label_id) REFERENCES labels(id)
        )""",
        [
            "CREATE INDEX IF NOT EXISTS idx_detection_model_label "
            "ON detections (model_id, label_id)",
            "CREATE INDEX IF NOT EXISTS idx_detection_label_date "
            "ON detections (label_id, detected_at)",
            "CREATE INDEX IF NOT EXISTS idx_detection_source "
            "ON detections (source_id)",
            "CREATE INDEX IF NOT EXISTS idx_detections_detected_at "
            "ON detections (detected_at)",
            "CREATE INDEX IF NOT EXISTS idx_detection_confidence "
            "ON detections (detected_at, confidence)",
            "CREATE INDEX IF NOT EXISTS idx_detections_legacy_id "
            "ON detections (legacy_id)",
        ],
    ),
    "daily_events": (
        """CREATE TABLE daily_events_new (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            date VARCHAR(10),
            sunrise INTEGER,
            sunset INTEGER,
            country VARCHAR(100),
            city_name VARCHAR(200),
            moon_phase REAL,
            moon_illumination REAL
        )""",
        [
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_events_date "
            "ON daily_events (date)",
        ],
    ),
}

# ALTER TABLE ADD COLUMN definitions for missing column repair.
# Maps table -> column -> SQL type + constraints for ALTER TABLE ADD COLUMN.
# Source of truth: Go entity structs in internal/datastore/v2/entities/.
# Primary key columns are excluded (cannot be added via ALTER TABLE).
V2_COLUMN_DEFS: dict[str, dict[str, str]] = {
    "label_types": {
        "name": "TEXT NOT NULL DEFAULT ''",
    },
    "taxonomic_classes": {
        "name": "TEXT NOT NULL DEFAULT ''",
    },
    "ai_models": {
        "name": "TEXT NOT NULL DEFAULT ''",
        "version": "TEXT NOT NULL DEFAULT ''",
        "variant": "TEXT NOT NULL DEFAULT 'default'",
        "model_type": "TEXT NOT NULL DEFAULT ''",
        "classifier_path": "TEXT",
        "created_at": "DATETIME",
    },
    "labels": {
        "scientific_name": "TEXT NOT NULL DEFAULT ''",
        "model_id": "INTEGER NOT NULL DEFAULT 0",
        "label_type_id": "INTEGER NOT NULL DEFAULT 0",
        "taxonomic_class_id": "INTEGER",
        "created_at": "DATETIME",
    },
    "audio_sources": {
        "source_uri": "TEXT NOT NULL DEFAULT ''",
        "node_name": "TEXT NOT NULL DEFAULT ''",
        "source_type": "TEXT NOT NULL DEFAULT ''",
        "display_name": "TEXT",
        "config_json": "TEXT",
        "created_at": "DATETIME",
    },
    "detections": {
        "model_id": "INTEGER NOT NULL DEFAULT 0",
        "label_id": "INTEGER NOT NULL DEFAULT 0",
        "source_id": "INTEGER",
        "detected_at": "INTEGER NOT NULL DEFAULT 0",
        "begin_time": "INTEGER",
        "end_time": "INTEGER",
        "confidence": "REAL NOT NULL DEFAULT 0",
        "latitude": "REAL",
        "longitude": "REAL",
        "clip_name": "TEXT",
        "processing_time_ms": "INTEGER",
        "unlikely": "NUMERIC NOT NULL DEFAULT 0",
        "legacy_id": "INTEGER",
    },
    "detection_predictions": {
        "detection_id": "INTEGER NOT NULL DEFAULT 0",
        "label_id": "INTEGER NOT NULL DEFAULT 0",
        "confidence": "REAL NOT NULL DEFAULT 0",
        "rank": "INTEGER NOT NULL DEFAULT 1",
    },
    "detection_model_contributions": {
        "detection_id": "INTEGER NOT NULL DEFAULT 0",
        "model_id": "INTEGER NOT NULL DEFAULT 0",
        "hit_count": "INTEGER NOT NULL DEFAULT 0",
        "max_confidence": "REAL NOT NULL DEFAULT 0",
    },
    "detection_reviews": {
        "detection_id": "INTEGER NOT NULL DEFAULT 0",
        "verified": "TEXT NOT NULL DEFAULT ''",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    },
    "detection_comments": {
        "detection_id": "INTEGER NOT NULL DEFAULT 0",
        "entry": "TEXT NOT NULL DEFAULT ''",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    },
    "detection_locks": {
        "detection_id": "INTEGER NOT NULL DEFAULT 0",
        "locked_at": "DATETIME",
    },
    "image_caches": {
        "provider_name": "TEXT NOT NULL DEFAULT 'wikimedia'",
        "label_id": "INTEGER NOT NULL DEFAULT 0",
        "source_provider": "TEXT NOT NULL DEFAULT 'wikimedia'",
        "url": "TEXT DEFAULT ''",
        "license_name": "TEXT DEFAULT ''",
        "license_url": "TEXT DEFAULT ''",
        "author_name": "TEXT DEFAULT ''",
        "author_url": "TEXT DEFAULT ''",
        "cached_at": "DATETIME",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    },
    "dynamic_thresholds": {
        "label_id": "INTEGER NOT NULL DEFAULT 0",
        "level": "INTEGER NOT NULL DEFAULT 0",
        "current_value": "REAL NOT NULL DEFAULT 0",
        "base_threshold": "REAL NOT NULL DEFAULT 0",
        "high_conf_count": "INTEGER NOT NULL DEFAULT 0",
        "valid_hours": "INTEGER NOT NULL DEFAULT 0",
        "expires_at": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "last_triggered": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "first_created": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "trigger_count": "INTEGER NOT NULL DEFAULT 0",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    },
    "threshold_events": {
        "label_id": "INTEGER NOT NULL DEFAULT 0",
        "previous_level": "INTEGER NOT NULL DEFAULT 0",
        "new_level": "INTEGER NOT NULL DEFAULT 0",
        "previous_value": "REAL NOT NULL DEFAULT 0",
        "new_value": "REAL NOT NULL DEFAULT 0",
        "change_reason": "TEXT NOT NULL DEFAULT ''",
        "confidence": "REAL DEFAULT 0",
        "created_at": "DATETIME",
    },
    "notification_histories": {
        "label_id": "INTEGER NOT NULL DEFAULT 0",
        "notification_type": "TEXT NOT NULL DEFAULT 'new_species'",
        "last_sent": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "expires_at": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    },
    "alert_rules": {
        "name": "TEXT NOT NULL DEFAULT ''",
        "description": "TEXT DEFAULT ''",
        "name_key": "TEXT DEFAULT ''",
        "description_key": "TEXT DEFAULT ''",
        "enabled": "NUMERIC NOT NULL DEFAULT 0",
        "built_in": "NUMERIC NOT NULL DEFAULT 0",
        "object_type": "TEXT NOT NULL DEFAULT ''",
        "trigger_type": "TEXT NOT NULL DEFAULT ''",
        "event_name": "TEXT DEFAULT ''",
        "metric_name": "TEXT DEFAULT ''",
        "cooldown_sec": "INTEGER NOT NULL DEFAULT 300",
        "escalation_steps": "TEXT",
        "created_at": "DATETIME",
        "updated_at": "DATETIME",
    },
    "alert_conditions": {
        "rule_id": "INTEGER NOT NULL DEFAULT 0",
        "property": "TEXT NOT NULL DEFAULT ''",
        "operator": "TEXT NOT NULL DEFAULT ''",
        "value": "TEXT NOT NULL DEFAULT ''",
        "duration_sec": "INTEGER DEFAULT 0",
        "sort_order": "INTEGER DEFAULT 0",
    },
    "alert_actions": {
        "rule_id": "INTEGER NOT NULL DEFAULT 0",
        "target": "TEXT NOT NULL DEFAULT ''",
        "template_title": "TEXT DEFAULT ''",
        "template_message": "TEXT DEFAULT ''",
        "sort_order": "INTEGER DEFAULT 0",
    },
    "alert_histories": {
        "rule_id": "INTEGER NOT NULL DEFAULT 0",
        "fired_at": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "event_data": "TEXT",
        "actions": "TEXT",
        "created_at": "DATETIME",
    },
    "migration_states": {
        "state": "TEXT NOT NULL DEFAULT 'idle'",
        "current_phase": "TEXT NOT NULL DEFAULT ''",
        "phase_number": "INTEGER DEFAULT 0",
        "total_phases": "INTEGER DEFAULT 0",
        "started_at": "DATETIME",
        "phase_started_at": "DATETIME",
        "completed_at": "DATETIME",
        "last_migrated_id": "INTEGER DEFAULT 0",
        "total_records": "INTEGER DEFAULT 0",
        "migrated_records": "INTEGER DEFAULT 0",
        "error_message": "TEXT",
        "related_data_error": "TEXT",
        "updated_at": "DATETIME",
    },
    "migration_dirty_ids": {
        "created_at": "DATETIME",
    },
    "daily_events": {
        "date": "TEXT NOT NULL DEFAULT ''",
        "sunrise": "INTEGER NOT NULL DEFAULT 0",
        "sunset": "INTEGER NOT NULL DEFAULT 0",
        "country": "TEXT NOT NULL DEFAULT ''",
        "city_name": "TEXT NOT NULL DEFAULT ''",
        "moon_phase": "REAL NOT NULL DEFAULT 0",
        "moon_illumination": "REAL NOT NULL DEFAULT 0",
    },
    "hourly_weathers": {
        "daily_events_id": "INTEGER NOT NULL DEFAULT 0",
        "time": "DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'",
        "temperature": "REAL NOT NULL DEFAULT 0",
        "feels_like": "REAL NOT NULL DEFAULT 0",
        "temp_min": "REAL NOT NULL DEFAULT 0",
        "temp_max": "REAL NOT NULL DEFAULT 0",
        "pressure": "INTEGER NOT NULL DEFAULT 0",
        "humidity": "INTEGER NOT NULL DEFAULT 0",
        "visibility": "INTEGER NOT NULL DEFAULT 0",
        "wind_speed": "REAL NOT NULL DEFAULT 0",
        "wind_deg": "INTEGER NOT NULL DEFAULT 0",
        "wind_gust": "REAL NOT NULL DEFAULT 0",
        "clouds": "INTEGER NOT NULL DEFAULT 0",
        "weather_main": "TEXT NOT NULL DEFAULT ''",
        "weather_desc": "TEXT NOT NULL DEFAULT ''",
        "weather_icon": "TEXT NOT NULL DEFAULT ''",
        "created_at": "DATETIME",
    },
    "app_metadata": {
        "value": "TEXT NOT NULL DEFAULT ''",
    },
}

# FK integrity checks: (child_table, child_column, parent_table, parent_column)
FK_CHECKS = [
    ("detections", "label_id", "labels", "id"),
    ("detections", "model_id", "ai_models", "id"),
    ("image_caches", "label_id", "labels", "id"),
    ("dynamic_thresholds", "label_id", "labels", "id"),
    ("notification_histories", "label_id", "labels", "id"),
    ("threshold_events", "label_id", "labels", "id"),
    ("detection_predictions", "detection_id", "detections", "id"),
    ("detection_reviews", "detection_id", "detections", "id"),
    ("detection_comments", "detection_id", "detections", "id"),
    ("detection_locks", "detection_id", "detections", "id"),
    ("detection_model_contributions", "detection_id", "detections", "id"),
    ("alert_conditions", "rule_id", "alert_rules", "id"),
    ("alert_actions", "rule_id", "alert_rules", "id"),
    ("alert_histories", "rule_id", "alert_rules", "id"),
]

# Valid migration table names (for SQL injection prevention)
VALID_MIGRATION_TABLES = {"migration_states", "migration_state"}


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------

@dataclass
class CheckResult:
    name: str
    status: str  # "pass", "fail", "warn", "skip"
    message: str
    details: list[str] = field(default_factory=list)
    fixable: bool = False
    data: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        result = {
            "name": self.name,
            "status": self.status,
            "message": self.message,
            "details": self.details,
            "fixable": self.fixable,
        }
        if self.data:
            result["data"] = self.data
        return result


@dataclass
class FixResult:
    name: str
    status: str  # "applied", "skipped", "failed"
    message: str
    rows_affected: int = 0
    details: list[str] = field(default_factory=list)

    def to_dict(self) -> dict:
        result = {
            "name": self.name,
            "status": self.status,
            "message": self.message,
            "rows_affected": self.rows_affected,
        }
        if self.details:
            result["details"] = self.details
        return result


@dataclass
class DiagnosticReport:
    db_path: str
    db_size: int = 0
    sqlite_version: str = ""
    python_version: str = ""
    platform_info: str = ""
    schema_version: str = "unknown"
    migration_status: Optional[str] = None
    migration_details: dict = field(default_factory=dict)
    checks: list[CheckResult] = field(default_factory=list)
    table_stats: dict[str, int] = field(default_factory=dict)
    tables_present: set[str] = field(default_factory=set)
    run_timestamp: str = ""

    def has_failures(self) -> bool:
        return any(c.status == "fail" for c in self.checks)

    def has_warnings(self) -> bool:
        return any(c.status == "warn" for c in self.checks)

    def to_dict(self) -> dict:
        return {
            "db_path": self.db_path,
            "db_size": self.db_size,
            "sqlite_version": self.sqlite_version,
            "python_version": self.python_version,
            "platform": self.platform_info,
            "schema_version": self.schema_version,
            "migration_status": self.migration_status,
            "migration_details": self.migration_details,
            "tables_present": sorted(self.tables_present),
            "table_stats": self.table_stats,
            "run_timestamp": self.run_timestamp,
            "checks": [c.to_dict() for c in self.checks],
        }


@dataclass
class FixReport:
    backup_path: Optional[str] = None
    fixes: list[FixResult] = field(default_factory=list)

    def has_failures(self) -> bool:
        return any(f.status == "failed" for f in self.fixes)

    def to_dict(self) -> dict:
        return {
            "backup_path": self.backup_path,
            "fixes": [f.to_dict() for f in self.fixes],
        }


# ---------------------------------------------------------------------------
# Database Doctor
# ---------------------------------------------------------------------------

class DatabaseDoctor:
    """Diagnoses and repairs BirdNET-Go SQLite databases."""

    def __init__(
        self,
        db_path: str,
        clips_dir: Optional[str] = None,
        verbose: bool = False,
    ):
        self.db_path = os.path.abspath(db_path)
        self.clips_dir = os.path.abspath(clips_dir) if clips_dir else None
        self.verbose = verbose

    def _log(self, msg: str) -> None:
        if self.verbose:
            print(f"  [DEBUG] {msg}")

    def _get_tables(self, conn: sqlite3.Connection) -> set[str]:
        cur = conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table' "
            "AND name NOT LIKE 'sqlite_%'"
        )
        return {row[0] for row in cur.fetchall()}

    def _get_columns(self, conn: sqlite3.Connection, table: str) -> list[str]:
        cur = conn.execute(f"PRAGMA table_info(`{table}`)")
        return [row[1] for row in cur.fetchall()]

    def _table_row_count(
        self, conn: sqlite3.Connection, table: str,
    ) -> Optional[int]:
        """Get row count for a table. Returns None on error."""
        try:
            cur = conn.execute(f"SELECT COUNT(*) FROM `{table}`")
            return cur.fetchone()[0]
        except sqlite3.Error as e:
            self._log(f"Row count query failed for {table}: {e}")
            return None

    def _resolve_migration_table(
        self, tables: set[str],
    ) -> Optional[str]:
        """Find the migration state table name (handles pre-PR #2165 rename)."""
        if "migration_states" in tables:
            return "migration_states"
        if "migration_state" in tables:
            return "migration_state"
        return None

    def _resolve_clip_table(self, tables: set[str]) -> Optional[str]:
        """Find the table that holds clip paths."""
        if "detections" in tables:
            return "detections"
        if "notes" in tables:
            return "notes"
        return None

    # ------------------------------------------------------------------
    # Diagnostic checks
    # ------------------------------------------------------------------

    def diagnose(self) -> DiagnosticReport:
        report = DiagnosticReport(
            db_path=self.db_path,
            run_timestamp=datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
            python_version=sys.version.split()[0],
            platform_info=f"{platform.system()} {platform.machine()}",
        )

        access_check = self._check_accessibility(report)
        report.checks.append(access_check)
        if access_check.status == "fail":
            return report

        try:
            conn = sqlite3.connect(
                f"file:{self.db_path}?mode=ro", uri=True
            )
        except sqlite3.Error as e:
            report.checks.append(CheckResult(
                name="Database open",
                status="fail",
                message=f"Cannot open database: {e}",
            ))
            return report

        try:
            report.tables_present = self._get_tables(conn)
            report.checks.append(self._check_schema_version(conn, report))
            report.checks.append(self._check_integrity(conn))

            if report.schema_version in ("v2", "mixed"):
                report.checks.append(self._check_schema_contamination(conn, report))
                report.checks.append(self._check_foreign_keys(conn, report))
            else:
                report.checks.append(CheckResult(
                    name="Schema contamination", status="skip",
                    message="Skipped (not a v2 database)",
                ))
                report.checks.append(CheckResult(
                    name="Foreign key integrity", status="skip",
                    message="Skipped (not a v2 database)",
                ))

            report.checks.append(self._check_migration_state(conn, report))
            report.checks.append(self._check_clip_paths(conn, report))
            self._collect_table_stats(conn, report)
        finally:
            conn.close()

        return report

    def _check_accessibility(self, report: DiagnosticReport) -> CheckResult:
        path = Path(self.db_path)

        if not path.exists():
            return CheckResult(
                name="Database accessibility", status="fail",
                message=f"File not found: {self.db_path}",
            )
        if not path.is_file():
            return CheckResult(
                name="Database accessibility", status="fail",
                message=f"Not a regular file: {self.db_path}",
            )
        if not os.access(self.db_path, os.R_OK):
            return CheckResult(
                name="Database accessibility", status="fail",
                message=f"File not readable: {self.db_path}",
            )

        try:
            with open(self.db_path, "rb") as f:
                header = f.read(SQLITE_HEADER_SIZE)
            if not header.startswith(SQLITE_MAGIC):
                return CheckResult(
                    name="Database accessibility", status="fail",
                    message="Not a valid SQLite database (bad magic bytes)",
                )
        except OSError as e:
            return CheckResult(
                name="Database accessibility", status="fail",
                message=f"Cannot read file: {e}",
            )

        report.db_size = path.stat().st_size
        report.sqlite_version = sqlite3.sqlite_version

        details = [
            f"Size: {_format_size(report.db_size)}",
            f"SQLite version: {report.sqlite_version}",
            f"Python: {report.python_version}",
            f"Platform: {report.platform_info}",
        ]

        try:
            conn = sqlite3.connect(
                f"file:{self.db_path}?mode=ro", uri=True
            )
            try:
                page_size = conn.execute("PRAGMA page_size").fetchone()[0]
                page_count = conn.execute("PRAGMA page_count").fetchone()[0]
                freelist = conn.execute("PRAGMA freelist_count").fetchone()[0]
                details.append(f"Page size: {page_size}")
                details.append(f"Pages: {page_count} ({freelist} free)")
            finally:
                conn.close()
        except sqlite3.Error as e:
            self._log(f"Page info query failed: {e}")

        return CheckResult(
            name="Database accessibility", status="pass",
            message="Database is accessible and valid",
            details=details,
        )

    def _check_schema_version(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> CheckResult:
        tables = report.tables_present
        has_legacy = bool(tables & LEGACY_V1_TABLES)
        has_v2 = bool(tables & V2_ONLY_TABLES)
        migration_table = self._resolve_migration_table(tables)

        details = []

        if has_v2 and not has_legacy:
            report.schema_version = "v2"
            details.append("V2 normalized schema detected")
        elif has_legacy and not has_v2:
            report.schema_version = "v1"
            details.append("Legacy v1 schema detected")
        elif has_legacy and has_v2:
            report.schema_version = "mixed"
            details.append("Both v1 and v2 tables present (mid-migration)")
        else:
            report.schema_version = "unknown"
            details.append("No recognized schema tables found")

        if migration_table and migration_table in VALID_MIGRATION_TABLES:
            try:
                cur = conn.execute(
                    f"SELECT * FROM `{migration_table}` LIMIT 1"
                )
                col_names = [desc[0] for desc in cur.description]
                row = cur.fetchone()
                if row:
                    mig_data = dict(zip(col_names, row))
                    report.migration_status = mig_data.get("state")
                    report.migration_details = {
                        k: v for k, v in mig_data.items() if v is not None
                    }
                    details.append(f"Migration status: {report.migration_status}")
                    if mig_data.get("completed_at"):
                        details.append(f"Completed at: {mig_data['completed_at']}")
                    if mig_data.get("last_migrated_id"):
                        details.append(
                            f"Last migrated ID: {mig_data['last_migrated_id']}"
                        )
                    if migration_table == "migration_state":
                        details.append(
                            "Note: pre-PR #2165 table name (singular)"
                        )
            except sqlite3.Error as e:
                details.append(f"Could not read migration state: {e}")

        for old_name, new_name in TABLE_RENAMES.items():
            if old_name in tables and new_name not in tables:
                details.append(
                    f"Found pre-PR #2165 table '{old_name}' "
                    f"(should be '{new_name}')"
                )

        v2_present = tables & set(V2_EXPECTED_COLUMNS.keys())
        details.append(
            f"V2 tables present: {len(v2_present)}/{len(V2_EXPECTED_COLUMNS)}"
        )

        # Read app_metadata for build info
        if "app_metadata" in tables:
            try:
                cur = conn.execute(
                    "SELECT key, value FROM app_metadata"
                )
                for key, value in cur.fetchall():
                    if value:
                        details.append(f"app_metadata.{key}: {value}")
            except sqlite3.Error as e:
                self._log(f"app_metadata query failed: {e}")

        return CheckResult(
            name="Schema version", status="pass",
            message=f"Schema: {report.schema_version}",
            details=details,
        )

    def _check_integrity(self, conn: sqlite3.Connection) -> CheckResult:
        self._log("Running PRAGMA quick_check...")
        try:
            if self.verbose:
                cur = conn.execute("PRAGMA integrity_check")
            else:
                cur = conn.execute("PRAGMA quick_check")
            results = [row[0] for row in cur.fetchall()]
        except sqlite3.Error as e:
            return CheckResult(
                name="SQLite integrity", status="fail",
                message=f"Integrity check failed: {e}",
                fixable=True,
            )

        if results == ["ok"]:
            return CheckResult(
                name="SQLite integrity", status="pass",
                message="Integrity check passed",
            )

        return CheckResult(
            name="SQLite integrity", status="fail",
            message=f"Integrity check found {len(results)} error(s)",
            details=results[:MAX_INTEGRITY_ERRORS_SHOWN],
            fixable=True,
        )

    def _check_schema_contamination(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> CheckResult:
        issues: list[str] = []
        tables = report.tables_present

        for table_name, expected_cols in V2_EXPECTED_COLUMNS.items():
            if table_name not in tables:
                continue

            actual_cols = self._get_columns(conn, table_name)
            expected_set = set(expected_cols)
            actual_set = set(actual_cols)

            extra = actual_set - expected_set
            missing = expected_set - actual_set

            if extra:
                row_count = self._table_row_count(conn, table_name)
                count_str = f"{row_count:,}" if row_count is not None else "?"
                issues.append(
                    f"{table_name}: unexpected column(s) "
                    f"{sorted(extra)} ({count_str} rows)"
                )
                issues.append(
                    f"  actual columns: {sorted(actual_set)}"
                )
                issues.append(
                    f"  expected columns: {sorted(expected_set)}"
                )

            if missing:
                issues.append(
                    f"{table_name}: missing column(s) {sorted(missing)}"
                )

        if not issues:
            return CheckResult(
                name="Schema contamination", status="pass",
                message="All V2 table schemas match expected definitions",
            )

        fixable_extra = any(
            table_name in V2_TABLE_SCHEMAS
            for table_name in V2_CONTAMINATION_COLUMNS
            if any(
                f"{table_name}: unexpected" in issue for issue in issues
            )
        )
        fixable_missing = any(
            any(f"{table_name}: missing" in issue for issue in issues)
            for table_name in V2_COLUMN_DEFS
        )

        return CheckResult(
            name="Schema contamination", status="fail",
            message="Schema issues found",
            details=issues,
            fixable=fixable_extra or fixable_missing,
        )

    def _check_foreign_keys(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> CheckResult:
        issues: list[str] = []
        errors: list[str] = []
        tables = report.tables_present

        for child, child_col, parent, parent_col in FK_CHECKS:
            if child not in tables or parent not in tables:
                continue

            try:
                cur = conn.execute(
                    f"SELECT COUNT(*) FROM `{child}` "
                    f"WHERE `{child_col}` IS NOT NULL "
                    f"AND `{child_col}` NOT IN "
                    f"(SELECT `{parent_col}` FROM `{parent}`)"
                )
                count = cur.fetchone()[0]
                if count > 0:
                    issues.append(
                        f"{child}.{child_col} -> {parent}.{parent_col}: "
                        f"{count:,} orphaned reference(s)"
                    )
            except sqlite3.Error as e:
                errors.append(f"FK check failed for {child}.{child_col}: {e}")

        all_details = issues + errors

        if not issues and not errors:
            return CheckResult(
                name="Foreign key integrity", status="pass",
                message="All foreign key references are valid",
            )

        if errors and not issues:
            return CheckResult(
                name="Foreign key integrity", status="warn",
                message=f"Some FK checks could not run",
                details=all_details,
            )

        fixable = any("detections.label_id" in i for i in issues)

        return CheckResult(
            name="Foreign key integrity", status="warn",
            message=f"Found {len(issues)} foreign key violation(s)",
            details=all_details,
            fixable=fixable,
        )

    def _check_migration_state(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> CheckResult:
        tables = report.tables_present
        migration_table = self._resolve_migration_table(tables)

        if not migration_table:
            if report.schema_version == "v1":
                return CheckResult(
                    name="Migration state", status="pass",
                    message="No migration state table (legacy v1 database)",
                )
            return CheckResult(
                name="Migration state", status="skip",
                message="No migration state table found",
            )

        if migration_table not in VALID_MIGRATION_TABLES:
            return CheckResult(
                name="Migration state", status="warn",
                message=f"Unexpected migration table name: {migration_table}",
            )

        try:
            cur = conn.execute(
                f"SELECT state, last_migrated_id, total_records, "
                f"migrated_records, error_message "
                f"FROM `{migration_table}` LIMIT 1"
            )
            row = cur.fetchone()
        except sqlite3.Error as e:
            return CheckResult(
                name="Migration state", status="warn",
                message=f"Cannot read migration state: {e}",
            )

        if not row:
            return CheckResult(
                name="Migration state", status="warn",
                message="Migration state table is empty",
                fixable=True,
            )

        state, last_migrated, total, migrated, error_msg = row
        details = [
            f"State: {state}",
            f"Last migrated ID: {last_migrated}",
            f"Records: {migrated}/{total}",
        ]

        if state not in KNOWN_MIGRATION_STATES:
            return CheckResult(
                name="Migration state", status="fail",
                message=f"Unknown migration state: {state}",
                details=details, fixable=True,
            )

        if state in ACTIVE_MIGRATION_STATES:
            details.append(f"Migration appears stuck in '{state}'")
            if error_msg:
                details.append(f"Error: {error_msg}")
            return CheckResult(
                name="Migration state", status="warn",
                message=f"Migration stuck in active state: {state}",
                details=details, fixable=True,
            )

        if state == "failed":
            details.append(f"Error: {error_msg or '(no message)'}")
            return CheckResult(
                name="Migration state", status="warn",
                message="Migration in failed state",
                details=details, fixable=True,
            )

        if state == "completed":
            if "detections" in tables:
                det_count = self._table_row_count(conn, "detections")
                if det_count is not None:
                    details.append(f"Detections: {det_count:,}")

                if "notes" in tables:
                    note_count = self._table_row_count(conn, "notes")
                    if note_count is not None:
                        details.append(f"Legacy notes still present: {note_count:,}")
                        if note_count > 0 and (det_count is not None and det_count == 0):
                            details.append(
                                "WARNING: detections empty but notes has data"
                            )
                            return CheckResult(
                                name="Migration state", status="warn",
                                message="Migration marked complete but "
                                        "detections table is empty",
                                details=details, fixable=True,
                            )

            if "migration_dirty_ids" in tables:
                dirty = self._table_row_count(conn, "migration_dirty_ids")
                if dirty is not None and dirty > 0:
                    details.append(
                        f"Stale migration_dirty_ids: {dirty:,} entries"
                    )

        return CheckResult(
            name="Migration state", status="pass",
            message=f"Migration state: {state}",
            details=details,
        )

    def _check_clip_paths(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> CheckResult:
        tables = report.tables_present
        clip_table = self._resolve_clip_table(tables)

        if not clip_table:
            return CheckResult(
                name="Clip path extensions", status="skip",
                message="No detection table found",
            )

        stripped_count = 0
        try:
            cur = conn.execute(
                f"SELECT COUNT(*) FROM `{clip_table}` "
                f"WHERE clip_name IS NOT NULL AND clip_name LIKE '%.'"
            )
            stripped_count = cur.fetchone()[0]
        except sqlite3.Error as e:
            return CheckResult(
                name="Clip path extensions", status="warn",
                message=f"Could not check clip paths in {clip_table}: {e}",
            )

        if stripped_count > 0:
            details = [
                f"{stripped_count:,} clip path(s) end with '.' "
                f"(missing extension) in {clip_table}"
            ]
            if self.clips_dir and os.path.isdir(self.clips_dir):
                details.append(
                    f"Clips directory provided: {self.clips_dir}"
                )

            return CheckResult(
                name="Clip path extensions", status="warn",
                message=f"{stripped_count:,} clip path(s) with stripped extensions",
                details=details,
                fixable=self.clips_dir is not None,
                data={"count": stripped_count, "table": clip_table},
            )

        return CheckResult(
            name="Clip path extensions", status="pass",
            message="All clip paths have valid extensions",
        )

    def _collect_table_stats(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> None:
        """Collect table row counts into report.table_stats."""
        tables = report.tables_present

        stat_tables = [
            "detections", "notes", "labels", "ai_models",
            "image_caches", "dynamic_thresholds", "threshold_events",
            "notification_histories", "alert_rules", "daily_events",
            "hourly_weathers", "detection_reviews", "detection_predictions",
            "detection_comments", "detection_locks", "audio_sources",
            "results", "note_reviews",
        ]

        for table in stat_tables:
            if table in tables:
                count = self._table_row_count(conn, table)
                if count is not None:
                    report.table_stats[table] = count

        # Date range for detections
        if "detections" in tables:
            try:
                cur = conn.execute(
                    "SELECT MIN(detected_at), MAX(detected_at) "
                    "FROM detections"
                )
                row = cur.fetchone()
                if row and row[0] is not None:
                    try:
                        min_ts = datetime.fromtimestamp(row[0])
                        max_ts = datetime.fromtimestamp(row[1])
                        report.table_stats["_detection_range"] = (
                            f"{min_ts:%Y-%m-%d} to {max_ts:%Y-%m-%d}"
                        )
                    except (TypeError, ValueError, OSError):
                        report.table_stats["_detection_range"] = (
                            f"{row[0]} to {row[1]}"
                        )
            except sqlite3.Error as e:
                self._log(f"Detection date range query failed: {e}")
        elif "notes" in tables:
            try:
                cur = conn.execute(
                    "SELECT MIN(date), MAX(date) FROM notes"
                )
                row = cur.fetchone()
                if row and row[0]:
                    report.table_stats["_detection_range"] = (
                        f"{row[0]} to {row[1]}"
                    )
            except sqlite3.Error as e:
                self._log(f"Notes date range query failed: {e}")

        if "labels" in tables:
            try:
                cur = conn.execute(
                    "SELECT COUNT(DISTINCT scientific_name) FROM labels"
                )
                report.table_stats["_distinct_species"] = cur.fetchone()[0]
            except sqlite3.Error as e:
                self._log(f"Distinct species query failed: {e}")

    # ------------------------------------------------------------------
    # Fix operations
    # ------------------------------------------------------------------

    def fix(
        self,
        report: DiagnosticReport,
        no_backup: bool = False,
        only: Optional[list[str]] = None,
    ) -> FixReport:
        fix_report = FixReport()

        # Create backup first (before acquiring lock, so backup is clean)
        if not no_backup:
            backup_result, backup_path = self._create_backup()
            fix_report.fixes.append(backup_result)
            if backup_result.status == "failed":
                return fix_report
            fix_report.backup_path = backup_path
        else:
            fix_report.fixes.append(FixResult(
                name="Backup", status="skipped",
                message="Backup skipped (--no-backup)",
            ))

        # Open database for writing and hold the connection for all fixes.
        conn = None
        try:
            conn = sqlite3.connect(self.db_path, timeout=5)
            conn.execute("PRAGMA journal_mode=WAL")
            conn.execute("PRAGMA foreign_keys=OFF")
        except sqlite3.OperationalError as e:
            if conn is not None:
                conn.close()
            err_lower = str(e).lower()
            if "locked" in err_lower or "busy" in err_lower:
                fix_report.fixes.append(FixResult(
                    name="Lock check", status="failed",
                    message="Database is locked. Stop BirdNET-Go before "
                            "running fixes.",
                ))
                return fix_report
            if "readonly" in err_lower:
                fix_report.fixes.append(FixResult(
                    name="Lock check", status="failed",
                    message="Database file is read-only. Check file permissions.",
                ))
                return fix_report
            fix_report.fixes.append(FixResult(
                name="Database open", status="failed",
                message=f"Cannot open database for writing: {e}",
            ))
            return fix_report
        except sqlite3.Error as e:
            if conn is not None:
                conn.close()
            fix_report.fixes.append(FixResult(
                name="Database open", status="failed",
                message=f"Cannot open database for writing: {e}",
            ))
            return fix_report

        try:
            if self._should_fix("indexes", only):
                fix_report.fixes.append(self._fix_reindex(conn, report))

            if self._should_fix("schema", only):
                fix_report.fixes.append(
                    self._fix_schema_contamination(conn, report)
                )

            if self._should_fix("migration", only):
                fix_report.fixes.append(
                    self._fix_migration_state(conn, report)
                )

            if self._should_fix("clips", only):
                fix_report.fixes.append(
                    self._fix_clip_extensions(conn, report)
                )

            if self._should_fix("labels", only):
                fix_report.fixes.append(
                    self._fix_orphaned_labels(conn, report)
                )
        finally:
            conn.close()

        return fix_report

    def _should_fix(
        self, fix_name: str, only: Optional[list[str]],
    ) -> bool:
        """Check if a fix category is enabled by the --only filter."""
        if only is not None:
            return fix_name in only
        return True

    def _create_backup(self) -> tuple[FixResult, Optional[str]]:
        timestamp = datetime.now().strftime("%Y%m%d-%H%M%S")
        backup_path = f"{self.db_path}.{timestamp}.doctor-backup"

        db_size = os.path.getsize(self.db_path)
        try:
            free_space = shutil.disk_usage(os.path.dirname(self.db_path)).free
            if free_space < db_size * BACKUP_DISK_HEADROOM:
                return (
                    FixResult(
                        name="Backup", status="failed",
                        message=f"Insufficient disk space for backup. "
                                f"Need {_format_size(int(db_size * BACKUP_DISK_HEADROOM))}, "
                                f"have {_format_size(free_space)}. "
                                f"Use --no-backup to skip.",
                    ),
                    None,
                )
        except OSError:
            pass

        # Use sqlite3 backup API for a consistent snapshot.
        # Each connection is guarded independently so a failure opening
        # the destination does not leak the source connection.
        backup_error = None
        src_conn = None
        dst_conn = None
        try:
            src_conn = sqlite3.connect(self.db_path)
            dst_conn = sqlite3.connect(backup_path)
            src_conn.backup(dst_conn)
        except sqlite3.Error as e:
            backup_error = e
        finally:
            if dst_conn is not None:
                dst_conn.close()
            if src_conn is not None:
                src_conn.close()

        if backup_error is not None:
            self._cleanup_failed_backup(backup_path)
            return (
                FixResult(
                    name="Backup", status="failed",
                    message=f"Backup failed: {backup_error}",
                ),
                None,
            )

        # Verify backup
        verify_conn = None
        try:
            verify_conn = sqlite3.connect(
                f"file:{backup_path}?mode=ro", uri=True
            )
            result = verify_conn.execute("PRAGMA quick_check").fetchone()[0]
            if result != "ok":
                self._cleanup_failed_backup(backup_path)
                return (
                    FixResult(
                        name="Backup", status="failed",
                        message=f"Backup verification failed: {result}",
                    ),
                    None,
                )
        except sqlite3.Error as e:
            self._cleanup_failed_backup(backup_path)
            return (
                FixResult(
                    name="Backup", status="failed",
                    message=f"Backup verification failed: {e}",
                ),
                None,
            )
        finally:
            if verify_conn is not None:
                verify_conn.close()

        return (
            FixResult(
                name="Backup", status="applied",
                message=f"Backup created: {backup_path}",
            ),
            backup_path,
        )

    @staticmethod
    def _cleanup_failed_backup(backup_path: str) -> None:
        for path in (backup_path, backup_path + "-wal", backup_path + "-shm"):
            try:
                os.remove(path)
            except OSError:
                pass

    def _fix_reindex(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> FixResult:
        integrity_check = next(
            (c for c in report.checks if c.name == "SQLite integrity"), None
        )
        if integrity_check and integrity_check.status == "pass":
            return FixResult(
                name="REINDEX", status="skipped",
                message="Integrity check passed, REINDEX not needed",
            )

        self._log("Running REINDEX...")
        try:
            conn.execute("REINDEX")
            return FixResult(
                name="REINDEX", status="applied",
                message="All indexes rebuilt successfully",
            )
        except sqlite3.Error as e:
            return FixResult(
                name="REINDEX", status="failed",
                message=f"REINDEX failed: {e}",
            )

    def _fix_schema_contamination(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> FixResult:
        schema_check = next(
            (c for c in report.checks if c.name == "Schema contamination"),
            None,
        )
        if not schema_check or schema_check.status != "fail":
            return FixResult(
                name="Schema repair", status="skipped",
                message="No schema contamination detected",
            )

        tables_fixed = 0
        tables_failed = 0
        fix_details: list[str] = []

        for table_name, contam_cols in V2_CONTAMINATION_COLUMNS.items():
            try:
                actual_cols = self._get_columns(conn, table_name)
            except sqlite3.Error:
                continue

            actual_set = set(actual_cols)
            contam_present = [c for c in contam_cols if c in actual_set]
            if not contam_present:
                continue

            if table_name not in V2_TABLE_SCHEMAS:
                fix_details.append(
                    f"{table_name}: no recreation template, skipped"
                )
                continue

            expected = set(V2_EXPECTED_COLUMNS.get(table_name, []))
            if "label_id" in expected and "label_id" not in actual_set:
                fix_details.append(
                    f"{table_name}: label_id column missing, "
                    "cannot recreate safely"
                )
                continue

            row_count = self._table_row_count(conn, table_name) or 0

            # Table recreation (including label recovery) in a single transaction
            create_sql, index_sqls = V2_TABLE_SCHEMAS[table_name]
            new_cols = set(V2_EXPECTED_COLUMNS[table_name])
            copy_cols = sorted(actual_set & new_cols)

            if not copy_cols:
                fix_details.append(
                    f"{table_name}: no common columns for data copy, skipped"
                )
                continue

            col_list = ", ".join(f"`{c}`" for c in copy_cols)
            self._log(
                f"Recreating {table_name}: copying columns {copy_cols}"
            )

            try:
                conn.execute("BEGIN IMMEDIATE")

                # Recover label_ids from contamination columns before recreation
                if (
                    row_count > 0
                    and "label_id" in actual_set
                    and "labels" in report.tables_present
                ):
                    recovered = self._try_recover_label_ids_in_txn(
                        conn, table_name, contam_present
                    )
                    if recovered > 0:
                        fix_details.append(
                            f"{table_name}: recovered {recovered} "
                            "label_id(s) from legacy columns"
                        )

                conn.execute(create_sql)
                conn.execute(
                    f"INSERT INTO `{table_name}_new` ({col_list}) "
                    f"SELECT {col_list} FROM `{table_name}`"
                )
                conn.execute(f"DROP TABLE `{table_name}`")
                conn.execute(
                    f"ALTER TABLE `{table_name}_new` "
                    f"RENAME TO `{table_name}`"
                )
                for idx_sql in index_sqls:
                    conn.execute(idx_sql)

                conn.execute("COMMIT")
                tables_fixed += 1
                fix_details.append(
                    f"{table_name}: recreated ({row_count} rows preserved, "
                    f"removed: {contam_present}, kept: {copy_cols})"
                )

            except sqlite3.Error as e:
                try:
                    conn.execute("ROLLBACK")
                except sqlite3.Error:
                    pass
                tables_failed += 1
                fix_details.append(f"{table_name}: FAILED: {e}")

        # Fix missing columns via ALTER TABLE ADD COLUMN.
        # This is fast and safe (existing rows get the default value).
        columns_added = 0
        columns_failed = 0
        for table_name, expected_cols in V2_EXPECTED_COLUMNS.items():
            if table_name not in report.tables_present:
                continue

            try:
                actual_cols = self._get_columns(conn, table_name)
            except sqlite3.Error:
                continue

            missing = set(expected_cols) - set(actual_cols)
            if not missing:
                continue

            table_defs = V2_COLUMN_DEFS.get(table_name, {})
            for col in sorted(missing):
                if col not in table_defs:
                    fix_details.append(
                        f"{table_name}: cannot add `{col}` (no definition)"
                    )
                    columns_failed += 1
                    continue

                col_def = table_defs[col]
                try:
                    conn.execute(
                        f"ALTER TABLE `{table_name}` "
                        f"ADD COLUMN `{col}` {col_def}"
                    )
                    columns_added += 1
                    fix_details.append(
                        f"{table_name}: added missing column "
                        f"`{col}` ({col_def})"
                    )
                except sqlite3.Error as e:
                    columns_failed += 1
                    fix_details.append(
                        f"{table_name}: FAILED to add `{col}`: {e}"
                    )

        total_fixed = tables_fixed + columns_added
        total_failed = tables_failed + columns_failed

        if total_fixed == 0 and total_failed == 0:
            return FixResult(
                name="Schema repair", status="skipped",
                message="No fixable schema issues found",
                details=fix_details,
            )

        if total_failed > 0 and total_fixed == 0:
            status = "failed"
        else:
            status = "applied"

        parts = []
        if tables_fixed:
            parts.append(f"{tables_fixed} table(s) recreated")
        if columns_added:
            parts.append(f"{columns_added} column(s) added")
        if total_failed:
            parts.append(f"{total_failed} failed")

        return FixResult(
            name="Schema repair", status=status,
            message=", ".join(parts),
            rows_affected=total_fixed,
            details=fix_details,
        )

    def _try_recover_label_ids_in_txn(
        self,
        conn: sqlite3.Connection,
        table_name: str,
        contam_cols: list[str],
    ) -> int:
        """Recover label_ids from contamination columns. Must be called
        inside an existing transaction."""
        sci_col = "scientific_name" if "scientific_name" in contam_cols else None
        if not sci_col:
            return 0

        try:
            cur = conn.execute(
                f"UPDATE `{table_name}` SET label_id = ("
                f"  SELECT l.id FROM labels l "
                f"  WHERE l.scientific_name = `{table_name}`.`{sci_col}` "
                f"  LIMIT 1"
                f") WHERE (label_id = 0 OR label_id IS NULL) "
                f"AND `{sci_col}` IS NOT NULL AND `{sci_col}` != '' "
                f"AND EXISTS ("
                f"  SELECT 1 FROM labels l "
                f"  WHERE l.scientific_name = `{table_name}`.`{sci_col}`"
                f")"
            )
            return cur.rowcount
        except sqlite3.Error as e:
            self._log(f"Label recovery failed for {table_name}: {e}")
            return 0

    def _fix_migration_state(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> FixResult:
        migration_check = next(
            (c for c in report.checks if c.name == "Migration state"), None
        )
        if not migration_check or migration_check.status == "pass":
            return FixResult(
                name="Migration state", status="skipped",
                message="Migration state is healthy",
            )

        tables = report.tables_present
        migration_table = self._resolve_migration_table(tables)
        if not migration_table or migration_table not in VALID_MIGRATION_TABLES:
            return FixResult(
                name="Migration state", status="skipped",
                message="No valid migration state table found",
            )

        has_detections = "detections" in tables
        has_notes = "notes" in tables

        det_count = self._table_row_count(conn, "detections") if has_detections else 0
        note_count = self._table_row_count(conn, "notes") if has_notes else 0
        det_count = det_count or 0
        note_count = note_count or 0

        # Most specific case first: both tables have data
        if det_count > 0 and note_count > 0:
            target_state = "completed"
            reason = (
                f"both tables have data (detections: {det_count:,}, "
                f"notes: {note_count:,}), setting to completed"
            )
        elif det_count > 0:
            target_state = "completed"
            reason = (
                f"detections table has {det_count:,} rows, "
                "setting state to completed"
            )
        elif note_count > 0 and det_count == 0:
            target_state = "idle"
            reason = (
                "detections empty but notes has data, "
                "resetting to idle for re-migration"
            )
        else:
            target_state = "idle"
            reason = "no detection data found, resetting to idle"

        try:
            conn.execute("BEGIN IMMEDIATE")
            conn.execute(
                f"UPDATE `{migration_table}` SET state = ?, "
                f"error_message = '', updated_at = CURRENT_TIMESTAMP",
                (target_state,),
            )
            conn.execute("COMMIT")
            return FixResult(
                name="Migration state", status="applied",
                message=f"State set to '{target_state}': {reason}",
            )
        except sqlite3.Error as e:
            try:
                conn.execute("ROLLBACK")
            except sqlite3.Error:
                pass
            return FixResult(
                name="Migration state", status="failed",
                message=f"Failed to update migration state: {e}",
            )

    def _fix_clip_extensions(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> FixResult:
        clip_check = next(
            (c for c in report.checks if c.name == "Clip path extensions"),
            None,
        )
        if not clip_check or clip_check.status == "pass":
            return FixResult(
                name="Clip extensions", status="skipped",
                message="No clip extension issues found",
            )

        if not self.clips_dir or not os.path.isdir(self.clips_dir):
            return FixResult(
                name="Clip extensions", status="skipped",
                message="No --clips-dir provided, cannot repair extensions",
            )

        clip_table = clip_check.data.get("table")
        if not clip_table:
            clip_table = self._resolve_clip_table(report.tables_present)
        if not clip_table:
            return FixResult(
                name="Clip extensions", status="skipped",
                message="No detection table found",
            )

        fixed = 0
        skipped = 0
        updates: list[tuple[str, int]] = []

        try:
            cur = conn.execute(
                f"SELECT id, clip_name FROM `{clip_table}` "
                f"WHERE clip_name IS NOT NULL AND clip_name LIKE '%.'"
            )
            for row_id, clip_name in cur:
                base = clip_name.rstrip(".")
                if not base:
                    skipped += 1
                    continue

                search_dir = os.path.join(
                    self.clips_dir, os.path.dirname(base)
                )
                basename = os.path.basename(base)

                if not os.path.isdir(search_dir):
                    skipped += 1
                    continue

                matches = []
                try:
                    for entry in os.scandir(search_dir):
                        if not entry.is_file():
                            continue
                        stem, ext = os.path.splitext(entry.name)
                        if stem == basename and ext:
                            matches.append(entry.name)
                except OSError:
                    skipped += 1
                    continue

                if len(matches) == 1:
                    new_path = os.path.join(
                        os.path.dirname(base), matches[0]
                    )
                    updates.append((new_path, row_id))
                    fixed += 1
                else:
                    skipped += 1
        except sqlite3.Error as e:
            return FixResult(
                name="Clip extensions", status="failed",
                message=f"Query failed: {e}",
            )

        if updates:
            try:
                conn.execute("BEGIN IMMEDIATE")
                conn.executemany(
                    f"UPDATE `{clip_table}` SET clip_name = ? WHERE id = ?",
                    updates,
                )
                conn.execute("COMMIT")
            except sqlite3.Error as e:
                try:
                    conn.execute("ROLLBACK")
                except sqlite3.Error:
                    pass
                return FixResult(
                    name="Clip extensions", status="failed",
                    message=f"Update failed: {e}",
                )

        return FixResult(
            name="Clip extensions",
            status="applied" if fixed > 0 else "skipped",
            message=f"Fixed {fixed:,} clip path(s), skipped {skipped:,}",
            rows_affected=fixed,
        )

    def _fix_orphaned_labels(
        self, conn: sqlite3.Connection, report: DiagnosticReport,
    ) -> FixResult:
        fk_check = next(
            (c for c in report.checks if c.name == "Foreign key integrity"),
            None,
        )
        if not fk_check or fk_check.status == "pass":
            return FixResult(
                name="Orphaned labels", status="skipped",
                message="No orphaned label references found",
            )

        tables = report.tables_present
        if "detections" not in tables or "labels" not in tables:
            return FixResult(
                name="Orphaned labels", status="skipped",
                message="Required tables not present",
            )

        try:
            cur = conn.execute(
                "SELECT COUNT(*) FROM detections "
                "WHERE label_id NOT IN (SELECT id FROM labels)"
            )
            orphan_count = cur.fetchone()[0]
        except sqlite3.Error as e:
            return FixResult(
                name="Orphaned labels", status="failed",
                message=f"Query failed: {e}",
            )

        if orphan_count == 0:
            return FixResult(
                name="Orphaned labels", status="skipped",
                message="No orphaned detection label references",
            )

        recovered = 0
        if "notes" in tables:
            try:
                conn.execute("BEGIN IMMEDIATE")
                # Only update rows where a matching label actually exists
                # (prevents setting label_id to NULL)
                cur = conn.execute(
                    "UPDATE detections SET label_id = ("
                    "  SELECT l.id FROM labels l"
                    "  JOIN notes n ON n.id = detections.legacy_id"
                    "  WHERE l.scientific_name = n.scientific_name"
                    "  LIMIT 1"
                    ") WHERE label_id NOT IN (SELECT id FROM labels)"
                    "  AND legacy_id IS NOT NULL"
                    "  AND legacy_id IN (SELECT id FROM notes)"
                    "  AND EXISTS ("
                    "    SELECT 1 FROM labels l"
                    "    JOIN notes n ON n.id = detections.legacy_id"
                    "    WHERE l.scientific_name = n.scientific_name"
                    "  )"
                )
                recovered = cur.rowcount
                conn.execute("COMMIT")
            except sqlite3.Error as e:
                try:
                    conn.execute("ROLLBACK")
                except sqlite3.Error:
                    pass
                self._log(f"Label recovery via legacy_id failed: {e}")

        remaining = orphan_count - recovered
        msg_parts = []
        if recovered > 0:
            msg_parts.append(f"recovered {recovered:,} via legacy notes")
        if remaining > 0:
            msg_parts.append(
                f"{remaining:,} still orphaned (manual intervention needed)"
            )

        return FixResult(
            name="Orphaned labels",
            status="applied" if recovered > 0 else "skipped",
            message="; ".join(msg_parts) if msg_parts else "No recovery possible",
            rows_affected=recovered,
        )


# ---------------------------------------------------------------------------
# Formatting helpers
# ---------------------------------------------------------------------------

def _format_size(size_bytes: int) -> str:
    value = float(size_bytes)
    for unit in ("B", "KB", "MB", "GB"):
        if abs(value) < 1024:
            return f"{value:.1f} {unit}"
        value /= 1024
    return f"{value:.1f} TB"


def _status_icon(status: str) -> str:
    return {
        "pass": "PASS",
        "fail": "FAIL",
        "warn": "WARN",
        "skip": "SKIP",
        "applied": " OK ",
        "skipped": "SKIP",
        "failed": "FAIL",
    }.get(status, "????")


# ---------------------------------------------------------------------------
# Output formatting
# ---------------------------------------------------------------------------

def print_report(report: DiagnosticReport) -> None:
    print(f"\nBirdNET-Go Database Doctor v{SCRIPT_VERSION}")
    print(f"Schema definition: {SCHEMA_VERSION}")
    print(f"Run at: {report.run_timestamp}")
    print()
    print(f"Database: {report.db_path}")
    if report.db_size:
        print(f"Size:     {_format_size(report.db_size)}")
    if report.sqlite_version:
        print(f"SQLite:   {report.sqlite_version}")
    if report.python_version:
        print(f"Python:   {report.python_version}")
    if report.platform_info:
        print(f"Platform: {report.platform_info}")
    print(f"Schema:   {report.schema_version}", end="")
    if report.migration_status:
        print(f" (migration: {report.migration_status})", end="")
    print()

    # Compact table fingerprint (always shown)
    if report.table_stats:
        print()
        print("Database fingerprint:")
        # Primary data
        for key in ("detections", "notes", "labels", "ai_models", "audio_sources"):
            if key in report.table_stats:
                print(f"  {key}: {report.table_stats[key]:,}")
        # Detection range
        if "_detection_range" in report.table_stats:
            print(f"  date range: {report.table_stats['_detection_range']}")
        if "_distinct_species" in report.table_stats:
            print(f"  distinct species: {report.table_stats['_distinct_species']:,}")
        # Auxiliary
        aux_tables = [
            "image_caches", "dynamic_thresholds", "notification_histories",
            "alert_rules", "detection_reviews", "detection_predictions",
        ]
        aux_parts = []
        for key in aux_tables:
            if key in report.table_stats:
                short = key.replace("detection_", "").replace("_", "-")
                aux_parts.append(f"{short}={report.table_stats[key]:,}")
        if aux_parts:
            print(f"  aux: {', '.join(aux_parts)}")

    # Migration details (always shown if available)
    if report.migration_details:
        relevant_keys = [
            "state", "started_at", "completed_at", "last_migrated_id",
            "total_records", "migrated_records", "error_message",
        ]
        shown = {
            k: v for k, v in report.migration_details.items()
            if k in relevant_keys and v
        }
        if shown:
            print()
            print("Migration state:")
            for k, v in shown.items():
                print(f"  {k}: {v}")

    print()
    print("Checks:")
    for check in report.checks:
        print(f"  [{_status_icon(check.status)}] {check.name}")
        if check.status != "pass" and check.message:
            print(f"         {check.message}")
        if check.details and check.status in ("fail", "warn"):
            for detail in check.details:
                print(f"         {detail}")

    print()

    passes = sum(1 for c in report.checks if c.status == "pass")
    failures = sum(1 for c in report.checks if c.status == "fail")
    warnings = sum(1 for c in report.checks if c.status == "warn")
    skips = sum(1 for c in report.checks if c.status == "skip")

    parts = []
    if failures:
        parts.append(f"{failures} failure(s)")
    if warnings:
        parts.append(f"{warnings} warning(s)")
    parts.append(f"{passes} passed")
    if skips:
        parts.append(f"{skips} skipped")
    print(f"Summary: {', '.join(parts)}")

    fixable = [c for c in report.checks if c.fixable and c.status != "pass"]
    if fixable:
        print()
        print("Run with --fix to repair fixable issues.")

    print()
    print("-- Copy everything above when reporting issues --")


def print_fix_report(fix_report: FixReport) -> None:
    print()
    print("Fixes:")
    for fix in fix_report.fixes:
        print(f"  [{_status_icon(fix.status)}] {fix.name}")
        if fix.message:
            print(f"         {fix.message}")
        if fix.details:
            for detail in fix.details:
                print(f"         {detail}")

    print()
    applied = sum(1 for f in fix_report.fixes if f.status == "applied")
    failed = sum(1 for f in fix_report.fixes if f.status == "failed")
    skipped = sum(1 for f in fix_report.fixes if f.status == "skipped")

    parts = []
    if applied:
        parts.append(f"{applied} applied")
    if failed:
        parts.append(f"{failed} failed")
    if skipped:
        parts.append(f"{skipped} skipped")
    print(f"Fix summary: {', '.join(parts)}")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main() -> int:
    parser = argparse.ArgumentParser(
        description="BirdNET-Go Database Doctor - diagnose and repair "
                    "SQLite database issues",
        epilog="Example: python3 db-doctor.py /data/birdnet.db --fix",
    )
    parser.add_argument(
        "database",
        help="Path to the BirdNET-Go SQLite database file",
    )
    parser.add_argument(
        "--fix", action="store_true",
        help="Apply fixes for detected issues (default: diagnose only)",
    )
    parser.add_argument(
        "--verbose", "-v", action="store_true",
        help="Show detailed output including SQL queries",
    )
    parser.add_argument(
        "--json", action="store_true", dest="json_output",
        help="Output results as JSON",
    )
    parser.add_argument(
        "--clips-dir",
        help="Path to clips directory for extension repair",
    )
    parser.add_argument(
        "--no-backup", action="store_true",
        help="Skip backup before applying fixes",
    )
    parser.add_argument(
        "--only",
        help="Comma-separated list of fixes to apply "
             "(schema, indexes, migration, clips, labels)",
    )
    parser.add_argument(
        "--check-schema-version", action="store_true",
        help="Print the schema version this script was built for and exit",
    )
    parser.add_argument(
        "--version", action="version",
        version=f"db-doctor {SCRIPT_VERSION} (schema: {SCHEMA_VERSION})",
    )

    args = parser.parse_args()

    if args.check_schema_version:
        print(f"Schema version: {SCHEMA_VERSION}")
        return 0

    db_path = args.database
    if not os.path.exists(db_path):
        if not args.json_output:
            print(f"Error: database file not found: {db_path}", file=sys.stderr)
        else:
            json.dump({"error": f"File not found: {db_path}"}, sys.stdout)
        return 2

    only = None
    if args.only:
        valid_fixes = {"schema", "indexes", "migration", "clips", "labels"}
        only = [f.strip() for f in args.only.split(",")]
        invalid = set(only) - valid_fixes
        if invalid:
            msg = f"Unknown fix name(s): {invalid}. Valid: {valid_fixes}"
            if not args.json_output:
                print(f"Error: {msg}", file=sys.stderr)
            else:
                json.dump({"error": msg}, sys.stdout)
            return 2

    doctor = DatabaseDoctor(
        db_path=db_path,
        clips_dir=args.clips_dir,
        verbose=args.verbose,
    )

    report = doctor.diagnose()

    if args.json_output:
        output: dict = {"diagnosis": report.to_dict()}
    else:
        print_report(report)

    if args.fix:
        fix_report = doctor.fix(
            report=report,
            no_backup=args.no_backup,
            only=only,
        )

        if args.json_output:
            output["fixes"] = fix_report.to_dict()
        else:
            print_fix_report(fix_report)

        lock_fail = any(
            f.name == "Lock check" and f.status == "failed"
            for f in fix_report.fixes
        )
        if lock_fail:
            if args.json_output:
                json.dump(output, sys.stdout, indent=2)
            return 3

        if args.json_output:
            json.dump(output, sys.stdout, indent=2)
            print()
        else:
            if not fix_report.has_failures():
                print()
                print("Re-running diagnostics after fixes...")
                verify_report = doctor.diagnose()
                print_report(verify_report)
                if verify_report.has_failures():
                    return 1

        if fix_report.has_failures():
            return 1
        return 0

    if args.json_output:
        json.dump(output, sys.stdout, indent=2)
        print()

    if report.has_failures():
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

package diagnostics

import (
	"fmt"
	"regexp"
	"strconv"
)

// Anomaly kind constants for AnomalyRecord.Kind.
const (
	// AnomalyDBLost: previous boot saw a present, non-trivially-sized DB;
	// this boot resolved to an absent DB with a fresh_install decision.
	AnomalyDBLost = "db_lost"
	// AnomalyDBPathChanged: resolved absolute DB path differs between boots.
	AnomalyDBPathChanged = "db_path_changed"
	// AnomalyMountChanged: the mount source behind /data or /config changed.
	AnomalyMountChanged = "mount_changed"
	// AnomalyVersionRollback: the running version is older than the previous boot's.
	AnomalyVersionRollback = "version_rollback"
)

// dbLostMinSizeBytes is the "non-trivial size" threshold for db_lost.
// A fresh v2 schema-only SQLite database measures roughly 330 KB after
// AutoMigrate; 10 MiB implies real detection data with a wide safety
// margin, trading a false negative on near-empty databases (where the
// boot record still carries all the facts) against never alarming on
// schema-only files.
const dbLostMinSizeBytes = 10 * 1024 * 1024

// decisionFreshInstall mirrors the datastore startup decision string
// (datastoreV2.DecisionFreshInstall). Kept as a private duplicate because
// this package must not import internal/datastore.
const decisionFreshInstall = "fresh_install"

// dialectSQLite is the dialect label for file-backed SQLite databases.
const dialectSQLite = "sqlite"

// mountWatchedDests are the mount destinations whose source changes matter.
var mountWatchedDests = []string{"/data", "/config"}

// versionDatePattern extracts the YYYYMMDD component of a release or
// nightly version string (formats observed in git tags: "20260716",
// "nightly-20260615").
var versionDatePattern = regexp.MustCompile(`(20\d{6})`)

// compareVersionDates compares two version strings by their embedded
// YYYYMMDD date. Returns -1 / 0 / 1 like strings.Compare. When either
// version lacks a date component (dev builds, empty strings), it returns 0
// (incomparable, never an anomaly): the robust fallback.
func compareVersionDates(a, b string) int {
	da, oka := extractVersionDate(a)
	db, okb := extractVersionDate(b)
	if !oka || !okb {
		return 0
	}
	switch {
	case da < db:
		return -1
	case da > db:
		return 1
	default:
		return 0
	}
}

// extractVersionDate pulls the first YYYYMMDD run out of a version string.
func extractVersionDate(v string) (int, bool) {
	m := versionDatePattern.FindString(v)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m)
	if err != nil {
		return 0, false
	}
	return n, true
}

// mountSourceFor returns the source of the mount at dest, or "" if absent.
func mountSourceFor(mounts []Mount, dest string) string {
	for _, m := range mounts {
		if m.Destination == dest {
			return m.Source
		}
	}
	return ""
}

// newAnomaly builds a stamped AnomalyRecord.
func newAnomaly(kind, previous, current, message string) AnomalyRecord {
	return AnomalyRecord{
		RecordHeader: NewRecordHeader(RecordTypeAnomaly),
		Kind:         kind,
		Previous:     previous,
		Current:      current,
		Message:      message,
	}
}

// detectAnomalies diffs the previous boot against the current one and
// returns zero or more anomaly records. Pure function: no I/O, no logging.
func detectAnomalies(prev, cur *BootRecord) []AnomalyRecord {
	var anomalies []AnomalyRecord

	// db_lost (sqlite only: a file-level signal).
	if prev.Datastore.Dialect == dialectSQLite &&
		cur.Datastore.Dialect == dialectSQLite &&
		prev.Datastore.ConfiguredExists &&
		prev.Datastore.ConfiguredSize >= dbLostMinSizeBytes &&
		!cur.Datastore.ConfiguredExists &&
		cur.Datastore.StartupDecision == decisionFreshInstall {
		anomalies = append(anomalies, newAnomaly(
			AnomalyDBLost,
			fmt.Sprintf("db present at %s (%d bytes)", prev.Datastore.ResolvedAbsPath, prev.Datastore.ConfiguredSize),
			fmt.Sprintf("db absent, decision %s", cur.Datastore.StartupDecision),
			"database present at previous boot is gone and a fresh install was triggered",
		))
	}

	// db_path_changed.
	if prev.Datastore.ResolvedAbsPath != "" && cur.Datastore.ResolvedAbsPath != "" &&
		prev.Datastore.ResolvedAbsPath != cur.Datastore.ResolvedAbsPath {
		anomalies = append(anomalies, newAnomaly(
			AnomalyDBPathChanged,
			prev.Datastore.ResolvedAbsPath,
			cur.Datastore.ResolvedAbsPath,
			"resolved database path changed between boots",
		))
	}

	// mount_changed for /data and /config. A missing source on either side
	// (non-container boot, mountinfo unreadable) is not a change.
	for _, dest := range mountWatchedDests {
		prevSrc := mountSourceFor(prev.Mounts, dest)
		curSrc := mountSourceFor(cur.Mounts, dest)
		if prevSrc != "" && curSrc != "" && prevSrc != curSrc {
			anomalies = append(anomalies, newAnomaly(
				AnomalyMountChanged,
				fmt.Sprintf("%s <- %s", dest, prevSrc),
				fmt.Sprintf("%s <- %s", dest, curSrc),
				"bind mount source changed for "+dest,
			))
		}
	}

	// version_rollback.
	if compareVersionDates(cur.App.Version, prev.App.Version) < 0 {
		anomalies = append(anomalies, newAnomaly(
			AnomalyVersionRollback,
			prev.App.Version,
			cur.App.Version,
			"running version is older than the previously booted version",
		))
	}

	return anomalies
}

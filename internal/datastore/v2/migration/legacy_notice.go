package migration

import "github.com/tphakala/birdnet-go/internal/datastore/v2/entities"

// LegacyNoticeKind names which notice an install that still runs the legacy
// schema should show.
type LegacyNoticeKind string

// The legacy notice kinds.
const (
	// LegacyNoticeNone means no notice is due.
	LegacyNoticeNone LegacyNoticeKind = "none"
	// LegacyNoticeNotStarted means the migration has not been started.
	LegacyNoticeNotStarted LegacyNoticeKind = "not_started"
	// LegacyNoticeInProgress means the migration is running.
	LegacyNoticeInProgress LegacyNoticeKind = "in_progress"
	// LegacyNoticeNeedsAttention means the migration is paused or has failed.
	LegacyNoticeNeedsAttention LegacyNoticeKind = "needs_attention"
	// LegacyNoticeRestartRequired means the migration has finished but the
	// process still runs the legacy store until it is restarted.
	LegacyNoticeRestartRequired LegacyNoticeKind = "restart_required"
)

// LegacyNotice maps the schema the process runs and the persisted migration
// state to the notice kind to show. It is total over the nine migration
// states; an empty or unknown status maps to LegacyNoticeNone, as does any
// state on an install that does not run the legacy schema.
//
// It has no dialect input because MySQL and SQLite share the same StateManager.
// Migration prerequisites are deliberately not an input: they are costly to
// evaluate and the notice only points the user at the migration page.
func LegacyNotice(legacySchema bool, status entities.MigrationStatus) LegacyNoticeKind {
	if !legacySchema {
		return LegacyNoticeNone
	}
	if (&entities.MigrationState{State: status}).IsActive() {
		return LegacyNoticeInProgress
	}
	switch status {
	case entities.MigrationStatusIdle:
		return LegacyNoticeNotStarted
	case entities.MigrationStatusPaused, entities.MigrationStatusFailed:
		return LegacyNoticeNeedsAttention
	case entities.MigrationStatusCompleted:
		return LegacyNoticeRestartRequired
	default:
		return LegacyNoticeNone
	}
}

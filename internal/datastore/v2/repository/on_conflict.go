package repository

import "gorm.io/gorm/clause"

// No-op columns for onConflictSkip. Each is a plain NOT NULL column of the
// table's unique key, never the auto-increment column.
const (
	// detectionIDColumn is used for predictions, reviews, comments and locks.
	detectionIDColumn = "detection_id"
	// scientificNameColumn is used for labels.
	scientificNameColumn = "scientific_name"
	// dailyEventsDateColumn is used for daily events.
	dailyEventsDateColumn = "date"
)

// onConflictSkip returns the clause for a multi-row insert that skips rows
// whose unique key is already stored or repeated earlier in the same statement.
// The stored row and the first row of the statement are kept; nothing is
// overwritten, and errors other than a duplicate key are still returned.
//
// On SQLite it is ON CONFLICT DO NOTHING, with target as the conflict target
// when given. On MySQL and MariaDB GORM renders DoNothing as
// "ON DUPLICATE KEY UPDATE id=id", which MySQL 8.4 rejects with Error 1869
// when two rows of one INSERT share a unique key, losing the whole statement.
// There the skip is written as an assignment of noopColumn to itself instead.
// noopColumn must be a plain column of the table, never the auto-increment
// column or a generated column. The MySQL driver ignores target.
func onConflictSkip(isMySQL bool, noopColumn string, target ...clause.Column) clause.OnConflict {
	if !isMySQL {
		return clause.OnConflict{Columns: target, DoNothing: true}
	}
	c := clause.Column{Name: noopColumn}
	return clause.OnConflict{DoUpdates: []clause.Assignment{{Column: c, Value: c}}}
}

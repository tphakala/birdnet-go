package v2

import "sync/atomic"

// databaseMode tracks whether we're using the enhanced (v2) database schema.
// This is set once at startup and never changes.
var databaseMode atomic.Bool

// SetEnhancedDatabaseMode marks the system as using the enhanced v2 schema.
// This should be called once during startup for:
//   - Fresh installations (v2 schema at configured path)
//   - Post-migration mode (v2 schema in birdnet_v2.db)
//
// This is NOT called for legacy mode or during active migration.
func SetEnhancedDatabaseMode() {
	databaseMode.Store(true)
}

// IsEnhancedDatabase returns true if the system is using the enhanced v2 schema.
// This is true for:
//   - Fresh installations using v2 schema
//   - Post-migration v2-only mode
//
// This is false for:
//   - Legacy database mode
//   - During active migration (dual-write mode)
func IsEnhancedDatabase() bool {
	return databaseMode.Load()
}

// ResetDatabaseMode resets the database mode flag.
// This is only for testing purposes and should not be used in production code.
func ResetDatabaseMode() {
	databaseMode.Store(false)
}

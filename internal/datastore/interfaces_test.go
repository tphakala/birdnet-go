package datastore

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// createDatabase initializes a temporary database for testing purposes.
// It ensures the database connection is opened and handles potential errors.
func createDatabase(t *testing.T, settings *conf.Settings) Interface {
	tempDir := t.TempDir()
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = tempDir + "/test.db"

	dataStore := New(settings)

	// Attempt to open a database connection.
	if err := dataStore.Open(); err != nil {
		t.Fatalf("Failed to open database: %v", err) // Use t.Fatalf to immediately fail the test on error.
	}

	// Ensure the database is closed after the test completes.
	t.Cleanup(func() { dataStore.Close() })

	return dataStore
}

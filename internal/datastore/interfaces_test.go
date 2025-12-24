package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// createDatabase initializes a temporary database for testing purposes.
// It ensures the database connection is opened and handles potential errors.
func createDatabase(t *testing.T, settings *conf.Settings) Interface {
	t.Helper()
	tempDir := t.TempDir()
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = tempDir + "/test.db"

	dataStore := New(settings)

	// Attempt to open a database connection.
	require.NoError(t, dataStore.Open(), "Failed to open database")

	// Ensure the database is closed after the test completes.
	t.Cleanup(func() {
		assert.NoError(t, dataStore.Close(), "Failed to close datastore")
	})

	return dataStore
}

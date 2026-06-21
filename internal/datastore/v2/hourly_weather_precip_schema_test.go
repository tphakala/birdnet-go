package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateV2SchemaIntegrity_HourlyWeatherPrecipitationColumns guards the
// schema-corruption cascade the maintainer flagged: adding the
// precipitation columns to entities.HourlyWeather must let AutoMigrate create
// them AND keep validateV2SchemaIntegrity clean, so v2 init never reports
// ErrV2SchemaCorrupted and falls back to the deprecated legacy path.
func TestValidateV2SchemaIntegrity_HourlyWeatherPrecipitationColumns(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Initialize runs AutoMigrate then validateV2SchemaIntegrity. A failure here
	// would mean the new columns broke the post-AutoMigrate schema check.
	require.NoError(t, mgr.Initialize())
	require.NoError(t, mgr.validateV2SchemaIntegrity())

	cols, err := sqliteColumnLister(mgr.DB(), "hourly_weathers")
	require.NoError(t, err)
	assert.Contains(t, cols, "precipitation", "AutoMigrate must add the precipitation column")
	assert.Contains(t, cols, "precipitation_type", "AutoMigrate must add the precipitation_type column")
}

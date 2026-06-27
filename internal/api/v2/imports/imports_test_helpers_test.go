// imports_test_helpers_test.go: shared scaffolding for the import/migration
// domain tests.
//
// getTestSettings was copied from the package-api test_helpers_test.go when the
// import domain moved out of package api; the migration and prerequisites tests
// store it into the core's atomic Settings pointer so the prerequisite checks see
// a valid SQLite path under a per-test t.TempDir().
package importsapi

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

const (
	testMQTTTopic        = "birdnet/detections"
	testNewYorkLongitude = -74.0060 // New York City longitude for test data
)

// getTestSettings returns a *conf.Settings populated with valid defaults for the
// import/migration domain tests, including a test-isolated SQLite path under
// t.TempDir() for the migration prerequisite checks.
func getTestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	settings := &conf.Settings{}

	// Initialize with valid defaults
	settings.Realtime.Interval = 15                // Must be positive after hardening
	settings.Realtime.Dashboard.SummaryLimit = 100 // Valid range: 10-1000
	settings.Realtime.Dashboard.Thumbnails.Summary = true
	settings.Realtime.Dashboard.Thumbnails.Recent = true
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
	settings.Realtime.Dashboard.Locale = "en"

	// Weather settings
	settings.Realtime.Weather.Provider = "yrno"
	settings.Realtime.Weather.PollInterval = 60

	// MQTT settings
	settings.Realtime.MQTT.Enabled = false
	settings.Realtime.MQTT.Broker = "tcp://localhost:1883"
	settings.Realtime.MQTT.Topic = testMQTTTopic

	// BirdNET settings
	settings.BirdNET.Latitude = 40.7128
	settings.BirdNET.Longitude = testNewYorkLongitude
	settings.BirdNET.Sensitivity = 1.0
	settings.BirdNET.Threshold = 0.8
	settings.BirdNET.Locale = "en"
	settings.BirdNET.RangeFilter.Model = "latest"
	settings.BirdNET.RangeFilter.Threshold = 0.03

	// Audio settings
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
		Name:   "Test Sound Card",
		Device: "default",
	}}
	settings.Realtime.Audio.Export.Enabled = true
	settings.Realtime.Audio.Export.Type = "wav"
	settings.Realtime.Audio.Export.Path = "clips"
	settings.Realtime.Audio.Export.Bitrate = "192k"
	settings.Realtime.Audio.Export.Length = 15

	// Species settings
	settings.Realtime.Species.Include = []string{"American Robin"}
	settings.Realtime.Species.Config = make(map[string]conf.SpeciesConfig)

	// WebServer settings
	settings.WebServer.Port = "8080"
	settings.WebServer.Enabled = true
	settings.WebServer.LiveStream.BitRate = 128
	settings.WebServer.LiveStream.SegmentLength = 5

	// Security settings - session duration must be positive
	settings.Security.SessionDuration = 168 * time.Hour // 7 days

	// Output settings - SQLite path for prerequisite checks
	// Use t.TempDir() for test-isolated, auto-cleaned directory
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet-test.db")

	// Initialize other maps to prevent nil pointer issues
	settings.Realtime.MQTT.RetrySettings.MaxRetries = 3
	settings.Realtime.MQTT.RetrySettings.InitialDelay = 10
	settings.Realtime.MQTT.RetrySettings.MaxDelay = 300
	settings.Realtime.MQTT.RetrySettings.BackoffMultiplier = 2.0

	return settings
}

// fakeV2Manager implements the subset of datastoreV2.Manager used by the
// migration prerequisite checks (DB() and TablePrefix()). It is copied from the
// package-api app_wizard_test.go fake so the prerequisites tests stay
// self-contained after the import-domain move.
type fakeV2Manager struct {
	db          *gorm.DB
	tablePrefix string
}

func (f *fakeV2Manager) Initialize() error    { return nil }
func (f *fakeV2Manager) DB() *gorm.DB         { return f.db }
func (f *fakeV2Manager) Path() string         { return ":memory:" }
func (f *fakeV2Manager) Close() error         { return nil }
func (f *fakeV2Manager) CheckpointWAL() error { return nil }
func (f *fakeV2Manager) Delete() error        { return nil }
func (f *fakeV2Manager) Exists() bool         { return true }
func (f *fakeV2Manager) IsMySQL() bool        { return false }
func (f *fakeV2Manager) TablePrefix() string  { return f.tablePrefix }

// newFakeV2ManagerWithTable creates a fakeV2Manager backed by an in-memory SQLite
// database with a single arbitrary table (e.g. "v2_detections") holding count rows,
// and reports the given prefix from TablePrefix(). Used to verify that table names are
// derived from the manager's prefix rather than guessed from the dialect.
func newFakeV2ManagerWithTable(t *testing.T, tableName, prefix string, count int) *fakeV2Manager {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS "+tableName+" (id INTEGER PRIMARY KEY)").Error)
	for i := range count {
		require.NoError(t, db.Exec("INSERT INTO "+tableName+" (id) VALUES (?)", i+1).Error)
	}

	return &fakeV2Manager{db: db, tablePrefix: prefix}
}

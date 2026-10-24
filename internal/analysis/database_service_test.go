package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	importsapi "github.com/tphakala/birdnet-go/internal/api/v2/imports"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// Compile-time interface compliance check.
var _ app.Service = (*DatabaseService)(nil)
var _ app.TieredService = (*DatabaseService)(nil)

func TestDatabaseService_Name(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	assert.Equal(t, "database", svc.Name())
}

func TestDatabaseService_ShutdownTier(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	assert.Equal(t, app.TierCore, svc.ShutdownTier())
}

func TestDatabaseService_GettersBeforeStart(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	assert.Nil(t, svc.DataStore(), "DataStore() should return nil before Start()")
	assert.Nil(t, svc.V2Manager(), "V2Manager() should return nil before Start()")
	assert.False(t, svc.IsV2OnlyMode(), "IsV2OnlyMode() should return false before Start()")
}

func TestDatabaseService_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := svc.Stop(t.Context())
		assert.NoError(t, err)
	})
}

// TestErrV2SchemaCorrupted_PropagatesThroughInitializeWrapChain pins the unwrap
// behavior the silent-fallback prevention in Start() depends on. validateSchemaIntegrity
// wraps the sentinel with fmt.Errorf, SQLiteManager.Initialize wraps that again, and
// initializeV2OnlyMode / v2only.InitializeFreshInstall wrap the result with the internal
// errors package. errors.Is must unwrap through all three layers and still match the
// sentinel; otherwise Start() would silently fall back to legacy mode for corrupted
// databases (GitHub #3211).
func TestErrV2SchemaCorrupted_PropagatesThroughInitializeWrapChain(t *testing.T) {
	t.Parallel()

	// Layer 1: validator returns the sentinel wrapped with details.
	validatorErr := fmt.Errorf("%w: detections missing columns [unlikely]", datastoreV2.ErrV2SchemaCorrupted)
	// Layer 2: Manager.Initialize wraps the validator error.
	initializeErr := fmt.Errorf("v2 schema integrity check failed: %w", validatorErr)
	// Layer 3: initializeV2OnlyMode wraps the manager error with the internal errors
	// package. EnhancedError.Unwrap() must keep the chain intact.
	wrapped := errors.New(initializeErr).
		Component("analysis").
		Category(errors.CategoryDatabase).
		Context("operation", "initialize_v2_database").
		Build()

	assert.True(t, errors.Is(wrapped, datastoreV2.ErrV2SchemaCorrupted),
		"errors.Is must unwrap through fmt.Errorf and EnhancedError to find the sentinel")
}

func TestDatabaseService_Start_FreshInstall(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "metrics initialization should succeed")

	svc := NewDatabaseService(settings, metrics)
	err = svc.Start(t.Context())
	require.NoError(t, err, "Start() should succeed for fresh install")
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(t.Context()), "Stop() should succeed")
	})

	// DataStore should be set after Start.
	assert.NotNil(t, svc.DataStore(), "DataStore() should not be nil after Start()")

	// Fresh install goes to v2-only mode.
	assert.True(t, svc.IsV2OnlyMode(), "should be in v2-only mode for fresh install")
	assert.NotNil(t, svc.V2Manager(), "V2Manager() should be set for fresh install")
	assert.Nil(t, svc.legacyMonitor, "no migration state monitor on a v2 install")
}

// Not parallel: Start publishes the migration state manager and worker to
// package-level state in the imports API package, so the test resets it.
func TestDatabaseService_Start_LegacyInstallRunsStateMonitor(t *testing.T) {
	// Registered first so it runs after the Stop cleanup below.
	t.Cleanup(func() { importsapi.SetMigrationDependencies(nil, nil) })

	configuredPath := filepath.Join(t.TempDir(), "birdnet.db")
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	// Create a legacy database so startup does not take the fresh-install path.
	legacy := datastore.New(settings)
	require.NoError(t, legacy.Open())
	require.NoError(t, legacy.Close())

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	svc := NewDatabaseService(settings, metrics)
	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(t.Context()), "Stop() should succeed")
		assert.Nil(t, svc.legacyMonitor, "Stop releases the monitor")
	})

	require.False(t, svc.IsV2OnlyMode(), "an existing legacy database starts in legacy mode")
	require.NotNil(t, svc.legacyMonitor, "legacy install with a state manager gets the monitor")
	assert.True(t, svc.legacyMonitor.legacySchema, "the monitor of a legacy install raises the legacy schema notice")
}

// discardTransport is a sentry.Transport that drops every event; the test below
// only reads tags off the hub scope.
type discardTransport struct{}

func (discardTransport) Configure(sentry.ClientOptions)        {}
func (discardTransport) SendEvent(*sentry.Event)               {}
func (discardTransport) Flush(time.Duration) bool              { return true }
func (discardTransport) FlushWithContext(context.Context) bool { return true }
func (discardTransport) Close()                                {}

// TestDatabaseService_Start_LegacyInstallWithoutStateManager pins the startup
// path of a legacy install whose migration setup fails: a directory at the v2
// migration path makes the state manager setup fail, startup still succeeds
// without a monitor, and the install gets the deprecation warning and the
// migration_state=unavailable tag together.
// Not parallel: Start publishes migration state to package-level state in the
// imports API package, the test swaps the global logger and it binds a client
// on the process-wide Sentry hub.
func TestDatabaseService_Start_LegacyInstallWithoutStateManager(t *testing.T) {
	// Registered first so it runs after the Stop cleanup below.
	t.Cleanup(func() { importsapi.SetMigrationDependencies(nil, nil) })

	configuredPath := filepath.Join(t.TempDir(), "birdnet.db")
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	// Create a legacy database so startup does not take the fresh-install path.
	legacy := datastore.New(settings)
	require.NoError(t, legacy.Open())
	require.NoError(t, legacy.Close())

	// A directory where the migration database file belongs makes the state
	// manager setup fail while the legacy database stays usable.
	require.NoError(t, os.MkdirAll(datastoreV2.V2MigrationPathFromConfigured(configuredPath), 0o750))

	buf := logtest.CaptureBufferAt(t, slog.LevelWarn)

	// Telemetry reports only when a client is bound; test mode skips the opt-in
	// and build age checks.
	telemetry.EnableTestMode()
	t.Cleanup(telemetry.DisableTestMode)
	hub := sentry.CurrentHub()
	original := hub.Client()
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: discardTransport{}})
	require.NoError(t, err)
	hub.BindClient(client)
	t.Cleanup(func() { hub.BindClient(original) })
	// The tag lands on the top scope; pop it afterwards so it does not leak.
	hub.PushScope()
	t.Cleanup(hub.PopScope)

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	svc := NewDatabaseService(settings, metrics)
	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(t.Context()), "Stop() should succeed")
	})

	require.False(t, svc.IsV2OnlyMode(), "an existing legacy database starts in legacy mode")
	assert.Nil(t, svc.legacyMonitor, "no monitor without a readable migration state")

	logged := buf.String()
	assert.Contains(t, logged, legacySchemaWarnMessage)
	assert.Contains(t, logged, "migration_state="+migrationTagUnavailable)
	assert.Contains(t, logged, "notice_kind="+string(migration.LegacyNoticeNone))

	event := hub.Scope().ApplyToEvent(&sentry.Event{}, nil, client)
	require.NotNil(t, event)
	assert.Equal(t, migrationTagUnavailable, event.Tags["migration_state"])
}

func TestBootParamsFromStartupSQLite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := &conf.Settings{}
	settings.Version = "20260716"
	settings.BuildDate = "2026-07-16"
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(dir, "birdnet.db")

	st := datastoreV2.StartupState{
		MigrationStatus: entities.MigrationStatusCompleted,
		Decision:        datastoreV2.DecisionV2Restart,
	}

	p := bootParamsFromStartup(settings, st)
	assert.Equal(t, "20260716", p.Version)
	assert.Equal(t, "sqlite", p.Dialect)
	assert.Equal(t, settings.Output.SQLite.Path, p.ConfiguredDBPath)
	assert.Equal(t, datastoreV2.V2MigrationPathFromConfigured(settings.Output.SQLite.Path), p.V2SidecarPath)
	assert.Equal(t, datastoreV2.DecisionV2Restart, p.StartupDecision)
	assert.Equal(t, string(entities.MigrationStatusCompleted), p.MigrationState)
	assert.Equal(t, dir, p.DataDir)
	assert.NotEmpty(t, p.ConfigDir)
}

func TestBootParamsFromStartupMySQL(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Version = "20260716"
	settings.Output.MySQL.Enabled = true

	p := bootParamsFromStartup(settings, datastoreV2.StartupState{Decision: datastoreV2.DecisionLegacyMode})
	assert.Equal(t, "mysql", p.Dialect)
	assert.Empty(t, p.ConfiguredDBPath, "MySQL has no file path facts")
	assert.Empty(t, p.V2SidecarPath)
}

// TestBootParamsDBLostUsesDatastoreConstants is a cross-package value-sync
// guard. diagnostics keeps PRIVATE copies of the dialect ("sqlite") and
// decision ("fresh_install") strings because it must not import datastore
// (import cycle). This test drives the REAL flow: datastore.DialectSQLite and
// datastoreV2.DecisionFreshInstall map through bootParamsFromStartup into the
// BootParams that diagnostics.detectAnomalies compares against its private
// copies. If either upstream constant ever drifts from the diagnostics copy,
// db_lost silently stops firing (the exact #3956-class silent failure this
// feature exists to catch) and this test fails.
func TestBootParamsDBLostUsesDatastoreConstants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "birdnet.db") // never created: absent this boot
	j := diagnostics.NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"))

	// Previous boot: a large sqlite DB present at the same resolved path,
	// labelled with the datastore constants the real startup flow produces.
	prev := &diagnostics.BootRecord{
		RecordHeader: diagnostics.NewRecordHeader(diagnostics.RecordTypeBoot),
		Datastore: diagnostics.DatastoreSnapshot{
			Dialect:          datastore.DialectSQLite,
			ResolvedAbsPath:  dbPath,
			ConfiguredExists: true,
			ConfiguredSize:   64 * 1024 * 1024,
			StartupDecision:  datastoreV2.DecisionV2Restart,
		},
	}
	require.NoError(t, j.Append(prev))

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = dbPath
	p := bootParamsFromStartup(settings, datastoreV2.StartupState{
		Decision:        datastoreV2.DecisionFreshInstall,
		MigrationStatus: entities.MigrationStatusIdle,
	})
	_, anomalies := diagnostics.RecordBoot(j, p)

	found := false
	for _, a := range anomalies {
		if a.Kind == diagnostics.AnomalyDBLost {
			found = true
		}
	}
	assert.True(t, found,
		"db_lost must fire for the real datastore dialect+decision constants; a drift from diagnostics' private copies would break #3956 detection")
}

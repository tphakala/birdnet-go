package analysis

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
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
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// probeZoneOffsetSeconds is a UTC offset (+07:17) that no real zone uses, so an
// instant expressed in it differs from the same wall time read in any host zone.
const probeZoneOffsetSeconds = 7*3600 + 17*60

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
	probed := 0
	svc.probeServer = func(context.Context, *gorm.DB, time.Time) databaseServerReport {
		probed++
		return databaseServerReport{}
	}
	err = svc.Start(t.Context())
	require.NoError(t, err, "Start() should succeed for fresh install")
	assert.Zero(t, probed, "a SQLite install must not probe a MySQL server")
	assert.Nil(t, svc.serverNotice, "no database server notice on SQLite")
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(t.Context()), "Stop() should succeed")
	})

	// DataStore should be set after Start.
	assert.NotNil(t, svc.DataStore(), "DataStore() should not be nil after Start()")

	// Fresh install goes to v2-only mode.
	assert.True(t, svc.IsV2OnlyMode(), "should be in v2-only mode for fresh install")
	assert.NotNil(t, svc.V2Manager(), "V2Manager() should be set for fresh install")
	assert.Nil(t, svc.legacyMonitor, "no migration state monitor on a v2 install")

	// The repository must be the v2 one: it stores the detection's own instant. The
	// zone has an offset no real zone uses, so a wall-clock round trip through the
	// host's time.Local would move the instant.
	repo := svc.DetectionRepository()
	require.NotNil(t, repo, "DetectionRepository() should be set after Start()")
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.FixedZone("probe", probeZoneOffsetSeconds))
	result := &detection.Result{
		Timestamp: at,
		Species:   detection.Species{ScientificName: "Parus major", CommonName: "Great Tit"},
		Model:     detection.DefaultModelInfo(),
	}
	require.NoError(t, repo.Save(t.Context(), result, nil))
	require.NotZero(t, result.ID)
	var detectedAt int64
	require.NoError(t, svc.V2Manager().DB().
		Model(&entities.Detection{}).Select("detected_at").Where("id = ?", result.ID).
		Scan(&detectedAt).Error)
	assert.Equal(t, at.Unix(), detectedAt, "stored detected_at must be the detection's own instant")
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
		assert.Nil(t, svc.DetectionRepository(), "Stop releases the detection repository")
	})

	require.False(t, svc.IsV2OnlyMode(), "an existing legacy database starts in legacy mode")
	require.NotNil(t, svc.legacyMonitor, "legacy install with a state manager gets the monitor")
	assert.True(t, svc.legacyMonitor.legacySchema, "the monitor of a legacy install raises the legacy schema notice")
	assert.NotNil(t, svc.DetectionRepository(), "legacy mode must have a detection repository")
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

// unreachableServerError is the error of a dial to a port nobody listens on, the cause the
// MySQL state checks wrap in ErrStartupStateUnknown.
func unreachableServerError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
}

// unknownStateOutcome is a startup state that could not be read, wrapping cause.
func unknownStateOutcome(cause error) datastoreV2.StartupState {
	return datastoreV2.StartupState{
		Decision: datastoreV2.DecisionStateUnknown,
		Error:    fmt.Errorf("%w: open MySQL database: %w", datastoreV2.ErrStartupStateUnknown, cause),
	}
}

// pick returns the i-th outcome of a script, repeating the last one.
func pick[T any](script []T, i int) T {
	return script[min(i, len(script)-1)]
}

// TestDatabaseService_AwaitStartupState pins the retry of the boot decision read: the
// whole unit (tail check, state check, prefix probe) is read again while the cause can
// heal, the delays are bounded and capped, and an unhealable cause fails at once.
func TestDatabaseService_AwaitStartupState(t *testing.T) {
	t.Parallel()

	type tailOutcome struct {
		hasUnmigrated bool
		err           error
	}
	type probeOutcome struct {
		fresh bool
		err   error
	}
	conn := unreachableServerError()
	completed := datastoreV2.StartupState{
		MigrationStatus: entities.MigrationStatusCompleted,
		V2Available:     true,
		Decision:        datastoreV2.DecisionV2Restart,
	}
	legacy := datastoreV2.StartupState{
		MigrationStatus: entities.MigrationStatusIdle,
		LegacyRequired:  true,
		Decision:        datastoreV2.DecisionLegacyMode,
	}
	cleanTail := []tailOutcome{{}}
	connTail := tailOutcome{err: fmt.Errorf("%w: open MySQL database: %w", datastoreV2.ErrLegacyTailUnverified, conn)}
	flatProbeErr := fmt.Errorf("%w: check bare v2 schema: flattened server text", datastoreV2.ErrStartupStateUnknown)

	tests := []struct {
		name           string
		tail           []tailOutcome
		state          []datastoreV2.StartupState
		probe          []probeOutcome
		waitErr        error
		wantAttempts   int
		wantProbeCalls int
		wantDelays     []time.Duration
		wantErrIs      []error
		wantFresh      bool
		wantTailErr    bool
		sqliteToo      bool
	}{
		{
			name:         "retries an unknown state until it is known",
			tail:         cleanTail,
			state:        []datastoreV2.StartupState{unknownStateOutcome(conn), unknownStateOutcome(conn), legacy},
			probe:        []probeOutcome{{}},
			wantAttempts: 3,
			wantDelays:   []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:         "gives up after the attempt bound with a capped schedule",
			tail:         cleanTail,
			state:        []datastoreV2.StartupState{unknownStateOutcome(conn)},
			probe:        []probeOutcome{{}},
			wantAttempts: 8,
			wantDelays: []time.Duration{
				time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
				15 * time.Second, 15 * time.Second, 15 * time.Second,
			},
			wantErrIs: []error{datastoreV2.ErrStartupStateUnknown},
		},
		{
			name:           "re-reads the tail check with the state",
			tail:           []tailOutcome{connTail, {}},
			state:          []datastoreV2.StartupState{completed},
			probe:          []probeOutcome{{fresh: true}},
			wantAttempts:   2,
			wantProbeCalls: 1,
			wantDelays:     []time.Duration{time.Second},
			wantFresh:      true,
		},
		{
			name:         "does not need the tail for a legacy start",
			tail:         []tailOutcome{connTail},
			state:        []datastoreV2.StartupState{legacy},
			probe:        []probeOutcome{{}},
			wantAttempts: 1,
			wantTailErr:  true,
		},
		{
			name:           "retries the prefix probe with the unit",
			tail:           cleanTail,
			state:          []datastoreV2.StartupState{completed},
			probe:          []probeOutcome{{err: unknownStateOutcome(conn).Error}, {fresh: false}},
			wantAttempts:   2,
			wantProbeCalls: 2,
			wantDelays:     []time.Duration{time.Second},
		},
		{
			name:           "fails at once on a probe cause that cannot heal",
			tail:           cleanTail,
			state:          []datastoreV2.StartupState{completed},
			probe:          []probeOutcome{{err: flatProbeErr}},
			wantAttempts:   1,
			wantProbeCalls: 1,
			wantErrIs:      []error{datastoreV2.ErrStartupStateUnknown},
		},
		{
			name:         "keeps the refusal for a tail error that cannot heal",
			tail:         []tailOutcome{{err: fmt.Errorf("%w: open MySQL database: flattened server text", datastoreV2.ErrLegacyTailUnverified)}},
			state:        []datastoreV2.StartupState{completed},
			probe:        []probeOutcome{{}},
			wantAttempts: 1,
			wantTailErr:  true,
		},
		{
			name:         "does not probe while unmigrated records wait for the tail sync",
			tail:         []tailOutcome{{hasUnmigrated: true}},
			state:        []datastoreV2.StartupState{completed},
			probe:        []probeOutcome{{}},
			wantAttempts: 1,
		},
		{
			name:         "does not probe when SQLite is enabled too",
			tail:         cleanTail,
			state:        []datastoreV2.StartupState{completed},
			probe:        []probeOutcome{{}},
			wantAttempts: 1,
			sqliteToo:    true,
		},
		{
			name:         "stops when the wait fails",
			tail:         cleanTail,
			state:        []datastoreV2.StartupState{unknownStateOutcome(conn)},
			probe:        []probeOutcome{{}},
			waitErr:      context.Canceled,
			wantAttempts: 1,
			wantDelays:   []time.Duration{time.Second},
			wantErrIs:    []error{datastoreV2.ErrStartupStateUnknown, context.Canceled},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &conf.Settings{}
			settings.Output.MySQL.Enabled = true
			if tt.sqliteToo {
				// The real SQLite consolidation runs before the state check; point it at an
				// empty directory so it finds nothing to rename.
				settings.Output.SQLite.Enabled = true
				settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet.db")
			}
			svc := NewDatabaseService(settings, nil)

			var tailCalls, stateCalls, probeCalls int
			var delays []time.Duration
			svc.checkLegacyTail = func(*conf.Settings, logger.Logger) (bool, error) {
				o := pick(tt.tail, tailCalls)
				tailCalls++
				return o.hasUnmigrated, o.err
			}
			svc.checkStartupState = func(*conf.Settings) datastoreV2.StartupState {
				st := pick(tt.state, stateCalls)
				stateCalls++
				return st
			}
			svc.checkFreshV2Schema = func(*conf.Settings) (bool, error) {
				o := pick(tt.probe, probeCalls)
				probeCalls++
				return o.fresh, o.err
			}
			svc.waitStartupRetry = func(_ context.Context, d time.Duration) error {
				delays = append(delays, d)
				return tt.waitErr
			}

			log := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
			reading, err := svc.awaitStartupState(t.Context(), settings, log)

			assert.Equal(t, tt.wantAttempts, stateCalls, "state check calls")
			assert.Equal(t, tt.wantAttempts, tailCalls, "tail check calls")
			assert.Equal(t, tt.wantProbeCalls, probeCalls, "prefix probe calls")
			assert.Equal(t, tt.wantDelays, delays)
			if len(tt.wantErrIs) > 0 {
				require.Error(t, err)
				for _, target := range tt.wantErrIs {
					require.ErrorIs(t, err, target)
				}
				return
			}
			require.NoError(t, err)
			assert.NoError(t, reading.unknown)
			assert.Equal(t, tt.wantFresh, reading.mysqlFreshV2)
			assert.Equal(t, tt.wantTailErr, reading.tailErr != nil)
		})
	}
}

// TestDatabaseService_Start_StartupStateUnknownRefuses pins the wiring of the unknown
// state refusal: Start must return the error before any datastore is
// constructed. Without it the legacy default branch would open the database and create
// the SQLite file. Not parallel: it swaps the global logger.
func TestDatabaseService_Start_StartupStateUnknownRefuses(t *testing.T) {
	configuredPath := filepath.Join(t.TempDir(), "birdnet.db")
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	buf := logtest.CaptureBufferAt(t, slog.LevelError)

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	svc := NewDatabaseService(settings, metrics)
	svc.checkStartupState = func(*conf.Settings) datastoreV2.StartupState {
		return unknownStateOutcome(unreachableServerError())
	}
	svc.waitStartupRetry = func(context.Context, time.Duration) error { return nil }

	err = svc.Start(t.Context())
	require.ErrorIs(t, err, datastoreV2.ErrStartupStateUnknown)
	assert.Nil(t, svc.DataStore(), "no datastore may be constructed when the state is unknown")
	assert.False(t, svc.IsV2OnlyMode())
	assert.NoFileExists(t, configuredPath, "the legacy fallback would have created the database file")
	assert.Contains(t, buf.String(), startupStateUnknownRecoveryHint, "the log must tell the user what to do")
}

// TestDatabaseService_Start_MixedSchemaRefuses pins the wiring of the mixed schema
// refusal: Start must return the error before any datastore is constructed. Without
// it the legacy default branch would open the database and create the SQLite file.
// Not parallel: it swaps the global logger.
func TestDatabaseService_Start_MixedSchemaRefuses(t *testing.T) {
	configuredPath := filepath.Join(t.TempDir(), "birdnet.db")
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	buf := logtest.CaptureBufferAt(t, slog.LevelWarn)

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	svc := NewDatabaseService(settings, metrics)
	svc.checkStartupState = func(*conf.Settings) datastoreV2.StartupState {
		return datastoreV2.StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			Decision:        datastoreV2.DecisionMixedSchemaRefused,
			Error:           fmt.Errorf("%w: test", datastoreV2.ErrMixedSchemaDatabase),
		}
	}

	err = svc.Start(t.Context())
	require.ErrorIs(t, err, datastoreV2.ErrMixedSchemaDatabase)
	assert.Nil(t, svc.DataStore(), "no datastore may be constructed for a refused database")
	assert.False(t, svc.IsV2OnlyMode())
	assert.NoFileExists(t, configuredPath, "the legacy fallback would have created the database file")
	assert.Contains(t, buf.String(), mixedSchemaGuideURL, "the log must point the user to the guide")
	assert.NotContains(t, buf.String(), legacyFallbackWarnMessage,
		"a refused start must not log that the legacy database is being started")
}

// legacyFallbackWarnMessage is the Warn emitted when a state check error falls back to the
// legacy database.
const legacyFallbackWarnMessage = "database state check failed, starting with the legacy database"

// TestDatabaseService_Start_StateErrorWarnsOnLegacyFallback pins the Warn log of the legacy
// fallback: a state check error that is not a refusal is the only record that the start
// took the legacy path because of an error. Not parallel: it swaps the global logger.
func TestDatabaseService_Start_StateErrorWarnsOnLegacyFallback(t *testing.T) {
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet.db")

	buf := logtest.CaptureBufferAt(t, slog.LevelWarn)

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)
	svc := NewDatabaseService(settings, metrics)
	svc.checkStartupState = func(*conf.Settings) datastoreV2.StartupState {
		return datastoreV2.StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			LegacyRequired:  true,
			Decision:        datastoreV2.DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: test", datastoreV2.ErrV2DatabaseCorrupted),
		}
	}

	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, svc.Stop(t.Context())) })
	assert.Contains(t, buf.String(), legacyFallbackWarnMessage)
}

// legacyTailFixture builds a SQLite install whose migration is COMPLETED with watermark
// 10 while the legacy notes table holds 15 rows, and returns the settings and the
// configured legacy path.
func legacyTailFixture(t *testing.T) (settings *conf.Settings, legacyPath string) {
	t.Helper()
	dir := t.TempDir()
	legacyPath = filepath.Join(dir, "birdnet.db")

	legacyDB, err := gorm.Open(sqlite.Open(legacyPath), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	require.NoError(t, legacyDB.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, source_node TEXT)").Error)
	for i := range 15 {
		require.NoError(t, legacyDB.Exec("INSERT INTO notes (id, source_node) VALUES (?, 'test')", i+1).Error)
	}
	legacySQL, err := legacyDB.DB()
	require.NoError(t, err)
	require.NoError(t, legacySQL.Close())

	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{ConfiguredPath: legacyPath})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	sm := datastoreV2.NewStateManager(mgr.DB())
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, mgr.DB().Model(&entities.MigrationState{}).
		Where("id = 1").Update("last_migrated_id", 10).Error)
	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.TransitionToCutover())
	require.NoError(t, sm.Complete())
	require.NoError(t, mgr.Close())

	settings = &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath
	return settings, legacyPath
}

// dirEntryNames lists the file names in dir.
func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestDatabaseService_Start_UnverifiedLegacyTail pins what Start does when the legacy
// tail check cannot tell whether records beyond the watermark are missing from v2.
// Not parallel: it swaps the global logger and Start sets process globals.
func TestDatabaseService_Start_UnverifiedLegacyTail(t *testing.T) {
	tests := []struct {
		name          string
		stateOverride func(*conf.Settings) datastoreV2.StartupState
		wantRefused   bool
	}{
		{
			name:        "refuses when the install would start in enhanced mode",
			wantRefused: true,
		},
		{
			name: "is ignored when the install is not complete",
			stateOverride: func(*conf.Settings) datastoreV2.StartupState {
				return datastoreV2.StartupState{
					MigrationStatus: entities.MigrationStatusIdle,
					LegacyRequired:  true,
					Decision:        datastoreV2.DecisionLegacyMode,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logtest.CaptureBufferAt(t, slog.LevelError)
			settings, legacyPath := legacyTailFixture(t)
			dir := filepath.Dir(legacyPath)
			before := dirEntryNames(t, dir)

			metrics, err := observability.NewMetrics()
			require.NoError(t, err)
			svc := NewDatabaseService(settings, metrics)
			svc.checkLegacyTail = func(*conf.Settings, logger.Logger) (bool, error) {
				return false, fmt.Errorf("%w: test", datastoreV2.ErrLegacyTailUnverified)
			}
			if tt.stateOverride != nil {
				svc.checkStartupState = tt.stateOverride
			}

			err = svc.Start(t.Context())
			if tt.wantRefused {
				require.ErrorIs(t, err, datastoreV2.ErrLegacyTailUnverified)
				assert.Nil(t, svc.DataStore(), "no datastore may be constructed for a refused start")
				assert.False(t, svc.IsV2OnlyMode())
				assert.ElementsMatch(t, before, dirEntryNames(t, dir),
					"a refused start must not rename or create any database file")
				return
			}

			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, svc.Stop(t.Context())) })
			assert.False(t, svc.IsV2OnlyMode())
			assert.NotNil(t, svc.DataStore())
			assert.FileExists(t, legacyPath, "consolidation must not run on an unverified tail")
			for _, name := range dirEntryNames(t, dir) {
				assert.NotContains(t, name, ".old", "no backup means no consolidation ran")
			}
		})
	}
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

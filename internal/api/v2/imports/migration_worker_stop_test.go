package importsapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	repomocks "github.com/tphakala/birdnet-go/internal/datastore/v2/repository/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// recordGate holds the worker inside a record: entered receives once the worker
// reaches the model lookup, and the lookup returns after release is closed.
type recordGate struct {
	entered chan struct{}
	release chan struct{}
}

// stopTestEnv is a migration worker wired to a file-backed v2 SQLite database
// and a mocked legacy repository, installed as the package-level worker.
type stopTestEnv struct {
	echo   *echo.Echo
	h      *Handler
	sm     *datastoreV2.StateManager
	worker *migration.Worker
	legacy *mocks.MockDetectionRepository

	// cancelWorker cancels the context passed to the worker by startWorker.
	cancelWorker context.CancelFunc
}

// newStopTestEnv builds the environment inside the current synctest bubble.
// A nil modelRepo uses the real repository.
func newStopTestEnv(t *testing.T, modelRepo repository.ModelRepository) *stopTestEnv {
	t.Helper()
	migrationTestMu.Lock()
	t.Cleanup(migrationTestMu.Unlock)

	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{ConfiguredPath: filepath.Join(t.TempDir(), "v2.db")})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	db := mgr.DB()
	sm := datastoreV2.NewStateManager(db)
	if modelRepo == nil {
		modelRepo = repository.NewModelRepository(db, nil, false, false)
	}
	legacy := mocks.NewMockDetectionRepository(t)
	w, err := migration.NewWorker(&migration.WorkerConfig{
		Legacy:             legacy,
		V2Detection:        repository.NewDetectionRepository(db, nil, false, false),
		LabelRepo:          repository.NewLabelRepository(db, nil, false, false),
		ModelRepo:          modelRepo,
		SourceRepo:         repository.NewAudioSourceRepository(db, nil, false, false),
		StateManager:       sm,
		Logger:             logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC),
		SpeciesLabelTypeID: 1,
	})
	require.NoError(t, err)

	prevSM, prevW := stateManager, migrationWorker
	stateManager, migrationWorker = sm, w
	t.Cleanup(func() { stateManager, migrationWorker = prevSM, prevW })

	e := echo.New()
	return &stopTestEnv{
		echo:   e,
		h:      &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}},
		sm:     sm,
		worker: w,
		legacy: legacy,
	}
}

// startWorker starts the worker with a context cancelled at test cleanup.
func (env *stopTestEnv) startWorker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	env.cancelWorker = cancel
	t.Cleanup(cancel)
	require.NoError(t, env.worker.Start(ctx))
}

// toCompleted moves the state machine to COMPLETED with a watermark.
func (env *stopTestEnv) toCompleted(t *testing.T, lastID uint) {
	t.Helper()
	require.NoError(t, env.sm.StartMigration(10))
	require.NoError(t, env.sm.TransitionToDualWrite())
	require.NoError(t, env.sm.TransitionToMigrating())
	require.NoError(t, env.sm.TransitionToValidating())
	require.NoError(t, env.sm.TransitionToCutover())
	require.NoError(t, env.sm.Complete())
	require.NoError(t, env.sm.IncrementProgress(lastID, int64(lastID)))
}

// call runs a handler against a fresh recorder.
func (env *stopTestEnv) call(handler func(echo.Context) error, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	_ = handler(env.echo.NewContext(httptest.NewRequest(http.MethodPost, path, http.NoBody), rec))
	return rec
}

func TestRollbackMigration_DoesNotDrainAfterReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		env := newStopTestEnv(t, nil)
		env.toCompleted(t, 5)
		env.startWorker(t)
		synctest.Wait() // worker parked in tail sync

		rec := env.call(env.h.RollbackMigration, "/rollback")
		synctest.Wait()

		assert.Equal(t, http.StatusOK, rec.Code)
		state, err := env.sm.GetState()
		require.NoError(t, err)
		assert.Equal(t, entities.MigrationStatusIdle, state.State)
		assert.Zero(t, state.LastMigratedID)
		assert.Zero(t, state.MigratedRecords)
		dirty, err := env.sm.GetDirtyIDCount()
		require.NoError(t, err)
		assert.Zero(t, dirty)
		assert.False(t, env.worker.IsRunning())
		env.legacy.AssertNotCalled(t, "Search", mock.Anything, mock.Anything)
	})
}

// newCancelEnv returns an environment in DUAL_WRITE whose worker is held
// inside the first record of a three-record batch.
func newCancelEnv(t *testing.T) (*stopTestEnv, *recordGate) {
	t.Helper()
	gate := &recordGate{entered: make(chan struct{}), release: make(chan struct{})}
	models := repomocks.NewMockModelRepository(t)
	models.EXPECT().GetOrCreate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string, string, string, entities.ModelType, *string) (*entities.AIModel, error) {
			gate.entered <- struct{}{}
			<-gate.release
			return nil, io.ErrUnexpectedEOF
		})
	env := newStopTestEnv(t, models)
	env.legacy.EXPECT().Search(mock.Anything, mock.Anything).
		Return([]*detection.Result{{ID: 1}, {ID: 2}, {ID: 3}}, int64(3), nil).Once()
	require.NoError(t, env.sm.StartMigration(3))
	require.NoError(t, env.sm.TransitionToDualWrite())
	env.startWorker(t)
	<-gate.entered
	return env, gate
}

func TestCancelMigration_WaitsForInFlightBatch(t *testing.T) {
	t.Run("waits for the worker before resetting state", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			env, gate := newCancelEnv(t)
			done := make(chan struct{})
			var rec *httptest.ResponseRecorder
			go func() {
				defer close(done)
				rec = env.call(env.h.CancelMigration, "/cancel")
			}()
			synctest.Wait()
			returnedEarly := false
			select {
			case <-done:
				returnedEarly = true
			default:
			}

			close(gate.release)
			synctest.Wait()
			<-done

			assert.False(t, returnedEarly, "cancel must wait while the worker is inside a record")
			assert.Equal(t, http.StatusOK, rec.Code)
			state, err := env.sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, entities.MigrationStatusIdle, state.State)
			assert.Zero(t, state.LastMigratedID)
			dirty, err := env.sm.GetDirtyIDCount()
			require.NoError(t, err)
			assert.Zero(t, dirty)
			assert.False(t, env.worker.IsRunning())
		})
	})

	t.Run("times out without cancelling", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			env, gate := newCancelEnv(t)
			done := make(chan struct{})
			var rec *httptest.ResponseRecorder
			go func() {
				defer close(done)
				rec = env.call(env.h.CancelMigration, "/cancel")
			}()
			synctest.Wait()
			time.Sleep(migrationWorkerStopTimeout)
			<-done

			assert.Equal(t, http.StatusConflict, rec.Code)
			state, err := env.sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, entities.MigrationStatusDualWrite, state.State)

			close(gate.release)
			synctest.Wait()
		})
	})
}

// completedEnvWithBlockedDrain returns an environment in COMPLETED whose worker
// is parked in tail sync and whose final drain blocks inside the legacy Search
// until release is closed.
func completedEnvWithBlockedDrain(t *testing.T) (env *stopTestEnv, release chan struct{}) {
	t.Helper()
	const watermark = 5
	release = make(chan struct{})
	env = newStopTestEnv(t, nil)
	env.legacy.EXPECT().
		Search(mock.Anything, mock.MatchedBy(func(f *datastore.DetectionFilters) bool { return f.MinID == watermark })).
		RunAndReturn(func(context.Context, *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
			<-release
			return []*detection.Result{{ID: watermark + 1}}, 1, nil
		}).Once()
	env.toCompleted(t, watermark)
	env.startWorker(t)
	synctest.Wait() // worker parked in tail sync
	migrationMu.RLock()
	prevCancel := migrationWorkerCancel
	migrationMu.RUnlock()
	SetMigrationWorkerCancel(env.cancelWorker)
	t.Cleanup(func() { SetMigrationWorkerCancel(prevCancel) })
	return env, release
}

func TestStopMigrationWorker_DrainsBeforeReturning(t *testing.T) {
	t.Run("returns after the final drain", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			env, release := completedEnvWithBlockedDrain(t)
			returned := make(chan error, 1)
			go func() { returned <- StopMigrationWorker(t.Context()) }()
			synctest.Wait()
			select {
			case err := <-returned:
				t.Errorf("StopMigrationWorker returned (%v) while the final drain was still running", err)
			default:
			}

			close(release)
			synctest.Wait()

			require.NoError(t, <-returned)
			state, err := env.sm.GetState()
			require.NoError(t, err)
			assert.Equal(t, uint(6), state.LastMigratedID, "the drain must finish before shutdown returns")
			assert.False(t, env.worker.IsRunning())
		})
	})

	t.Run("bounded by the shutdown wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			env, release := completedEnvWithBlockedDrain(t)
			returned := make(chan error, 1)
			go func() { returned <- StopMigrationWorker(t.Context()) }()
			synctest.Wait()
			time.Sleep(migrationWorkerShutdownWait)

			require.ErrorIs(t, <-returned, migration.ErrWorkerStopTimeout)

			close(release)
			synctest.Wait()
			assert.False(t, env.worker.IsRunning())
		})
	})
}

func TestCancelMigration_CompletedKeepsTailSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		env := newStopTestEnv(t, nil)
		env.toCompleted(t, 5)
		env.startWorker(t)
		synctest.Wait() // worker parked in tail sync

		rec := env.call(env.h.CancelMigration, "/cancel")
		synctest.Wait()

		assert.Equal(t, http.StatusConflict, rec.Code, "a completed migration cannot be cancelled")
		state, err := env.sm.GetState()
		require.NoError(t, err)
		assert.Equal(t, entities.MigrationStatusCompleted, state.State)
		assert.True(t, env.worker.IsRunning(), "a rejected cancel must leave tail sync running")
	})
}

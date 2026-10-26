package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// These tests are NOT parallel: they publish to the process-wide settings
// snapshot and use the process-wide settings writer lock.

const (
	// writeLockTestInterval is the realtime.interval value the writers under
	// test set; the seeded value is 15 (getTestSettings).
	writeLockTestInterval = 20
	// writeLockSectionRealtime is the PATCH section whose body carries the interval.
	writeLockSectionRealtime = "realtime"
)

// TestSettingsWriters_WaitForSettingsWriterLock pins that the PUT handler, the
// PATCH handler and the hand-off writer used by the TLS, MQTT TLS and species
// exclusion handlers all read their baseline under the process-wide settings
// writer lock: while another writer is mid-cycle (a gallery install is the
// real-world one) none of them completes, and once that writer publishes its
// model install, the save lands on top of it instead of replacing it. A handler
// that read its baseline before waiting would finish while the lock is held, or
// would publish a clone of the pre-install snapshot and revert models.enabled.
func TestSettingsWriters_WaitForSettingsWriterLock(t *testing.T) {
	tests := []struct {
		name string
		// write performs the write and returns the HTTP status (0 when the writer
		// is not an HTTP handler) and the writer's error.
		write func(e *echo.Echo, c *Controller) (status int, err error)
	}{
		{
			name: "PUT",
			write: func(e *echo.Echo, c *Controller) (int, error) {
				req := httptest.NewRequest(http.MethodPut, "/api/v2/settings",
					bytes.NewReader([]byte(`{"realtime":{"interval":20}}`)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				err := c.UpdateSettings(e.NewContext(req, rec))
				return rec.Code, err
			},
		},
		{
			name: "PATCH",
			write: func(e *echo.Echo, c *Controller) (int, error) {
				req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+writeLockSectionRealtime,
					bytes.NewReader([]byte(`{"interval":20}`)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				ctx := e.NewContext(req, rec)
				ctx.SetParamNames("section")
				ctx.SetParamValues(writeLockSectionRealtime)
				err := c.UpdateSectionSettings(ctx)
				return rec.Code, err
			},
		},
		{
			name: "updateAndSaveSettings",
			write: func(_ *echo.Echo, c *Controller) (int, error) {
				c.settingsMutex.Lock()
				defer c.settingsMutex.Unlock()
				_, _, err := c.updateAndSaveSettings(func(_, updated *conf.Settings) error {
					updated.Realtime.Interval = writeLockTestInterval
					return nil
				})
				return 0, err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := getTestSettings(t)
			initial.Models.Enabled = stalenessBaseModels()
			e := echo.New()
			c := newGlobalOwnerController(t, e, initial)
			c.SetTestContext(t.Context(), func() {})
			c.DisableSaveSettings = true

			// A model install is mid-cycle and holds the writer lock.
			release := conftest.HoldSettingsWriter(t, func(updated *conf.Settings) {
				updated.Models.Enabled = stalenessInstalledModels()
			})

			done := make(chan struct{})
			var (
				status int
				err    error
			)
			go func() {
				defer close(done)
				status, err = tt.write(e, c)
			}()

			conftest.AssertStillBlocked(t, done, tt.name)
			release()
			<-done

			require.NoError(t, err)
			if tt.name != "updateAndSaveSettings" {
				require.Equal(t, http.StatusOK, status)
			}
			final := conf.GetSettings()
			assert.Equal(t, stalenessInstalledModels(), final.Models.Enabled,
				"the install must survive the save that waited for it")
			assert.Equal(t, writeLockTestInterval, final.Realtime.Interval,
				"the save must land on top of the install")
			assert.Same(t, final, c.ControllerSettings(),
				"the controller snapshot must follow the published global")
		})
	}
}

// TestRollbackSettings pins invariant "rollback never removes a later writer's
// update": the owner of the global snapshot restores the previous snapshot only
// while its own is still published, otherwise it leaves the newer snapshot in
// place and reports errRollbackSuperseded so the caller does not claim a
// rollback.
func TestRollbackSettings(t *testing.T) {
	tests := []struct {
		name           string
		supersede      bool
		wantRolledBack bool
	}{
		{name: "own snapshot still published is rolled back", supersede: false, wantRolledBack: true},
		{name: "another writer published since is left in place", supersede: true, wantRolledBack: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := getTestSettings(t)
			initial.Models.Enabled = stalenessBaseModels()
			c := newGlobalOwnerController(t, echo.New(), initial)

			current, updated, err := c.commitSettings(func(_, updated *conf.Settings) error {
				updated.Realtime.Interval = writeLockTestInterval
				return nil
			})
			require.NoError(t, err)
			require.Same(t, initial, current)

			if tt.supersede {
				_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
					updated.Models.Enabled = stalenessInstalledModels()
					return nil
				})
				require.NoError(t, err)
			}

			rbErr := c.rollbackSettings(current, updated)

			if tt.wantRolledBack {
				require.NoError(t, rbErr)
				assertRestoredContent(t, initial, c)
				return
			}
			require.ErrorIs(t, rbErr, errRollbackSuperseded)
			final := conf.GetSettings()
			assert.Equal(t, stalenessInstalledModels(), final.Models.Enabled, "the newer writer's change must stay")
			assert.Equal(t, writeLockTestInterval, final.Realtime.Interval, "the newer snapshot is left as published")
		})
	}
}

// TestUpdateSettings_SaveFailureRollsBack pins the end-to-end PUT and PATCH
// answer when the disk save fails and no other writer interfered: 500, the
// message says the change was rolled back, and the published snapshot is the
// pre-request one.
func TestUpdateSettings_SaveFailureRollsBack(t *testing.T) {
	tests := []struct {
		name string
		// write performs the request against c and returns the recorder.
		write func(e *echo.Echo, c *Controller) *httptest.ResponseRecorder
	}{
		{
			name: "PUT",
			write: func(e *echo.Echo, c *Controller) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPut, "/api/v2/settings",
					bytes.NewReader([]byte(`{"realtime":{"interval":20}}`)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))
				return rec
			},
		},
		{
			name: "PATCH",
			write: func(e *echo.Echo, c *Controller) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+writeLockSectionRealtime,
					bytes.NewReader([]byte(`{"interval":20}`)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				ctx := e.NewContext(req, rec)
				ctx.SetParamNames("section")
				ctx.SetParamValues(writeLockSectionRealtime)
				require.NoError(t, c.UpdateSectionSettings(ctx))
				return rec
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := getTestSettings(t)
			e := echo.New()
			c := newGlobalOwnerController(t, e, initial)
			c.SetTestContext(t.Context(), func() {})
			c.DisableSaveSettings = false
			failSettingsSave(t)

			rec := tt.write(e, c)

			assert.Equal(t, http.StatusInternalServerError, rec.Code)
			assert.Contains(t, rec.Body.String(), msgSaveFailedRolledBack)
			assertRestoredContent(t, initial, c)
		})
	}
}

// recordSentryReconfigures replaces c.controlChan with an unbuffered channel
// and starts a reader that stands in for the control monitor: each received
// reconfigure_sentry records the live opt-in, as ReconfigureSentry would read
// it. Other signals are drained and ignored. The returned stop waits for the
// controller's goroutines, closes the channel and waits for the reader; it is
// idempotent, registered as a cleanup (so an early require failure does not
// leak the reader) and must also be called before the recorded slice is read.
// Call it after newGlobalOwnerController and failSettingsSave so the cleanup
// runs before the settings restore.
func recordSentryReconfigures(t *testing.T, c *Controller) (stop func(), seen *[]bool) {
	t.Helper()
	ch := make(chan string)
	c.controlChan = ch
	seen = new([]bool)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for action := range ch {
			if action == telemetry.SignalReconfigureSentry {
				*seen = append(*seen, conf.GetSettings().Sentry.Enabled)
			}
		}
	}()
	stop = sync.OnceFunc(func() {
		c.Wait()
		close(ch)
		<-readerDone
	})
	t.Cleanup(stop)
	return stop, seen
}

// TestUpdateSettings_SaveFailureResendsSentryReconfigure pins when a save
// failure queues reconfigure_sentry again. A rolled-back Error Tracking opt-in
// change queues it a second time, after the rollback: the first signal is
// queued before the save, so the control monitor may already have bound the
// client for the snapshot that is then taken back. A rolled-back write that
// leaves the opt-in alone queues nothing extra.
func TestUpdateSettings_SaveFailureResendsSentryReconfigure(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		section string // PATCH section; empty for PUT
		body    string
		// wantSignals is the number of reconfigure_sentry signals expected.
		wantSignals int
	}{
		{name: "PUT opt-in change", method: http.MethodPut, path: "/api/v2/settings",
			body: `{"sentry":{"enabled":true}}`, wantSignals: 2},
		{name: "PATCH opt-in change", method: http.MethodPatch, path: "/api/v2/settings/sentry",
			section: "sentry", body: `{"enabled":true}`, wantSignals: 2},
		{name: "PUT opt-in unchanged", method: http.MethodPut, path: "/api/v2/settings",
			body: `{"realtime":{"interval":20}}`},
		{name: "PATCH opt-in unchanged", method: http.MethodPatch, path: "/api/v2/settings/" + writeLockSectionRealtime,
			section: writeLockSectionRealtime, body: `{"interval":20}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := getTestSettings(t)
			require.False(t, initial.Sentry.Enabled, "the opt-in starts off")
			e := echo.New()
			c := newGlobalOwnerController(t, e, initial)
			c.SetTestContext(t.Context(), func() {})
			c.DisableSaveSettings = false
			failSettingsSave(t)
			stop, seen := recordSentryReconfigures(t, c)

			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader([]byte(tt.body)))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			if tt.method == http.MethodPut {
				require.NoError(t, c.UpdateSettings(ctx))
			} else {
				ctx.SetParamNames("section")
				ctx.SetParamValues(tt.section)
				require.NoError(t, c.UpdateSectionSettings(ctx))
			}
			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Contains(t, rec.Body.String(), msgSaveFailedRolledBack)
			stop()

			require.Len(t, *seen, tt.wantSignals)
			if tt.wantSignals > 0 {
				assert.False(t, (*seen)[len(*seen)-1], "the last reconfigure must run against the restored opt-in")
			}
		})
	}
}

// TestFinishSettingsWrite_SupersededRollbackDoesNotResendSentryReconfigure pins
// that no reconfigure_sentry is queued again when another writer published on
// top of an opt-in change before the save failed: the newer snapshot stays, so
// the signal queued before the save already converges to the live opt-in.
func TestFinishSettingsWrite_SupersededRollbackDoesNotResendSentryReconfigure(t *testing.T) {
	initial := getTestSettings(t)
	require.False(t, initial.Sentry.Enabled, "the opt-in starts off")
	c := newGlobalOwnerController(t, echo.New(), initial)
	c.SetTestContext(t.Context(), func() {})
	c.DisableSaveSettings = false
	failSettingsSave(t)
	stop, seen := recordSentryReconfigures(t, c)

	current, updated, err := c.commitSettings(func(_, updated *conf.Settings) error {
		updated.Sentry.Enabled = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, sentryEnabledChanged(current, updated))

	// The other writer (a model install) publishes on top of the write, so the
	// rollback is superseded.
	apply := func() error {
		_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
			updated.Models.Enabled = stalenessInstalledModels()
			return nil
		})
		return err
	}

	msg, err := c.finishSettingsWrite(current, updated, apply)
	stop()

	require.Error(t, err)
	assert.Equal(t, msgSaveFailedNotRolledBack, msg)
	assert.True(t, conf.GetSettings().Sentry.Enabled, "the newer snapshot must stay as published")
	assert.Empty(t, *seen, "a superseded rollback must not queue reconfigure_sentry again")
}

// failSettingsSave points conf.ConfigPath at a file in a missing directory so
// conf.SaveSettings fails without touching any real configuration file.
func failSettingsSave(t *testing.T) {
	t.Helper()
	prevPath := conf.ConfigPath
	conf.ConfigPath = filepath.Join(t.TempDir(), "missing.yaml")
	t.Cleanup(func() { conf.ConfigPath = prevPath })
}

// TestUpdateAndSaveSettings_SaveFailureRollsBack pins that the hand-off writer
// reports a failed save with the shared rollback message and restores the
// previous snapshot.
func TestUpdateAndSaveSettings_SaveFailureRollsBack(t *testing.T) {
	initial := getTestSettings(t)
	c := newGlobalOwnerController(t, echo.New(), initial)
	c.DisableSaveSettings = false
	failSettingsSave(t)

	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()
	_, _, err := c.updateAndSaveSettings(func(_, updated *conf.Settings) error {
		updated.Realtime.Interval = writeLockTestInterval
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), msgSaveFailedRolledBack)
	assertRestoredContent(t, initial, c)
}

// TestUpdateAndSaveSettings_MintsProfilingToken pins that the hand-off writer,
// on the controller that owns the global snapshot, mints the profiling token
// through commitSettings after a successful mutate, and mints nothing into the
// clone when the mutate returns an error. Profiling is already enabled in the
// seeded snapshot, so a mint that ran before the mutate result was checked
// would leave a token in the clone.
func TestUpdateAndSaveSettings_MintsProfilingToken(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantToken bool
	}{
		{name: "mutate succeeds", wantToken: true},
		{name: "mutate publishes nothing", err: conf.ErrSettingsUnchanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := getTestSettings(t)
			initial.Diagnostics.Profiling.Enabled = true
			c := newGlobalOwnerController(t, echo.New(), initial)
			c.DisableSaveSettings = true

			var clone *conf.Settings
			c.settingsMutex.Lock()
			defer c.settingsMutex.Unlock()
			_, _, err := c.updateAndSaveSettings(func(_, updated *conf.Settings) error {
				clone = updated
				updated.Realtime.Interval = writeLockTestInterval
				return tt.err
			})

			require.NotNil(t, clone)
			if tt.wantToken {
				require.NoError(t, err)
				assert.NotEmpty(t, conf.GetSettings().Diagnostics.Profiling.Token)
				return
			}
			require.ErrorIs(t, err, tt.err)
			assert.Empty(t, clone.Diagnostics.Profiling.Token, "a failed mutate must not leave a minted token")
			assert.Same(t, initial, conf.GetSettings())
		})
	}
}

// TestFinishSettingsWrite_RollbackMessages pins which client message
// finishSettingsWrite picks for each failure and outcome pair. The "not rolled
// back" rows are the ones no handler test reached before: a writer (here a model
// install) publishes between the write and its failure, so the newer snapshot
// must stay and the message must not claim a rollback.
func TestFinishSettingsWrite_RollbackMessages(t *testing.T) {
	errApply := errors.NewStd("apply failed")
	publishInstall := func() {
		_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
			updated.Models.Enabled = stalenessInstalledModels()
			return nil
		})
		require.NoError(t, err)
	}

	resetProfileRates(t)
	// applyRate stands in for handleSettingsChanges applying a profiling rate
	// that the saved config does not carry.
	applyRate := func() { runtime.SetMutexProfileFraction(conf.RecommendedMutexProfileFraction) }

	tests := []struct {
		name string
		// apply is the apply step handed to finishSettingsWrite; nil skips it.
		apply          func() error
		failSave       bool
		wantMsg        string
		wantRolledBack bool
	}{
		{
			name:           "apply fails, nothing published since",
			apply:          func() error { return errApply },
			wantMsg:        msgApplyFailedRolledBack,
			wantRolledBack: true,
		},
		{
			name:    "apply publishes then fails",
			apply:   func() error { publishInstall(); return errApply },
			wantMsg: msgApplyFailedNotRolledBack,
		},
		{
			name:           "save fails, nothing published since",
			apply:          func() error { applyRate(); return nil },
			failSave:       true,
			wantMsg:        msgSaveFailedRolledBack,
			wantRolledBack: true,
		},
		{
			name:     "apply publishes, save fails",
			apply:    func() error { publishInstall(); applyRate(); return nil },
			failSave: true,
			wantMsg:  msgSaveFailedNotRolledBack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := getTestSettings(t)
			initial.Models.Enabled = stalenessBaseModels()
			c := newGlobalOwnerController(t, echo.New(), initial)
			c.DisableSaveSettings = false
			if tt.failSave {
				failSettingsSave(t)
			}

			current, updated, err := c.commitSettings(func(_, updated *conf.Settings) error {
				updated.Realtime.Interval = writeLockTestInterval
				return nil
			})
			require.NoError(t, err)

			msg, err := c.finishSettingsWrite(current, updated, tt.apply)

			require.Error(t, err)
			assert.Equal(t, tt.wantMsg, msg)
			final := conf.GetSettings()
			if tt.wantRolledBack {
				assertRestoredContent(t, initial, c)
				if tt.failSave {
					assert.Zero(t, runtime.SetMutexProfileFraction(-1),
						"a rolled-back save must restore the profiling rate of the surviving config")
				}
				return
			}
			if tt.failSave {
				assert.Equal(t, conf.RecommendedMutexProfileFraction, runtime.SetMutexProfileFraction(-1),
					"a superseded rollback keeps the newer snapshot, so its rate must stay")
			}
			assert.Equal(t, stalenessInstalledModels(), final.Models.Enabled, "the newer writer's change must stay")
			assert.Equal(t, writeLockTestInterval, final.Realtime.Interval, "the newer snapshot is left as published")
		})
	}
}

// TestUpdateSettings_OwnerWithoutGlobalFails pins that a controller owning the
// global singleton refuses to write when no global snapshot is published (it no
// longer publishes its own snapshot as the global): the existing "settings not
// initialized" 500, nothing published.
func TestUpdateSettings_OwnerWithoutGlobalFails(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })
	conf.StoreSettings(nil)

	e := echo.New()
	c := getTestController(t, e)
	c.isGlobalOwner = true

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings",
		bytes.NewReader([]byte(`{"realtime":{"interval":20}}`)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Failed to get settings")
	assert.Nil(t, conf.GetSettings(), "nothing may be published as the global")
}

// assertRestoredContent pins that a rolled-back write left the published
// snapshot holding the pre-write content. The rollback publishes a clone under a
// fresh revision, so the pointer is not the original one; it must be the one the
// controller serves.
func assertRestoredContent(t *testing.T, initial *conf.Settings, c *Controller) {
	t.Helper()
	published := conf.GetSettings()
	assert.Equal(t, initial.Realtime.Interval, published.Realtime.Interval, "a rollback must restore the previous interval")
	assert.Equal(t, initial.Models.Enabled, published.Models.Enabled, "a rollback must restore the previous models")
	assert.NotEqual(t, initial.Revision(), published.Revision(), "a rollback must publish under a fresh revision")
	assert.Same(t, published, c.ControllerSettings())
}

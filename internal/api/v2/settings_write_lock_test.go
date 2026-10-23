package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
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
				assert.Same(t, initial, conf.GetSettings())
				assert.Same(t, initial, c.ControllerSettings())
				return
			}
			require.ErrorIs(t, rbErr, errRollbackSuperseded)
			final := conf.GetSettings()
			assert.Equal(t, stalenessInstalledModels(), final.Models.Enabled, "the newer writer's change must stay")
			assert.Equal(t, writeLockTestInterval, final.Realtime.Interval, "the newer snapshot is left as published")
		})
	}
}

// TestUpdateSettings_SaveFailureRollsBack pins the end-to-end PUT answer when the
// disk save fails and no other writer interfered: 500, the message says the
// change was rolled back, and the published snapshot is the pre-request one.
func TestUpdateSettings_SaveFailureRollsBack(t *testing.T) {
	initial := getTestSettings(t)
	e := echo.New()
	c := newGlobalOwnerController(t, e, initial)
	c.SetTestContext(t.Context(), func() {})
	c.DisableSaveSettings = false

	// An explicit config path that does not exist makes conf.SaveSettings fail
	// without touching any real configuration file.
	prevPath := conf.ConfigPath
	conf.ConfigPath = filepath.Join(t.TempDir(), "missing.yaml")
	t.Cleanup(func() { conf.ConfigPath = prevPath })

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings",
		bytes.NewReader([]byte(`{"realtime":{"interval":20}}`)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), msgSaveFailedRolledBack)
	assert.Same(t, initial, conf.GetSettings(), "a failed save must restore the previous snapshot")
	assert.Same(t, initial, c.ControllerSettings())
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

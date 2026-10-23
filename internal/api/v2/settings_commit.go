// internal/api/v2/settings_commit.go
package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Client-facing messages for a settings write whose side effects or disk save
// failed after the new snapshot was published. The "not rolled back" variants
// are used when another writer (a model install, a range filter rebuild)
// published on top of this write in between, so the previous snapshot could not
// be restored without discarding that writer's change. They must never claim a
// rollback happened.
const (
	msgApplyFailedRolledBack    = "Failed to apply settings changes, rolled back to previous settings"
	msgApplyFailedNotRolledBack = "Failed to apply settings changes. The change could not be rolled back because settings changed at the same time; reload the settings to see their current state"
	msgSaveFailedRolledBack     = "Failed to save settings, rolled back to previous settings"
	msgSaveFailedNotRolledBack  = "Failed to save settings. The change could not be rolled back because settings changed at the same time; reload the settings to see their current state"
)

// errRollbackSuperseded reports that a rollback was skipped because another
// writer published a newer settings snapshot after the one being rolled back.
var errRollbackSuperseded = errors.NewStd("settings changed concurrently, change not rolled back")

// settingsRejection records why a commitSettings mutate function refused a
// request (malformed body, failed validation), so the handler can answer with
// the right status and message after commitSettings returns. The zero value
// means "not rejected".
type settingsRejection struct {
	err     error
	message string
	status  int
}

// reject records a rejection and returns err, so a mutate function can
// "return rej.reject(err, ...)".
func (r *settingsRejection) reject(err error, message string, status int) error {
	r.err, r.message, r.status = err, message, status
	return err
}

// rejected reports whether reject was called.
func (r *settingsRejection) rejected() bool {
	return r.err != nil
}

// commitSettings builds and publishes a new settings snapshot. mutate receives
// the snapshot that is current when the write happens and a clone to change;
// when it returns nil the clone is published and c.Settings is refreshed, when
// it returns an error nothing is published and the error is returned.
//
// For the controller that owns the global snapshot the baseline is read and the
// clone published inside conf.UpdateSettings, under the process-wide settings
// writer lock, so the write cannot be built from a snapshot another writer
// (model install, range filter rebuild, path correction) has since replaced. A
// controller that does not own the global (tests) works on its own snapshot
// only and never touches the global.
//
// Callers hold c.settingsMutex. mutate runs under the writer lock and must obey
// the rules documented on conf.UpdateSettings: it may read and log, and must
// not call into the classifier, the orchestrator, the model manager or
// handleSettingsChanges. Read the request body before calling this, not inside
// mutate, so a slow client cannot hold the writer lock.
func (c *Controller) commitSettings(mutate func(current, updated *conf.Settings) error) (current, updated *conf.Settings, err error) {
	if c.isGlobalOwner {
		current, updated, err = conf.UpdateSettings(mutate)
		if err != nil {
			return nil, nil, err
		}
		c.Settings.Store(updated)
		return current, updated, nil
	}

	current = c.getSettingsOrFallback()
	if current == nil {
		return nil, nil, conf.ErrSettingsNotLoaded
	}
	updated = conf.CloneSettings(current)
	if err := mutate(current, updated); err != nil {
		return nil, nil, err
	}
	c.Settings.Store(updated)
	return current, updated, nil
}

// rollbackSettings takes back a snapshot that commitSettings published. The
// owner of the global snapshot restores current only if updated is still the
// published snapshot; if another writer published since, that newer snapshot
// stays (discarding it would lose an install or a species list rebuild), a
// warning is logged, and errRollbackSuperseded is returned. The caller must then
// tell the client the change was NOT rolled back. Callers hold c.settingsMutex.
func (c *Controller) rollbackSettings(current, updated *conf.Settings) error {
	if c.isGlobalOwner && !conf.CompareAndStoreSettings(updated, current) {
		apicore.GetLogger().Warn("Settings changed concurrently, leaving the newer snapshot in place instead of rolling back")
		return errRollbackSuperseded
	}
	c.Settings.Store(current)
	return nil
}

// respondCommitError answers a failed commitSettings call. A mutate function
// that rejected the request keeps its own status and message; a missing settings
// snapshot is the usual "settings not initialized" 500; anything else is a 500.
func (c *Controller) respondCommitError(ctx echo.Context, err error, rej *settingsRejection) error {
	if rej.rejected() {
		return c.HandleError(ctx, rej.err, rej.message, rej.status)
	}
	if errors.Is(err, conf.ErrSettingsNotLoaded) {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Settings not initialized during update attempt")
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}
	return c.HandleError(ctx, err, "Failed to update settings", http.StatusInternalServerError)
}

// updateAndSaveSettings publishes a settings change built by mutate and
// persists it to disk. It is the hand-off for the sub-package writers (TLS,
// MQTT TLS, species exclusion) that read-modify-write settings. Must be called
// while c.settingsMutex is held. mutate follows the rules of commitSettings; it
// may return conf.ErrSettingsUnchanged to publish nothing, which is returned
// as is so the caller can treat it as success.
//
// On a save failure the snapshot is rolled back by compare-and-swap; if another
// writer published on top in the meantime the newer snapshot is kept and the
// returned error says the change could not be rolled back.
func (c *Controller) updateAndSaveSettings(mutate func(current, updated *conf.Settings) error) (current, updated *conf.Settings, err error) {
	current, updated, err = c.commitSettings(func(cur, upd *conf.Settings) error {
		if err := mutate(cur, upd); err != nil {
			return err
		}
		ensureProfilingTokenForSave(upd)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	if c.isGlobalOwner && !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			if rbErr := c.rollbackSettings(current, updated); rbErr != nil {
				return nil, nil, fmt.Errorf("failed to save settings and the change could not be rolled back because settings changed at the same time: %w", err)
			}
			return nil, nil, fmt.Errorf("failed to save settings: %w", err)
		}
	}

	c.emitSettingsSaved(current, updated)
	return current, updated, nil
}

// emitSettingsSaved emits the settings_saved event with the key-level diff
// between two snapshots (fire-and-forget). It emits nothing when they do not
// differ.
func (c *Controller) emitSettingsSaved(current, updated *conf.Settings) {
	if changes := diffSettings(current, updated); len(changes) > 0 {
		events.Emit(context.Background(), "settings", "settings_saved", "Settings saved via UI", map[string]any{
			"source":       "ui",
			"change_count": len(changes),
			"changes":      changes,
		})
	}
}

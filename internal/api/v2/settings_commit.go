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

// rollbackMessage picks the client message for a failed write after
// rollbackSettings returned rbErr: rolledBack when the rollback happened,
// notRolledBack when it was superseded. The latter must never claim a rollback.
func rollbackMessage(rbErr error, rolledBack, notRolledBack string) string {
	if rbErr != nil {
		return notRolledBack
	}
	return rolledBack
}

// errRollbackSuperseded reports that a rollback was skipped because another
// writer published a newer settings snapshot after the one being rolled back.
var errRollbackSuperseded = errors.NewStd("settings changed concurrently, change not rolled back")

// rejectionError is the error a commitSettings mutate function returns when
// it refuses the request (malformed body, failed validation), carrying the HTTP
// status and client message the handler answers with.
type rejectionError struct {
	err     error
	message string
	status  int
	// etag, when set, is sent as the ETag response header (the current settings
	// ETag of a refused If-Match).
	etag string
}

// errSettingsPreconditionFailed is the cause of a refused If-Match.
var errSettingsPreconditionFailed = errors.NewStd("settings precondition failed")

// newPreconditionRejection builds the rejection for an If-Match that does not
// match the current settings; etag is the current ETag, returned to the client
// so it can reload and retry.
func newPreconditionRejection(etag string) error {
	return &rejectionError{
		err:     errSettingsPreconditionFailed,
		message: msgSettingsPreconditionFailed,
		status:  http.StatusPreconditionFailed,
		etag:    etag,
	}
}

// newRejection wraps err as a rejectionError with the given client message
// and HTTP status.
func newRejection(err error, message string, status int) error {
	return &rejectionError{err: err, message: message, status: status}
}

// Error returns the wrapped error text.
func (r *rejectionError) Error() string { return r.err.Error() }

// Unwrap returns the wrapped error.
func (r *rejectionError) Unwrap() error { return r.err }

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
// Every write that goes through here also gets the profiling token minted (see
// ensureProfilingTokenForSave) after mutate succeeds, so callers do not mint.
//
// Callers hold c.settingsMutex. mutate runs under the writer lock and must obey
// the rules documented on conf.UpdateSettings: it may read, validate and log, and must
// not call into the classifier, the orchestrator, the model manager or
// handleSettingsChanges. Read the request body before calling this, not inside
// mutate, so a slow client cannot hold the writer lock.
func (c *Controller) commitSettings(mutate func(current, updated *conf.Settings) error) (current, updated *conf.Settings, err error) {
	inner := mutate
	mutate = func(cur, upd *conf.Settings) error {
		if err := inner(cur, upd); err != nil {
			return err
		}
		ensureProfilingTokenForSave(upd)
		return nil
	}

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
	// No new revision here: only conf stamps one, so a write through a controller
	// that does not own the global snapshot (tests) leaves its ETag unchanged.
	c.Settings.Store(updated)
	return current, updated, nil
}

// rollbackSettings takes back a snapshot that commitSettings published. The
// owner of the global snapshot restores current only if updated is still the
// published snapshot; if another writer published since, that newer snapshot
// stays (discarding it would lose an install or a species list rebuild), a
// warning is logged, and errRollbackSuperseded is returned. The caller must then
// tell the client the change was NOT rolled back. For the owner of the global
// snapshot the restored snapshot carries a fresh revision (see
// conf.CompareAndStoreSettings), so an ETag issued for the rolled-back snapshot
// cannot match it; a controller that does not own it restores current as it was. Callers hold c.settingsMutex.
func (c *Controller) rollbackSettings(current, updated *conf.Settings) error {
	restored := current
	if c.isGlobalOwner {
		published, ok := conf.CompareAndStoreSettings(updated, current)
		if !ok {
			apicore.GetLogger().Warn("Settings changed concurrently, leaving the newer snapshot in place instead of rolling back")
			return errRollbackSuperseded
		}
		restored = published
	}
	c.Settings.Store(restored)
	return nil
}

// respondCommitError answers a failed commitSettings call. A mutate function
// that rejected the request keeps its own status and message; a missing settings
// snapshot is the usual "settings not initialized" 500; anything else is a 500.
func (c *Controller) respondCommitError(ctx echo.Context, err error) error {
	if rej, ok := errors.AsType[*rejectionError](err); ok {
		if rej.etag != "" {
			ctx.Response().Header().Set(apicore.HeaderETag, rej.etag)
		}
		return c.HandleError(ctx, rej.err, rej.message, rej.status)
	}
	if errors.Is(err, conf.ErrSettingsNotLoaded) {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Settings not initialized during update attempt")
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}
	return c.HandleError(ctx, err, "Failed to update settings", http.StatusInternalServerError)
}

// persistsSettings reports whether a settings write is saved to disk: only the
// controller that owns the global snapshot persists, and tests can switch it off
// with DisableSaveSettings.
func (c *Controller) persistsSettings() bool {
	return c.isGlobalOwner && !c.DisableSaveSettings
}

// finishSettingsWrite runs the steps that follow a commitSettings publish: the
// optional apply step (handleSettingsChanges for the HTTP handlers) and, for the
// owner of the global snapshot unless DisableSaveSettings is set, the disk save.
// When either fails the published snapshot is taken back by rollbackSettings, and
// the profiling sampling rates are restored after a rolled-back save failure. It
// returns the client message that matches the outcome (a "not rolled back"
// variant when another writer published on top, see rollbackMessage) together
// with the failure, or "", nil on success. Each failure is logged through the
// API logger when it is enabled. Callers hold c.settingsMutex.
//
// It runs outside the settings writer lock, because apply reaches into the
// classifier; never call it from a commitSettings mutate.
func (c *Controller) finishSettingsWrite(current, updated *conf.Settings, apply func() error) (clientMsg string, err error) {
	if apply != nil {
		if err := apply(); err != nil {
			// Take back our own snapshot so in-memory state matches what is on disk
			// (which was never overwritten), unless another writer published on top.
			rbErr := c.rollbackSettings(current, updated)
			c.LogErrorIfEnabled("Failed to apply settings changes, rolling back",
				logger.Error(err), logger.Bool("rolled_back", rbErr == nil))
			return rollbackMessage(rbErr, msgApplyFailedRolledBack, msgApplyFailedNotRolledBack), err
		}
	}

	// Persist to disk only when this controller owns the global snapshot
	// (production path) AND DisableSaveSettings is not set. conf.SaveSettings
	// reads conf.GetSettings internally; persisting from a test that stored a
	// standalone snapshot via c.Settings.Store would save an unrelated snapshot.
	if c.persistsSettings() {
		if err := conf.SaveSettings(); err != nil {
			// Roll back in-memory; the disk write never happened successfully.
			rbErr := c.rollbackSettings(current, updated)
			if rbErr == nil {
				restoreProfilingRates(current)
			}
			c.LogErrorIfEnabled("Failed to save settings to disk, rolling back",
				logger.Error(err), logger.Bool("rolled_back", rbErr == nil))
			return rollbackMessage(rbErr, msgSaveFailedRolledBack, msgSaveFailedNotRolledBack), err
		}
	}
	return "", nil
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
	current, updated, err = c.commitSettings(mutate)
	if err != nil {
		return nil, nil, err
	}

	if msg, err := c.finishSettingsWrite(current, updated, nil); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", msg, err)
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

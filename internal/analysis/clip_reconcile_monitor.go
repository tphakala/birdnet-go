// clip_reconcile_monitor.go - background monitor that reconciles persisted
// clip_name references against the audio files on disk.
package analysis

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// clipReconcileStartupDelay defers the first reconcile pass so it does not contend
// with boot-time I/O (model load, DB open, initial captures).
const clipReconcileStartupDelay = 2 * time.Minute

// clipReconcilePassInterval is the base wait between full reconcile passes, used
// at startup and after any pass that is not idle (it changed something, needs a
// retry, or was skipped for lack of an export path). Ghosts are rare and not
// time-critical to clear, so a slow cadence keeps disk I/O negligible.
const clipReconcilePassInterval = 1 * time.Hour

// clipReconcileMaxPassInterval caps the wait between passes while consecutive
// passes find nothing to change.
const clipReconcileMaxPassInterval = 24 * time.Hour

// clipReconcileBackoffFactor is the multiplier applied to the wait after each
// pass that changed nothing.
const clipReconcileBackoffFactor = 2

// exportBaseDir returns the configured audio export path, trimmed, read from the
// live settings so hot-reload takes effect.
func exportBaseDir() string {
	return strings.TrimSpace(conf.Setting().Realtime.Audio.Export.Path)
}

// clipReconcileMonitor continuously reconciles persisted clip_name references
// against the audio files on disk, clearing references to files that no longer
// exist (ghosts from failed exports, or from detections created while export was
// off), or re-linking them to a surviving spectrogram render when there is one.
// Unlike clipCleanupMonitor it runs regardless of the retention policy and
// regardless of whether audio export is currently enabled, because orphaned
// references persist across runtime toggling of the export setting. It reads the
// export path via conf.Setting() each pass so hot-reload takes effect, and the
// underlying diskmanager pass clears a reference only after a surviving clip
// earlier in the same pass shows the storage is attached, and only when its month
// directory exists, so a detached or replaced export volume never causes mass
// clearing. While passes keep changing nothing the wait between them doubles from
// clipReconcilePassInterval up to clipReconcileMaxPassInterval; a pass that
// cleared or retained rows, aborted or had write errors returns it to
// clipReconcilePassInterval. The interval is kept in memory only, so a restart
// begins at clipReconcilePassInterval again.
func clipReconcileMonitor(quitChan <-chan struct{}, dataStore datastore.Interface) {
	log := GetLogger()
	log.Info("clip reconcile monitor initialized",
		logger.String("operation", "clip_reconcile_init"))

	runClipReconcileLoop(quitChan, reconcileMonitorWait, func() (stop, idle bool) {
		return runClipReconcilePass(log, quitChan, dataStore, exportBaseDir)
	})
}

// runClipReconcileLoop defers the first pass by clipReconcileStartupDelay, then
// runs pass repeatedly. pass reports whether the loop should stop and whether the
// pass was idle (found nothing to change and needs no retry). The wait after a
// pass starts at clipReconcilePassInterval and doubles after each consecutive
// idle pass up to clipReconcileMaxPassInterval; a pass that is not idle returns it
// to clipReconcilePassInterval. The loop returns when wait reports shutdown or
// when pass asks to stop. wait and pass are parameters so tests can drive the
// loop without real delays or settings.
func runClipReconcileLoop(quitChan <-chan struct{}, wait func(<-chan struct{}, time.Duration) bool, pass func() (stop, idle bool)) {
	// Defer the first pass; bail out immediately if shutdown arrives first.
	if !wait(quitChan, clipReconcileStartupDelay) {
		return
	}
	interval := clipReconcilePassInterval
	logged := interval
	for {
		stop, idle := pass()
		if stop {
			return
		}
		if !idle {
			interval = clipReconcilePassInterval
		}
		if interval != logged {
			GetLogger().Debug("clip reconcile pass interval changed",
				logger.Duration("interval", interval),
				logger.String("operation", "clip_reconcile_interval"))
			logged = interval
		}
		if !wait(quitChan, interval) {
			return
		}
		if idle {
			interval = min(interval*clipReconcileBackoffFactor, clipReconcileMaxPassInterval)
		}
	}
}

// reconcileIdle reports whether a completed pass changed nothing and needs no
// retry: it cleared no rows, retained none, did not abort and had no write
// errors. Rows held for lack of evidence or a missing month directory do not
// count as a change.
func reconcileIdle(result *diskmanager.ReconcileResult) bool {
	return result.Cleared == 0 && result.Retained == 0 && !result.Aborted && result.WriteErrors == 0
}

// runClipReconcilePass runs one reconcile pass against the directory exportDir
// returns now, and logs its outcome. exportDir is read again during the pass so a
// hot-reloaded export path aborts a pass that started on the old one. stop is
// true when the pass ended because shutdown was requested. idle is true only for
// a completed pass that changed nothing; a skipped pass is not idle, so an
// unconfigured export path does not lengthen the wait.
func runClipReconcilePass(log logger.Logger, quitChan <-chan struct{}, store diskmanager.ReconcileStore, exportDir func() string) (stop, idle bool) {
	baseDir := exportDir()
	if baseDir == "" {
		log.Debug("skipping clip reconcile: export path not configured",
			logger.String("operation", "clip_reconcile_skip"))
		return false, false
	}
	pathCurrent := func() bool {
		return exportDir() == baseDir
	}
	result := diskmanager.ReconcileClipOrphansPass(quitChan, store, baseDir, pathCurrent)
	if result.ShutdownRequested {
		return true, false
	}
	logReconcileResult(log, &result)
	return false, reconcileIdle(&result)
}

// logReconcileResult logs the outcome of one reconcile pass. An aborted pass
// stays at Debug: the pass itself warns about read errors, export changes and an
// unreadable export directory, and the monitor skips an unconfigured path before
// calling the pass. A pass
// whose clear or retain writes failed is raised to Warn, aborted or not, so rows
// that could not be healed are not reported as a clean run.
func logReconcileResult(log logger.Logger, result *diskmanager.ReconcileResult) {
	fields := []logger.Field{
		logger.Int("scanned", result.Scanned),
		logger.Int64("cleared", result.Cleared),
		logger.Int64("retained", result.Retained),
		logger.Int("held_no_evidence", result.HeldNoEvidence),
		logger.Int("held_missing_dir", result.HeldMissingDir),
		logger.Int("write_errors", result.WriteErrors),
		logger.String("operation", "clip_reconcile_pass"),
	}
	switch {
	case result.Aborted && result.WriteErrors > 0:
		log.Warn("clip reconcile pass aborted with write errors",
			append([]logger.Field{logger.String("reason", result.AbortReason)}, fields...)...)
	case result.Aborted:
		log.Debug("clip reconcile pass aborted",
			append([]logger.Field{logger.String("reason", result.AbortReason)}, fields...)...)
	case result.WriteErrors > 0:
		log.Warn("clip reconcile pass completed with write errors", fields...)
	default:
		log.Info("clip reconcile pass completed", fields...)
	}
}

// reconcileMonitorWait waits for d or until quitChan closes. It returns false if
// quitChan closed (shutdown requested), true if the full duration elapsed.
func reconcileMonitorWait(quitChan <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-quitChan:
		return false
	case <-timer.C:
		return true
	}
}

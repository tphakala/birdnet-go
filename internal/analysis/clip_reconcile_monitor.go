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

// clipReconcilePassInterval is the wait between full reconcile passes. Ghosts are
// rare and not time-critical to clear, so a slow cadence keeps disk I/O negligible.
const clipReconcilePassInterval = 1 * time.Hour

// clipReconcileMonitor continuously reconciles persisted clip_name references
// against the audio files on disk, clearing references to files that no longer
// exist (ghosts from failed exports, or from detections created while export was
// off). Unlike clipCleanupMonitor it runs regardless of the retention policy and
// regardless of whether audio export is currently enabled, because orphaned
// references persist across runtime toggling of the export setting. It reads the
// export path via conf.Setting() each pass so hot-reload takes effect, and the
// underlying diskmanager pass applies fail-safe guards so a detached/unmounted
// export volume never causes mass clearing.
func clipReconcileMonitor(quitChan <-chan struct{}, dataStore datastore.Interface) {
	log := GetLogger()
	log.Info("clip reconcile monitor initialized",
		logger.String("operation", "clip_reconcile_init"))

	// Defer the first pass; bail out immediately if shutdown arrives first.
	if !reconcileMonitorWait(quitChan, clipReconcileStartupDelay) {
		return
	}

	for {
		baseDir := strings.TrimSpace(conf.Setting().Realtime.Audio.Export.Path)
		if baseDir == "" {
			log.Debug("skipping clip reconcile: export path not configured",
				logger.String("operation", "clip_reconcile_skip"))
		} else {
			result := diskmanager.ReconcileClipOrphansPass(quitChan, dataStore, baseDir)
			switch {
			case result.ShutdownRequested:
				return
			case result.Aborted:
				log.Debug("clip reconcile pass aborted",
					logger.String("reason", result.AbortReason),
					logger.Int("scanned", result.Scanned),
					logger.String("operation", "clip_reconcile_pass"))
			default:
				log.Info("clip reconcile pass completed",
					logger.Int("scanned", result.Scanned),
					logger.Int64("cleared", result.Cleared),
					logger.String("operation", "clip_reconcile_pass"))
			}
		}

		if !reconcileMonitorWait(quitChan, clipReconcilePassInterval) {
			return
		}
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

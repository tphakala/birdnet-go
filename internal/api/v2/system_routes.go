// internal/api/v2/system_routes.go
package api

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/system"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// MetricsHistoryMaxPoints re-exports the system domain's metrics-history ring
// capacity so the parent server wiring (internal/api/server.go) keeps reading it
// as apiv2.MetricsHistoryMaxPoints after the system domain extraction.
const MetricsHistoryMaxPoints = system.MetricsHistoryMaxPoints

// initSystemRoutes registers the /api/v2/system/* routes. The genuine
// system-domain endpoints (info, resources, disks, jobs, processes, temperature,
// database stats/backup, network interfaces, restart status, active models,
// inference status, and the events sub-routes) live in the system package and
// are registered by c.system.RegisterSystemRoutes. The remaining routes below
// share the /system namespace but belong to domains not yet extracted into their
// own packages; they stay here until their own phase:
//   - GET /system/external-media -> media domain (external_media.go)
//   - /system/database/overview -> analytics domain (database_overview.go)
//   - /system/database/{migration,backup,legacy} -> import domain
//
// The /system/audio/* device routes have moved to the audio/streaming domain and
// are registered by c.audio.RegisterAudioDeviceRoutes (its own ordered initRoutes
// entry), which recreates its own /system group.
//
// Recreating the /system group and its auth-protected subgroup here (in addition
// to the one RegisterSystemRoutes creates) is safe: Echo deduplicates the group
// not-found stubs by method+path, and the metrics-history initializer already
// creates a second /system group today.
func (c *Controller) initSystemRoutes() {
	// System-domain routes + CPU sampler live in the system package.
	c.system.RegisterSystemRoutes(c.Group)

	systemGroup := c.Group.Group("/system")
	authMiddleware := c.AuthMiddleware
	protectedGroup := systemGroup.Group("", authMiddleware)

	// External media status (media domain).
	protectedGroup.GET("/external-media", c.media.GetExternalMedia)

	// Database overview (analytics domain).
	c.analytics.RegisterDatabaseOverviewRoutes(c.Group)

	// Migration, async backup and legacy cleanup routes (import domain).
	c.imports.RegisterMigrationRoutes(c.Group)
	c.imports.RegisterBackupRoutes(c.Group)
	c.imports.RegisterLegacyCleanupRoutes(c.Group)
}

// BroadcastInferenceTopologyChanged signals all metrics-stream SSE clients that
// the inference topology (models or source attachment) changed so they re-fetch
// the /api/v2/system/inference snapshot. Safe to call when the controller, its
// core, or its metrics store is nil. It stays on the facade (rather than moving
// to apicore with the other broadcasters, or into the system domain package) to
// preserve its nil-*Controller-safe contract: promotion of a *Core method would
// dereference the embedded core on a nil *Controller before the guard could run.
// It is part of the external surface: internal/analysis calls it through
// *apiv2.Controller.
func (c *Controller) BroadcastInferenceTopologyChanged() {
	if c == nil || c.Core == nil || c.MetricsStore == nil {
		return
	}
	c.MetricsStore.BroadcastTopologyChanged()
}

// HealthMetricsStore returns the diagnostics health metrics store owned by the
// system domain handler, or nil if it has not been initialized. It is part of the
// external surface: internal/analysis feeds health samples into the same store the
// health checks read by calling it through *apiv2.Controller.
func (c *Controller) HealthMetricsStore() *observability.HealthMetricsStore {
	if c == nil || c.system == nil {
		return nil
	}
	return c.system.HealthMetricsStore()
}

// HealthEventBuffer returns the diagnostics health event buffer owned by the
// system domain handler, or nil if it has not been initialized. Part of the
// external surface used by internal/analysis through *apiv2.Controller.
func (c *Controller) HealthEventBuffer() *observability.HealthEventBuffer {
	if c == nil || c.system == nil {
		return nil
	}
	return c.system.HealthEventBuffer()
}

// modelTopologyReconfigureDebounce coalesces the audio-source reconfigure that
// follows a model topology change. A gallery variant switch fires the topology
// callback twice (once when the old variant unloads, once when the new one
// loads), and each reconfigure re-probes every enabled RTSP stream with ffprobe,
// so reacting to both would double that cost for no benefit.
const modelTopologyReconfigureDebounce = 2 * time.Second

// OnModelTopologyChanged reacts to a model being loaded into or unloaded from
// the orchestrator at runtime, which happens when the user installs, reinstalls,
// switches the variant of, or removes a model in the gallery.
//
// It does two things. It broadcasts over the metrics SSE stream so open clients
// re-fetch the inference snapshot, and it asks the audio pipeline to reconcile
// its per-source model registration.
//
// The second half is the fix for GitHub issues #4201 and #4204. Loading a model
// into the orchestrator does not attach it to anything: the audio router fans a
// source out to the models registered for it, and that registration is computed
// only at startup and when settings change. So a model installed from the
// gallery while the server ran would load, report itself as installed, and then
// receive no audio at all until the user toggled the model assignment on the
// source to force a reconfigure. Signalling reconfigure_audio_sources here runs
// the same diff the settings path uses: sourceModelsChanged compares each
// source's allocated analysis buffers against the models the config asks for
// that are actually loaded, so a newly loaded model shows up as a change and
// the source is re-registered. An unload is handled by the same diff in reverse.
func (c *Controller) OnModelTopologyChanged() {
	if c == nil {
		return
	}
	c.BroadcastInferenceTopologyChanged()
	c.scheduleAudioSourceReconfigure()
}

// scheduleAudioSourceReconfigure starts or resets the debounce timer that sends
// the audio-source reconfigure signal, mirroring the debounce the MQTT
// discovery publisher uses for source-registry churn.
func (c *Controller) scheduleAudioSourceReconfigure() {
	c.topologyReconfigureMu.Lock()
	defer c.topologyReconfigureMu.Unlock()

	// Do not re-arm once the controller is shutting down: Shutdown has already
	// stopped the timer under this lock, and a fresh timer would fire into a
	// controller whose context is cancelled and whose control channel is closed.
	if c.topologyReconfigureShutdown {
		return
	}

	if c.topologyReconfigureTimer != nil {
		c.topologyReconfigureTimer.Stop()
	}
	c.topologyReconfigureTimer = time.AfterFunc(modelTopologyReconfigureDebounce, func() {
		c.sendAudioSourceReconfigure()
	})
}

// sendAudioSourceReconfigure delivers the reconfigure signal to the control
// monitor. The channel has capacity 1 and the monitor drains it synchronously,
// so dropping the signal when the channel is momentarily full would lose it
// permanently and recreate the exact bug this reconfigure exists to fix (a
// gallery model that loads but never receives audio). The caller is the debounce
// timer goroutine, so blocking until the monitor drains the channel is harmless.
// It mirrors the send shape sendReconfigActions uses: a select on the send
// versus the controller context being cancelled.
//
// The deferred recover is load-bearing. On shutdown internal/analysis closes the
// control channel; the Controller holds a copy of that same (now closed) channel,
// so the c.controlChan == nil guard never fires (only analysis nils its OWN
// field). A send on a closed channel is always ready, so select would pick it
// over the Done case and panic; recover absorbs that during the shutdown window.
func (c *Controller) sendAudioSourceReconfigure() {
	defer func() {
		if r := recover(); r != nil {
			c.LogWarnIfEnabled("Recovered from send on closed controlChan during audio source reconfigure",
				logger.Any("panic", r))
		}
	}()

	if c.controlChan == nil {
		return
	}
	select {
	case <-c.Context().Done():
	case c.controlChan <- actionReconfigureAudioSources:
	}
}

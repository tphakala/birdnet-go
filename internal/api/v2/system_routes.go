// internal/api/v2/system_routes.go
package api

import (
	"github.com/tphakala/birdnet-go/internal/api/v2/system"
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
	protectedGroup.GET("/external-media", c.GetExternalMedia)

	// Database overview (analytics domain).
	c.initDatabaseOverviewRoutes()

	// Migration, async backup and legacy cleanup routes (import domain).
	c.initMigrationRoutes()
	c.initBackupRoutes()
	c.initLegacyCleanupRoutes()
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

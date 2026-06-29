// Package app implements the v2 API app/debug domain: the public /app/config
// bootstrap endpoint (which issues the frontend CSRF token via
// middleware.EnsureCSRFToken and returns the SPA configuration), the wizard
// dismiss endpoint, and the debug-mode-gated /debug/* endpoints (trigger-error,
// trigger-notification, status).
//
// The Handler embeds *apicore.Core by pointer so the shared dependencies and
// helpers (Settings accessors, V2Manager, AuthMiddleware, the HandleError/logging
// helpers) are available without re-plumbing. Beyond the core it owns:
//   - authService: the facade-injected auth service, used by /app/config to
//     decide whether the current request is authenticated (nil-guarded).
//   - notificationService: the facade-injected notification service (nil-guarded,
//     falling back to the process-global singleton), used by the debug
//     trigger-notification and status handlers.
//   - appMetadataRepo: the app-metadata repository, initialized lazily in
//     RegisterAppRoutes from the V2Manager and read by the wizard-state helpers.
package app

import (
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Handler serves the api/v2 app/debug domain endpoints. It embeds the shared
// *apicore.Core (by pointer) and additionally owns the facade-injected auth and
// notification services and the lazily-initialized app-metadata repository.
type Handler struct {
	*apicore.Core

	// authService is the auth service injected from the facade. It is nil-guarded
	// in determineAccessAllowed (fail closed when unset). The facade hands it the
	// same value as the other domains receive (see NewWithOptions).
	authService auth.Service

	// notificationService is the notification service injected from the facade. It
	// is nil in production, where getNotificationService() falls back to the
	// process-global singleton (notification.GetService()). Tests inject an
	// isolated per-test instance so each test gets its own config and store without
	// touching the global singleton.
	notificationService *notification.Service

	// appMetadataRepo is the application metadata repository. It is initialized
	// lazily in RegisterAppRoutes from the V2Manager (nil when no V2 manager is
	// wired); the wizard-state helpers read it nil-guarded.
	appMetadataRepo repository.AppMetadataRepository
}

// New constructs the app/debug domain handler around the shared core and the
// facade-injected auth and notification services. The app-metadata repository is
// created lazily in RegisterAppRoutes.
func New(core *apicore.Core, authService auth.Service, notificationService *notification.Service) *Handler {
	return &Handler{
		Core:                core,
		authService:         authService,
		notificationService: notificationService,
	}
}

// getNotificationService returns the notification service this handler uses: the
// injected instance when set (tests inject it), otherwise the process-global
// singleton (notification.GetService()). It mirrors the imports/notifications
// domain accessor of the same name; the field is nil in production, so it falls
// back to the singleton exactly as before.
func (c *Handler) getNotificationService() *notification.Service {
	if c.notificationService != nil {
		return c.notificationService
	}
	return notification.GetService()
}

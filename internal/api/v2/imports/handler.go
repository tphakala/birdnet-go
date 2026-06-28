// Package importsapi implements the v2 API import/migration domain: the
// BirdNET-Pi import endpoints (/api/v2/import/*), the legacy->v2 database
// migration endpoints and the background migration-worker control surface
// (/api/v2/system/database/migration/*), the migration prerequisite checks, the
// async SQLite backup-job endpoints (/api/v2/system/database/backup/jobs/*), and
// the legacy-database cleanup endpoints (/api/v2/system/database/legacy/*).
//
// The package is named importsapi (not the bare imports) to avoid colliding with
// the internal/imports package it depends on, mirroring the rangeapi/authapi
// precedent for domains whose natural name shadows an existing import.
//
// The Handler embeds *apicore.Core by pointer so the shared dependencies and
// helpers (Settings accessors, DS/Repo/V2Manager, AuthMiddleware, the
// HandleError/logging helpers, the Context/Go/Cancel/Wait lifecycle, and the SSE
// write primitives) are available without re-plumbing. Beyond the core it owns:
//   - importMgr: the one-at-a-time import lifecycle manager.
//   - importSourceRoot/importSourceFactory: the import source-path root and the
//     injectable Source factory (both default lazily; overridden in tests).
//   - cleanupStatus: the legacy-cleanup state tracker, initialized lazily in
//     RegisterLegacyCleanupRoutes (the migration status handler reads it
//     nil-guarded).
//   - notificationService: the facade-injected notification service (nil-guarded,
//     falling back to the process-global singleton), used by the migration and
//     legacy-cleanup completion notifications.
//
// The migration-worker control functions (SetMigrationDependencies,
// SetMigrationWorkerCancel, SetV2OnlyMode, StopMigrationWorker,
// SetMigrationTelemetry) are package-level functions over package-level state
// (migration.go); internal/analysis drives the worker lifecycle through them.
package importsapi

import (
	"context"
	"os"
	"os/user"
	"strconv"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/imports"
	"github.com/tphakala/birdnet-go/internal/imports/discovery"
	"github.com/tphakala/birdnet-go/internal/imports/elevation"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// apiV2Prefix is the v2 API path prefix. It mirrors the facade's apiV2Prefix; the
// import domain keeps a local copy (the audio/auth/system precedent) rather than
// importing it from package api.
const apiV2Prefix = "/api/v2"

// Handler serves the api/v2 import/migration domain endpoints. It embeds the
// shared *apicore.Core (by pointer) and additionally owns the import lifecycle
// manager, the import source-path root and factory, the legacy-cleanup state
// tracker, and the facade-injected notification service.
type Handler struct {
	*apicore.Core

	// importMgr manages the one-at-a-time import lifecycle.
	importMgr *importManager

	// importSourceRoot is the directory under which import source paths must
	// resolve. Defaults to sysinfo.DefaultExternalMountPath when empty.
	importSourceRoot string

	// importSourceFactory builds an import Source from a resolved path. Defaults to
	// a BirdNET-Pi adapter when nil. Overridable in tests.
	importSourceFactory func(path string) (imports.Source, error)

	// isContainerEnv reports whether BirdNET-Go runs in a container. Defaults to
	// sysinfo.IsContainer; overridable in tests to exercise the native branch.
	isContainerEnv func() bool

	// importEnvInfo reports the runtime environment + run-as identity for the
	// /import/sources response and guidance. Defaults to a sysinfo+os/user
	// reader; overridable in tests.
	importEnvInfo func() envInfo
	// scanCandidates runs the discovery scan for a provider. Defaults to a real
	// bounded Scanner; tests inject fixed candidates.
	scanCandidates func(ctx context.Context, provider discovery.LocationProvider) []discovery.SourceCandidate
	// newLadder builds the elevation ladder. Defaults to elevation.NewLadder;
	// tests inject a fake-runner ladder.
	newLadder func() (*elevation.Ladder, error)
	// stagingBase is the trusted, service-user-owned directory under which
	// per-import staging subdirectories are created. Resolved lazily; overridable
	// in tests.
	stagingBase string
	// freeBytesFn reports free bytes on the filesystem holding path. Defaults to
	// the platform freeBytes; tests inject a stub for the disk preflight.
	freeBytesFn func(path string) (uint64, error)
	// verifyTrustedBase confirms the staging parent is a root-owned, sticky
	// directory the service user cannot swap. Defaults to the platform
	// assertTrustedBase; tests override it (a test cannot create a root-owned dir).
	verifyTrustedBase func(path string) error

	// cleanupStatus tracks the state of legacy database cleanup. It is initialized
	// lazily in RegisterLegacyCleanupRoutes; the migration status handler reads it
	// nil-guarded.
	cleanupStatus *CleanupStatus

	// notificationService is the notification service injected from the facade. It
	// is nil in production, where getNotificationService() falls back to the
	// process-global singleton (notification.GetService()). Tests inject an
	// isolated per-test instance so each test gets its own config and store without
	// touching the global singleton.
	notificationService *notification.Service
}

// envInfo is the runtime environment plus the BirdNET-Go process run-as identity.
type envInfo struct {
	envType       string
	containerized bool
	uid           int
	username      string
	home          string
}

// defaultEnvInfo reads the cached environment and the current process identity.
// Username/home come from os/user; both default to "" if the lookup fails (a
// containerized uid may have no passwd entry), which guidance handles.
func defaultEnvInfo() envInfo {
	envType, _ := sysinfo.GetEnvironment()
	uid := os.Getuid()
	info := envInfo{
		envType:       envType,
		containerized: sysinfo.IsContainerEnv(envType),
		uid:           uid,
	}
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		info.username = u.Username
		info.home = u.HomeDir
	}
	return info
}

// New constructs the import/migration domain handler around the shared core and
// the facade-injected notification service. The import lifecycle manager is
// created here; the import source-path root/factory default lazily and the legacy
// cleanup tracker is created in RegisterLegacyCleanupRoutes.
func New(core *apicore.Core, notificationService *notification.Service) *Handler {
	return &Handler{
		Core:                core,
		notificationService: notificationService,
		importMgr:           newImportManager(),
		isContainerEnv:      sysinfo.IsContainer,
		importEnvInfo:       defaultEnvInfo,
		scanCandidates: func(ctx context.Context, p discovery.LocationProvider) []discovery.SourceCandidate {
			return discovery.NewScanner(p).Scan(ctx)
		},
		newLadder:         elevation.NewLadder,
		freeBytesFn:       freeBytes,
		verifyTrustedBase: assertTrustedBase,
	}
}

// getNotificationService returns the notification service this handler uses: the
// injected instance when set (tests inject it), otherwise the process-global
// singleton (notification.GetService()). It mirrors the facade accessor of the
// same name (which still serves the remaining package-api callers); the field is
// nil in production, so it falls back to the singleton exactly as before.
func (c *Handler) getNotificationService() *notification.Service {
	if c.notificationService != nil {
		return c.notificationService
	}
	return notification.GetService()
}

// Shutdown stops the background singletons this domain owns. It stops the backup
// job manager's cleanup goroutine (a no-op when no backup routes were registered,
// so the singleton is nil). The migration worker is stopped separately by
// internal/analysis via StopMigrationWorker() as part of the datastore lifecycle,
// so it is deliberately not torn down here.
func (c *Handler) Shutdown() {
	if backupJobManager != nil {
		backupJobManager.Shutdown()
	}
}

package app

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// =============================================================================
// mockAppMetadataRepo - simple mock for AppMetadataRepository
// =============================================================================

// mockAppMetadataRepo implements repository.AppMetadataRepository for testing.
type mockAppMetadataRepo struct {
	store      map[string]string
	getErr     error            // injected error for Get calls
	getErrKeys map[string]error // injected error for Get calls on specific keys
	setErr     error            // injected error for Set calls
	setErrKeys map[string]error // injected error for Set calls on specific keys
	setCalls   []string         // keys passed to Set, in call order
}

func newMockAppMetadataRepo() *mockAppMetadataRepo {
	return &mockAppMetadataRepo{
		store:      make(map[string]string),
		getErrKeys: make(map[string]error),
		setErrKeys: make(map[string]error),
	}
}

func (m *mockAppMetadataRepo) Get(_ context.Context, key string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	if err := m.getErrKeys[key]; err != nil {
		return "", err
	}
	return m.store[key], nil
}

func (m *mockAppMetadataRepo) Set(_ context.Context, key, value string) error {
	m.setCalls = append(m.setCalls, key)
	if m.setErr != nil {
		return m.setErr
	}
	if err := m.setErrKeys[key]; err != nil {
		return err
	}
	m.store[key] = value
	return nil
}

// =============================================================================
// fakeV2Manager - minimal Manager that exposes a real *gorm.DB
// =============================================================================

// fakeV2Manager implements the subset of datastoreV2.Manager used by
// hasZeroDetections (DB() and optionally TablePrefix()).
type fakeV2Manager struct {
	db          *gorm.DB
	tablePrefix string
}

func (f *fakeV2Manager) Initialize() error    { return nil }
func (f *fakeV2Manager) DB() *gorm.DB         { return f.db }
func (f *fakeV2Manager) Path() string         { return ":memory:" }
func (f *fakeV2Manager) Close() error         { return nil }
func (f *fakeV2Manager) CheckpointWAL() error { return nil }
func (f *fakeV2Manager) Delete() error        { return nil }
func (f *fakeV2Manager) Exists() bool         { return true }
func (f *fakeV2Manager) IsMySQL() bool        { return false }
func (f *fakeV2Manager) TablePrefix() string  { return f.tablePrefix }

// newFakeV2ManagerWithDetections creates a fakeV2Manager backed by an
// in-memory SQLite database.  If count > 0, it inserts that many dummy
// rows into a "detections" table.
func newFakeV2ManagerWithDetections(t *testing.T, count int) *fakeV2Manager {
	t.Helper()
	// Bare (no-prefix) "detections" table matching what hasZeroDetections queries.
	return newFakeV2ManagerWithTable(t, "detections", "", count)
}

// newFakeV2ManagerWithTable creates a fakeV2Manager backed by an in-memory SQLite
// database with a single arbitrary table (e.g. "v2_detections") holding count rows,
// and reports the given prefix from TablePrefix(). Used to verify that table names are
// derived from the manager's prefix rather than guessed from the dialect.
func newFakeV2ManagerWithTable(t *testing.T, tableName, prefix string, count int) *fakeV2Manager {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS "+tableName+" (id INTEGER PRIMARY KEY)").Error)
	for i := range count {
		require.NoError(t, db.Exec("INSERT INTO "+tableName+" (id) VALUES (?)", i+1).Error)
	}

	return &fakeV2Manager{db: db, tablePrefix: prefix}
}

// newFakeV2ManagerWithMetadata creates a fakeV2Manager whose database holds a
// "detections" table with count rows and the real app_metadata table, so the
// production repository can be built on it.
func newFakeV2ManagerWithMetadata(t *testing.T, count int) *fakeV2Manager {
	t.Helper()
	m := newFakeV2ManagerWithDetections(t, count)
	require.NoError(t, m.db.AutoMigrate(&entities.AppMetadata{}))
	return m
}

// freshTemplateSettings returns settings shaped like a fresh install's config:
// the default template ships one audio source, so the config-based
// isExistingInstall check reports an existing install.
func freshTemplateSettings(version string) *conf.Settings {
	s := &conf.Settings{Version: version}
	s.Realtime.Audio.Sources = []conf.AudioSourceConfig{{Name: "Sound Card 1", Device: "sysdefault"}}
	return s
}

// installScriptSettings returns settings shaped like a fresh install.sh config:
// the default template plus the coordinates the script writes.
func installScriptSettings(version string) *conf.Settings {
	s := freshTemplateSettings(version)
	s.BirdNET.Latitude = 60.1
	s.BirdNET.Longitude = 24.9
	return s
}

// =============================================================================
// isDevBuild tests
// =============================================================================

func TestIsDevBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "empty string", version: "", want: true},
		{name: "Development Build literal", version: "Development Build", want: true},
		{name: "release version", version: "v0.9.0", want: false},
		{name: "semver without v prefix", version: "1.2.3", want: false},
		{name: "pre-release", version: "v1.0.0-rc.1", want: false},
		{name: "development build lowercase", version: "development build", want: false},
		{name: "whitespace only", version: "   ", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isDevBuild(tt.version))
		})
	}
}

// =============================================================================
// determineWizardState tests
// =============================================================================

func TestDetermineWizardState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		version         string                            // Settings.Version
		metadataRepo    repository.AppMetadataRepository  // nil means no repo
		v2Manager       func(t *testing.T) *fakeV2Manager // nil means no V2Manager
		wantFresh       bool
		wantNew         bool
		wantPrevVersion string
	}{
		{
			name:    "dev build empty version",
			version: "",
		},
		{
			name:    "dev build Development Build",
			version: "Development Build",
		},
		{
			name:    "nil metadata repo",
			version: "v0.9.0",
			// metadataRepo left nil
		},
		{
			name:         "fresh install - no last_seen_version, zero detections",
			version:      "v0.9.0",
			metadataRepo: newMockAppMetadataRepo(), // empty store -> Get returns ""
			v2Manager: func(t *testing.T) *fakeV2Manager {
				t.Helper()
				return newFakeV2ManagerWithDetections(t, 0)
			},
			wantFresh: true,
		},
		{
			name:         "existing install auto-seeds - no last_seen_version, has detections",
			version:      "v0.9.0",
			metadataRepo: newMockAppMetadataRepo(),
			v2Manager: func(t *testing.T) *fakeV2Manager {
				t.Helper()
				return newFakeV2ManagerWithDetections(t, 5)
			},
			wantPrevVersion: "v0.9.0",
		},
		{
			name:    "upgrade with known previous version",
			version: "v0.9.0",
			metadataRepo: func() *mockAppMetadataRepo {
				m := newMockAppMetadataRepo()
				m.store["last_seen_version"] = "v0.8.0"
				return m
			}(),
			wantNew:         true,
			wantPrevVersion: "v0.8.0",
		},
		{
			name:    "same version - no wizard needed",
			version: "v0.9.0",
			metadataRepo: func() *mockAppMetadataRepo {
				m := newMockAppMetadataRepo()
				m.store["last_seen_version"] = "v0.9.0"
				return m
			}(),
			wantPrevVersion: "v0.9.0",
		},
		{
			name:         "fresh install - nil V2Manager treated as zero detections",
			version:      "v0.9.0",
			metadataRepo: newMockAppMetadataRepo(),
			// v2Manager left nil -> hasZeroDetections returns true
			wantFresh: true,
		},
		{
			name:    "metadata repo Get error - returns safe defaults",
			version: "v0.9.0",
			metadataRepo: func() *mockAppMetadataRepo {
				m := newMockAppMetadataRepo()
				m.getErr = assert.AnError
				return m
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &Handler{Core: &apicore.Core{}}
			c.Settings.Store(&conf.Settings{Version: tt.version})
			c.appMetadataRepo = tt.metadataRepo
			if tt.v2Manager != nil {
				c.V2Manager = tt.v2Manager(t)
			}
			// determineWizardState now takes the settings snapshot directly, so
			// the subtest stays fully per-handler and parallel-safe.
			freshInstall, newVersion, previousVersion := c.determineWizardState(t.Context(), c.Settings.Load())

			assert.Equal(t, tt.wantFresh, freshInstall, "freshInstall mismatch")
			assert.Equal(t, tt.wantNew, newVersion, "newVersion mismatch")
			assert.Equal(t, tt.wantPrevVersion, previousVersion, "previousVersion mismatch")
		})
	}
}

// =============================================================================
// DismissWizard handler tests
// =============================================================================

func TestDismissWizard_Success(t *testing.T) {
	// Not parallel: serveDismiss publishes the process-wide settings snapshot.
	mockRepo := newMockAppMetadataRepo()

	rec := serveDismiss(t, mockRepo)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "v0.9.0", mockRepo.store["last_seen_version"],
		"DismissWizard should persist the current version")
}

func TestDismissWizard_NilRepo(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := &Handler{Core: &apicore.Core{}}
	c.Settings.Store(&conf.Settings{
		Version:   "v0.9.0",
		WebServer: conf.WebServerSettings{Debug: true},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/app/wizard/dismiss", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/app/wizard/dismiss")

	err := c.DismissWizard(ctx)
	// HandleError writes the response and returns nil (no echo error propagation)
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestDismissWizard_SetError(t *testing.T) {
	t.Parallel()

	mockRepo := newMockAppMetadataRepo()
	mockRepo.setErr = assert.AnError

	e := echo.New()
	c := &Handler{Core: &apicore.Core{}, appMetadataRepo: mockRepo}
	c.Settings.Store(&conf.Settings{
		Version:   "v0.9.0",
		WebServer: conf.WebServerSettings{Debug: true},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/app/wizard/dismiss", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/app/wizard/dismiss")

	err := c.DismissWizard(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// =============================================================================
// Onboarding state tests
// =============================================================================

// TestDetermineWizardState_OnboardingPending covers the startup-recorded
// onboarding flag: it reports a fresh install even when the config looks
// configured, never auto-seeds, and never masks the version comparison once
// last_seen_version is set.
func TestDetermineWizardState_OnboardingPending(t *testing.T) {
	t.Parallel()

	const version = "v0.9.0"

	tests := []struct {
		name            string
		pending         bool
		pendingReadErr  bool
		lastSeen        string
		settings        func(string) *conf.Settings
		detections      int
		wantFresh       bool
		wantNew         bool
		wantPrevVersion string
		wantLastSeen    string // last_seen_version stored after the call
	}{
		{
			name:       "pending with template config and detections is fresh",
			pending:    true,
			settings:   freshTemplateSettings,
			detections: 5,
			wantFresh:  true,
		},
		{
			name:      "pending with install.sh config is fresh",
			pending:   true,
			settings:  installScriptSettings,
			wantFresh: true,
		},
		{
			name:            "pending with matching last_seen_version is not fresh",
			pending:         true,
			lastSeen:        version,
			settings:        freshTemplateSettings,
			wantPrevVersion: version,
			wantLastSeen:    version,
		},
		{
			name:            "pending with older last_seen_version reports the upgrade",
			pending:         true,
			lastSeen:        "v0.8.0",
			settings:        freshTemplateSettings,
			wantNew:         true,
			wantPrevVersion: "v0.8.0",
			wantLastSeen:    "v0.8.0",
		},
		{
			name:           "flag read error returns safe defaults without auto-seeding",
			pending:        true,
			pendingReadErr: true,
			settings:       freshTemplateSettings,
		},
		{
			name:            "no flag with template config falls back to auto-seed",
			settings:        freshTemplateSettings,
			wantPrevVersion: version,
			wantLastSeen:    version,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := newMockAppMetadataRepo()
			if tt.pending {
				repo.store[appMetadataKeyOnboardingPending] = onboardingPendingValue
			}
			if tt.pendingReadErr {
				repo.getErrKeys[appMetadataKeyOnboardingPending] = assert.AnError
			}
			if tt.lastSeen != "" {
				repo.store[appMetadataKeyLastSeenVersion] = tt.lastSeen
			}
			c := &Handler{Core: &apicore.Core{}, appMetadataRepo: repo}
			c.V2Manager = newFakeV2ManagerWithDetections(t, tt.detections)

			freshInstall, newVersion, previousVersion := c.determineWizardState(t.Context(), tt.settings(version))

			assert.Equal(t, tt.wantFresh, freshInstall, "freshInstall mismatch")
			assert.Equal(t, tt.wantNew, newVersion, "newVersion mismatch")
			assert.Equal(t, tt.wantPrevVersion, previousVersion, "previousVersion mismatch")
			assert.Equal(t, tt.wantLastSeen, repo.store[appMetadataKeyLastSeenVersion], "stored last_seen_version mismatch")
		})
	}
}

func TestRecordOnboardingState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		seed        map[string]string // initial app_metadata contents
		getErr      error             // error for every Get
		setErrKey   string            // key whose Set fails
		detections  int
		noTable     bool // database without a detections table
		noV2Manager bool
		noRepo      bool
		legacy      bool // database mode is not enhanced
		nilMode     bool // no database-mode check injected
		wantPending bool
		wantSet     bool // whether Set was called at all
	}{
		{name: "enhanced mode with empty database records the flag", wantPending: true, wantSet: true},
		{name: "legacy mode does not record", legacy: true},
		{name: "nil mode check does not record", nilMode: true},
		{name: "existing last_seen_version does not record", seed: map[string]string{appMetadataKeyLastSeenVersion: "v0.8.0"}},
		{name: "a detection row does not record", detections: 1},
		{
			name:        "restart with detections keeps an already recorded flag",
			seed:        map[string]string{appMetadataKeyOnboardingPending: onboardingPendingValue},
			detections:  1,
			wantPending: true,
		},
		{
			name:        "already pending stays pending",
			seed:        map[string]string{appMetadataKeyOnboardingPending: onboardingPendingValue},
			wantPending: true,
			wantSet:     true,
		},
		{name: "metadata read error does not record", getErr: assert.AnError},
		{name: "flag write error leaves the flag unset", setErrKey: appMetadataKeyOnboardingPending, wantSet: true},
		{name: "missing detections table does not record", noTable: true},
		{name: "nil V2Manager is a no-op", noV2Manager: true},
		{name: "nil repository is a no-op", noRepo: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &Handler{Core: &apicore.Core{}}
			if !tt.nilMode {
				c.isEnhancedDatabase = func() bool { return !tt.legacy }
			}
			repo := newMockAppMetadataRepo()
			maps.Copy(repo.store, tt.seed)
			repo.getErr = tt.getErr
			if tt.setErrKey != "" {
				repo.setErrKeys[tt.setErrKey] = assert.AnError
			}
			if !tt.noRepo {
				c.appMetadataRepo = repo
			}
			switch {
			case tt.noV2Manager:
			case tt.noTable:
				c.V2Manager = newFakeV2ManagerWithTable(t, "unrelated", "", 0)
			default:
				c.V2Manager = newFakeV2ManagerWithDetections(t, tt.detections)
			}

			require.NotPanics(t, func() { c.recordOnboardingState(t.Context()) })

			assert.Equal(t, tt.wantPending, repo.store[appMetadataKeyOnboardingPending] == onboardingPendingValue, "pending flag mismatch")
			assert.Equal(t, tt.wantSet, len(repo.setCalls) > 0, "unexpected Set calls: %v", repo.setCalls)
		})
	}
}

// serveDismiss runs DismissWizard against repo with the current settings
// published to the process-wide snapshot, so callers must not run in parallel.
func serveDismiss(t *testing.T, repo *mockAppMetadataRepo) *httptest.ResponseRecorder {
	t.Helper()
	c := &Handler{Core: &apicore.Core{}, appMetadataRepo: repo}
	c.Settings.Store(&conf.Settings{Version: "v0.9.0"})
	apitest.PublishTestSettings(t, c.Settings.Load())

	req := httptest.NewRequest(http.MethodPost, "/api/v2/app/wizard/dismiss", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)
	ctx.SetPath("/api/v2/app/wizard/dismiss")
	require.NoError(t, c.DismissWizard(ctx))
	return rec
}

func TestDismissWizard_ClearsOnboardingPending(t *testing.T) {
	// Not parallel: serveDismiss publishes the process-wide settings snapshot.
	mockRepo := newMockAppMetadataRepo()
	mockRepo.store[appMetadataKeyOnboardingPending] = onboardingPendingValue

	rec := serveDismiss(t, mockRepo)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "v0.9.0", mockRepo.store[appMetadataKeyLastSeenVersion])
	assert.Empty(t, mockRepo.store[appMetadataKeyOnboardingPending], "dismiss should clear the onboarding flag")
	assert.Equal(t, []string{appMetadataKeyLastSeenVersion, appMetadataKeyOnboardingPending}, mockRepo.setCalls,
		"last_seen_version must be written before the flag is cleared")
}

func TestDismissWizard_ClearPendingFailure_StillSucceeds(t *testing.T) {
	// Not parallel: serveDismiss publishes the process-wide settings snapshot.
	mockRepo := newMockAppMetadataRepo()
	mockRepo.store[appMetadataKeyOnboardingPending] = onboardingPendingValue
	mockRepo.setErrKeys[appMetadataKeyOnboardingPending] = assert.AnError

	rec := serveDismiss(t, mockRepo)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "v0.9.0", mockRepo.store[appMetadataKeyLastSeenVersion])
}

// TestNew_BindsDatabaseModeCheck pins that New wires the database-mode check to
// the process-wide mode, without which no onboarding state is ever recorded.
func TestNew_BindsDatabaseModeCheck(t *testing.T) {
	// Not parallel: toggles the process-wide database mode.
	wasEnhanced := datastoreV2.IsEnhancedDatabase()
	t.Cleanup(func() {
		if wasEnhanced {
			datastoreV2.SetEnhancedDatabaseMode()
		} else {
			datastoreV2.ResetDatabaseMode()
		}
	})

	c := New(&apicore.Core{}, nil, nil)
	require.NotNil(t, c.isEnhancedDatabase)

	datastoreV2.SetEnhancedDatabaseMode()
	assert.True(t, c.isEnhancedDatabase(), "enhanced mode must be reported")
	datastoreV2.ResetDatabaseMode()
	assert.False(t, c.isEnhancedDatabase(), "legacy mode must be reported")
}

// TestRegisterAppRoutes_RecordsOnboardingState pins the startup wiring against
// the real app_metadata repository and table.
func TestRegisterAppRoutes_RecordsOnboardingState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		isEnhanced  bool
		wantPending string
	}{
		{name: "enhanced database records the flag", isEnhanced: true, wantPending: onboardingPendingValue},
		{name: "legacy database records nothing", isEnhanced: false, wantPending: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newFakeV2ManagerWithMetadata(t, 0)
			c := New(&apicore.Core{}, nil, nil)
			c.V2Manager = m
			c.isEnhancedDatabase = func() bool { return tt.isEnhanced }

			c.RegisterAppRoutes(echo.New().Group("/api/v2"))

			repo := repository.NewAppMetadataRepository(m.db, nil, false, false)
			got, err := repo.Get(t.Context(), appMetadataKeyOnboardingPending)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPending, got)
		})
	}
}

// TestDismissWizardRoute_RequiresSameAccessAsWizardState pins the route wiring of
// POST /api/v2/app/wizard/dismiss: it goes through the auth middleware, so a
// request can dismiss the wizard if and only if GET /api/v2/app/config reports
// accessAllowed for it (the same condition under which the wizard is offered).
// It is not parallel because setupAppConfigTestWithAuth publishes the global
// settings snapshot and sets gothic.Store.
func TestDismissWizardRoute_RequiresSameAccessAsWizardState(t *testing.T) {
	const (
		apiPrefix     = "/api/v2"
		remoteAddr    = "203.0.113.10:1234"
		testSubnet    = "203.0.113.0/24"
		sessionSecret = "test-session-secret-32-chars-long"
	)

	basicAuth := conf.BasicAuth{
		Enabled:        true,
		Password:       "testpassword",
		ClientID:       "test-client",
		AuthCodeExp:    5 * time.Minute,
		AccessTokenExp: 24 * time.Hour,
	}

	tests := []struct {
		name     string
		security *conf.Security
		wantCode int
	}{
		{
			name:     "no auth configured allows dismiss",
			wantCode: http.StatusNoContent,
		},
		{
			name: "auth configured without access is rejected",
			security: &conf.Security{
				SessionSecret: sessionSecret,
				BasicAuth:     basicAuth,
			},
			wantCode: http.StatusUnauthorized,
		},
		{
			name: "auth configured with access dismisses",
			security: &conf.Security{
				SessionSecret:     sessionSecret,
				BasicAuth:         basicAuth,
				AllowSubnetBypass: conf.AllowSubnetBypass{Enabled: true, Subnet: testSubnet},
			},
			wantCode: http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, h := setupAppConfigTestWithAuth(t, tt.security)
			h.AuthMiddleware = auth.NewMiddleware(h.authService).Authenticate
			h.RegisterAppRoutes(e.Group(apiPrefix))

			repo := newMockAppMetadataRepo()
			repo.store[appMetadataKeyOnboardingPending] = onboardingPendingValue
			h.appMetadataRepo = repo

			// Wizard state: the request is offered the wizard only with access.
			cfgReq := httptest.NewRequest(http.MethodGet, apiPrefix+AppConfigEndpoint, http.NoBody)
			cfgReq.RemoteAddr = remoteAddr
			cfgRec := httptest.NewRecorder()
			e.ServeHTTP(cfgRec, cfgReq)
			require.Equal(t, http.StatusOK, cfgRec.Code)
			var cfg AppConfigResponse
			require.NoError(t, json.Unmarshal(cfgRec.Body.Bytes(), &cfg))

			req := httptest.NewRequest(http.MethodPost, apiPrefix+WizardDismissEndpoint, http.NoBody)
			req.RemoteAddr = remoteAddr
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Equal(t, cfg.Security.AccessAllowed, rec.Code == http.StatusNoContent,
				"dismiss must succeed exactly when config reports accessAllowed")

			// The handler's writes are covered by the DismissWizard tests; here
			// only whether it ran matters: a rejected request must not write.
			assert.Equal(t, tt.wantCode == http.StatusNoContent, len(repo.setCalls) > 0,
				"unexpected Set calls: %v", repo.setCalls)
		})
	}
}

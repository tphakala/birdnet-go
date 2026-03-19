package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// =============================================================================
// mockAppMetadataRepo — simple mock for AppMetadataRepository
// =============================================================================

// mockAppMetadataRepo implements repository.AppMetadataRepository for testing.
type mockAppMetadataRepo struct {
	store  map[string]string
	getErr error // injected error for Get calls
	setErr error // injected error for Set calls
}

func newMockAppMetadataRepo() *mockAppMetadataRepo {
	return &mockAppMetadataRepo{store: make(map[string]string)}
}

func (m *mockAppMetadataRepo) Get(_ context.Context, key string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.store[key], nil
}

func (m *mockAppMetadataRepo) Set(_ context.Context, key, value string) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.store[key] = value
	return nil
}

// =============================================================================
// fakeV2Manager — minimal Manager that exposes a real *gorm.DB
// =============================================================================

// fakeV2Manager implements the subset of datastoreV2.Manager used by
// hasZeroDetections (DB() and optionally TablePrefix()).
type fakeV2Manager struct {
	db *gorm.DB
}

func (f *fakeV2Manager) Initialize() error    { return nil }
func (f *fakeV2Manager) DB() *gorm.DB         { return f.db }
func (f *fakeV2Manager) Path() string         { return ":memory:" }
func (f *fakeV2Manager) Close() error         { return nil }
func (f *fakeV2Manager) CheckpointWAL() error { return nil }
func (f *fakeV2Manager) Delete() error        { return nil }
func (f *fakeV2Manager) Exists() bool         { return true }
func (f *fakeV2Manager) IsMySQL() bool        { return false }

// newFakeV2ManagerWithDetections creates a fakeV2Manager backed by an
// in-memory SQLite database.  If count > 0, it inserts that many dummy
// rows into a "detections" table.
func newFakeV2ManagerWithDetections(t *testing.T, count int) *fakeV2Manager {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Create a minimal "detections" table matching what hasZeroDetections queries.
	require.NoError(t, db.Exec("CREATE TABLE IF NOT EXISTS detections (id INTEGER PRIMARY KEY)").Error)
	for i := range count {
		require.NoError(t, db.Exec("INSERT INTO detections (id) VALUES (?)", i+1).Error)
	}

	return &fakeV2Manager{db: db}
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
			name:         "fresh install — no last_seen_version, zero detections",
			version:      "v0.9.0",
			metadataRepo: newMockAppMetadataRepo(), // empty store → Get returns ""
			v2Manager: func(t *testing.T) *fakeV2Manager {
				t.Helper()
				return newFakeV2ManagerWithDetections(t, 0)
			},
			wantFresh: true,
		},
		{
			name:         "upgrade from unknown — no last_seen_version, has detections",
			version:      "v0.9.0",
			metadataRepo: newMockAppMetadataRepo(),
			v2Manager: func(t *testing.T) *fakeV2Manager {
				t.Helper()
				return newFakeV2ManagerWithDetections(t, 5)
			},
			wantNew: true,
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
			name:    "same version — no wizard needed",
			version: "v0.9.0",
			metadataRepo: func() *mockAppMetadataRepo {
				m := newMockAppMetadataRepo()
				m.store["last_seen_version"] = "v0.9.0"
				return m
			}(),
			wantPrevVersion: "v0.9.0",
		},
		{
			name:         "fresh install — nil V2Manager treated as zero detections",
			version:      "v0.9.0",
			metadataRepo: newMockAppMetadataRepo(),
			// v2Manager left nil → hasZeroDetections returns true
			wantFresh: true,
		},
		{
			name:    "metadata repo Get error — returns safe defaults",
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

			c := &Controller{
				Settings: &conf.Settings{Version: tt.version},
			}
			c.appMetadataRepo = tt.metadataRepo
			if tt.v2Manager != nil {
				c.V2Manager = tt.v2Manager(t)
			}

			freshInstall, newVersion, previousVersion := c.determineWizardState(t.Context())

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
	t.Parallel()

	mockRepo := newMockAppMetadataRepo()
	e := echo.New()
	c := &Controller{
		Settings:        &conf.Settings{Version: "v0.9.0"},
		appMetadataRepo: mockRepo,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v2/app/wizard/dismiss", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/app/wizard/dismiss")

	err := c.DismissWizard(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "v0.9.0", mockRepo.store["last_seen_version"],
		"DismissWizard should persist the current version")
}

func TestDismissWizard_NilRepo(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := &Controller{
		Settings: &conf.Settings{
			Version:   "v0.9.0",
			WebServer: conf.WebServerSettings{Debug: true},
		},
		// appMetadataRepo intentionally nil
	}

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
	c := &Controller{
		Settings: &conf.Settings{
			Version:   "v0.9.0",
			WebServer: conf.WebServerSettings{Debug: true},
		},
		appMetadataRepo: mockRepo,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v2/app/wizard/dismiss", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/app/wizard/dismiss")

	err := c.DismissWizard(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

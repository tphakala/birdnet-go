// sources_test.go: unit tests for GetImportSources and ValidateImportSource.
package importsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/imports/discovery"
)

// sourcesHandler builds a Handler wired for sources/validate tests: valid settings,
// a passthrough auth middleware, and a cancel-backed context so c.Wait() completes.
func sourcesHandler(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()
	e := echo.New()
	ctx, cancel := context.WithCancel(t.Context())
	core := apitest.NewCore(t)
	core.Group = e.Group(apiV2Prefix)
	core.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	core.SetTestContext(ctx, cancel)
	h := New(core, nil)
	t.Cleanup(func() {
		cancel()
		h.Wait()
	})
	return e, h
}

// TestGetImportSources_ReturnsCandidates verifies that injected candidates are
// reflected in the JSON response and no guidance is returned when candidates exist.
func TestGetImportSources_ReturnsCandidates(t *testing.T) {
	e, h := sourcesHandler(t)

	want := discovery.SourceCandidate{
		Path:           "/mnt/usb/birds.db",
		Kind:           discovery.KindLocal,
		DetectionCount: 42,
		LatestDate:     "2026-06-20",
		Valid:          true,
		OwnerUID:       1000,
	}
	h.importEnvInfo = func() envInfo {
		return envInfo{envType: "native", containerized: false, uid: 1000, username: "pi"}
	}
	h.scanCandidates = func(_ context.Context, _ discovery.LocationProvider) []discovery.SourceCandidate {
		return []discovery.SourceCandidate{want}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/import/sources", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.GetImportSources(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp sourcesResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Nil(t, resp.Guidance, "guidance must be nil when candidates are present")
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, want.Path, resp.Candidates[0].Path)
	assert.Equal(t, want.DetectionCount, resp.Candidates[0].DetectionCount)
	assert.True(t, resp.Candidates[0].Valid)
}

// TestGetImportSources_ZeroCandidatesReturnsGuidance verifies that setup guidance
// is included in the response whenever the scan finds no candidates.
func TestGetImportSources_ZeroCandidatesReturnsGuidance(t *testing.T) {
	e, h := sourcesHandler(t)

	h.importEnvInfo = func() envInfo {
		return envInfo{envType: "native", containerized: false, uid: 1000, username: "pi"}
	}
	h.scanCandidates = func(_ context.Context, _ discovery.LocationProvider) []discovery.SourceCandidate {
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/import/sources", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.GetImportSources(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp sourcesResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Empty(t, resp.Candidates)
	assert.NotNil(t, resp.Guidance, "guidance must be non-nil when no candidates found")
}

// TestValidateImportSource_Valid verifies that a well-formed BirdNET-Pi SQLite
// database at an absolute path is reported as valid.
func TestValidateImportSource_Valid(t *testing.T) {
	e, h := sourcesHandler(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "birds.db")
	writeMinimalBirdNetPiDB(t, dbPath)

	body := `{"source_path":"` + dbPath + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/validate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.ValidateImportSource(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp validateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.True(t, resp.Valid)
	assert.Empty(t, resp.Reason)
}

// TestValidateImportSource_NotFound verifies that a non-existent absolute path
// produces valid:false with reason "not_found". Uses t.TempDir() for a
// cross-platform absolute path; sets native mode so the path is treated as
// native-absolute on all OSes (filepath.IsAbs("/...") is false on Windows).
func TestValidateImportSource_NotFound(t *testing.T) {
	e, h := sourcesHandler(t)
	h.isContainerEnv = func() bool { return false } // native mode

	nonexistent := filepath.Join(t.TempDir(), "nonexistent.db")
	body := `{"source_path":"` + nonexistent + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/validate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.ValidateImportSource(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp validateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.False(t, resp.Valid)
	assert.Equal(t, reasonNotFound, resp.Reason)
}

// TestValidateImportSource_CancelledContextDoesNotReturnNotFound verifies that a
// cancelled request context does not mislabel the probe result as "not_found" (C2).
func TestValidateImportSource_CancelledContextDoesNotReturnNotFound(t *testing.T) {
	e, h := sourcesHandler(t)
	h.isContainerEnv = func() bool { return false } // native mode

	nonexistent := filepath.Join(t.TempDir(), "nonexistent.db")
	body := `{"source_path":"` + nonexistent + `"}`

	// Build the request with an already-cancelled context.
	reqCtx, cancel := context.WithCancel(t.Context())
	cancel() // immediately cancelled
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/validate", strings.NewReader(body))
	req = req.WithContext(reqCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.ValidateImportSource(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp validateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.False(t, resp.Valid)
	assert.NotEqual(t, reasonNotFound, resp.Reason, "cancelled context must not produce not_found")
}

// TestValidateImportSource_RejectsRelative verifies that a relative path produces
// valid:false with reason "invalid_path" without touching the filesystem.
func TestValidateImportSource_RejectsRelative(t *testing.T) {
	e, h := sourcesHandler(t)

	body := `{"source_path":"relative/birds.db"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/validate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.ValidateImportSource(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp validateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.False(t, resp.Valid)
	assert.Equal(t, reasonInvalidPath, resp.Reason)
}

// elevate_handler_test.go: unit tests for ElevateImport. No build tag: the
// ladder is fully injectable so these run on all platforms.
package importsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imports/elevation"
)

// failingRunner is a SudoRunner that always fails, simulating a system
// without passwordless sudo configured.
type failingRunner struct{}

func (failingRunner) Run(_ context.Context, _ []byte, _ string, _ ...string) error {
	return errors.NewStd("sudo unavailable")
}

// notReadable is a DirectReader that always reports the file is unreadable,
// forcing the ladder to attempt elevation.
type notReadable struct{}

func (notReadable) CanRead(string) bool { return false }

func TestElevateImport_FallbackWhenNotSudoer(t *testing.T) {
	// chmod 0o000 is a no-op on Windows, so the source stays readable and the
	// handler never reaches the elevation path. Native elevation is unix-only.
	if runtime.GOOS == osWindows {
		t.Skip("chmod-forced unreadable source is unix-only; native elevation does not run on Windows")
	}
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalBirdNetPiDB(t, src)
	require.NoError(t, os.Chmod(src, 0o000)) // make unreadable so direct fails
	t.Cleanup(func() { _ = os.Chmod(src, 0o600) })

	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.stagingBase = t.TempDir()
	h.verifyTrustedBase = func(string) error { return nil }
	h.freeBytesFn = func(string) (uint64, error) { return 1 << 30, nil }
	// Ladder whose runner fails sudo -n and has no password -> fallback.
	h.newLadder = func() (*elevation.Ladder, error) {
		return &elevation.Ladder{
			Runner:  failingRunner{},
			Direct:  notReadable{},
			SelfExe: "/bin/birdnet-go",
			Log:     slog.Default(),
		}, nil
	}

	body := fmt.Sprintf(`{"source_path":%q,"mode":"db-only"}`, src)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))

	var resp elevateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "fallback", resp.Method)
	assert.NotEmpty(t, resp.FallbackCommands)
}

func TestElevateImport_RejectsContainer(t *testing.T) {
	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return true }

	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(`{"source_path":"/x/birds.db","mode":"db-only"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestElevateImport_InsufficientSpace(t *testing.T) {
	// A regular file that is not a BirdNET-Pi database: discovery.Probe reports it
	// invalid whatever the uid, so the handler takes the staging path where the
	// disk preflight runs. A valid database would be read directly and skip staging.
	src := filepath.Join(t.TempDir(), "birds.db")
	require.NoError(t, os.WriteFile(src, []byte("not a BirdNET-Pi database"), 0o600))

	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.stagingBase = t.TempDir()
	h.verifyTrustedBase = func(string) error { return nil }
	h.freeBytesFn = func(string) (uint64, error) { return 0, nil } // no space
	h.newLadder = func() (*elevation.Ladder, error) {
		return &elevation.Ladder{Runner: failingRunner{}, Direct: notReadable{}, SelfExe: "/x", Log: slog.Default()}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(fmt.Sprintf(`{"source_path":%q,"mode":"db-only"}`, src)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))
	assert.Equal(t, http.StatusInsufficientStorage, rec.Code)
}

// TestElevateImport_PasswordRequired_WhenAllowElevationAndNoPassword verifies
// that when in-app elevation is enabled and no password was supplied, a fallback
// from the ladder produces "password_required" instead of copy-paste commands.
func TestElevateImport_PasswordRequired_WhenAllowElevationAndNoPassword(t *testing.T) {
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalBirdNetPiDB(t, src)

	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.stagingBase = t.TempDir()
	h.verifyTrustedBase = func(string) error { return nil }
	h.freeBytesFn = func(string) (uint64, error) { return 1 << 30, nil }
	h.newLadder = func() (*elevation.Ladder, error) {
		return &elevation.Ladder{
			Runner:  failingRunner{},
			Direct:  notReadable{},
			SelfExe: "/bin/birdnet-go",
			Log:     slog.Default(),
		}, nil
	}
	// Enable in-app elevation. Publish to the global snapshot too so
	// CurrentSettings() (which prefers the global) observes AllowInAppElevation=true.
	settings := apitest.NewValidTestSettings()
	settings.Import.AllowInAppElevation = true
	h.Settings.Store(settings)
	apitest.PublishTestSettings(t, settings)

	// No password supplied -> expect password_required.
	body := fmt.Sprintf(`{"source_path":%q,"mode":"db-only"}`, src)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp elevateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, responseMethodPasswordRequired, resp.Method)
	assert.Empty(t, resp.JobID)
	assert.Empty(t, resp.FallbackCommands)
}

// TestElevateImport_FallbackWhenPasswordSuppliedButStillFails verifies that when
// a password was supplied but the ladder still returns fallback (e.g. wrong
// password), the response is "fallback" not "password_required".
func TestElevateImport_FallbackWhenPasswordSuppliedButStillFails(t *testing.T) {
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalBirdNetPiDB(t, src)

	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.stagingBase = t.TempDir()
	h.verifyTrustedBase = func(string) error { return nil }
	h.freeBytesFn = func(string) (uint64, error) { return 1 << 30, nil }
	h.newLadder = func() (*elevation.Ladder, error) {
		return &elevation.Ladder{
			Runner:  failingRunner{},
			Direct:  notReadable{},
			SelfExe: "/bin/birdnet-go",
			Log:     slog.Default(),
		}, nil
	}
	// Enable in-app elevation and publish globally so CurrentSettings() sees it.
	settings := apitest.NewValidTestSettings()
	settings.Import.AllowInAppElevation = true
	h.Settings.Store(settings)
	apitest.PublishTestSettings(t, settings)

	// Password supplied -> must get fallback, not password_required.
	body := fmt.Sprintf(`{"source_path":%q,"mode":"db-only","password":"wrongpass"}`, src)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))

	var resp elevateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, responseMethodFallback, resp.Method)
	assert.NotEmpty(t, resp.FallbackCommands)
}

// TestElevateImport_FallbackWhenAllowElevationDisabled verifies that when
// AllowInAppElevation is false, the ladder result is always "fallback" even
// when no password was supplied.
func TestElevateImport_FallbackWhenAllowElevationDisabled(t *testing.T) {
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalBirdNetPiDB(t, src)

	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.stagingBase = t.TempDir()
	h.verifyTrustedBase = func(string) error { return nil }
	h.freeBytesFn = func(string) (uint64, error) { return 1 << 30, nil }
	h.newLadder = func() (*elevation.Ladder, error) {
		return &elevation.Ladder{
			Runner:  failingRunner{},
			Direct:  notReadable{},
			SelfExe: "/bin/birdnet-go",
			Log:     slog.Default(),
		}, nil
	}
	// AllowInAppElevation is false by default; no settings override needed.

	body := fmt.Sprintf(`{"source_path":%q,"mode":"db-only"}`, src)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))

	var resp elevateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, responseMethodFallback, resp.Method)
	assert.NotEmpty(t, resp.FallbackCommands)
}

// TestElevateImport_Returns503WhenDatastoreUnavailable verifies the datastore
// guard fires before any elevation is attempted when DS is nil.
func TestElevateImport_Returns503WhenDatastoreUnavailable(t *testing.T) {
	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.DS = nil // simulate missing datastore

	src := filepath.Join(t.TempDir(), "birds.db")
	body := fmt.Sprintf(`{"source_path":%q,"mode":"db-only"}`, src)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

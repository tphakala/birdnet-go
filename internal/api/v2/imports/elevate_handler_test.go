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

// TestElevateImport_LadderFallback verifies the response when the ladder falls
// back because passwordless elevation does not work. With in-app elevation on
// and no password yet, the handler asks for one ("password_required"); with a
// password already supplied, or with in-app elevation off, it returns the
// copy-paste commands. No row starts a job. The staged source rows use a regular
// file that is not a BirdNET-Pi database: discovery.Probe reports it invalid
// whatever the uid, so the handler takes the staging path (as root too) and
// needs no chmod. Only that path asks the disk probe for free space, which is how
// each row shows whether staging ran.
func TestElevateImport_LadderFallback(t *testing.T) {
	tests := []struct {
		name           string
		stagedSource   bool
		allowElevation bool
		password       string
		wantMethod     string
	}{
		{name: "staged source with elevation off", stagedSource: true, wantMethod: responseMethodFallback},
		{name: "staged source, elevation on, no password", stagedSource: true, allowElevation: true, wantMethod: responseMethodPasswordRequired},
		{name: "valid database, elevation on, no password", allowElevation: true, wantMethod: responseMethodPasswordRequired},
		{name: "valid database, elevation on, password supplied", allowElevation: true, password: "wrongpass", wantMethod: responseMethodFallback},
		{name: "valid database, elevation off, no password", wantMethod: responseMethodFallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "birds.db")
			if tt.stagedSource {
				require.NoError(t, os.WriteFile(src, []byte("not a BirdNET-Pi database"), 0o600))
			} else {
				writeMinimalBirdNetPiDB(t, src)
			}

			h := New(testCore(t), nil)
			h.isContainerEnv = func() bool { return false }
			h.stagingBase = t.TempDir()
			h.verifyTrustedBase = func(string) error { return nil }
			freeProbes := 0
			h.freeBytesFn = func(string) (uint64, error) {
				freeProbes++
				return 1 << 30, nil
			}
			h.newLadder = func() (*elevation.Ladder, error) {
				return &elevation.Ladder{
					Runner:  failingRunner{},
					Direct:  notReadable{},
					SelfExe: "/bin/birdnet-go",
					Log:     slog.Default(),
				}, nil
			}
			if tt.allowElevation {
				// Publish to the global snapshot too so CurrentSettings(), which
				// prefers the global, observes AllowInAppElevation=true.
				settings := apitest.NewValidTestSettings()
				settings.Import.AllowInAppElevation = true
				h.Settings.Store(settings)
				apitest.PublishTestSettings(t, settings)
			}

			body := fmt.Sprintf(`{"source_path":%q,"mode":"db-only","password":%q}`, src, tt.password)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := echo.New().NewContext(req, rec)

			require.NoError(t, h.ElevateImport(ctx))
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp elevateResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, tt.wantMethod, resp.Method)
			assert.Empty(t, resp.JobID)
			if tt.stagedSource {
				assert.Equal(t, 1, freeProbes, "a source that is not a valid database must go through staging")
			} else {
				assert.Zero(t, freeProbes, "a valid database skips staging")
			}
			if tt.wantMethod == responseMethodPasswordRequired {
				assert.Empty(t, resp.FallbackCommands, "asking for a password must not also hand out commands")
			} else {
				assert.NotEmpty(t, resp.FallbackCommands)
			}
		})
	}
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

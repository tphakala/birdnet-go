// elevate_handler_test.go: unit tests for ElevateImport. No build tag: the
// ladder is fully injectable so these run on all platforms.
package importsapi

import (
	"context"
	"encoding/json"
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

	body := `{"source_path":"` + src + `","mode":"db-only"}`
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
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalBirdNetPiDB(t, src)

	h := New(testCore(t), nil)
	h.isContainerEnv = func() bool { return false }
	h.stagingBase = t.TempDir()
	h.verifyTrustedBase = func(string) error { return nil }
	h.freeBytesFn = func(string) (uint64, error) { return 0, nil } // no space
	h.newLadder = func() (*elevation.Ladder, error) {
		return &elevation.Ladder{Runner: failingRunner{}, Direct: notReadable{}, SelfExe: "/x", Log: slog.Default()}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/elevate", strings.NewReader(`{"source_path":"`+src+`","mode":"db-only"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	require.NoError(t, h.ElevateImport(ctx))
	assert.Equal(t, http.StatusInsufficientStorage, rec.Code)
}

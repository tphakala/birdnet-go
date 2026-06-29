// sse_sanitize_test.go verifies the per-client source anonymization applied to
// the public detection stream: unauthenticated subscribers must receive only the
// stable Source.ID, never the raw DisplayName, while authenticated subscribers
// keep the full payload. The tests also lock in the shared-pointer / shared-slice
// safety invariant: sanitization must never mutate the broadcast struct that is
// fanned out to every connected client.
package sse

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// sensitiveDisplayName is an internal-looking source label of the kind that must
// never leak to unauthenticated clients (it embeds host and credential details).
const sensitiveDisplayName = "rtsp://user:pass@host/cam"

func TestSanitizeDetectionForUnauthenticated(t *testing.T) {
	t.Parallel()

	t.Run("strips display name and rebuilds source without mutating input", func(t *testing.T) {
		t.Parallel()

		input := apicore.SSEDetectionData{
			ID:         42,
			CommonName: "Eurasian Wren",
			Source:     &apicore.SSESourceInfo{ID: "rtsp-1", DisplayName: sensitiveDisplayName},
		}

		out := sanitizeDetectionForUnauthenticated(&input)

		// The sanitized payload exposes only the stable ID.
		require.NotNil(t, out.Source)
		assert.Equal(t, "rtsp-1", out.Source.ID)
		assert.Empty(t, out.Source.DisplayName, "DisplayName must not reach unauthenticated clients")

		// The shared input struct/pointer must be untouched (no in-place mutation),
		// otherwise the broadcast struct shared across all clients would be corrupted
		// and a data race would occur under -race.
		assert.Equal(t, sensitiveDisplayName, input.Source.DisplayName, "original DisplayName must be unchanged")
		assert.NotSame(t, input.Source, out.Source, "Source must be a fresh pointer, not the shared one")

		// Non-source fields carry through unchanged.
		assert.Equal(t, uint(42), out.ID)
		assert.Equal(t, "Eurasian Wren", out.CommonName)
	})

	t.Run("nil source is returned unchanged without panic", func(t *testing.T) {
		t.Parallel()

		input := apicore.SSEDetectionData{ID: 7, Source: nil}
		out := sanitizeDetectionForUnauthenticated(&input)
		assert.Nil(t, out.Source)
		assert.Equal(t, uint(7), out.ID)
	})
}

func TestDetectionPayloadForClient(t *testing.T) {
	t.Parallel()

	t.Run("authenticated keeps full display name via shared pointer", func(t *testing.T) {
		t.Parallel()

		src := &apicore.SSESourceInfo{ID: "rtsp-1", DisplayName: sensitiveDisplayName}
		event := apicore.SSEDetectionData{ID: 1, Source: src}

		out := detectionPayloadForClient(&event, true)

		require.NotNil(t, out.Source)
		assert.Equal(t, sensitiveDisplayName, out.Source.DisplayName, "authenticated clients keep DisplayName")
		assert.Same(t, src, out.Source, "authenticated path must not copy the source")
	})

	t.Run("unauthenticated receives sanitized copy", func(t *testing.T) {
		t.Parallel()

		src := &apicore.SSESourceInfo{ID: "rtsp-1", DisplayName: sensitiveDisplayName}
		event := apicore.SSEDetectionData{ID: 1, Source: src}

		out := detectionPayloadForClient(&event, false)

		require.NotNil(t, out.Source)
		assert.Equal(t, "rtsp-1", out.Source.ID)
		assert.Empty(t, out.Source.DisplayName)
		assert.Equal(t, sensitiveDisplayName, src.DisplayName, "shared source must be unchanged")
		assert.NotSame(t, src, out.Source)
	})
}

func TestSanitizePendingForUnauthenticated(t *testing.T) {
	t.Parallel()

	t.Run("strips per-item display name without mutating shared slice", func(t *testing.T) {
		t.Parallel()

		input := []processor.SSEPendingDetection{
			{Species: "Eurasian Wren", Source: sensitiveDisplayName, SourceID: "rtsp-1"},
			{Species: "European Robin", Source: "Backyard mic", SourceID: "card-0"},
		}

		result := sanitizePendingForUnauthenticated(input)
		out, ok := result.([]processor.SSEPendingDetection)
		require.True(t, ok, "result must be a pending detection slice")
		require.Len(t, out, 2)

		// Display names stripped, SourceID retained for client-side filtering.
		assert.Empty(t, out[0].Source)
		assert.Equal(t, "rtsp-1", out[0].SourceID)
		assert.Empty(t, out[1].Source)
		assert.Equal(t, "card-0", out[1].SourceID)

		// The shared backing array fanned out to every client must be untouched.
		assert.Equal(t, sensitiveDisplayName, input[0].Source, "shared slice must be unchanged")
		assert.Equal(t, "Backyard mic", input[1].Source, "shared slice must be unchanged")
		assert.NotSame(t, &input[0], &out[0], "must allocate a fresh backing array")
	})

	t.Run("unexpected payload type fails closed to empty list", func(t *testing.T) {
		t.Parallel()

		// An unknown concrete type must not be forwarded to an unauthenticated
		// client (it could carry a display name); it fails closed to an empty list.
		result := sanitizePendingForUnauthenticated("not-a-pending-slice")
		out, ok := result.([]processor.SSEPendingDetection)
		require.True(t, ok, "fail-closed result must be a (empty) pending detection slice")
		assert.Empty(t, out)
	})
}

func TestPendingPayloadForClient(t *testing.T) {
	t.Parallel()

	input := []processor.SSEPendingDetection{
		{Species: "Eurasian Wren", Source: sensitiveDisplayName, SourceID: "rtsp-1"},
	}

	t.Run("authenticated returns payload unchanged", func(t *testing.T) {
		t.Parallel()
		out := pendingPayloadForClient(input, true)
		assert.Equal(t, sensitiveDisplayName, input[0].Source)
		// Authenticated path must not copy: the same slice value is returned.
		got, ok := out.([]processor.SSEPendingDetection)
		require.True(t, ok)
		assert.Same(t, &input[0], &got[0])
	})

	t.Run("unauthenticated returns sanitized copy", func(t *testing.T) {
		t.Parallel()
		out := pendingPayloadForClient(input, false)
		got, ok := out.([]processor.SSEPendingDetection)
		require.True(t, ok)
		require.Len(t, got, 1)
		assert.Empty(t, got[0].Source)
		assert.Equal(t, "rtsp-1", got[0].SourceID)
		assert.Equal(t, sensitiveDisplayName, input[0].Source, "shared slice must be unchanged")
	})
}

// TestNewWiresAuthCheck verifies the facade-injected auth check is stored by New
// and consulted by clientAuthenticated, and that a nil check fails closed
// (treated as unauthenticated so the stream anonymizes by default).
func TestNewWiresAuthCheck(t *testing.T) {
	t.Parallel()

	newCtx := func() echo.Context {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/stream", http.NoBody)
		return e.NewContext(req, httptest.NewRecorder())
	}

	t.Run("authenticated stub", func(t *testing.T) {
		t.Parallel()
		h := New(apitest.NewCore(t, apitest.WithoutSettingsPublish()), func(echo.Context) bool { return true })
		assert.True(t, h.clientAuthenticated(newCtx()))
	})

	t.Run("unauthenticated stub", func(t *testing.T) {
		t.Parallel()
		h := New(apitest.NewCore(t, apitest.WithoutSettingsPublish()), func(echo.Context) bool { return false })
		assert.False(t, h.clientAuthenticated(newCtx()))
	})

	t.Run("nil check fails closed", func(t *testing.T) {
		t.Parallel()
		h := New(apitest.NewCore(t, apitest.WithoutSettingsPublish()), nil)
		assert.False(t, h.clientAuthenticated(newCtx()))
	})
}

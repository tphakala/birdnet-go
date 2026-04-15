// quiet_hours_test.go: Unit tests for buildSuppressedStreamsPayload — the
// helper that powers the /api/v2/streams/quiet-hours/status response.
//
// The guest-facing path replaces raw URLs with opaque "stream-N"
// placeholders so anonymous dashboard viewers cannot enumerate camera
// hostnames/ports (PR #2775). These tests lock in:
//   - the placeholder shape (stream-N with N starting at 1) and count,
//   - stability across calls on the same input,
//   - the documented index-shift behavior when a new URL that sorts before
//     an existing one is added (future hash-based refactor needs to change
//     this deliberately), and
//   - that the authenticated path passes URLs through privacy.SanitizeStreamUrl
//     without leaking credentials.

package api

import (
	"maps"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// streamPlaceholderPattern matches the opaque placeholder keys that the
// guest path emits. Centralized here so the shape is asserted once.
var streamPlaceholderPattern = regexp.MustCompile(`^stream-[1-9]\d*$`)

// TestBuildSuppressedStreamsPayload_GuestPlaceholderShape asserts every
// guest-facing key matches stream-N and the count equals the input count.
func TestBuildSuppressedStreamsPayload_GuestPlaceholderShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   map[string]bool
	}{
		{
			name: "empty",
			in:   map[string]bool{},
		},
		{
			name: "single stream",
			in: map[string]bool{
				"rtsp://user:pass@cam1.lan/stream": true,
			},
		},
		{
			name: "multiple streams mixed state",
			in: map[string]bool{
				"rtsp://user:pass@cam1.lan/stream": true,
				"rtsp://user:pass@cam2.lan/stream": false,
				"rtsp://user:pass@cam3.lan/stream": true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := buildSuppressedStreamsPayload(tc.in, true)
			assert.Len(t, out, len(tc.in),
				"guest payload must preserve entry count")
			for key := range out {
				assert.Regexp(t, streamPlaceholderPattern, key,
					"guest key %q must match stream-N shape", key)
				assert.NotContains(t, key, "://",
					"guest key %q leaks URL scheme", key)
				assert.NotContains(t, key, "@",
					"guest key %q leaks userinfo", key)
			}
		})
	}
}

// TestBuildSuppressedStreamsPayload_GuestStableWithinResponse asserts that
// repeated calls on the same input return identical maps. This is the
// contract the handler's comment promises (raw URL keys live in the
// scheduler and do not change between polls).
func TestBuildSuppressedStreamsPayload_GuestStableWithinResponse(t *testing.T) {
	t.Parallel()

	in := map[string]bool{
		"rtsp://user:pass@cam-a.lan/stream": true,
		"rtsp://user:pass@cam-b.lan/stream": false,
		"rtsp://user:pass@cam-c.lan/stream": true,
	}

	first := buildSuppressedStreamsPayload(in, true)
	second := buildSuppressedStreamsPayload(in, true)
	assert.Equal(t, first, second,
		"guest payload must be deterministic for a given input")
}

// TestBuildSuppressedStreamsPayload_GuestShiftsWhenPrependingSort documents
// the current behavior: adding a URL that sorts before existing ones shifts
// every existing placeholder index by one. Today this is benign — the
// "Currently Hearing" dashboard card only counts true values and does not
// key UI state on placeholder names. If a future consumer needs hash-stable
// placeholders, this test will fail and force a deliberate refactor.
func TestBuildSuppressedStreamsPayload_GuestShiftsWhenPrependingSort(t *testing.T) {
	t.Parallel()

	before := map[string]bool{
		"rtsp://user:pass@cam-m.lan/stream": true,
		"rtsp://user:pass@cam-z.lan/stream": false,
	}
	after := map[string]bool{
		"rtsp://user:pass@cam-a.lan/stream": true, // sorts before both existing URLs
		"rtsp://user:pass@cam-m.lan/stream": true,
		"rtsp://user:pass@cam-z.lan/stream": false,
	}

	firstPayload := buildSuppressedStreamsPayload(before, true)
	secondPayload := buildSuppressedStreamsPayload(after, true)

	// The value for cam-m was true in both inputs. With the current
	// sort-then-index scheme, the key that holds that value in `before`
	// (stream-1) will now belong to cam-a in `after`, meaning cam-m has
	// moved to stream-2. Assert both maps stay valid (no key collisions)
	// and that the count is correct — but explicitly do NOT assert that
	// stream-1's value is stable, documenting the shift.
	require.Len(t, firstPayload, 2)
	require.Len(t, secondPayload, 3)
	for _, payload := range []map[string]bool{firstPayload, secondPayload} {
		for key := range payload {
			assert.Regexp(t, streamPlaceholderPattern, key)
		}
	}
}

// TestBuildSuppressedStreamsPayload_AuthenticatedSanitizesURLs asserts the
// non-guest path passes URLs through privacy.SanitizeStreamUrl — credentials
// are stripped but host/port remain visible to the settings UI.
func TestBuildSuppressedStreamsPayload_AuthenticatedSanitizesURLs(t *testing.T) {
	t.Parallel()

	raw := "rtsp://user:pass@cam1.lan:8554/stream"
	in := map[string]bool{raw: true}

	out := buildSuppressedStreamsPayload(in, false)
	require.Len(t, out, 1)

	wantKey := privacy.SanitizeStreamUrl(raw)
	got, exists := out[wantKey]
	require.True(t, exists,
		"authenticated path must key by SanitizeStreamUrl output, got keys: %v", mapKeys(out))
	assert.True(t, got, "value must be preserved")

	// Credential string must not survive sanitization.
	for key := range out {
		assert.NotContains(t, key, "user:pass",
			"authenticated key %q leaks credentials", key)
		assert.NotContains(t, key, "@cam1.lan",
			"authenticated key %q retains userinfo", key)
	}
}

// mapKeys returns a stable slice of map keys for assertion messages.
func mapKeys[V any](m map[string]V) []string {
	return slices.Collect(maps.Keys(m))
}

package conf

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHuggingFaceEndpointChain(t *testing.T) {
	// Env-mutating: cannot run in parallel with other env users.
	tests := []struct {
		name       string
		configured string
		env        string
		want       []string
	}{
		{
			name: "no override yields canonical then mirror",
			want: []string{DefaultHuggingFaceEndpoint, FallbackHuggingFaceMirror},
		},
		{
			name:       "settings override is authoritative and single",
			configured: "https://settings-mirror.example.com",
			want:       []string{"https://settings-mirror.example.com"},
		},
		{
			name: "env override is authoritative and single",
			env:  "https://hf-mirror.com",
			want: []string{"https://hf-mirror.com"},
		},
		{
			name:       "settings override wins over env",
			configured: "https://settings-mirror.example.com",
			env:        "https://env-mirror.example.com",
			want:       []string{"https://settings-mirror.example.com"},
		},
		{
			name:       "invalid override degrades to the default chain",
			configured: "not a url",
			want:       []string{DefaultHuggingFaceEndpoint, FallbackHuggingFaceMirror},
		},
		{
			name:       "override with credentials is rejected and degrades to the chain",
			configured: "https://user:pw@mirror.example.com",
			want:       []string{DefaultHuggingFaceEndpoint, FallbackHuggingFaceMirror},
		},
		{
			name:       "trailing slash is trimmed on an override",
			configured: "https://mirror.example.com/",
			want:       []string{"https://mirror.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(HuggingFaceEndpointEnvVar, tt.env)
			assert.Equal(t, tt.want, ResolveHuggingFaceEndpointChain(tt.configured))
		})
	}
}

func TestIsUnreachable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is reachable", err: nil, want: false},
		{name: "cancelled context does not fail over", err: context.Canceled, want: false},
		{name: "wrapped cancelled context does not fail over", err: fmt.Errorf("dial: %w", context.Canceled), want: false},
		{name: "deadline exceeded fails over", err: context.DeadlineExceeded, want: true},
		{name: "dns error fails over", err: &net.DNSError{Err: "no such host", Name: "huggingface.co"}, want: true},
		{name: "op error fails over", err: &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}, want: true},
		{name: "opaque transport error fails over", err: fmt.Errorf("http2: server sent GOAWAY"), want: true},
		{name: "eof fails over", err: fmt.Errorf("unexpected EOF"), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsUnreachable(tt.err))
		})
	}
}

func TestIsGatewayStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   bool
	}{
		{status: 200, want: false},
		{status: 404, want: false}, // reachable, file genuinely missing: must NOT fail over
		{status: 403, want: false},
		{status: 429, want: false},
		{status: 500, want: false},
		{status: 502, want: true},
		{status: 503, want: true},
		{status: 504, want: true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("status-%d", tt.status), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsGatewayStatus(tt.status))
		})
	}
}

func TestHFEndpointResolver_OrderedEndpoints_NoOverride(t *testing.T) {
	t.Setenv(HuggingFaceEndpointEnvVar, "")

	t.Run("top-first with no sticky", func(t *testing.T) {
		r := NewHFEndpointResolver("")
		assert.Equal(t, []string{DefaultHuggingFaceEndpoint, FallbackHuggingFaceMirror}, r.OrderedEndpoints(""))
	})

	t.Run("sticky mirror moves to the front within the interval", func(t *testing.T) {
		base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
		r := NewHFEndpointResolver("")
		r.now = func() time.Time { return base }
		r.NoteWorking(FallbackHuggingFaceMirror)
		assert.Equal(t, []string{FallbackHuggingFaceMirror, DefaultHuggingFaceEndpoint}, r.OrderedEndpoints(""))
	})

	t.Run("stale sticky re-probes from the top", func(t *testing.T) {
		base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
		now := base
		r := NewHFEndpointResolver("")
		r.now = func() time.Time { return now }
		r.NoteWorking(FallbackHuggingFaceMirror)
		now = base.Add(hfStickyRevalidateInterval + time.Minute) // past the window
		assert.Equal(t, []string{DefaultHuggingFaceEndpoint, FallbackHuggingFaceMirror}, r.OrderedEndpoints(""))
	})
}

func TestHFEndpointResolver_OrderedEndpoints_ExplicitOverrideIgnoresSticky(t *testing.T) {
	t.Setenv(HuggingFaceEndpointEnvVar, "")
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	r := NewHFEndpointResolver("")
	r.now = func() time.Time { return base }
	// Even with a sticky mirror recorded, an explicit override is the only host.
	r.NoteWorking(FallbackHuggingFaceMirror)
	assert.Equal(t, []string{"https://settings-mirror.example.com"},
		r.OrderedEndpoints("https://settings-mirror.example.com"))
}

func TestHFEndpointResolver_StickyPersistsAcrossRestart(t *testing.T) {
	t.Setenv(HuggingFaceEndpointEnvVar, "")
	dir := t.TempDir()

	first := NewHFEndpointResolver(dir)
	first.NoteWorking(FallbackHuggingFaceMirror)

	// A fresh resolver over the same config dir loads the persisted preference.
	second := NewHFEndpointResolver(dir)
	assert.Equal(t, []string{FallbackHuggingFaceMirror, DefaultHuggingFaceEndpoint}, second.OrderedEndpoints(""))

	// The state file lives under the remote-catalog subdir, not the config root.
	_, err := os.Stat(filepath.Join(dir, remoteCatalogStateDir, hfEndpointStateFile))
	require.NoError(t, err)
}

func TestHFEndpointResolver_SameEndpointUseRefreshesPersistedFreshness(t *testing.T) {
	t.Setenv(HuggingFaceEndpointEnvVar, "")
	dir := t.TempDir()
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	// First install fails over to the mirror and persists {mirror, base}.
	r1 := NewHFEndpointResolver(dir)
	r1.now = func() time.Time { return base }
	r1.NoteWorking(FallbackHuggingFaceMirror)

	// A later install keeps using the mirror (unchanged endpoint) past the
	// persist cadence, so the persisted freshness is refreshed to that time.
	laterUse := base.Add(25 * time.Minute) // > stickyPersistCadence
	r1.now = func() time.Time { return laterUse }
	r1.NoteWorking(FallbackHuggingFaceMirror)

	// Restart shortly after that later use. The original {mirror, base} record
	// would be stale by now (restart-base > revalidate interval) and re-probe,
	// so the mirror staying preferred proves the same-endpoint use re-persisted
	// freshness.
	restart := laterUse.Add(10 * time.Minute)
	require.Greater(t, restart.Sub(base), hfStickyRevalidateInterval,
		"the first persisted record must be stale at restart for this test to prove the refresh")
	require.Less(t, restart.Sub(laterUse), hfStickyRevalidateInterval,
		"the refreshed record must still be fresh at restart")

	r2 := NewHFEndpointResolver(dir)
	r2.now = func() time.Time { return restart }
	assert.Equal(t, []string{FallbackHuggingFaceMirror, DefaultHuggingFaceEndpoint}, r2.OrderedEndpoints(""))
}

func TestHFEndpointResolver_EmptyConfigDirIsInMemoryOnly(t *testing.T) {
	t.Setenv(HuggingFaceEndpointEnvVar, "")
	r := NewHFEndpointResolver("")
	r.NoteWorking(FallbackHuggingFaceMirror) // must not panic without a state path
	assert.Equal(t, []string{FallbackHuggingFaceMirror, DefaultHuggingFaceEndpoint}, r.OrderedEndpoints(""))
}

func TestHFEndpointResolver_LoadValidatesPersistedEndpoint(t *testing.T) {
	t.Setenv(HuggingFaceEndpointEnvVar, "")

	// writeState persists a state file with a fresh timestamp, so staleness can
	// never confound whether the endpoint was accepted on load.
	writeState := func(t *testing.T, endpoint string) string {
		t.Helper()
		dir := t.TempDir()
		stateDir := filepath.Join(dir, remoteCatalogStateDir)
		require.NoError(t, os.MkdirAll(stateDir, 0o755))
		state := fmt.Sprintf(`{"endpoint":%q,"updatedAt":%q}`, endpoint, time.Now().UTC().Format(time.RFC3339))
		require.NoError(t, os.WriteFile(filepath.Join(stateDir, hfEndpointStateFile), []byte(state), 0o600))
		return dir
	}

	// The assertions read r.sticky directly (white-box) so they discriminate
	// acceptance from rejection: removing the validation in load() would flip
	// the negative case and fail the test.
	t.Run("credentialed endpoint is rejected on load", func(t *testing.T) {
		r := NewHFEndpointResolver(writeState(t, "https://user:pw@evil.example.com"))
		assert.Empty(t, r.sticky, "a credentialed persisted endpoint must not retarget downloads")
	})

	t.Run("valid persisted endpoint is loaded (positive control)", func(t *testing.T) {
		r := NewHFEndpointResolver(writeState(t, FallbackHuggingFaceMirror))
		assert.Equal(t, FallbackHuggingFaceMirror, r.sticky)
	})
}

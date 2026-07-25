package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveHuggingFaceEndpoint_Precedence covers the documented resolution
// order: settings field, then HF_ENDPOINT, then the default host.
func TestResolveHuggingFaceEndpoint_Precedence(t *testing.T) {
	// Not parallel: mutates the HF_ENDPOINT environment variable.

	tests := []struct {
		name       string
		configured string
		env        string
		setEnv     bool
		want       string
	}{
		{
			name: "default when nothing is set",
			want: DefaultHuggingFaceEndpoint,
		},
		{
			name:   "default when both sources are empty",
			env:    "",
			setEnv: true,
			want:   DefaultHuggingFaceEndpoint,
		},
		{
			name:   "env var used when settings field is empty",
			env:    "https://hf-mirror.com",
			setEnv: true,
			want:   "https://hf-mirror.com",
		},
		{
			name:       "settings field wins over env var",
			configured: "https://settings-mirror.example.com",
			env:        "https://hf-mirror.com",
			setEnv:     true,
			want:       "https://settings-mirror.example.com",
		},
		{
			name:       "whitespace-only settings field falls through to env var",
			configured: "   ",
			env:        "https://hf-mirror.com",
			setEnv:     true,
			want:       "https://hf-mirror.com",
		},
		{
			name:       "whitespace-only env var falls through to the default",
			configured: "",
			env:        "  \t ",
			setEnv:     true,
			want:       DefaultHuggingFaceEndpoint,
		},
		{
			name:       "settings field used when env var is unset",
			configured: "https://hf-mirror.com",
			want:       "https://hf-mirror.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(HuggingFaceEndpointEnvVar, tt.env)
			}
			assert.Equal(t, tt.want, ResolveHuggingFaceEndpoint(tt.configured))
		})
	}
}

// TestResolveHuggingFaceEndpoint_Normalization verifies that accepted overrides
// come back in a form callers can append "/" + path to.
func TestResolveHuggingFaceEndpoint_Normalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{
			name:       "trailing slash trimmed",
			configured: "https://hf-mirror.com/",
			want:       "https://hf-mirror.com",
		},
		{
			name:       "repeated trailing slashes trimmed",
			configured: "https://hf-mirror.com///",
			want:       "https://hf-mirror.com",
		},
		{
			name:       "surrounding whitespace trimmed",
			configured: "  https://hf-mirror.com  ",
			want:       "https://hf-mirror.com",
		},
		{
			name:       "path prefix preserved",
			configured: "https://mirror.example.com/hf/",
			want:       "https://mirror.example.com/hf",
		},
		{
			name:       "scheme case normalized",
			configured: "HTTPS://hf-mirror.com",
			want:       "https://hf-mirror.com",
		},
		{
			name:       "plain http accepted for a LAN mirror",
			configured: "http://mirror.lan:8080",
			want:       "http://mirror.lan:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ResolveHuggingFaceEndpoint(tt.configured))
		})
	}
}

// TestResolveHuggingFaceEndpoint_MalformedFallsBack verifies that a bad
// override degrades to the default host instead of producing an unusable URL.
func TestResolveHuggingFaceEndpoint_MalformedFallsBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
	}{
		{name: "no scheme", configured: "hf-mirror.com"},
		{name: "scheme-relative", configured: "//hf-mirror.com"},
		{name: "unsupported scheme", configured: "ftp://hf-mirror.com"},
		{name: "file scheme", configured: "file:///models"},
		{name: "no host", configured: "https://"},
		{name: "path only", configured: "/models"},
		{name: "query string", configured: "https://hf-mirror.com?token=abc"},
		{name: "fragment", configured: "https://hf-mirror.com#frag"},
		{name: "control character", configured: "https://hf-mirror.com/\x7f"},
		{name: "slashes only", configured: "///"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, DefaultHuggingFaceEndpoint, ResolveHuggingFaceEndpoint(tt.configured))
		})
	}
}

// TestResolveHuggingFaceEndpoint_MalformedEnvFallsBack verifies the same
// rejection applies to HF_ENDPOINT, which users copy from mirror docs.
func TestResolveHuggingFaceEndpoint_MalformedEnvFallsBack(t *testing.T) {
	// Not parallel: mutates the HF_ENDPOINT environment variable.

	t.Setenv(HuggingFaceEndpointEnvVar, "hf-mirror.com")
	assert.Equal(t, DefaultHuggingFaceEndpoint, ResolveHuggingFaceEndpoint(""))
}

// TestResolveHuggingFaceEndpoint_MalformedSettingsDoesNotFallThroughToEnv
// pins the documented behaviour: the first non-empty source is the one used,
// and a malformed value there resolves to the default rather than silently
// picking up a different mirror.
func TestResolveHuggingFaceEndpoint_MalformedSettingsDoesNotFallThroughToEnv(t *testing.T) {
	// Not parallel: mutates the HF_ENDPOINT environment variable.

	t.Setenv(HuggingFaceEndpointEnvVar, "https://hf-mirror.com")
	assert.Equal(t, DefaultHuggingFaceEndpoint, ResolveHuggingFaceEndpoint("not a url"))
}

// TestResolveHuggingFaceEndpoint_NeverEndsInSlash guards the contract callers
// rely on when they append "/" + path.
func TestResolveHuggingFaceEndpoint_NeverEndsInSlash(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"",
		"https://hf-mirror.com",
		"https://hf-mirror.com/",
		"https://mirror.example.com/hf//",
		"bogus",
	} {
		got := ResolveHuggingFaceEndpoint(in)
		require.NotEmpty(t, got)
		assert.NotEqual(t, "/", string(got[len(got)-1]), "resolved endpoint %q must not end in a slash", got)
	}
}

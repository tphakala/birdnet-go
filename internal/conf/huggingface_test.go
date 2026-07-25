package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestResolveHuggingFaceEndpoint_Precedence covers the documented resolution
// order: settings field, then HF_ENDPOINT, then the default host.
func TestResolveHuggingFaceEndpoint_Precedence(t *testing.T) {
	// Not parallel: mutates the HF_ENDPOINT environment variable.

	tests := []struct {
		name       string
		configured string
		env        string
		want       string
	}{
		{
			name: "default when neither source is set",
			want: DefaultHuggingFaceEndpoint,
		},
		{
			name: "env var used when settings field is empty",
			env:  "https://hf-mirror.com",
			want: "https://hf-mirror.com",
		},
		{
			name:       "settings field wins over env var",
			configured: "https://settings-mirror.example.com",
			env:        "https://hf-mirror.com",
			want:       "https://settings-mirror.example.com",
		},
		{
			name:       "whitespace-only settings field falls through to env var",
			configured: "   ",
			env:        "https://hf-mirror.com",
			want:       "https://hf-mirror.com",
		},
		{
			name:       "whitespace-only env var falls through to the default",
			configured: "",
			env:        "  \t ",
			want:       DefaultHuggingFaceEndpoint,
		},
		{
			name:       "settings field used when env var is empty",
			configured: "https://hf-mirror.com",
			want:       "https://hf-mirror.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Always set the variable, even to "". Without this the empty-env
			// cases inherit the developer's or CI runner's environment, and the
			// people most likely to have HF_ENDPOINT exported are the mirror
			// users this feature exists for.
			t.Setenv(HuggingFaceEndpointEnvVar, tt.env)
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
		{
			name:       "ipv6 literal accepted",
			configured: "http://[2001:db8::1]:8080",
			want:       "http://[2001:db8::1]:8080",
		},
		{
			name:       "punycode host accepted",
			configured: "https://xn--e1afmkfd.xn--p1ai",
			want:       "https://xn--e1afmkfd.xn--p1ai",
		},
		{
			name:       "single dot path segment is not traversal",
			configured: "https://mirror.example.com/hf/./models",
			want:       "https://mirror.example.com/hf/./models",
		},
		{
			name:       "dot-dot inside a path segment is not traversal",
			configured: "https://mirror.example.com/hf..models",
			want:       "https://mirror.example.com/hf..models",
		},
		{
			// Regression guard: decoding the path a second time inside the
			// traversal check rejected this as a ".." segment, which both broke a
			// legitimate mirror and blamed a segment the value does not contain.
			name:       "literal percent in the path is not traversal",
			configured: "https://mirror.example.com/50%25off",
			want:       "https://mirror.example.com/50%25off",
		},
		{
			// Double-encoded, so the origin decodes it once to a literal "%2e%2e"
			// segment; it is not traversal and must not be rejected as such.
			name:       "double-encoded dot-dot is not traversal",
			configured: "https://mirror.example.com/hf/%252e%252e",
			want:       "https://mirror.example.com/hf/%252e%252e",
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
		{name: "opaque url", configured: "https:hf-mirror.com"},
		// Port with no hostname: url.Parse leaves Host non-empty (":8080"), but
		// Go's dialer resolves an empty host to the local machine, so accepting
		// this would silently point every download at this host.
		{name: "port with no hostname", configured: "https://:8080"},
		{name: "userinfo with port and no hostname", configured: "https://user@:8080"},
		// Credentials would prefix every download URL, be sent to the mirror,
		// and land in logs and support dumps.
		{name: "userinfo with password", configured: "https://user:hunter2@hf-mirror.com"},
		{name: "userinfo without password", configured: "https://user@hf-mirror.com"},
		// A bare "?" leaves RawQuery empty but sets ForceQuery, and String()
		// re-emits it, which would swallow the repo path into the query string.
		{name: "bare trailing question mark", configured: "https://hf-mirror.com?"},
		{name: "bare question mark after a path", configured: "https://mirror.example.com/hf?"},
		// A ".." in the prefix escapes it once the origin server normalizes the
		// joined path.
		{name: "dot-dot path segment", configured: "https://mirror.example.com/hf/.."},
		{name: "percent-encoded dot-dot", configured: "https://mirror.example.com/hf/%2e%2e"},
		{name: "dot-dot mid-path", configured: "https://mirror.example.com/a/../b"},
		// A non-ASCII host is percent-encoded rather than punycoded by String(),
		// so it could never resolve.
		{name: "non-ascii host", configured: "https://пример.рф"},
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

// TestNormalizeHuggingFaceEndpoint_ErrorsNeverEchoCredentials covers both
// rejection branches, not just the one that rejects userinfo by name. A value
// that fails url.Parse takes a different path, and url.Error formats as
// `parse %q: ...`, so an unscrubbed reason would carry the password into the
// warning log and into the settings validation warning.
func TestNormalizeHuggingFaceEndpoint_ErrorsNeverEchoCredentials(t *testing.T) {
	t.Parallel()

	const password = "hunter2"

	tests := []struct {
		name       string
		configured string
	}{
		// Rejected by the explicit userinfo check.
		{name: "parses cleanly", configured: "https://user:" + password + "@hf-mirror.com"},
		// Rejected by url.Parse itself, so the reason comes from url.Error.
		{name: "invalid port", configured: "https://user:" + password + "@hf-mirror.com:notaport"},
		{name: "invalid escape", configured: "https://user:" + password + "@hf-mirror.com/%zz"},
		{name: "control character", configured: "https://user:" + password + "@hf-mirror.com/\x7f"},
		// Empty username: privacy.ScrubCredentialURL's patterns require at least
		// one character before the colon, so scrubbing the url.Error string left
		// this shape untouched. Pinned so the reason can never go back to being
		// derived from the raw value.
		{name: "empty username with invalid port", configured: "https://:" + password + "@hf-mirror.com:notaport"},
		{name: "empty username parses cleanly", configured: "https://:" + password + "@hf-mirror.com"},
		// A literal "@" inside the password leaked its tail through the scrubber.
		{name: "at sign inside the password", configured: "https://user:p@" + password + "@hf-mirror.com:notaport"},
		// Shapes with no "://" at all. All are rejected, and all would be echoed
		// with credentials intact by a redactor that keys on the "://" separator.
		{name: "opaque url", configured: "https:user:" + password + "@hf-mirror.com"},
		{name: "scheme-less", configured: "user:" + password + "@hf-mirror.com"},
		{name: "scheme-relative", configured: "//user:" + password + "@hf-mirror.com"},
		{name: "unsupported scheme", configured: "ftp://user:" + password + "@hf-mirror.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := normalizeHuggingFaceEndpoint(tt.configured)
			require.Error(t, err, "a credential-bearing endpoint must be rejected")
			assert.NotContains(t, err.Error(), password,
				"the rejection reason is logged and embedded in a settings warning, so it must not echo the password")

			// The error context is reported to telemetry, so it is a second sink
			// and needs its own assertion; asserting on the message alone let a
			// context-scrubbing regression survive.
			var enhanced *errors.EnhancedError
			require.True(t, errors.As(err, &enhanced), "endpoint errors must be enhanced errors")
			for k, v := range enhanced.GetContext() {
				if s, ok := v.(string); ok {
					assert.NotContains(t, s, password,
						"error context %q is reported to telemetry and must not echo the password", k)
				}
			}
		})
	}
}

// TestValidateSettings_HuggingFaceEndpointWarning pins the load-time warning.
// It must land in Settings.ValidationWarnings, which main.go forwards to the
// notification centre, rather than in ValidateBirdNETSettings' warning list,
// which only reaches a Debug log line the user will never see.
func TestValidateSettings_HuggingFaceEndpointWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		endpoint    string
		wantWarning bool
	}{
		{name: "empty is not warned about", endpoint: "", wantWarning: false},
		{name: "whitespace is not warned about", endpoint: "   ", wantWarning: false},
		{name: "valid mirror is not warned about", endpoint: "https://hf-mirror.com", wantWarning: false},
		{name: "missing scheme warns", endpoint: "hf-mirror.com", wantWarning: true},
		{name: "credentials warn", endpoint: "https://user:pw@hf-mirror.com", wantWarning: true},
		{name: "hostless authority warns", endpoint: "https://:8080", wantWarning: true},
		{name: "unparseable value warns", endpoint: "https://user:pw@hf-mirror.com:notaport", wantWarning: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &Settings{}
			settings.BirdNET.HuggingFaceEndpoint = tt.endpoint
			// Errors are ignored: an empty Settings fails other validators, and
			// this test is only about the endpoint warning being recorded.
			_ = ValidateSettings(settings)

			var warned bool
			for _, w := range settings.ValidationWarnings {
				if strings.Contains(w, "model download endpoint") {
					warned = true
				}
			}
			assert.Equal(t, tt.wantWarning, warned, "warnings: %v", settings.ValidationWarnings)

			if tt.wantWarning {
				for _, w := range settings.ValidationWarnings {
					assert.NotContains(t, w, "pw",
						"a rejected endpoint's credentials must not reach the warning text")
				}
			}
		})
	}
}

// TestResolveHuggingFaceEndpoint_NeverEndsInSlash guards the contract callers
// rely on when they append "/" + path.
func TestResolveHuggingFaceEndpoint_NeverEndsInSlash(t *testing.T) {
	// Not parallel: the empty input reads HF_ENDPOINT, which must be pinned
	// rather than inherited from the environment running the test.

	t.Setenv(HuggingFaceEndpointEnvVar, "")

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

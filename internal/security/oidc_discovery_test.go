package security

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/openidConnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	testOIDCClientSecret = "oidc-client-secret-must-not-be-logged"
	testOIDCRedirectURI  = "http://localhost/auth/openid-connect/callback"
)

// splitLogReaders reads the two files a split-log test logger writes.
type splitLogReaders struct {
	main     func() string
	security func() string
}

// useSplitLogFiles installs a global logger that writes the base handler (the
// stand-in for console and main log) to application.log and the security module
// to its own security.log, so a test can tell which of the two an event reached.
// logtest.Capture* cannot be used: it disables every module output, which puts
// both loggers in one buffer and hides the routing. Not for parallel tests.
func useSplitLogFiles(t *testing.T) splitLogReaders {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "application.log")
	securityPath := filepath.Join(dir, "security.log")

	outputs := logger.DisabledModuleOutputs()
	outputs["security"] = logger.ModuleOutput{Enabled: true, FilePath: securityPath, Level: "info"}
	cl, err := logger.NewCentralLogger(&logger.LoggingConfig{
		DefaultLevel:  "info",
		Timezone:      "UTC",
		Console:       &logger.ConsoleOutput{Enabled: false},
		FileOutput:    &logger.FileOutput{Enabled: true, Path: mainPath, Level: "info"},
		ModuleOutputs: outputs,
	})
	require.NoError(t, err)
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() {
		logger.SetGlobal(prev)
		_ = cl.Close()
	})

	read := func(path string) func() string {
		return func() string {
			require.NoError(t, cl.Flush())
			data, err := os.ReadFile(path) //nolint:gosec // test-controlled path in t.TempDir
			if os.IsNotExist(err) {
				return ""
			}
			require.NoError(t, err)
			return string(data)
		}
	}
	return splitLogReaders{main: read(mainPath), security: read(securityPath)}
}

func TestOIDCDiscoveryFailure_ReachesMainLogAndIsRecorded(t *testing.T) {
	logs := useSplitLogFiles(t)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	t.Cleanup(clearOIDCDiscoveryFailure)
	enableOIDCRetries()
	t.Cleanup(cancelAllOIDCRetries)

	idp := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(idp.Close)

	initializeProviders(&conf.Settings{
		Security: conf.Security{
			Host:            "localhost",
			SessionSecret:   "test-secret-that-is-long-enough-32chars",
			SessionDuration: 24 * time.Hour,
			OAuthProviders: []conf.OAuthProviderConfig{{
				Provider:     ConfigOIDC,
				Enabled:      true,
				ClientID:     "test-client",
				ClientSecret: testOIDCClientSecret,
				IssuerURL:    idp.URL,
			}},
		},
	})

	discoveryURL := idp.URL + "/.well-known/openid-configuration"
	mainLog, securityLog := logs.main(), logs.security()
	for name, content := range map[string]string{"main log": mainLog, "security log": securityLog} {
		assert.Contains(t, content, "OIDC discovery failed", name)
		assert.Contains(t, content, discoveryURL, name)
		assert.Contains(t, content, "404", name)
		assert.NotContains(t, content, testOIDCClientSecret, name)
	}
	assert.Contains(t, mainLog, "security.oidc", "the mirrored event must name its module")
	assert.Contains(t, mainLog, "ERROR")

	err := ProviderRegistrationError(ProviderOIDC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), discoveryURL)
	assert.Contains(t, err.Error(), "404")
}

// TestOIDCDiscoveryFailure_RedactsURLUserinfo covers the two shapes of a URL in a
// net/http error: a transport failure masks the password but keeps the username,
// and a URL that does not parse is printed whole.
func TestOIDCDiscoveryFailure_RedactsURLUserinfo(t *testing.T) {
	const username = "tokenuser-must-not-be-logged"
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	tests := []struct {
		name     string
		issuer   string
		wantText string
	}{
		{name: "transport failure", issuer: strings.Replace(closedURL, "http://", "http://"+username+":secretpw@", 1), wantText: "connection refused"},
		{name: "unparseable URL", issuer: "http://" + username + ":secretpw@localhost:12x", wantText: "invalid port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := useSplitLogFiles(t)
			goth.ClearProviders()
			t.Cleanup(goth.ClearProviders)
			t.Cleanup(clearOIDCDiscoveryFailure)
			enableOIDCRetries()
			t.Cleanup(cancelAllOIDCRetries)

			initializeProviders(&conf.Settings{
				Security: conf.Security{
					Host:            "localhost",
					SessionSecret:   "test-secret-that-is-long-enough-32chars",
					SessionDuration: 24 * time.Hour,
					OAuthProviders: []conf.OAuthProviderConfig{{
						Provider:     ConfigOIDC,
						Enabled:      true,
						ClientID:     "test-client",
						ClientSecret: testOIDCClientSecret,
						IssuerURL:    tt.issuer,
					}},
				},
			})

			err := ProviderRegistrationError(ProviderOIDC)
			require.Error(t, err)
			var urlErr *url.Error
			require.ErrorAs(t, err, &urlErr, "the redacted text must still unwrap to the net/http error")
			for name, content := range map[string]string{
				"main log":     logs.main(),
				"security log": logs.security(),
				"recorded":     err.Error(),
			} {
				assert.Contains(t, content, tt.wantText, name)
				assert.NotContains(t, content, username, name)
				assert.NotContains(t, content, "secretpw", name)
			}
		})
	}
}

func TestOIDCRetryExhausted_ReachesMainLogAndSaysRestart(t *testing.T) {
	logs := useSplitLogFiles(t)

	const issuer = "https://idp.example.com/realms/home"
	logOIDCRetryExhausted(issuer, oidcDiscoveryURL(issuer), errors.NewStd("last discovery error text"))

	mainLog, securityLog := logs.main(), logs.security()
	for name, content := range map[string]string{"main log": mainLog, "security log": securityLog} {
		assert.Contains(t, content, issuer, name)
		assert.Contains(t, content, issuer+"/.well-known/openid-configuration", name)
		assert.Contains(t, content, "last discovery error text", name)
		assert.Contains(t, content, "restart", name)
		assert.NotContains(t, content, "Save settings", name)
	}
	assert.Contains(t, mainLog, "ERROR")
	assert.Contains(t, mainLog, "security.oidc")
}

func TestProviderRegistrationError_Transitions(t *testing.T) {
	const discoveryURL = "https://idp.example/.well-known/openid-configuration"
	cause := errors.NewStd("Non-success code for Discovery URL: 404")
	newerCause := errors.NewStd("dial tcp: no such host")

	registerOIDC := func() {
		idp := mockOIDCDiscovery(t)
		op, err := openidConnect.New("id", "secret", testOIDCRedirectURI, idp.URL+"/.well-known/openid-configuration", "openid")
		require.NoError(t, err)
		goth.UseProviders(newPKCEOIDCProvider(op))
	}

	tests := []struct {
		name       string
		steps      func()
		query      string
		wantErr    error    // the returned error must wrap this; nil means the provider is registered and no error is expected
		notErr     error    // the returned error must not wrap this
		wantText   string   // substring of the error text
		wantNoText []string // substrings that must not appear in the error text
	}{
		{
			name:    "nothing recorded, nothing registered",
			steps:   func() {},
			query:   ProviderOIDC,
			wantErr: errProviderNotRegistered,
		},
		{
			name:     "recorded failure wraps the cause and names the URL",
			steps:    func() { recordOIDCDiscoveryFailure(discoveryURL, cause) },
			query:    ProviderOIDC,
			wantErr:  cause,
			wantText: discoveryURL,
		},
		{
			name: "userinfo in the discovery URL is redacted",
			steps: func() {
				recordOIDCDiscoveryFailure("https://user:secretpw@idp.example/.well-known/openid-configuration", cause)
			},
			query:      ProviderOIDC,
			wantErr:    cause,
			wantNoText: []string{"secretpw", "user"},
		},
		{
			name: "later failure replaces the earlier one",
			steps: func() {
				recordOIDCDiscoveryFailure(discoveryURL, cause)
				recordOIDCDiscoveryFailure(discoveryURL, newerCause)
			},
			query:   ProviderOIDC,
			wantErr: newerCause,
			notErr:  cause,
		},
		{
			name: "registered provider is never reported as failed",
			steps: func() {
				recordOIDCDiscoveryFailure(discoveryURL, cause)
				registerOIDC()
			},
			query: ProviderOIDC,
		},
		{
			name:    "OIDC failure is not attributed to another provider",
			steps:   func() { recordOIDCDiscoveryFailure(discoveryURL, cause) },
			query:   ProviderGoogle,
			wantErr: errProviderNotRegistered,
			notErr:  cause,
		},
		{
			name: "cleared failure reports not registered",
			steps: func() {
				recordOIDCDiscoveryFailure(discoveryURL, cause)
				clearOIDCDiscoveryFailure()
			},
			query:   ProviderOIDC,
			wantErr: errProviderNotRegistered,
		},
		{
			name:  "registered non-OIDC provider",
			steps: func() { goth.UseProviders(github.New("id", "secret", "http://localhost/cb")) },
			query: ProviderGitHub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goth.ClearProviders()
			clearOIDCDiscoveryFailure()
			t.Cleanup(goth.ClearProviders)
			t.Cleanup(clearOIDCDiscoveryFailure)
			tt.steps()

			err := ProviderRegistrationError(tt.query)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
			if tt.notErr != nil {
				require.NotErrorIs(t, err, tt.notErr)
			}
			if tt.wantText != "" {
				assert.Contains(t, err.Error(), tt.wantText)
			}
			for _, absent := range tt.wantNoText {
				assert.NotContains(t, err.Error(), absent)
			}
		})
	}
}

func TestOIDCDiscoveryURL(t *testing.T) {
	tests := []struct{ name, issuer, want string }{
		{name: "without trailing slash", issuer: "https://idp.example/realms/home", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "with trailing slash", issuer: "https://idp.example/application/o/app/", want: "https://idp.example/application/o/app/.well-known/openid-configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, oidcDiscoveryURL(tt.issuer))
		})
	}
}

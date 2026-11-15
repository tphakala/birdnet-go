package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/markbates/goth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// mismatchWarning is the message logged when the discovery document reports a
// different issuer than the configured issuer URL.
const mismatchWarning = "OIDC discovery document reports a different issuer"

// newIssuerIdP starts an identity provider whose discovery document reports
// the issuer built by documentIssuer from the server URL.
func newIssuerIdP(t *testing.T, documentIssuer func(serverURL string) string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != oidcDiscoveryPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "` + documentIssuer(server.URL) + `",
			"authorization_endpoint": "` + server.URL + `/authorize",
			"token_endpoint": "` + server.URL + `/token",
			"userinfo_endpoint": "` + server.URL + `/userinfo",
			"jwks_uri": "` + server.URL + `/jwks"
		}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestInitializeProviders_OIDCIssuerComparison(t *testing.T) {
	tests := []struct {
		name             string
		documentIssuer   func(serverURL string) string
		configuredIssuer func(serverURL string) string
		wantWarning      bool
	}{
		{
			name:             "different issuer warns and still registers",
			documentIssuer:   func(string) string { return "https://public.example/realm" },
			configuredIssuer: func(u string) string { return u },
			wantWarning:      true,
		},
		{
			name:             "trailing slash difference is tolerated",
			documentIssuer:   func(u string) string { return u + "/" },
			configuredIssuer: func(u string) string { return u },
		},
		{
			name:             "pasted discovery URL matches the base issuer",
			documentIssuer:   func(u string) string { return u },
			configuredIssuer: func(u string) string { return u + oidcDiscoveryPath },
		},
		{
			name:             "pasted discovery URL with a query matches the base issuer",
			documentIssuer:   func(u string) string { return u },
			configuredIssuer: func(u string) string { return u + oidcDiscoveryPath + "/?appid=test-client" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := useSplitLogFiles(t)
			goth.ClearProviders()
			t.Cleanup(goth.ClearProviders)
			t.Cleanup(clearOIDCDiscoveryFailure)

			idp := newIssuerIdP(t, tt.documentIssuer)

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
						IssuerURL:    tt.configuredIssuer(idp.URL),
					}},
				},
			})

			_, err := goth.GetProvider(ProviderOIDC)
			require.NoError(t, err, "the provider must register whatever the issuer comparison says")

			for name, content := range map[string]string{"main log": logs.main(), "security log": logs.security()} {
				if tt.wantWarning {
					assert.Contains(t, content, mismatchWarning, name)
					assert.Contains(t, content, "WARN", name)
				} else {
					assert.NotContains(t, content, mismatchWarning, name)
				}
			}
		})
	}
}

func TestStartOIDCRetry_WarnsOnIssuerMismatch(t *testing.T) {
	logs := useSplitLogFiles(t)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	t.Cleanup(clearOIDCDiscoveryFailure)

	idp := newIssuerIdP(t, func(string) string { return "https://public.example/realm" })
	config := conf.OAuthProviderConfig{
		Provider:     ConfigOIDC,
		Enabled:      true,
		ClientID:     "test-client",
		ClientSecret: testOIDCClientSecret,
		IssuerURL:    idp.URL,
	}

	done := startOIDCRetry(t.Context(), config, "http://localhost/auth/openid-connect/callback", []string{"openid"}, errors.NewStd("initial discovery failure"))
	select {
	case <-done:
	case <-time.After(OIDCRetryInitialBackoff + 20*time.Second):
		t.Fatal("OIDC retry goroutine did not exit after registering the provider")
	}

	_, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err, "the provider must register whatever the issuer comparison says")
	assert.Contains(t, logs.main(), mismatchWarning)
	assert.Contains(t, logs.security(), mismatchWarning)
}

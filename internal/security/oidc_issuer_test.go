package security

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/markbates/goth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			t.Cleanup(clearOIDCDiscoveryFailure)

			idp := newIssuerIdP(t, tt.documentIssuer)

			// The discovery goroutine logs any mismatch before it exits, so
			// waiting for it makes the log assertions deterministic.
			waitDone(t, registerProviders(t, oidcSettings(tt.configuredIssuer(idp.URL))))

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

// TestStartOIDCDiscovery_WarnsOnIssuerMismatchAfterRetry pins the warning on a
// registration that succeeds only after a failed first attempt.
func TestStartOIDCDiscovery_WarnsOnIssuerMismatchAfterRetry(t *testing.T) {
	logs := useSplitLogFiles(t)
	t.Cleanup(clearOIDCDiscoveryFailure)

	var requests atomic.Int32
	mismatching := newIssuerIdP(t, func(string) string { return "https://public.example/realm" })
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		mismatching.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(idp.Close)

	ctx, gen := beginDiscoveryForTest(t)
	waitDone(t, startOIDCDiscovery(ctx, gen, testOIDCRegistration(idp.URL), testOIDCPolicy))

	require.GreaterOrEqual(t, requests.Load(), int32(2), "the first attempt failed, so a retry must have run")
	_, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err, "the provider must register whatever the issuer comparison says")
	assert.Contains(t, logs.main(), mismatchWarning)
	assert.Contains(t, logs.security(), mismatchWarning)
}

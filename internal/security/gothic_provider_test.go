package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/google"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGothicProviderNameUsesContextOnly pins that gothic resolves the provider
// from the handler's context value and from nothing the request can supply.
// Not parallel: it swaps gothic.Store and the goth registry.
func TestGothicProviderNameUsesContextOnly(t *testing.T) {
	prevStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte("test-session-secret-long-enough-32"))
	t.Cleanup(func() { gothic.Store = prevStore })

	// The default resolver only looks for session keys of registered providers.
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	goth.UseProviders(google.New("id", "secret", "http://localhost/auth/google/callback"))

	// A session that already holds a provider key, which the default resolver
	// treats as the chosen provider.
	primeRec := httptest.NewRecorder()
	primeReq := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	require.NoError(t, gothic.StoreInSession(ProviderGoogle, "state", primeReq, primeRec))

	oidc, empty := ProviderOIDC, ""
	tests := []struct {
		name    string
		target  string
		cookies []*http.Cookie
		ctx     *string
		want    string
		wantErr bool
	}{
		{name: "context value", target: "/", ctx: &oidc, want: ProviderOIDC},
		{name: "context value wins over query", target: "/?provider=google", ctx: &oidc, want: ProviderOIDC},
		{name: "empty context value", target: "/", ctx: &empty, wantErr: true},
		{name: "query only", target: "/?provider=google", wantErr: true},
		{name: "colon query only", target: "/?:provider=google", wantErr: true},
		{name: "session only", target: "/", cookies: primeRec.Result().Cookies(), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, http.NoBody)
			for _, cookie := range tt.cookies {
				req.AddCookie(cookie)
			}
			if tt.ctx != nil {
				req = gothic.GetContextWithProvider(req, *tt.ctx)
			}

			got, err := gothic.GetProviderName(req)

			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

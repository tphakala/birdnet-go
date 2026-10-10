package security

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/openidConnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	pkceTestClientID    = "pkce-test-client"
	pkceTestState       = "pkce-test-state"
	pkceTestAuthCode    = "pkce-test-code"
	pkceTestSubject     = "pkce-user-123"
	pkceTestCallbackURL = "http://localhost/auth/openid-connect/callback"
)

// pkceTestIdP is a minimal OIDC token endpoint that enforces PKCE S256: the
// token request only succeeds when code_verifier hashes to the expected challenge.
type pkceTestIdP struct {
	server *httptest.Server

	mu                sync.Mutex
	idToken           string
	expectedChallenge string
	receivedVerifier  string
}

func newPKCETestIdP(t *testing.T) *pkceTestIdP {
	t.Helper()
	idp := &pkceTestIdP{}
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		verifier := r.PostForm.Get(pkceParamCodeVerifier)

		idp.mu.Lock()
		idp.receivedVerifier = verifier
		expected := idp.expectedChallenge
		idToken := idp.idToken
		idp.mu.Unlock()

		if r.PostForm.Get("code") != pkceTestAuthCode || verifier == "" ||
			oauth2.S256ChallengeFromVerifier(verifier) != expected {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	}))
	t.Cleanup(idp.server.Close)

	// The issuer claim needs the server URL, so build the token after starting it.
	token := buildPKCETestIDToken(t, idp.server.URL)
	idp.mu.Lock()
	idp.idToken = token
	idp.mu.Unlock()
	return idp
}

// buildPKCETestIDToken builds an unsigned JWT with the claims goth validates.
func buildPKCETestIDToken(t *testing.T, issuer string) string {
	t.Helper()
	enc := base64.RawURLEncoding
	claims, err := json.Marshal(map[string]any{
		"iss": issuer,
		"aud": pkceTestClientID,
		"sub": pkceTestSubject,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(claims) + ".sig"
}

func (idp *pkceTestIdP) setExpectedChallenge(challenge string) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.expectedChallenge = challenge
}

func (idp *pkceTestIdP) lastVerifier() string {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	return idp.receivedVerifier
}

func newTestPKCEProvider(t *testing.T, idp *pkceTestIdP) *pkceOIDCProvider {
	t.Helper()
	base := idp.server.URL
	inner, err := openidConnect.NewCustomisedURL(
		pkceTestClientID, "secret", pkceTestCallbackURL,
		base+"/authorize", base+"/token", base, "", "",
		"openid",
	)
	require.NoError(t, err)
	return newPKCEOIDCProvider(inner)
}

// beginPKCEAuth starts a flow and returns the session and parsed auth URL query.
func beginPKCEAuth(t *testing.T, p *pkceOIDCProvider) (*pkceOIDCSession, url.Values) {
	t.Helper()
	sess, err := p.BeginAuth(pkceTestState)
	require.NoError(t, err)
	pkceSess, ok := sess.(*pkceOIDCSession)
	require.True(t, ok, "BeginAuth should return a PKCE session")

	rawURL, err := sess.GetAuthURL()
	require.NoError(t, err)
	authURL, err := url.Parse(rawURL)
	require.NoError(t, err)
	return pkceSess, authURL.Query()
}

func TestPKCEOIDCProvider_BeginAuthAddsS256Challenge(t *testing.T) {
	t.Parallel()
	p := newTestPKCEProvider(t, newPKCETestIdP(t))

	sess, query := beginPKCEAuth(t, p)

	require.NotEmpty(t, sess.CodeVerifier)
	assert.Equal(t, pkceChallengeMethodS256, query.Get(pkceParamCodeChallengeMethod))
	assert.Equal(t, oauth2.S256ChallengeFromVerifier(sess.CodeVerifier), query.Get(pkceParamCodeChallenge))
	assert.Empty(t, query.Get(pkceParamCodeVerifier), "verifier must never be sent in the auth URL")
	assert.Equal(t, pkceTestState, query.Get("state"), "original auth URL params must be preserved")
	assert.Equal(t, pkceTestClientID, query.Get("client_id"))

	other, _ := beginPKCEAuth(t, p)
	assert.NotEqual(t, sess.CodeVerifier, other.CodeVerifier, "each login must use a fresh verifier")
}

func TestPKCEOIDCProvider_SessionRoundTrip(t *testing.T) {
	t.Parallel()
	p := newTestPKCEProvider(t, newPKCETestIdP(t))
	sess, _ := beginPKCEAuth(t, p)

	restored, err := p.UnmarshalSession(sess.Marshal())
	require.NoError(t, err)
	restoredSess, ok := restored.(*pkceOIDCSession)
	require.True(t, ok)

	assert.Equal(t, sess.CodeVerifier, restoredSess.CodeVerifier)
	assert.Equal(t, sess.AuthURL, restoredSess.AuthURL)
}

func TestPKCEOIDCProvider_FullExchangeSendsStoredVerifier(t *testing.T) {
	t.Parallel()
	idp := newPKCETestIdP(t)
	p := newTestPKCEProvider(t, idp)

	sess, query := beginPKCEAuth(t, p)
	idp.setExpectedChallenge(query.Get(pkceParamCodeChallenge))

	// Simulate the gothic callback: the session is restored from storage and the
	// callback query carries an attacker-controlled code_verifier that must be ignored.
	restored, err := p.UnmarshalSession(sess.Marshal())
	require.NoError(t, err)
	params := url.Values{
		"code":                {pkceTestAuthCode},
		"state":               {pkceTestState},
		pkceParamCodeVerifier: {"attacker-supplied-verifier"},
	}

	_, err = restored.Authorize(p, params)
	require.NoError(t, err)
	assert.Equal(t, sess.CodeVerifier, idp.lastVerifier())

	user, err := p.FetchUser(restored)
	require.NoError(t, err)
	assert.Equal(t, pkceTestSubject, user.UserID)
}

func TestPKCEOIDCProvider_AuthorizeErrors(t *testing.T) {
	t.Parallel()
	idp := newPKCETestIdP(t)
	p := newTestPKCEProvider(t, idp)
	params := url.Values{"code": {pkceTestAuthCode}}

	t.Run("missing verifier", func(t *testing.T) {
		t.Parallel()
		// A session begun before PKCE was deployed has no verifier.
		legacy := &openidConnect.Session{AuthURL: "http://example/authorize?state=x"}
		restored, err := p.UnmarshalSession(legacy.Marshal())
		require.NoError(t, err)

		_, err = restored.Authorize(p, params)
		require.ErrorIs(t, err, errPKCEMissingVerifier)
	})

	t.Run("unexpected provider", func(t *testing.T) {
		t.Parallel()
		sess, _ := beginPKCEAuth(t, p)
		_, err := sess.Authorize(p.Provider, params)
		require.ErrorIs(t, err, errPKCEUnexpectedProvider)
	})

	t.Run("token endpoint rejects unverified challenge", func(t *testing.T) {
		t.Parallel()
		// The IdP was never told this flow's challenge, so the verifier fails
		// verification, proving the exchange depends on the PKCE check.
		sess, _ := beginPKCEAuth(t, p)
		_, err := sess.Authorize(p, params)
		require.Error(t, err)
	})
}

func TestPKCEOIDCProvider_FetchUserRejectsForeignSession(t *testing.T) {
	t.Parallel()
	p := newTestPKCEProvider(t, newPKCETestIdP(t))
	_, err := p.FetchUser(&openidConnect.Session{})
	require.ErrorIs(t, err, errPKCEUnexpectedSession)
}

func TestPKCEOIDCProvider_KeepsLogoutSupport(t *testing.T) {
	t.Parallel()
	var p goth.Provider = newTestPKCEProvider(t, newPKCETestIdP(t))
	_, ok := p.(goth.LogoutProvider)
	assert.True(t, ok, "PKCE wrapper must still support RP-initiated logout")
}

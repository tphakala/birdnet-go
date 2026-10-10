package security

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/openidConnect"
	"golang.org/x/oauth2"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// PKCE (RFC 7636) parameter names and the only challenge method we emit.
const (
	pkceParamCodeChallenge       = "code_challenge"
	pkceParamCodeChallengeMethod = "code_challenge_method"
	pkceParamCodeVerifier        = "code_verifier"
	pkceChallengeMethodS256      = "S256"
)

var (
	// errPKCEUnexpectedProvider is returned when a PKCE session is authorized
	// against a provider that is not the PKCE OIDC wrapper that created it.
	errPKCEUnexpectedProvider = errors.NewStd("pkce: session authorized with unexpected provider type")
	// errPKCEUnexpectedSession is returned when the PKCE provider is handed a
	// session it did not create.
	errPKCEUnexpectedSession = errors.NewStd("pkce: unexpected session type")
	// errPKCEMissingVerifier is returned when a stored session has no code
	// verifier, e.g. a login flow started before PKCE support was deployed.
	errPKCEMissingVerifier = errors.NewStd("pkce: session has no code verifier")
)

// pkceOIDCProvider wraps goth's OpenID Connect provider to add PKCE with the
// S256 challenge method to the authorization code flow.
//
// goth's openidConnect provider only accepts a code_verifier via the callback
// request parameters and never sends a code_challenge. This wrapper generates a
// fresh verifier per login in BeginAuth, adds the derived challenge to the
// authorization URL, and keeps the verifier inside the goth session, which
// gothic persists in the same session-store write as the state. On the
// callback the verifier is supplied to the token exchange from that session;
// any code_verifier present in the callback query is ignored.
//
// PKCE is always enabled: per RFC 6749 section 3.1 authorization servers must
// ignore unrecognized request parameters, so providers without PKCE support
// are unaffected, while providers that require it (OAuth 2.1, public clients,
// many IdPs' "require PKCE" toggles) now work.
//
// All other goth.Provider and goth.LogoutProvider methods are promoted from the
// embedded provider.
type pkceOIDCProvider struct {
	*openidConnect.Provider
}

// newPKCEOIDCProvider wraps an initialized OIDC provider with PKCE support.
func newPKCEOIDCProvider(p *openidConnect.Provider) *pkceOIDCProvider {
	return &pkceOIDCProvider{Provider: p}
}

// BeginAuth starts the flow with a new random verifier and adds its S256
// challenge to the authorization URL.
func (p *pkceOIDCProvider) BeginAuth(state string) (goth.Session, error) {
	sess, err := p.Provider.BeginAuth(state)
	if err != nil {
		return nil, err
	}
	inner, ok := sess.(*openidConnect.Session)
	if !ok {
		return nil, errPKCEUnexpectedSession
	}

	verifier := oauth2.GenerateVerifier()
	authURL, err := url.Parse(inner.AuthURL)
	if err != nil {
		return nil, fmt.Errorf("pkce: failed to parse authorization URL: %w", err)
	}
	query := authURL.Query()
	query.Set(pkceParamCodeChallenge, oauth2.S256ChallengeFromVerifier(verifier))
	query.Set(pkceParamCodeChallengeMethod, pkceChallengeMethodS256)
	authURL.RawQuery = query.Encode()
	inner.AuthURL = authURL.String()

	return &pkceOIDCSession{Session: inner, CodeVerifier: verifier}, nil
}

// UnmarshalSession restores a session produced by pkceOIDCSession.Marshal.
func (p *pkceOIDCProvider) UnmarshalSession(data string) (goth.Session, error) {
	sess := &pkceOIDCSession{Session: &openidConnect.Session{}}
	if err := json.Unmarshal([]byte(data), sess); err != nil {
		return nil, fmt.Errorf("pkce: failed to unmarshal session: %w", err)
	}
	return sess, nil
}

// FetchUser unwraps the PKCE session and delegates to the OIDC provider.
func (p *pkceOIDCProvider) FetchUser(session goth.Session) (goth.User, error) {
	sess, ok := session.(*pkceOIDCSession)
	if !ok {
		return goth.User{}, errPKCEUnexpectedSession
	}
	return p.Provider.FetchUser(sess.Session)
}

// pkceOIDCSession is an OIDC session that also carries the PKCE code verifier
// between BeginAuth and the callback. The embedded session's fields are
// flattened into the same JSON object when marshaled.
type pkceOIDCSession struct {
	*openidConnect.Session
	CodeVerifier string
}

// Marshal serializes the session including the code verifier.
func (s *pkceOIDCSession) Marshal() string {
	// Marshaling plain string and time fields cannot fail.
	b, _ := json.Marshal(s)
	return string(b)
}

// String returns the marshaled session.
func (s *pkceOIDCSession) String() string {
	return s.Marshal()
}

// Authorize exchanges the authorization code for tokens, sending the stored
// code verifier.
func (s *pkceOIDCSession) Authorize(provider goth.Provider, params goth.Params) (string, error) {
	p, ok := provider.(*pkceOIDCProvider)
	if !ok {
		return "", errPKCEUnexpectedProvider
	}
	if s.CodeVerifier == "" {
		return "", errPKCEMissingVerifier
	}
	return s.Session.Authorize(p.Provider, pkceParams{Params: params, verifier: s.CodeVerifier})
}

// pkceParams overrides the code_verifier callback parameter with the verifier
// stored in the session.
type pkceParams struct {
	goth.Params
	verifier string
}

// Get returns the stored verifier for code_verifier and delegates otherwise.
func (p pkceParams) Get(key string) string {
	if key == pkceParamCodeVerifier {
		return p.verifier
	}
	return p.Params.Get(key)
}

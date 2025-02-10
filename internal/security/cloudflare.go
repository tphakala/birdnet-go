package security

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

type CloudflareAccessClaims struct {
	Audience      []string `json:"aud"`
	Email         string   `json:"email"`
	ExpiresAt     int64    `json:"exp"`
	IssuedAt      int64    `json:"iat"`
	NotBefore     int64    `json:"nbf"`
	Issuer        string   `json:"iss"`
	Type          string   `json:"type"`
	IdentityNonce string   `json:"identity_nonce"`
	Subject       string   `json:"sub"`
	Country       string   `json:"country"`
}

type CloudflareAccess struct {
	certs      map[string]string
	teamDomain string
	audience   string
	certCache  struct {
		lastFetch time.Time
		mutex     sync.RWMutex
	}
	settings *conf.AllowCloudflareBypass
	debug    bool
}

func NewCloudflareAccess() *CloudflareAccess {
	settings := conf.GetSettings()
	debug := settings.Security.Debug
	cfBypass := settings.Security.AllowCloudflareBypass

	return &CloudflareAccess{
		certs:      make(map[string]string),
		teamDomain: cfBypass.TeamDomain,
		audience:   cfBypass.Audience,
		certCache: struct {
			lastFetch time.Time
			mutex     sync.RWMutex
		}{
			lastFetch: time.Time{},
		},
		settings: &cfBypass,
		debug:    debug,
	}
}

// fetchCertsIfNeeded fetches the certificates using a cache
func (ca *CloudflareAccess) fetchCertsIfNeeded(issuer string) error {
	ca.certCache.mutex.RLock()
	cacheAge := time.Since(ca.certCache.lastFetch)
	hasCache := len(ca.certs) > 0
	ca.certCache.mutex.RUnlock()

	// Refresh if cache is empty or older than 12 hours
	if !hasCache || cacheAge > 12*time.Hour {
		ca.certCache.mutex.Lock()
		defer ca.certCache.mutex.Unlock()

		// Double-check after acquiring lock
		if !hasCache || time.Since(ca.certCache.lastFetch) > 12*time.Hour {
			if err := ca.fetchCerts(issuer); err != nil {
				return err
			}
			ca.certCache.lastFetch = time.Now()
		}
	}
	return nil
}

// fetchCerts fetches the certificates from Cloudflare
func (ca *CloudflareAccess) fetchCerts(issuer string) error {
	certsURL := fmt.Sprintf("%s/cdn-cgi/access/certs", issuer)
	ca.Debug("Fetching Cloudflare certs from URL: %s", certsURL)

	resp, err := http.Get(certsURL)

	if err != nil {
		return fmt.Errorf("failed to fetch Cloudflare certs: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch Cloudflare certs: received status code %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	var certsResponse struct {
		PublicCerts []struct {
			Kid  string `json:"kid"`
			Cert string `json:"cert"`
		} `json:"public_certs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&certsResponse); err != nil {
		return fmt.Errorf("failed to decode certs response: %w", err)
	}

	// Lock before modifying the certs map
	ca.certCache.mutex.Lock()
	defer ca.certCache.mutex.Unlock()

	// Store the certificates with kids as keys
	for _, cert := range certsResponse.PublicCerts {
		ca.certs[cert.Kid] = cert.Cert
		ca.Debug("Added certificate with Kid: %s", cert.Kid)
	}

	return nil
}

// IsEnabled returns true if Cloudflare Access is enabled
func (ca *CloudflareAccess) IsEnabled(c echo.Context) bool {

	if !ca.settings.Enabled {
		ca.Debug("Cloudflare Access is disabled")
		return false
	}

	claims, err := ca.VerifyAccessJWT(c.Request())
	if err == nil && claims != nil {
		return true
	}
	return false
}

// VerifyAccessJWT verifies the JWT token and returns the claims
func (ca *CloudflareAccess) VerifyAccessJWT(r *http.Request) (*CloudflareAccessClaims, error) {
	jwtToken := r.Header.Get("Cf-Access-Jwt-Assertion")
	if jwtToken == "" {
		ca.Debug("No Cloudflare Access JWT found")
		return nil, fmt.Errorf("no Cloudflare Access JWT found")
	}

	// Parse the JWT without verifying to get claims and key ID
	parser := jwt.Parser{}
	claims := &CloudflareAccessClaims{}
	token, _, err := parser.ParseUnverified(jwtToken, claims)
	if err != nil {
		ca.Debug("Failed to parse JWT: %v", err)
		return nil, fmt.Errorf("failed to parse JWT: %w", err)
	}

	// Extract team domain from issuer URL
	if claims.Issuer != "" {
		parsedIssuer, err := url.Parse(claims.Issuer)
		if err != nil {
			ca.Debug("Invalid issuer URL: %v", err)
			return nil, fmt.Errorf("invalid issuer URL: %w", err)
		}
		ca.teamDomain = strings.Split(parsedIssuer.Hostname(), ".")[0]

		// Validate team domain if configured
		if ca.settings.TeamDomain != "" {
			if ca.teamDomain != ca.settings.TeamDomain {
				return nil, fmt.Errorf("team domain mismatch")
			}
		}
	}

	// Verify the JWT with the public key
	kid, ok := token.Header["kid"].(string)
	if !ok {
		ca.Debug("No key ID in JWT header")
		return nil, fmt.Errorf("no key ID in JWT header")
	}

	if err := ca.fetchCertsIfNeeded(claims.Issuer); err != nil {
		return nil, err
	}

	// Extract the public key from the certificate
	cert := ca.certs[kid]
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(cert))
	if err != nil {
		ca.Debug("Failed to parse public key: %v", err)
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	// Verify the JWT with the public key
	token, err = jwt.ParseWithClaims(jwtToken, claims, func(token *jwt.Token) (interface{}, error) {
		return pubKey, nil
	})

	if err != nil {
		ca.Debug("Invalid JWT: %v", err)
		return nil, fmt.Errorf("invalid JWT: %w", err)
	}

	if !token.Valid {
		ca.Debug("Token is not valid")
		return nil, fmt.Errorf("token is not valid")
	}

	if err := claims.Valid(); err != nil {
		ca.Debug("Invalid claims: %v", err)
		return nil, fmt.Errorf("invalid claims: %w", err)
	}

	// Validate audience if configured
	if ca.settings.Audience != "" {
		audienceValid := false
		for _, aud := range claims.Audience {
			if aud == ca.settings.Audience {
				audienceValid = true
				break
			}
		}
		if !audienceValid {
			return nil, fmt.Errorf("audience mismatch")
		}
	}

	if claims.Type != "app" {
		ca.Debug("Invalid token type: %s", claims.Type)
		return nil, fmt.Errorf("invalid token type: %s", claims.Type)
	}

	return claims, nil
}

// Needed to implement the jwt.Claims interface for CloudflareAccessClaims
func (c *CloudflareAccessClaims) GetExpirationTime() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.ExpiresAt, 0)), nil
}

func (c *CloudflareAccessClaims) GetIssuedAt() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.IssuedAt, 0)), nil
}

func (c *CloudflareAccessClaims) GetNotBefore() (*jwt.NumericDate, error) {
	return jwt.NewNumericDate(time.Unix(c.NotBefore, 0)), nil
}

func (c *CloudflareAccessClaims) GetIssuer() (string, error) {
	return c.Issuer, nil
}

func (c *CloudflareAccessClaims) GetSubject() (string, error) {
	return c.Subject, nil
}
func (c *CloudflareAccessClaims) GetAudience() (jwt.ClaimStrings, error) {
	return nil, nil
}

func (c *CloudflareAccessClaims) Valid() error {
	now := time.Now().Unix()
	if c.ExpiresAt < now {
		return fmt.Errorf("token expired")
	}
	if c.NotBefore > now {
		return fmt.Errorf("token not yet valid")
	}
	return nil
}

// Logout and remove cookie CF_Authorization
func (ca *CloudflareAccess) Logout(c echo.Context) error {
	c.SetCookie(&http.Cookie{
		Name:    "CF_Authorization",
		Value:   "",
		Expires: time.Now().Add(-time.Hour),
	})
	ca.Debug("Logged out from Cloudflare Access")

	// Redirect to GetLogoutURL
	return c.Redirect(http.StatusFound, ca.GetLogoutURL())
}

func (ca *CloudflareAccess) GetLogoutURL() string {
	return fmt.Sprintf("https://%s.cloudflareaccess.com/cdn-cgi/access/logout", ca.teamDomain)
}

func (ca *CloudflareAccess) Debug(format string, v ...interface{}) {
	if !ca.debug {
		prefix := "[security/cloudflare] "
		if len(v) == 0 {
			log.Print(prefix + format)
		} else {
			log.Printf(prefix+format, v...)
		}
	}
}

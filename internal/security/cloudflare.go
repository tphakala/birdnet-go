package security

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
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
	certCache  struct {
		lastFetch time.Time
		mutex     sync.RWMutex
	}
}

func NewCloudflareAccess() *CloudflareAccess {
	return &CloudflareAccess{
		certs: make(map[string]string),
		certCache: struct {
			lastFetch time.Time
			mutex     sync.RWMutex
		}{
			lastFetch: time.Time{},
		},
	}
}

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
	log.Printf("Fetching Cloudflare certs from URL: %s", certsURL)

	resp, err := http.Get(certsURL)
	if err != nil {
		return fmt.Errorf("failed to fetch Cloudflare certs: %w", err)
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

	for _, cert := range certsResponse.PublicCerts {
		ca.certs[cert.Kid] = cert.Cert
		log.Printf("Added certificate with Kid: %s", cert.Kid)
	}

	return nil
}

// IsEnabled returns true if Cloudflare Access is enabled
func (ca *CloudflareAccess) IsEnabled(c echo.Context) bool {
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
		log.Println("No Cloudflare Access JWT found")
		return nil, fmt.Errorf("no Cloudflare Access JWT found")
	}

	// Parse the JWT without verifying to get claims and key ID
	parser := jwt.Parser{}
	claims := &CloudflareAccessClaims{}
	token, _, err := parser.ParseUnverified(jwtToken, claims)
	if err != nil {
		log.Printf("Failed to parse JWT: %v", err)
		return nil, fmt.Errorf("failed to parse JWT: %w", err)
	}

	// Extract team domain from issuer URL
	if claims.Issuer != "" {
		parts := strings.Split(claims.Issuer, ".")
		if len(parts) > 0 {
			ca.teamDomain = strings.TrimPrefix(parts[0], "https://")
		}
	}

	// Verify the JWT with the public key
	kid, ok := token.Header["kid"].(string)
	if !ok {
		log.Println("No key ID in JWT header")
		return nil, fmt.Errorf("no key ID in JWT header")
	}

	if err := ca.fetchCertsIfNeeded(claims.Issuer); err != nil {
		return nil, err
	}

	// Extract the public key from the certificate
	cert := ca.certs[kid]
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(cert))
	if err != nil {
		log.Printf("Failed to parse public key: %v", err)
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	// Verify the JWT with the public key
	token, err = jwt.ParseWithClaims(jwtToken, claims, func(token *jwt.Token) (interface{}, error) {
		return pubKey, nil
	})

	if err != nil {
		log.Printf("Invalid JWT: %v", err)
		return nil, fmt.Errorf("invalid JWT: %w", err)
	}

	if !token.Valid {
		log.Println("Token is not valid")
		return nil, fmt.Errorf("token is not valid")
	}

	if err := claims.Valid(); err != nil {
		log.Printf("Invalid claims: %v", err)
		return nil, fmt.Errorf("invalid claims: %w", err)
	}

	now := time.Now().Unix()
	if claims.ExpiresAt < now {
		log.Println("Token expired")
		return nil, fmt.Errorf("token expired")
	}
	if claims.NotBefore > now {
		log.Println("Token not yet valid")
		return nil, fmt.Errorf("token not yet valid")
	}
	if claims.Type != "app" {
		log.Printf("Invalid token type: %s", claims.Type)
		return nil, fmt.Errorf("invalid token type: %s", claims.Type)
	}

	log.Println("Cloudflare Access JWT successfully verified")
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
	return nil
}

// Logout and remove cookie CF_Authorization
func (ca *CloudflareAccess) Logout(c echo.Context) error {
	c.SetCookie(&http.Cookie{
		Name:    "CF_Authorization",
		Value:   "",
		Expires: time.Now().Add(-time.Hour),
	})

	// Redirect to GetLogoutURL
	return c.Redirect(http.StatusFound, ca.GetLogoutURL())
}

func (ca *CloudflareAccess) GetLogoutURL() string {
	return fmt.Sprintf("https://%s.cloudflareaccess.com/cdn-cgi/access/logout", ca.teamDomain)
}

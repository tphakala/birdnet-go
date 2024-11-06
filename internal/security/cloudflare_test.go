package security

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	//"github.com/prometheus/common/server"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func BeforeEach(t *testing.T) (*httptest.Server, *CloudflareAccess) {

	// Add test certificates with proper PEM format
	certsJSON := `{
		"public_certs": [
			{
				"kid": "1234",
				"cert": "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0test1\n-----END PUBLIC KEY-----"
			},
			{
				"kid": "5678",
				"cert": "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0test2\n-----END PUBLIC KEY-----"
			}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/cdn-cgi/access/certs") {
			t.Errorf("Expected request to /cdn-cgi/access/certs, got %s", r.URL.Path)
		}
		fmt.Fprintln(w, certsJSON)
	}))

	// Set the settings instance
	conf.Setting()

	ca := NewCloudflareAccess()
	ca.debug = true
	ca.teamDomain = "test-team"
	ca.audience = "test-audience"

	ca.settings = &conf.AllowCloudflareBypass{
		Enabled:    true,
		TeamDomain: "test-team",
		Audience:   "test-audience",
	}

	return server, ca
}

func TestCloudflareAccessSuite(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*httptest.Server)
		test    func(*testing.T, *CloudflareAccess, *httptest.Server)
		wantErr bool
	}{
		{
			name: "successful certificate fetch",
			test: func(t *testing.T, ca *CloudflareAccess, server *httptest.Server) {
				err := ca.fetchCerts(server.URL)
				if err != nil {
					t.Fatalf("Expected no error, got %v", err)
				}
				if len(ca.certs) != 2 {
					t.Fatalf("Expected 2 certificates, got %d", len(ca.certs))
				}
			},
		},
		{
			name: "unsuccessful certificate fetch",
			test: func(t *testing.T, ca *CloudflareAccess, server *httptest.Server) {
				err := ca.fetchCerts("/invalid-url")
				if err == nil {
					t.Fatalf("Expected error, none")
				}
				if len(ca.certs) != 0 {
					t.Fatalf("Expected zero certificates, got %d", len(ca.certs))
				}
			},
		},
		// Add more test cases
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, ca := BeforeEach(t)
			defer server.Close()

			if tt.setup != nil {
				tt.setup(server)
			}

			tt.test(t, ca, server)
		})
	}
}

// TestFetchCertsSuccessProperlyUpdatesCertsMap tests the behavior of fetchCerts when the server returns a successful response
func TestFetchCertsSuccessProperlyUpdatesCertsMap(t *testing.T) {
	server, ca := BeforeEach(t)
	defer server.Close()

	err := ca.fetchCerts(server.URL)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(ca.certs) != 2 {
		t.Fatalf("Expected 2 certificates, got %d", len(ca.certs))
	}

	expectedCert1 := "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0test1\n-----END PUBLIC KEY-----"
	expectedCert2 := "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0test2\n-----END PUBLIC KEY-----"

	// Compare trimmed strings to handle whitespace differences
	if strings.TrimSpace(ca.certs["1234"]) != strings.TrimSpace(expectedCert1) ||
		strings.TrimSpace(ca.certs["5678"]) != strings.TrimSpace(expectedCert2) {
		t.Fatalf("Certificates not stored correctly")
	}
}

// TestFetchCertsInvalidJSONResponse tests the behavior of fetchCerts when the server returns invalid JSON
func TestFetchCertsInvalidJSONResponse(t *testing.T) {
	server, ca := BeforeEach(t)
	defer server.Close()

	// Override server handler for invalid JSON case
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `invalid JSON`)
	})

	err := ca.fetchCerts(server.URL)

	if err == nil {
		t.Fatalf("Expected an error, got nil")
	}

	expectedErrMsg := "failed to decode certs response"
	if !strings.Contains(err.Error(), expectedErrMsg) {
		t.Fatalf("Expected error message to contain '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

// TestFetchCertsError tests the behavior of fetchCerts when the server returns an error
func TestFetchCertsError(t *testing.T) {
	ca := &CloudflareAccess{certs: make(map[string]string)}

	err := ca.fetchCerts("malformed-url")

	if err == nil {
		t.Fatal("Expected an error, but got nil")
	}
}

// TestFetchCertsEmptyResponse tests the behavior of fetchCerts when the server returns an empty response
func TestFetchCertsEmptyResponse(t *testing.T) {
	server, ca := BeforeEach(t)
	defer server.Close()

	// Override server handler for empty response case
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{ "public_certs": [] }`)
	})

	err := ca.fetchCerts(server.URL)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(ca.certs) != 0 {
		t.Fatalf("Expected 0 certificates, got %d", len(ca.certs))
	}
}

// TestConcurrentAccessToCertsMap tests concurrent access to the certs map
func TestConcurrentAccessToCertsMap(t *testing.T) {
	// Setup test server
	certsJSON := `{
		"public_certs": [
			{"kid": "1234", "cert": "cert1"}
		]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, certsJSON)
	}))
	defer server.Close()

	ca := &CloudflareAccess{
		certs: make(map[string]string),
	}

	var wg sync.WaitGroup
	numRoutines := 10
	wg.Add(numRoutines)

	for i := 0; i < numRoutines; i++ {
		go func() {
			defer wg.Done()
			err := ca.fetchCerts(server.URL)
			if err != nil {
				t.Errorf("Error fetching certs: %v", err)
			}
		}()
	}

	wg.Wait()

	if len(ca.certs) != 1 {
		t.Errorf("Expected 1 certificate, got %d", len(ca.certs))
	}
}

func TestFetchCertsNon200Response(t *testing.T) {
	testCases := []struct {
		statusCode  int
		description string
	}{
		{http.StatusInternalServerError, "Internal Server Error"},
		{http.StatusNotFound, "Not Found"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			ca := &CloudflareAccess{certs: make(map[string]string)}
			err := ca.fetchCerts(server.URL)

			if err == nil {
				t.Fatalf("Expected an error for status code %d, but got nil", tc.statusCode)
			}

			if len(ca.certs) != 0 {
				t.Fatalf("Expected 0 certificates for status code %d, got %d", tc.statusCode, len(ca.certs))
			}
		})
	}
}

// TestFetchCertsExistingKeys tests the behavior of fetchCerts when the certs map already contains keys
func TestFetchCertsExistingKeys(t *testing.T) {
	certsJSON := `{
				"public_certs": [
					{"kid": "1234", "cert": "cert1"},
					{"kid": "5678", "cert": "cert2"}
				]
			}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, certsJSON)
	}))
	defer server.Close()

	// Prepare existing keys in the certs map
	existingKey := "existingKey"
	existingCert := "existingCert"
	ca := &CloudflareAccess{certs: map[string]string{existingKey: existingCert}}

	err := ca.fetchCerts(server.URL)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(ca.certs) != 3 {
		t.Fatalf("Expected 3 certificates, got %d", len(ca.certs))
	}

	if ca.certs["1234"] != "cert1" || ca.certs["5678"] != "cert2" || ca.certs[existingKey] != existingCert {
		t.Fatalf("Certificates not stored correctly")
	}
}

// TestFetchCertsLogging tests the logging functionality of the fetchCerts method
func TestFetchCertsLogging(t *testing.T) {
	certsJSON := `{
				"public_certs": [
					{"kid": "1234", "cert": "cert1"},
					{"kid": "5678", "cert": "cert2"}
				]
			}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, certsJSON)
	}))
	defer server.Close()

	var logs logWriter
	log.SetOutput(io.Discard)
	log.SetFlags(0)
	log.SetOutput(&logs)

	ca := &CloudflareAccess{certs: make(map[string]string)}
	err := ca.fetchCerts(server.URL)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedLogs := []string{
		fmt.Sprintf("Fetching Cloudflare certs from URL: %s/cdn-cgi/access/certs", server.URL),
		"Added certificate with Kid: 1234",
		"Added certificate with Kid: 5678",
	}

	for i, logMsg := range logs {
		if strings.TrimSpace(logMsg) != strings.TrimSpace(expectedLogs[i]) {
			t.Errorf("Log message mismatch. Expected: %s, Got: %s", expectedLogs[i], logMsg)
		}
	}
}

// Helper function to capture logs
type logWriter []string

func (l *logWriter) Write(p []byte) (n int, err error) {
	*l = append(*l, string(p))
	return len(p), nil
}

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestNewAutoTLSHTTPServerHandlesACMEChallengeBeforeRedirect(t *testing.T) {
	t.Parallel()

	server := &Server{
		echo: echo.New(),
		config: &Config{
			RedirectToHTTPS: true,
			ReadTimeout:     DefaultConfig().ReadTimeout,
			WriteTimeout:    DefaultConfig().WriteTimeout,
		},
	}

	httpServer := server.newAutoTLSHTTPServer(":80")

	redirectReq := httptest.NewRequest(http.MethodGet, "http://example.com/status", http.NoBody)
	redirectRec := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(redirectRec, redirectReq)

	if got, want := redirectRec.Code, http.StatusPermanentRedirect; got != want {
		t.Fatalf("non-challenge request status = %d, want %d", got, want)
	}
	if got, want := redirectRec.Header().Get("Location"), "https://example.com/status"; got != want {
		t.Fatalf("non-challenge redirect location = %q, want %q", got, want)
	}

	challengeReq := httptest.NewRequest(http.MethodGet, "http://example.com/.well-known/acme-challenge/missing", http.NoBody)
	challengeRec := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(challengeRec, challengeReq)

	if got, want := challengeRec.Code, http.StatusNotFound; got != want {
		t.Fatalf("challenge request status = %d, want %d", got, want)
	}
	if got := challengeRec.Header().Get("Location"); got != "" {
		t.Fatalf("challenge request unexpectedly redirected to %q", got)
	}
}

func TestNewAutoTLSHTTPServerReturnsNotFoundWhenRedirectDisabled(t *testing.T) {
	t.Parallel()

	server := &Server{
		echo: echo.New(),
		config: &Config{
			RedirectToHTTPS: false,
			ReadTimeout:     DefaultConfig().ReadTimeout,
			WriteTimeout:    DefaultConfig().WriteTimeout,
		},
	}

	httpServer := server.newAutoTLSHTTPServer(":80")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/status", http.NoBody)
	rec := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Fatalf("non-challenge request status = %d, want %d", got, want)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("non-challenge request unexpectedly redirected to %q", got)
	}
}

func TestNewAutoTLSHTTPServerServesHealthOverPlainHTTP(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.GET(healthCheckPath, func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"status": "healthy"})
	})

	server := &Server{
		echo: e,
		// RedirectToHTTPS is on so a plain request would normally 308; the health
		// endpoint must still be served directly so container healthchecks can
		// probe AutoTLS deployments on port 80.
		config: &Config{
			RedirectToHTTPS: true,
			ReadTimeout:     DefaultConfig().ReadTimeout,
			WriteTimeout:    DefaultConfig().WriteTimeout,
		},
	}

	httpServer := server.newAutoTLSHTTPServer(":80")
	req := httptest.NewRequest(http.MethodGet, "http://example.com"+healthCheckPath, http.NoBody)
	rec := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("health status = %d, want %d", got, want)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("health request unexpectedly redirected to %q", got)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"status":"healthy"`) {
		t.Fatalf("health body = %q, want it to contain healthy status", got)
	}
}

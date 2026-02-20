//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// setupNtfyContainerForAPI creates a no-auth ntfy container for API integration tests.
func setupNtfyContainerForAPI(t *testing.T) *containers.NtfyContainer {
	t.Helper()
	ctx := context.Background()
	c, err := containers.NewNtfyContainer(ctx, nil)
	require.NoError(t, err, "failed to start ntfy container")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

func TestCheckNtfyServer_RealContainer(t *testing.T) {
	container := setupNtfyContainerForAPI(t)
	ctx := context.Background()
	host := container.GetHost(ctx)

	t.Run("http_reachable", func(t *testing.T) {
		resp := probeNtfyServer(context.Background(), host)

		assert.Equal(t, "http", resp.Recommended, "container should be reachable via HTTP")
		assert.True(t, resp.HTTP, "HTTP should be true")
		assert.False(t, resp.HTTPS, "HTTPS should be false for plain HTTP container")
	})

	t.Run("wrong_port_unreachable", func(t *testing.T) {
		// Use host with port 1 which should be unreachable
		hostPart, _, err := net.SplitHostPort(container.GetHost(ctx))
		require.NoError(t, err, "should parse host:port")

		unreachableHost := fmt.Sprintf("%s:1", hostPart)
		resp := probeNtfyServer(context.Background(), unreachableHost)

		assert.Equal(t, "unreachable", resp.Recommended, "port 1 should be unreachable")
	})

	t.Run("handler_integration", func(t *testing.T) {
		// Test the full CheckNtfyServer handler via Echo context
		e := echo.New()
		ctrl := &Controller{}

		req := httptest.NewRequest(http.MethodGet,
			"/api/v2/notifications/check-ntfy-server?host="+host, http.NoBody)
		rec := httptest.NewRecorder()
		echoCtx := e.NewContext(req, rec)

		err := ctrl.CheckNtfyServer(echoCtx)
		require.NoError(t, err, "handler should not return error")
		assert.Equal(t, http.StatusOK, rec.Code, "should return 200 OK")

		var resp NtfyServerCheckResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "http", resp.Recommended, "should recommend HTTP")
		assert.True(t, resp.HTTP, "HTTP should be reachable")
		assert.False(t, resp.HTTPS, "HTTPS should not be reachable")
	})
}

package apitest

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestNewCoreWiresDefaults verifies NewCore returns a fully wired core with the
// isolation defaults: a mock datastore, an in-memory Echo, and a media SecureFS
// rooted under the per-test t.TempDir().
func TestNewCoreWiresDefaults(t *testing.T) {
	tmp := t.TempDir()

	core := NewCore(t, WithSettingsFunc(func(s *conf.Settings) {
		// Pin the export path so we can assert the SecureFS landed under it.
		s.Realtime.Audio.Export.Path = tmp
	}))

	require.NotNil(t, core)
	assert.NotNil(t, core.Echo, "Echo should be wired")
	assert.NotNil(t, core.DS, "datastore should default to a mock")
	assert.IsType(t, &mocks.MockInterface{}, core.DS, "default datastore should be a MockInterface")
	require.NotNil(t, core.SFS, "media SecureFS should be wired")
	assert.Equal(t, tmp, core.SFS.BaseDir(), "media SecureFS must be rooted under the test temp dir")
	assert.NotNil(t, core.SSEManager, "SSE manager should be wired")
	assert.NotNil(t, core.DetectionCache, "detection cache should be wired")
	assert.NotNil(t, core.Group, "v2 route group should be wired for domain route registration")

	// CurrentSettings should observe the published snapshot.
	got := core.CurrentSettings()
	require.NotNil(t, got)
	assert.Equal(t, tmp, got.Realtime.Audio.Export.Path)
}

// TestNewCoreGroupRegistersRoutes verifies the wired core.Group can register a
// route under the /api/v2 prefix and that AssertRoutesRegistered finds it. This
// exercises the route-registration path future domain tests rely on.
func TestNewCoreGroupRegistersRoutes(t *testing.T) {
	core := NewCore(t)
	require.NotNil(t, core.Group)

	core.Group.GET("/apitest/ping", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	AssertRoutesRegistered(t, core.Echo, []string{"GET /api/v2/apitest/ping"})
}

// TestNewCoreInjectedDatastore verifies WithDatastore wires a caller-provided
// datastore instead of creating a default mock.
func TestNewCoreInjectedDatastore(t *testing.T) {
	mockDS := mocks.NewMockInterface(t)
	core := NewCore(t, WithDatastore(mockDS))
	assert.Same(t, mockDS, core.DS, "WithDatastore should inject the provided datastore")
}

// TestNewCoreIsolatedExportPaths verifies two cores built in the same test get
// distinct media export directories, the property that keeps parallel binaries
// from colliding on a shared path.
func TestNewCoreIsolatedExportPaths(t *testing.T) {
	a := NewCore(t)
	b := NewCore(t)
	require.NotNil(t, a.SFS)
	require.NotNil(t, b.SFS)
	assert.NotEqual(t, a.SFS.BaseDir(), b.SFS.BaseDir(), "each core should get its own export dir")
}

// TestNewTestHTTPClientDisablesKeepAlives is a thin smoke test of the HTTP
// scaffolding so the helper stays exercised.
func TestNewTestHTTPClientDisablesKeepAlives(t *testing.T) {
	client := NewTestHTTPClient(TestResponseHeaderTimeout)
	require.NotNil(t, client)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.True(t, transport.DisableKeepAlives)
}

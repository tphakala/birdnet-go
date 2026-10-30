package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// newExternalMediaController builds a minimal Controller suitable for
// external-media endpoint tests, with the given environment getter and prober
// injected.
func newExternalMediaController(
	t *testing.T,
	envGetter sysinfo.EnvGetter,
	prober sysinfo.MountProber,
) (*echo.Echo, *Handler) {
	t.Helper()
	e := echo.New()
	c := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}, externalMediaEnv: envGetter, externalMediaProbe: prober}
	return e, c
}

// callExternalMediaEndpoint fires the GET /api/v2/system/external-media handler
// and returns the parsed response.
func callExternalMediaEndpoint(t *testing.T, ctrl *Handler) (resp ExternalMediaResponse, statusCode int) {
	t.Helper()
	e := ctrl.Echo
	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/external-media", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.GetExternalMedia(ctx)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp, rec.Code
}

// TestGetExternalMedia_Native tests state 1: running on bare metal (not containerized).
func TestGetExternalMedia_Native(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return "Bare Metal", "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: false, IsMountpoint: false, Readable: false}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Bare Metal", resp.Environment)
	assert.False(t, resp.Containerized)
	assert.Equal(t, sysinfo.DefaultExternalMountPath, resp.MountPath)
	assert.Nil(t, resp.Guidance, "guidance should be nil for native environments")
}

// TestGetExternalMedia_ContainerMountPresent tests state 2: container with the
// bind-mount wired up and accessible.
func TestGetExternalMedia_ContainerMountPresent(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return sysinfo.EnvDocker, "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: true, IsMountpoint: true, Readable: true}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, sysinfo.EnvDocker, resp.Environment)
	assert.True(t, resp.Containerized)
	assert.True(t, resp.MountPresent)
	assert.Nil(t, resp.Guidance, "guidance should be nil when mount is present")
}

// TestGetExternalMedia_ContainerMountUnreadable verifies that a mounted but
// unreadable path is reported as not present, with guidance populated.
func TestGetExternalMedia_ContainerMountUnreadable(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return sysinfo.EnvDocker, "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: true, IsMountpoint: true, Readable: false}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Containerized)
	assert.False(t, resp.MountPresent, "an unreadable mount must not be reported as present")
	require.NotNil(t, resp.Guidance, "guidance must be populated when the mount is not usable")
}

// TestGetExternalMedia_ContainerMountAbsent tests state 3: container without
// the bind-mount. Guidance must be populated for every container runtime: the
// common host setup commands plus the runtime's own volume instructions.
func TestGetExternalMedia_ContainerMountAbsent(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	tests := []struct {
		env string
		// markers are runtime-specific lines that must appear in the guidance.
		markers []string
	}{
		{sysinfo.EnvDocker, []string{"-v /mnt/birdnet-go/external:/external:rslave", "propagation: rslave"}},
		{sysinfo.EnvPodman, []string{"-v /mnt/birdnet-go/external:/external:rslave", "podman run"}},
		// LXC takes the generic branch shared with systemd-nspawn.
		{sysinfo.EnvLXC, []string{"volume mechanism"}},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Parallel()
			_, ctrl := newExternalMediaController(
				t,
				func() (string, string) { return tt.env, "" },
				func(_ string) sysinfo.MountProbeResult {
					return sysinfo.MountProbeResult{Exists: false, IsMountpoint: false, Readable: false}
				},
			)

			resp, code := callExternalMediaEndpoint(t, ctrl)

			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, tt.env, resp.Environment)
			assert.True(t, resp.Containerized)
			assert.False(t, resp.MountPresent)
			require.NotNil(t, resp.Guidance, "guidance must be populated when mount is absent in a container")
			assert.Equal(t, tt.env, resp.Guidance.Environment, "guidance.environment should identify the runtime")
			steps := strings.Join(resp.Guidance.Steps, "\n")
			for _, want := range []string{
				"mkdir -p /mnt/birdnet-go/external",
				"mount --bind",
				"make-rshared",
				`chown -h "${BIRDNET_UID:-1000}:${BIRDNET_GID:-1000}"`,
			} {
				assert.Contains(t, steps, want, "guidance must include the common host setup command")
			}
			for _, want := range tt.markers {
				assert.Contains(t, steps, want, "guidance must include the %s instructions", tt.env)
			}
		})
	}
}

// TestGetExternalMedia_ResponseShape verifies the JSON field names.
func TestGetExternalMedia_ResponseShape(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return sysinfo.EnvDocker, "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: true, IsMountpoint: true, Readable: true}
		},
	)

	e := ctrl.Echo
	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/external-media", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	require.NoError(t, ctrl.GetExternalMedia(ctx))

	// Decode into a raw map to verify JSON key names.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	assert.Contains(t, raw, "environment")
	assert.Contains(t, raw, "containerized")
	assert.Contains(t, raw, "mount_path")
	assert.Contains(t, raw, "mount_present")
	assert.Contains(t, raw, "guidance")
}

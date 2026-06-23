package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// newExternalMediaController builds a minimal Controller suitable for
// external-media endpoint tests, with the given environment getter and prober
// injected.
func newExternalMediaController(
	t *testing.T,
	envGetter sysinfo.EnvGetter,
	prober sysinfo.MountProber,
) (*echo.Echo, *Controller) {
	t.Helper()
	e := echo.New()
	c := &Controller{
		Echo:               e,
		Group:              e.Group("/api/v2"),
		externalMediaEnv:   envGetter,
		externalMediaProbe: prober,
	}
	return e, c
}

// callExternalMediaEndpoint fires the GET /api/v2/system/external-media handler
// and returns the parsed response.
func callExternalMediaEndpoint(t *testing.T, ctrl *Controller) (ExternalMediaResponse, int) {
	t.Helper()
	e := ctrl.Echo
	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/external-media", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := ctrl.GetExternalMedia(ctx)
	require.NoError(t, err)

	var resp ExternalMediaResponse
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
		func() (string, string) { return "Docker", "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: true, IsMountpoint: true, Readable: true}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Docker", resp.Environment)
	assert.True(t, resp.Containerized)
	assert.True(t, resp.MountPresent)
	assert.Nil(t, resp.Guidance, "guidance should be nil when mount is present")
}

// TestGetExternalMedia_ContainerMountAbsent tests state 3: container without
// the bind-mount (guidance must be populated).
func TestGetExternalMedia_ContainerMountAbsent(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return "Docker", "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: false, IsMountpoint: false, Readable: false}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Docker", resp.Environment)
	assert.True(t, resp.Containerized)
	assert.False(t, resp.MountPresent)
	require.NotNil(t, resp.Guidance, "guidance must be populated when mount is absent in a container")
	assert.NotEmpty(t, resp.Guidance.Environment, "guidance.environment should identify the runtime")
	assert.NotEmpty(t, resp.Guidance.Steps, "guidance.steps should contain setup instructions")
	// Verify the guidance steps contain required setup commands.
	steps := strings.Join(resp.Guidance.Steps, "\n")
	assert.Contains(t, steps, "mkdir -p /mnt/birdnet-go/external", "guidance must include mkdir step")
	assert.Contains(t, steps, "mount --bind", "guidance must include bind mount step")
	assert.Contains(t, steps, "make-rshared", "guidance must include make-rshared step")
	assert.Contains(t, steps, `chown -h "${BIRDNET_UID:-1000}:${BIRDNET_GID:-1000}"`, "guidance must include chown step with env vars")
	assert.Contains(t, steps, "-v /mnt/birdnet-go/external:/external:rslave", "guidance must include volume flag")
}

// TestGetExternalMedia_PodmanMountAbsent tests the Podman-specific guidance path.
func TestGetExternalMedia_PodmanMountAbsent(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return "Podman", "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: false, IsMountpoint: false, Readable: false}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Containerized)
	assert.False(t, resp.MountPresent)
	require.NotNil(t, resp.Guidance)
	assert.Equal(t, "Podman", resp.Guidance.Environment)
	assert.NotEmpty(t, resp.Guidance.Steps)
}

// TestGetExternalMedia_GenericContainerMountAbsent tests the default (generic)
// guidance branch for container runtimes other than Docker/Podman (e.g. LXC,
// systemd-nspawn).
func TestGetExternalMedia_GenericContainerMountAbsent(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return "LXC", "" },
		func(_ string) sysinfo.MountProbeResult {
			return sysinfo.MountProbeResult{Exists: false, IsMountpoint: false, Readable: false}
		},
	)

	resp, code := callExternalMediaEndpoint(t, ctrl)

	assert.Equal(t, http.StatusOK, code)
	assert.True(t, resp.Containerized)
	assert.False(t, resp.MountPresent)
	require.NotNil(t, resp.Guidance, "generic container must still receive guidance")
	assert.Equal(t, "LXC", resp.Guidance.Environment)
	// The generic branch still includes the common host setup commands.
	steps := strings.Join(resp.Guidance.Steps, "\n")
	assert.Contains(t, steps, "mkdir -p /mnt/birdnet-go/external")
	assert.Contains(t, steps, "mount --bind")
	assert.Contains(t, steps, "make-rshared")
	assert.Contains(t, steps, `chown -h "${BIRDNET_UID:-1000}:${BIRDNET_GID:-1000}"`)
}

// TestGetExternalMedia_ResponseShape verifies the JSON field names.
func TestGetExternalMedia_ResponseShape(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "unit")
	t.Attr("feature", "external-media")

	_, ctrl := newExternalMediaController(
		t,
		func() (string, string) { return "Docker", "" },
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

package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// ExternalMediaGuidance contains deployment-specific, copy-pasteable setup
// instructions returned when the external-media mount is absent. It is
// structured as machine-readable data so the frontend can render and localize it.
type ExternalMediaGuidance struct {
	// Environment is the detected container runtime (e.g. "Docker", "Podman").
	Environment string `json:"environment"`
	// Steps is an ordered list of setup steps. Each step is a plain string
	// (shell command or brief instruction) that is language-neutral and safe
	// to display verbatim or used as a key for frontend localization.
	Steps []string `json:"steps"`
}

// ExternalMediaResponse is the JSON body for GET /api/v2/system/external-media.
type ExternalMediaResponse struct {
	// Environment is the runtime environment type as returned by sysinfo.GetEnvironment
	// (e.g. "Docker", "Podman", "Bare Metal", "WSL2").
	Environment string `json:"environment"`
	// Containerized is true when the app is running inside a known container runtime.
	Containerized bool `json:"containerized"`
	// MountPath is the path inside the container that should be bind-mounted from the host.
	MountPath string `json:"mountPath"`
	// MountConfigured is true when the mount path exists and is a real mountpoint.
	MountConfigured bool `json:"mountConfigured"`
	// MountPresent is true when the mount path exists and is a real mountpoint.
	// It is always false for native (non-containerized) environments because
	// there is no bind-mount concept; the frontend should use Containerized to
	// decide whether to show mount-related UI.
	MountPresent bool `json:"mountPresent"`
	// Guidance is populated only when Containerized is true and MountPresent is
	// false. It provides copy-pasteable setup steps keyed to the detected runtime.
	// null when no setup action is needed.
	Guidance *ExternalMediaGuidance `json:"guidance"`
}

// GetExternalMedia handles GET /api/v2/system/external-media.
// It reports whether the external-media bind-mount is reachable from the
// running application, and when it is not, provides deployment-specific setup
// instructions for the detected container runtime.
func (c *Controller) GetExternalMedia(ctx echo.Context) error {
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting external media status")

	envGetter := c.externalMediaEnv
	if envGetter == nil {
		envGetter = sysinfo.GetEnvironment
	}
	prober := c.externalMediaProbe
	if prober == nil {
		prober = func(path string) sysinfo.MountProbeResult {
			return sysinfo.ProbeExternalMount(path, envGetter, nil)
		}
	}

	envType, _ := envGetter()
	containerized := sysinfo.IsContainerEnv(envType)
	probe := prober(sysinfo.DefaultExternalMountPath)

	mountPresent := containerized && probe.Exists && probe.IsMountpoint
	mountConfigured := mountPresent

	var guidance *ExternalMediaGuidance
	if containerized && !mountPresent {
		guidance = buildGuidance(envType)
	}

	resp := ExternalMediaResponse{
		Environment:     envType,
		Containerized:   containerized,
		MountPath:       sysinfo.DefaultExternalMountPath,
		MountConfigured: mountConfigured,
		MountPresent:    mountPresent,
		Guidance:        guidance,
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "External media status retrieved",
		logger.String("environment", envType),
		logger.Bool("containerized", containerized),
		logger.Bool("mountPresent", mountPresent),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// buildGuidance returns copy-pasteable setup instructions for the given
// container runtime. Commands match the A1 contract: host dir
// /mnt/birdnet-go/external, rslave bind-mount, chown to container UID, and
// runtime-specific volume flags.
func buildGuidance(envType string) *ExternalMediaGuidance {
	const (
		hostDir      = "/mnt/birdnet-go/external"
		containerDir = "/external"
		containerUID = "1000"
	)

	switch envType {
	case "Docker":
		return &ExternalMediaGuidance{
			Environment: envType,
			Steps: []string{
				"sudo mkdir -p " + hostDir,
				"sudo chown " + containerUID + ":" + containerUID + " " + hostDir,
				"# Add the following volume to your docker run command or compose file:",
				"-v " + hostDir + ":" + containerDir + ":rslave",
				"# Or in docker-compose.yml under the birdnet-go service volumes:",
				"- " + hostDir + ":" + containerDir + ":rslave",
				"# Then restart the container.",
			},
		}
	case "Podman":
		return &ExternalMediaGuidance{
			Environment: envType,
			Steps: []string{
				"sudo mkdir -p " + hostDir,
				"sudo chown " + containerUID + ":" + containerUID + " " + hostDir,
				"# Add the following volume to your podman run command or quadlet file:",
				"-v " + hostDir + ":" + containerDir + ":rslave",
				"# Then restart the container.",
			},
		}
	default:
		// Generic container guidance covers LXC, systemd-nspawn, and any
		// other container runtime the environment detection recognises.
		return &ExternalMediaGuidance{
			Environment: envType,
			Steps: []string{
				"sudo mkdir -p " + hostDir,
				"sudo chown " + containerUID + ":" + containerUID + " " + hostDir,
				"sudo mount --bind " + hostDir + " " + containerDir,
				"sudo mount --make-rshared " + containerDir,
				"# Or re-run the BirdNET-Go installer (install.sh) to configure the mount automatically.",
			},
		}
	}
}

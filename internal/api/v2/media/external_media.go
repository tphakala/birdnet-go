package media

import (
	"net/http"
	"slices"

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
	MountPath string `json:"mount_path"`
	// MountPresent is true when the mount path exists and is a real mountpoint.
	// It is always false for native (non-containerized) environments because
	// there is no bind-mount concept; the frontend should use Containerized to
	// decide whether to show mount-related UI.
	MountPresent bool `json:"mount_present"`
	// Guidance is populated only when Containerized is true and MountPresent is
	// false. It provides copy-pasteable setup steps keyed to the detected runtime.
	// null when no setup action is needed.
	Guidance *ExternalMediaGuidance `json:"guidance"`
}

// GetExternalMedia handles GET /api/v2/system/external-media.
// It reports whether the external-media bind-mount is reachable from the
// running application, and when it is not, provides deployment-specific setup
// instructions for the detected container runtime.
func (c *Handler) GetExternalMedia(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting external media status")

	envGetter := c.externalMediaEnv
	if envGetter == nil {
		envGetter = sysinfo.GetEnvironment
	}

	envType, _ := envGetter()
	containerized := sysinfo.IsContainerEnv(envType)

	prober := c.externalMediaProbe
	probe := sysinfo.ProbeExternalMount(sysinfo.DefaultExternalMountPath, prober)

	mountPresent := containerized && probe.Exists && probe.IsMountpoint && probe.Readable

	var guidance *ExternalMediaGuidance
	if containerized && !mountPresent {
		guidance = buildGuidance(envType)
	}

	resp := ExternalMediaResponse{
		Environment:   envType,
		Containerized: containerized,
		MountPath:     sysinfo.DefaultExternalMountPath,
		MountPresent:  mountPresent,
		Guidance:      guidance,
	}

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "External media status retrieved",
		logger.String("environment", envType),
		logger.Bool("containerized", containerized),
		logger.Bool("mountPresent", mountPresent),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// buildGuidance returns copy-pasteable setup instructions for the given
// container runtime. The commands set up the host mount: host dir
// /mnt/birdnet-go/external, a self bind-mount made rshared so sub-mounts
// propagate into the container, a chown to the container UID, and the
// runtime-specific volume flag.
func buildGuidance(envType string) *ExternalMediaGuidance {
	const (
		hostDir      = "/mnt/birdnet-go/external"
		containerDir = "/external"
	)

	// Common host-side setup steps required for all container runtimes.
	hostSetup := []string{
		"sudo mkdir -p " + hostDir,
		"sudo mount --bind " + hostDir + " " + hostDir,
		"sudo mount --make-rshared " + hostDir,
		`sudo chown -h "${BIRDNET_UID:-1000}:${BIRDNET_GID:-1000}" ` + hostDir,
	}

	switch envType {
	case sysinfo.EnvDocker:
		steps := slices.Clone(hostSetup)
		steps = append(steps,
			"# Add to your docker run command:",
			"-v "+hostDir+":"+containerDir+":rslave",
			"# Or in docker-compose.yml under the birdnet-go service volumes:",
			"volumes:",
			"  - type: bind",
			"    source: "+hostDir,
			"    target: "+containerDir,
			"    bind:",
			"      propagation: rslave",
			"# If you used install.sh, simply re-run the installer to wire this automatically.",
			"# Then restart the container.",
		)
		return &ExternalMediaGuidance{
			Environment: envType,
			Steps:       steps,
		}
	case sysinfo.EnvPodman:
		steps := slices.Clone(hostSetup)
		steps = append(steps,
			"# Add to your podman run command or quadlet file:",
			"-v "+hostDir+":"+containerDir+":rslave",
			"# If you used install.sh, simply re-run the installer to wire this automatically.",
			"# Then restart the container.",
		)
		return &ExternalMediaGuidance{
			Environment: envType,
			Steps:       steps,
		}
	default:
		// Generic container guidance covers LXC, systemd-nspawn, and any
		// other container runtime the environment detection recognises.
		steps := slices.Clone(hostSetup)
		steps = append(steps,
			"# Mount the host directory into the container using your runtime's volume mechanism.",
			"# Or re-run the BirdNET-Go installer (install.sh) to configure the mount automatically.",
		)
		return &ExternalMediaGuidance{
			Environment: envType,
			Steps:       steps,
		}
	}
}

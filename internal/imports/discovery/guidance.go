package discovery

import (
	"runtime"
	"slices"

	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// nativeLinuxEnv is the Environment label used for native (non-container) Linux
// guidance. It mirrors a typical sysinfo.GetEnvironment value for bare metal.
const nativeLinuxEnv = "Bare Metal"

// Guidance is environment-specific, copy-pasteable setup help shown when no
// importable database was found automatically. It is structured as data so the
// frontend can render and localize it.
type Guidance struct {
	// Environment is the detected runtime (e.g. "Docker", "Bare Metal").
	Environment string `json:"environment"`
	// Steps is an ordered list of plain instructions or shell commands.
	Steps []string `json:"steps"`
}

// BuildGuidance returns setup help for the given environment, or nil when none
// applies. runAsUser is the BirdNET-Go process user, used to make native
// permission hints concrete ("" if unknown).
func BuildGuidance(envType, runAsUser string) *Guidance {
	if sysinfo.IsContainerEnv(envType) {
		return buildContainerGuidance(envType)
	}
	// Native setup guidance is implemented for Linux only. macOS and Windows are
	// reserved: return no guidance rather than wrong, Linux-specific (lsblk/mount)
	// instructions. This matches SelectProvider, which only populates native
	// roots on Linux.
	if runtime.GOOS == osLinux {
		return buildNativeLinuxGuidance(runAsUser)
	}
	return nil
}

// buildNativeLinuxGuidance explains how to make BirdNET-Pi data reachable on a
// native Linux install: where the data usually lives, and how to connect and
// mount a USB stick or SD card.
func buildNativeLinuxGuidance(runAsUser string) *Guidance {
	user := runAsUser
	if user == "" {
		user = "birdnet"
	}
	steps := []string{
		"# If your BirdNET-Pi data is on this device, it is usually at:",
		"#   /home/<user>/BirdNET-Pi/birds.db",
		"# If it is on a USB stick or SD card, connect it and find it:",
		"lsblk -o NAME,SIZE,MOUNTPOINT,LABEL",
		"# If it is not mounted yet, mount it (replace sda1 with your device):",
		"sudo mkdir -p /mnt/usb",
		"sudo mount /dev/sda1 /mnt/usb",
		"# Then click Check again. BirdNET-Go runs as user '" + user + "';",
		"# if it cannot read the data, it will offer to copy it for you.",
	}
	return &Guidance{Environment: nativeLinuxEnv, Steps: steps}
}

// buildContainerGuidance returns the host-side bind-mount setup steps for a
// container runtime. The commands create the host external-media directory,
// make it rshared so sub-mounts propagate into the container, chown it to the
// container UID, and show the runtime-specific volume flag.
func buildContainerGuidance(envType string) *Guidance {
	const (
		hostDir      = "/mnt/birdnet-go/external"
		containerDir = "/external"
	)
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
			"# If you used install.sh, re-run the installer to wire this automatically.",
			"# Then restart the container.",
		)
		return &Guidance{Environment: envType, Steps: steps}
	case sysinfo.EnvPodman:
		steps := slices.Clone(hostSetup)
		steps = append(steps,
			"# Add to your podman run command or quadlet file:",
			"-v "+hostDir+":"+containerDir+":rslave",
			"# If you used install.sh, re-run the installer to wire this automatically.",
			"# Then restart the container.",
		)
		return &Guidance{Environment: envType, Steps: steps}
	default:
		steps := slices.Clone(hostSetup)
		steps = append(steps,
			"# Mount the host directory into the container using your runtime's volume mechanism.",
			"# Or re-run the BirdNET-Go installer (install.sh) to configure the mount automatically.",
		)
		return &Guidance{Environment: envType, Steps: steps}
	}
}

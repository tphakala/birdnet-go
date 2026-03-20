// Package sysinfo detects the runtime environment (container, VM, bare metal)
// and provides CPU architecture and model information.
package sysinfo

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v3/cpu"
)

// Common string constants to avoid repetition.
const (
	armFallback     = "arm"
	envDocker       = "Docker"
	envPodman       = "Podman"
	envLXC          = "LXC"
	envNspawn       = "systemd-nspawn"
	envContainerGen = "Container"
)

// Cached detection results — environment never changes at runtime.
var (
	cachedCPUArch   string
	cachedCPUModel  string
	cachedEnvType   string
	cachedEnvDetail string
	archOnce        sync.Once
	cpuOnce         sync.Once
	envOnce         sync.Once
)

// mapGOARCH converts Go's GOARCH values to conventional architecture names.
func mapGOARCH(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "x86"
	default:
		return goarch
	}
}

// detectARMVariant reads /proc/cpuinfo to distinguish armv6l from armv7l.
// Falls back to "arm" if detection fails.
func detectARMVariant(rootPath string) string {
	file, err := os.Open(filepath.Join(rootPath, "proc", "cpuinfo"))
	if err != nil {
		return armFallback
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CPU architecture") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				arch := strings.TrimSpace(parts[1])
				switch arch {
				case "7":
					return "armv7l"
				case "6":
					return "armv6l"
				case "5":
					return "armv5l"
				}
			}
		}
	}

	return armFallback
}

// GetCPUArch returns the human-readable CPU architecture name.
// For ARM 32-bit, it reads /proc/cpuinfo to distinguish armv6l/armv7l.
// Results are cached with sync.Once since architecture never changes at runtime.
func GetCPUArch() string {
	archOnce.Do(func() {
		if runtime.GOARCH == "arm" {
			cachedCPUArch = detectARMVariant("/")
		} else {
			cachedCPUArch = mapGOARCH(runtime.GOARCH)
		}
	})
	return cachedCPUArch
}

// GetCPUModel returns the CPU model name using gopsutil.
// Returns empty string if detection fails.
func GetCPUModel() string {
	cpuOnce.Do(func() {
		infos, err := cpu.Info()
		if err != nil || len(infos) == 0 {
			return
		}
		cachedCPUModel = infos[0].ModelName
	})
	return cachedCPUModel
}

// DetectEnvironment returns the runtime environment type and optional detail.
// rootPath allows overriding the filesystem root for testing (use "/" in production).
// On non-Linux systems, returns ("Native", "").
func DetectEnvironment(rootPath string) (envType, detail string) {
	if runtime.GOOS != "linux" {
		return "Native", ""
	}
	return detectLinuxEnvironment(rootPath)
}

// IsContainer reports whether the runtime environment is a container
// (Docker, Podman, LXC, systemd-nspawn, or generic container).
func IsContainer() bool {
	envType, _ := GetEnvironment()
	switch envType {
	case envDocker, envPodman, envLXC, envNspawn, envContainerGen:
		return true
	default:
		return false
	}
}

// GetEnvironment returns the cached environment detection result.
// Safe for concurrent use. Results are computed once on first call.
func GetEnvironment() (envType, detail string) {
	envOnce.Do(func() {
		cachedEnvType, cachedEnvDetail = DetectEnvironment("/")
	})
	return cachedEnvType, cachedEnvDetail
}

// detectLinuxEnvironment performs Linux-specific environment detection.
// Detection order: containers first, then WSL2, then VMs, then bare metal.
func detectLinuxEnvironment(root string) (envType, detail string) {
	// 1. Container detection — sentinel files
	if fileExists(filepath.Join(root, ".dockerenv")) {
		return envDocker, ""
	}
	if fileExists(filepath.Join(root, "run", ".containerenv")) {
		return envPodman, ""
	}

	// 2. Container detection — environment variable (only in production mode,
	// skipped during tests to avoid false positives when the test runner is in a container)
	if root == "/" {
		if containerEnv, exists := os.LookupEnv("container"); exists && containerEnv != "" {
			return mapContainerEnvVar(containerEnv)
		}
	}

	// 3. Container detection — cgroup contents
	if cgroupEnv := detectFromCgroup(filepath.Join(root, "proc", "self", "cgroup")); cgroupEnv != "" {
		return cgroupEnv, ""
	}

	// 4. Container detection — systemd container marker
	if systemdEnv, systemdDetail := detectFromSystemdContainer(filepath.Join(root, "run", "systemd", "container")); systemdEnv != "" {
		return systemdEnv, systemdDetail
	}

	// 5. WSL2 detection
	if isWSL2(filepath.Join(root, "proc", "version")) {
		return "WSL2", ""
	}

	// 6. VM/Hypervisor detection via DMI
	if dmiEnv, dmiDetail := detectFromDMI(root); dmiEnv != "" {
		return dmiEnv, dmiDetail
	}

	// 7. Hypervisor flag in cpuinfo
	if hasHypervisorFlag(filepath.Join(root, "proc", "cpuinfo")) {
		return "Virtual Machine", ""
	}

	return "Bare Metal", ""
}

// mapContainerEnvVar maps the "container" env var value to a known environment name.
func mapContainerEnvVar(value string) (envType, detail string) {
	switch strings.ToLower(value) {
	case "docker":
		return envDocker, ""
	case "podman":
		return envPodman, ""
	case "lxc":
		return envLXC, ""
	case envNspawn:
		return envNspawn, ""
	default:
		return envContainerGen, value
	}
}

// detectFromCgroup reads /proc/self/cgroup and looks for container runtime names.
func detectFromCgroup(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "docker") {
			return envDocker
		}
		if strings.Contains(line, "podman") {
			return envPodman
		}
		if strings.Contains(line, "lxc") {
			return envLXC
		}
	}
	return ""
}

// detectFromSystemdContainer reads /run/systemd/container for container type.
func detectFromSystemdContainer(path string) (envType, detail string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", ""
	}
	return mapContainerEnvVar(value)
}

// isWSL2 checks /proc/version for WSL2-specific kernel identifiers.
// Uses "microsoft-standard-wsl" to distinguish WSL2 from WSL1, which has
// a different kernel string (e.g., "Microsoft" without "standard-wsl").
func isWSL2(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := strings.ToLower(string(data))
	return strings.Contains(content, "microsoft-standard-wsl")
}

// detectFromDMI reads /sys/class/dmi/id/ files to identify hypervisors.
func detectFromDMI(root string) (envType, detail string) {
	dmiDir := filepath.Join(root, "sys", "class", "dmi", "id")
	vendor := readFileTrimmed(filepath.Join(dmiDir, "sys_vendor"))
	product := readFileTrimmed(filepath.Join(dmiDir, "product_name"))

	if vendor == "" {
		return "", ""
	}

	vendorLower := strings.ToLower(vendor)

	switch {
	case strings.Contains(vendorLower, "qemu") || strings.Contains(vendorLower, "kvm"):
		return "KVM", product
	case strings.Contains(vendorLower, "vmware"):
		return "VMware", product
	case strings.Contains(vendorLower, "microsoft") && strings.Contains(strings.ToLower(product), "virtual"):
		return "Hyper-V", product
	case strings.Contains(vendorLower, "innotek") || strings.Contains(vendorLower, "oracle"):
		return "VirtualBox", product
	case strings.Contains(vendorLower, "xen"):
		return "Xen", product
	case strings.Contains(vendorLower, "parallels"):
		return "Parallels", product
	}

	return "", ""
}

// hasHypervisorFlag checks the first CPU's flags in /proc/cpuinfo for the
// hypervisor flag. Only the first flags line is checked since the hypervisor
// flag is consistent across all cores on a given system.
func hasHypervisorFlag(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "flags") {
			return strings.Contains(line, " hypervisor")
		}
	}
	return false
}

// fileExists checks if a file exists at the given path.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readFileTrimmed reads a file and returns its trimmed content.
func readFileTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

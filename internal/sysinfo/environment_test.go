//go:build linux

package sysinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCPUArch(t *testing.T) {
	t.Parallel()
	arch := GetCPUArch()
	assert.NotEmpty(t, arch)
	switch runtime.GOARCH {
	case "amd64":
		assert.Equal(t, "x86_64", arch)
	case "arm64":
		assert.Equal(t, "aarch64", arch)
	case "386":
		assert.Equal(t, "x86", arch)
	default:
		assert.NotEmpty(t, arch)
	}
}

func TestMapGOARCH(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		goarch   string
		expected string
	}{
		{name: "amd64 maps to x86_64", goarch: "amd64", expected: "x86_64"},
		{name: "arm64 maps to aarch64", goarch: "arm64", expected: "aarch64"},
		{name: "386 maps to x86", goarch: "386", expected: "x86"},
		{name: "unknown passes through", goarch: "mips64", expected: "mips64"},
		{name: "riscv64 passes through", goarch: "riscv64", expected: "riscv64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := mapGOARCH(tt.goarch)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetectARMVariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cpuinfo  string
		expected string
	}{
		{name: "armv7l from cpuinfo", cpuinfo: "processor\t: 0\nmodel name\t: ARMv7 Processor\nCPU architecture: 7\n", expected: "armv7l"},
		{name: "armv6l from cpuinfo", cpuinfo: "processor\t: 0\nmodel name\t: ARMv6-compatible\nCPU architecture: 6\n", expected: "armv6l"},
		{name: "armv5l from cpuinfo", cpuinfo: "processor\t: 0\nmodel name\t: ARMv5 Processor\nCPU architecture: 5\n", expected: "armv5l"},
		{name: "missing cpuinfo falls back to arm", cpuinfo: "", expected: "arm"},
		{name: "no architecture field falls back to arm", cpuinfo: "processor\t: 0\nmodel name\t: ARM Processor\n", expected: "arm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tt.cpuinfo != "" {
				procDir := filepath.Join(root, "proc")
				require.NoError(t, os.MkdirAll(procDir, 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(procDir, "cpuinfo"), []byte(tt.cpuinfo), 0o644))
			}
			result := detectARMVariant(root)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetCPUModel(t *testing.T) {
	t.Parallel()
	model := GetCPUModel()
	t.Logf("Detected CPU model: %q", model)
}

func TestDetectEnvironment_Docker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".dockerenv"), []byte{}, 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Docker", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_Podman(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runDir := filepath.Join(root, "run")
	require.NoError(t, os.MkdirAll(runDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(runDir, ".containerenv"), []byte{}, 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Podman", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_KVM(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dmiDir := filepath.Join(root, "sys", "class", "dmi", "id")
	require.NoError(t, os.MkdirAll(dmiDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "sys_vendor"), []byte("QEMU\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "product_name"), []byte("Standard PC (Q35 + ICH9, 2009)\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "KVM", envType)
	assert.Equal(t, "Standard PC (Q35 + ICH9, 2009)", detail)
}

func TestDetectEnvironment_VMware(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dmiDir := filepath.Join(root, "sys", "class", "dmi", "id")
	require.NoError(t, os.MkdirAll(dmiDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "sys_vendor"), []byte("VMware, Inc.\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "product_name"), []byte("VMware Virtual Platform\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "VMware", envType)
	assert.Equal(t, "VMware Virtual Platform", detail)
}

func TestDetectEnvironment_HyperV(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dmiDir := filepath.Join(root, "sys", "class", "dmi", "id")
	require.NoError(t, os.MkdirAll(dmiDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "sys_vendor"), []byte("Microsoft Corporation\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "product_name"), []byte("Virtual Machine\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Hyper-V", envType)
	assert.Equal(t, "Virtual Machine", detail)
}

func TestDetectEnvironment_VirtualBox(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dmiDir := filepath.Join(root, "sys", "class", "dmi", "id")
	require.NoError(t, os.MkdirAll(dmiDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "sys_vendor"), []byte("innotek GmbH\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dmiDir, "product_name"), []byte("VirtualBox\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "VirtualBox", envType)
	assert.Equal(t, "VirtualBox", detail)
}

func TestDetectEnvironment_WSL2(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	procDir := filepath.Join(root, "proc")
	require.NoError(t, os.MkdirAll(procDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(procDir, "version"),
		[]byte("Linux version 5.15.90.1-microsoft-standard-WSL2 (root@1234) (gcc) #1 SMP\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "WSL2", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_BareMetal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Bare Metal", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_LXC(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	procDir := filepath.Join(root, "proc", "self")
	require.NoError(t, os.MkdirAll(procDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(procDir, "cgroup"),
		[]byte("12:memory:/lxc/container-id\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "LXC", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_CgroupDocker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	procDir := filepath.Join(root, "proc", "self")
	require.NoError(t, os.MkdirAll(procDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(procDir, "cgroup"),
		[]byte("0::/docker/abc123def456\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Docker", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_SystemdContainer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	systemdDir := filepath.Join(root, "run", "systemd")
	require.NoError(t, os.MkdirAll(systemdDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(systemdDir, "container"),
		[]byte("docker\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Docker", envType)
	assert.Empty(t, detail)
}

func TestDetectEnvironment_ContainerPrecedenceOverWSL2(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".dockerenv"), []byte{}, 0o644))
	procDir := filepath.Join(root, "proc")
	require.NoError(t, os.MkdirAll(procDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(procDir, "version"),
		[]byte("Linux version 5.15.90.1-microsoft-standard-WSL2\n"), 0o644))
	envType, _ := DetectEnvironment(root)
	assert.Equal(t, "Docker", envType)
}

func TestDetectEnvironment_HypervisorFlag(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	procDir := filepath.Join(root, "proc")
	require.NoError(t, os.MkdirAll(procDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(procDir, "cpuinfo"),
		[]byte("processor\t: 0\nflags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep hypervisor\n"), 0o644))
	envType, detail := DetectEnvironment(root)
	assert.Equal(t, "Virtual Machine", envType)
	assert.Empty(t, detail)
}

func TestMapContainerEnvVar(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		value          string
		expectedType   string
		expectedDetail string
	}{
		{name: "docker", value: "docker", expectedType: "Docker", expectedDetail: ""},
		{name: "podman", value: "podman", expectedType: "Podman", expectedDetail: ""},
		{name: "lxc", value: "lxc", expectedType: "LXC", expectedDetail: ""},
		{name: "systemd-nspawn", value: "systemd-nspawn", expectedType: "systemd-nspawn", expectedDetail: ""},
		{name: "unknown value", value: "custom-runtime", expectedType: "Container", expectedDetail: "custom-runtime"},
		{name: "case insensitive", value: "Docker", expectedType: "Docker", expectedDetail: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			envType, detail := mapContainerEnvVar(tt.value)
			assert.Equal(t, tt.expectedType, envType)
			assert.Equal(t, tt.expectedDetail, detail)
		})
	}
}

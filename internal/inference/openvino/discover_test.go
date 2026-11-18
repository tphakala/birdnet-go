package openvino

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMSIX2026_3  = "Intel.OpenVINOToolkit.2026.3.0_2026.3.0.0_x64__aqcv7fbxf4wm4"
	testMSIX2026_10 = "Intel.OpenVINOToolkit.2026.10.0_2026.10.0.0_x64__aqcv7fbxf4wm4"
	testMSIXArm     = "Intel.OpenVINOToolkit.2027.1.0_2027.1.0.0_arm64__aqcv7fbxf4wm4"
)

// discoveryEnv is a temp-dir sandbox with injectable lookups.
type discoveryEnv struct {
	t       *testing.T
	root    string
	env     map[string]string
	exeDir  string
	listErr map[string]bool
	reg     []installRoot
	regHits int
	deps    discoveryDeps
}

func newDiscoveryEnv(t *testing.T) *discoveryEnv {
	t.Helper()
	e := &discoveryEnv{t: t, root: t.TempDir(), env: map[string]string{}, listErr: map[string]bool{}}
	e.exeDir = filepath.Join(e.root, "app")
	require.NoError(t, os.MkdirAll(e.exeDir, 0o750))
	e.env[envProgramFiles] = filepath.Join(e.root, "pf")
	e.env[envProgramFilesX86] = filepath.Join(e.root, "pf86")
	e.deps = discoveryDeps{
		goos:   goosWindows,
		getenv: func(k string) string { return e.env[k] },
		executableDir: func() (string, error) {
			return e.exeDir, nil
		},
		stat: os.Stat,
		readDir: func(dir string) ([]string, error) {
			if e.listErr[dir] {
				return nil, os.ErrPermission
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(entries))
			for _, ent := range entries {
				names = append(names, ent.Name())
			}
			return names, nil
		},
		absPath: filepath.Abs,
		registryPackages: func() []installRoot {
			e.regHits++
			return e.reg
		},
	}
	return e
}

// touch creates an empty file and its parents.
func (e *discoveryEnv) touch(path string) string {
	e.t.Helper()
	require.NoError(e.t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(e.t, os.WriteFile(path, nil, 0o600))
	return path
}

// dll creates the runtime DLL under an install root and returns its path.
func (e *discoveryEnv) dll(installDir string) string {
	return e.touch(runtimeLibraryIn(installDir))
}

func (e *discoveryEnv) msixDir(name string) string {
	return filepath.Join(e.env[envProgramFiles], windowsAppsDir, name)
}

func (e *discoveryEnv) archiveDir(name string) string {
	return filepath.Join(e.env[envProgramFilesX86], intelDir, name)
}

func (e *discoveryEnv) resolve() (string, bool) {
	return resolveLibraryPath("", e.deps)
}

func TestResolveLibraryPath_PrecedenceAcrossSources(t *testing.T) {
	// Each step adds one more source, in increasing precedence, to a sandbox that
	// already holds the lower-precedence ones, so every adjacent pair is checked:
	// the higher source must win over the lower ones.
	e := newDiscoveryEnv(t)

	msix := e.dll(e.msixDir(testMSIX2026_3))
	got, discovered := e.resolve()
	assert.Equal(t, msix, got, "MSIX package is found when nothing else exists")
	assert.True(t, discovered)

	archive := e.dll(e.archiveDir("openvino_2026.1.0"))
	got, _ = e.resolve()
	assert.Equal(t, archive, got, "the Program Files (x86) install beats the MSIX package")

	setupvars := e.dll(filepath.Join(e.root, "setupvars-root"))
	e.env[envOpenVINODir] = filepath.Join(e.root, "setupvars-root")
	got, _ = e.resolve()
	assert.Equal(t, setupvars, got, "INTEL_OPENVINO_DIR beats the Program Files installs")

	pathDir := filepath.Join(e.root, "pathdir")
	pathDLL := e.touch(filepath.Join(pathDir, windowsLibraryName))
	e.env[envPath] = filepath.Join(e.root, "empty-a") + string(os.PathListSeparator) +
		"" + string(os.PathListSeparator) + pathDir
	got, _ = e.resolve()
	assert.Equal(t, pathDLL, got, "a later PATH entry that has the DLL beats INTEL_OPENVINO_DIR")

	exeDLL := e.touch(filepath.Join(e.exeDir, windowsLibraryName))
	got, _ = e.resolve()
	assert.Equal(t, exeDLL, got, "a DLL next to the executable beats PATH")
}

func TestResolveLibraryPath_SkipsRelativeSearchEntries(t *testing.T) {
	// A relative PATH or INTEL_OPENVINO_DIR entry would be resolved against the
	// working directory by the stat but loaded as a bare name by the loader, and
	// would stop discovery before a later valid install. Both are skipped.
	e := newDiscoveryEnv(t)
	valid := e.dll(e.msixDir(testMSIX2026_3))
	workDir := filepath.Join(e.root, "work")
	e.touch(filepath.Join(workDir, "bin", windowsLibraryName))
	e.touch(runtimeLibraryIn(filepath.Join(workDir, "ovroot")))
	t.Chdir(workDir)
	e.env[envPath] = "bin" + string(os.PathListSeparator) + "."
	e.env[envOpenVINODir] = "ovroot"

	got, discovered := e.resolve()

	assert.Equal(t, valid, got, "relative entries must not win over a later absolute install")
	assert.True(t, discovered)
}

func TestResolveLibraryPath_NewestVersionWinsNumerically(t *testing.T) {
	t.Run("msix", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		e.dll(e.msixDir(testMSIX2026_3))
		newest := e.dll(e.msixDir(testMSIX2026_10))
		e.dll(e.msixDir(testMSIXArm)) // newer, but not x64: must be skipped
		got, _ := e.resolve()
		assert.Equal(t, newest, got, "2026.10.0 is newer than 2026.3.0")
	})
	t.Run("msix package revision", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		e.dll(e.msixDir("Intel.OpenVINOToolkit.2026.3.0_2026.3.0.9_x64__aqcv7fbxf4wm4"))
		newest := e.dll(e.msixDir("Intel.OpenVINOToolkit.2026.3.0_2026.3.0.10_x64__aqcv7fbxf4wm4"))
		got, _ := e.resolve()
		assert.Equal(t, newest, got, "revision 10 is newer than revision 9")
	})
	t.Run("program files x86", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		e.dll(e.archiveDir("openvino_2026.3.0"))
		newest := e.dll(e.archiveDir("openvino_2026.10.0"))
		got, _ := e.resolve()
		assert.Equal(t, newest, got)
	})
	t.Run("a root without the DLL is skipped", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		older := e.dll(e.msixDir(testMSIX2026_3))
		require.NoError(t, os.MkdirAll(e.msixDir(testMSIX2026_10), 0o750))
		got, _ := e.resolve()
		assert.Equal(t, older, got)
	})
}

func TestResolveLibraryPath_InstallTransitions(t *testing.T) {
	e := newDiscoveryEnv(t)
	older := e.dll(e.msixDir(testMSIX2026_3))
	first, _ := e.resolve()
	second, _ := e.resolve()
	assert.Equal(t, older, first)
	assert.Equal(t, first, second, "the same call twice gives the same answer")

	newer := e.dll(e.msixDir(testMSIX2026_10))
	got, _ := e.resolve()
	assert.Equal(t, newer, got, "installing a newer package moves the pick to it")

	require.NoError(t, os.RemoveAll(e.msixDir(testMSIX2026_10)))
	got, _ = e.resolve()
	assert.Equal(t, older, got, "removing it returns the pick to the older one")
}

func TestResolveLibraryPath_RegistryFallback(t *testing.T) {
	t.Run("listing denied uses registry roots", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		store := filepath.Join(e.env[envProgramFiles], windowsAppsDir)
		root := filepath.Join(e.root, "elsewhere", testMSIX2026_3)
		want := e.dll(root)
		e.listErr[store] = true
		e.reg = []installRoot{{Name: testMSIX2026_3, Root: root}}
		got, discovered := e.resolve()
		assert.Equal(t, want, got)
		assert.True(t, discovered)
	})
	t.Run("a readable listing with no matching package uses registry roots", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		require.NoError(t, os.MkdirAll(filepath.Join(e.env[envProgramFiles], windowsAppsDir, "Unrelated.Package_1.0_x64__x"), 0o750))
		root := filepath.Join(e.root, "elsewhere", testMSIX2026_3)
		want := e.dll(root)
		e.reg = []installRoot{{Name: testMSIX2026_3, Root: root}}
		got, discovered := e.resolve()
		assert.Equal(t, want, got)
		assert.True(t, discovered)
	})
	t.Run("a listed package without the DLL uses registry roots", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		require.NoError(t, os.MkdirAll(e.msixDir(testMSIX2026_3), 0o750))
		root := filepath.Join(e.root, "elsewhere", testMSIX2026_3)
		want := e.dll(root)
		e.reg = []installRoot{{Name: testMSIX2026_3, Root: root}}
		got, discovered := e.resolve()
		assert.Equal(t, want, got)
		assert.True(t, discovered)
	})
	t.Run("a registry name without the x64 marker is ignored", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		root := filepath.Join(e.root, "elsewhere", testMSIXArm)
		e.dll(root)
		e.reg = []installRoot{{Name: testMSIXArm, Root: root}}
		got, discovered := e.resolve()
		assert.Empty(t, got)
		assert.False(t, discovered)
	})
	t.Run("a successful listing does not consult the registry", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		e.dll(e.msixDir(testMSIX2026_3))
		e.resolve()
		assert.Zero(t, e.regHits)
	})
}

func TestResolveLibraryPath_ConfiguredPath(t *testing.T) {
	t.Run("returned without touching any source", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		calls := 0
		e.deps.getenv = func(string) string { calls++; return "" }
		e.deps.stat = func(string) (os.FileInfo, error) { calls++; return nil, os.ErrNotExist }
		e.deps.readDir = func(string) ([]string, error) { calls++; return nil, os.ErrNotExist }
		e.deps.executableDir = func() (string, error) { calls++; return "", os.ErrNotExist }
		configured := filepath.Join(e.root, "pinned", windowsLibraryName)
		got, discovered := resolveLibraryPath(configured, e.deps)
		assert.Equal(t, configured, got, "a pinned path that does not exist is still used as given")
		assert.False(t, discovered)
		assert.Zero(t, calls)
		assert.Zero(t, e.regHits)
	})
	t.Run("relative path with a directory becomes absolute", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		got, discovered := resolveLibraryPath(filepath.Join("sub", windowsLibraryName), e.deps)
		assert.True(t, filepath.IsAbs(got))
		assert.False(t, discovered)
	})
	t.Run("bare file name stays bare", func(t *testing.T) {
		e := newDiscoveryEnv(t)
		got, discovered := resolveLibraryPath(windowsLibraryName, e.deps)
		assert.Equal(t, windowsLibraryName, got)
		assert.False(t, discovered)
	})
}

func TestResolveLibraryPath_NothingFound(t *testing.T) {
	e := newDiscoveryEnv(t)
	got, discovered := e.resolve()
	assert.Empty(t, got)
	assert.False(t, discovered)
}

func TestResolveLibraryPath_NonWindowsLeavesConfiguredUntouched(t *testing.T) {
	e := newDiscoveryEnv(t)
	e.dll(e.msixDir(testMSIX2026_3)) // would be found on Windows
	e.deps.goos = "linux"
	for _, configured := range []string{"", "libopenvino_c.so", "rel/libopenvino_c.so", "/opt/ov/libopenvino_c.so"} {
		got, discovered := resolveLibraryPath(configured, e.deps)
		assert.Equal(t, configured, got)
		assert.False(t, discovered)
	}
}

func TestTbbDirFor(t *testing.T) {
	e := newDiscoveryEnv(t)
	install := filepath.Join(e.root, "install")
	lib := e.dll(install)
	assert.Empty(t, tbbDirFor(lib, e.deps), "a flat layout needs no extra directory")

	tbb := filepath.Join(install, runtimeDir, thirdPartyDir, tbbDirName, binDir)
	require.NoError(t, os.MkdirAll(tbb, 0o750))
	assert.Equal(t, filepath.Clean(tbb), filepath.Clean(tbbDirFor(lib, e.deps)))
}

func TestCompareVersionedNames(t *testing.T) {
	assert.Positive(t, compareVersionedNames("openvino_2026.10.0", "openvino_2026.3.0"))
	assert.Negative(t, compareVersionedNames("openvino_2026.3", "openvino_2026.3.1"))
	assert.Zero(t, compareVersionedNames("openvino_2026.3.0", "openvino_2026.3.0"))
	assert.NotZero(t, compareVersionedNames("openvino_a2026.3.0", "openvino_b2026.3.0"), "ties break by name")
	assert.Positive(t, compareVersionedNames(
		"Intel.OpenVINOToolkit.2026.3.0_2026.3.0.10_x64__aqcv7fbxf4wm4",
		"Intel.OpenVINOToolkit.2026.3.0_2026.3.0.9_x64__aqcv7fbxf4wm4"),
		"the MSIX package revision compares numerically")
}

package openvino

import (
	"cmp"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// This file holds the pure OpenVINO runtime discovery used on Windows. It has no
// build tags and takes every environment lookup through discoveryDeps, so its
// tests run on any OS. On other operating systems the resolver returns the
// configured path unchanged and discovery never runs.

const (
	// goosWindows is the runtime.GOOS value that enables discovery.
	goosWindows = "windows"
	// windowsLibraryName is the OpenVINO C runtime file name on Windows.
	windowsLibraryName = "openvino_c.dll"

	// envPath is the executable search path, searched for the DLL for users who
	// ran Intel's setupvars script.
	envPath = "PATH"
	// envOpenVINODir is set by Intel's setupvars script to the install root.
	envOpenVINODir = "INTEL_OPENVINO_DIR"
	// envProgramFiles and envProgramFilesX86 name the program files roots.
	envProgramFiles    = "ProgramFiles"
	envProgramFilesX86 = "ProgramFiles(x86)"

	// windowsAppsDir is the MSIX package store under Program Files.
	windowsAppsDir = "WindowsApps"
	// intelDir is the vendor directory of the Intel archive install layout.
	intelDir = "Intel"
	// archiveDirPrefix is the (case-insensitive) prefix of the Intel archive
	// install directory names, such as openvino_2026.3.0.
	archiveDirPrefix = "openvino"
	// msixPackagePrefix and msixArchMarker identify the 64-bit OpenVINO Toolkit
	// MSIX package family, such as
	// Intel.OpenVINOToolkit.2026.3.0_2026.3.0.0_x64__aqcv7fbxf4wm4.
	msixPackagePrefix = "Intel.OpenVINOToolkit"
	msixArchMarker    = "_x64__"

	// Layout of an OpenVINO runtime tree: <root>\runtime\bin\intel64\Release.
	runtimeDir = "runtime"
	binDir     = "bin"
	archDir    = "intel64"
	releaseDir = "Release"
	// Layout of the bundled oneTBB: <root>\runtime\3rdparty\tbb\bin.
	thirdPartyDir = "3rdparty"
	tbbDirName    = "tbb"
)

// versionPattern matches the first dotted numeric run in a directory or package
// name, such as 2026.3.0 in Intel.OpenVINOToolkit.2026.3.0_2026.3.0.0_x64__x.
var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)*`)

// installRoot is one candidate install: the directory or package name that
// carries its version, and the root directory of the runtime tree.
type installRoot struct {
	Name string
	Root string
}

// discoveryDeps are the environment lookups discovery needs. Production wires
// the real ones; tests inject temp directories and fakes.
type discoveryDeps struct {
	// goos is the operating system name, as runtime.GOOS.
	goos string
	// getenv reads an environment variable.
	getenv func(string) string
	// executableDir returns the directory of the running executable.
	executableDir func() (string, error)
	// stat reports file information, as os.Stat.
	stat func(string) (fs.FileInfo, error)
	// readDir lists the entry names in a directory.
	readDir func(string) ([]string, error)
	// absPath makes a path absolute, as filepath.Abs.
	absPath func(string) (string, error)
	// registryPackages lists installed MSIX packages from the system package
	// repository registry key. It is the fallback for non-elevated processes,
	// which cannot list the WindowsApps directory. May be nil.
	registryPackages func() []installRoot
}

// fileExists reports whether path is an existing regular file.
func (d discoveryDeps) fileExists(path string) bool {
	info, err := d.stat(path)
	return err == nil && !info.IsDir()
}

// dirExists reports whether path is an existing directory.
func (d discoveryDeps) dirExists(path string) bool {
	info, err := d.stat(path)
	return err == nil && info.IsDir()
}

// runtimeLibraryIn returns the DLL path inside an OpenVINO install root.
func runtimeLibraryIn(root string) string {
	return filepath.Join(root, runtimeDir, binDir, archDir, releaseDir, windowsLibraryName)
}

// resolveLibraryPath decides which library the loader is given. A configured
// path is never replaced: on Windows a path with a directory part is made
// absolute (the loader requires a fully qualified path to add the DLL's own
// directory to the search), and a bare file name is left for the loader's own
// search. With nothing configured, non-Windows hosts get "" (a soname load) and
// Windows hosts get the first discovered install, or "" when none is found (a
// bare-name load that still finds a copy in System32). discovered is true only
// when the path came from the search.
func resolveLibraryPath(configured string, d discoveryDeps) (path string, discovered bool) {
	if d.goos != goosWindows {
		return configured, false
	}
	if configured != "" {
		if filepath.Base(configured) == configured {
			return configured, false
		}
		if abs, err := d.absPath(configured); err == nil {
			return abs, false
		}
		return configured, false
	}
	if found := discoverWindowsLibrary(d); found != "" {
		return found, true
	}
	return "", false
}

// discoverWindowsLibrary returns the first existing OpenVINO C runtime DLL in
// the documented search order, or "". Every candidate is an absolute path: a
// relative PATH or INTEL_OPENVINO_DIR entry is skipped.
func discoverWindowsLibrary(d discoveryDeps) string {
	if exeDir, err := d.executableDir(); err == nil {
		if p := filepath.Join(exeDir, windowsLibraryName); d.fileExists(p) {
			return p
		}
	}
	for _, dir := range filepath.SplitList(d.getenv(envPath)) {
		// A relative entry is resolved against the working directory by the
		// stat but loaded as a bare name by the loader, so it is skipped.
		if !filepath.IsAbs(dir) {
			continue
		}
		if p := filepath.Join(dir, windowsLibraryName); d.fileExists(p) {
			return p
		}
	}
	if root := d.getenv(envOpenVINODir); filepath.IsAbs(root) {
		if p := runtimeLibraryIn(root); d.fileExists(p) {
			return p
		}
	}
	if p := discoverArchiveInstall(d); p != "" {
		return p
	}
	return discoverMSIX(d)
}

// discoverArchiveInstall finds the newest Intel archive install under
// %ProgramFiles(x86)%\Intel\openvino*.
func discoverArchiveInstall(d discoveryDeps) string {
	pf := d.getenv(envProgramFilesX86)
	if pf == "" {
		return ""
	}
	intel := filepath.Join(pf, intelDir)
	names, err := d.readDir(intel)
	if err != nil {
		return ""
	}
	var roots []installRoot
	for _, name := range names {
		if strings.HasPrefix(strings.ToLower(name), archiveDirPrefix) {
			roots = append(roots, installRoot{Name: name, Root: filepath.Join(intel, name)})
		}
	}
	return newestLibrary(roots, d)
}

// discoverMSIX finds the newest 64-bit OpenVINO Toolkit MSIX package. It lists
// %ProgramFiles%\WindowsApps; when that fails or finds no matching package
// (non-elevated processes cannot list it), it falls back to the package
// repository registry key.
func discoverMSIX(d discoveryDeps) string {
	var roots []installRoot
	if pf := d.getenv(envProgramFiles); pf != "" {
		store := filepath.Join(pf, windowsAppsDir)
		if names, err := d.readDir(store); err == nil {
			for _, name := range names {
				if isOpenVINOMSIXName(name) {
					roots = append(roots, installRoot{Name: name, Root: filepath.Join(store, name)})
				}
			}
		}
	}
	if len(roots) == 0 && d.registryPackages != nil {
		for _, pkg := range d.registryPackages() {
			if isOpenVINOMSIXName(pkg.Name) && pkg.Root != "" {
				roots = append(roots, pkg)
			}
		}
	}
	return newestLibrary(roots, d)
}

// isOpenVINOMSIXName reports whether name is a 64-bit OpenVINO Toolkit package.
func isOpenVINOMSIXName(name string) bool {
	return strings.HasPrefix(name, msixPackagePrefix) && strings.Contains(name, msixArchMarker)
}

// newestLibrary returns the DLL of the newest root that contains one, or "".
// Roots without the DLL are skipped, not treated as the newest.
func newestLibrary(roots []installRoot, d discoveryDeps) string {
	sorted := slices.Clone(roots)
	slices.SortFunc(sorted, func(a, b installRoot) int {
		return compareVersionedNames(b.Name, a.Name)
	})
	for _, r := range sorted {
		if p := runtimeLibraryIn(r.Root); d.fileExists(p) {
			return p
		}
	}
	return ""
}

// compareVersionedNames orders two names by the first dotted numeric run in each,
// component by component as numbers (2026.10 is newer than 2026.3), then by name
// so the order is total and stable.
func compareVersionedNames(a, b string) int {
	if c := slices.Compare(parseVersion(a), parseVersion(b)); c != 0 {
		return c
	}
	return cmp.Compare(a, b)
}

// parseVersion extracts the first dotted numeric run of name as numbers. A name
// without one yields nil; a component too large for uint64 yields 0 for it.
func parseVersion(name string) []uint64 {
	run := versionPattern.FindString(name)
	if run == "" {
		return nil
	}
	parts := strings.Split(run, ".")
	out := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			n = 0
		}
		out[i] = n
	}
	return out
}

// tbbDirFor returns the bundled oneTBB directory that belongs to the runtime
// library at libraryPath, or "" when there is none (a flat layout, where the
// dependencies sit next to the DLL and need no extra search directory).
func tbbDirFor(libraryPath string, d discoveryDeps) string {
	dir := filepath.Join(filepath.Dir(libraryPath), "..", "..", "..", thirdPartyDir, tbbDirName, binDir)
	if d.dirExists(dir) {
		return dir
	}
	return ""
}

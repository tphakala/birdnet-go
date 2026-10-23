package inference

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ort "github.com/tphakala/birdnet-go/internal/inference/onnx"
)

// minORTMajor and minORTMinor are the oldest native ONNX Runtime release
// BirdNET-Go supports. The official binding negotiates the C API level down
// from 27 to 17 (cshim.h:8-9 in github.com/microsoft/onnxruntime/go at the
// pinned commit), so it no longer forces this floor. It is BirdNET-Go policy:
// the tested pin, and keeping the pre-1.27 KleidiAI memory leak out of reach.
// InitONNXRuntime enforces it after the library loads. Keep these in step with
// the Dependabot ignore rule and ort_pins_test.go.
const (
	minORTMajor = 1
	minORTMinor = 29

	// ortRestartHint is appended to the status of a rejected library: the
	// binding keeps the first loaded library, so a replacement only takes
	// effect after a restart.
	ortRestartHint = "; restart BirdNET-Go after installing a newer library"

	// ortOrNewerSuffix turns the minimum version into the user-facing
	// requirement text.
	ortOrNewerSuffix = " or newer"
)

// ORTInstallGuideURL is the install guide linked from ONNX Runtime errors.
const ORTInstallGuideURL = "https://github.com/tphakala/birdnet-go/wiki/ONNX-Runtime-Installation"

// ORTStatus describes the availability and version compatibility of the
// ONNX Runtime shared library on the current system.
type ORTStatus struct {
	// Available is true when a compatible ORT library is present.
	Available bool `json:"available"`
	// Initialized is true when the ORT environment has already been set up
	// (i.e., InitONNXRuntime succeeded at some point).
	Initialized bool `json:"initialized"`
	// Version holds the detected library version string (e.g. "1.31.0"),
	// or empty if the version could not be determined.
	Version string `json:"version,omitempty"`
	// LibraryPath is the resolved filesystem path to the shared library,
	// or empty if no library was found.
	LibraryPath string `json:"libraryPath,omitempty"`
	// Error describes why ORT is unavailable; empty when Available is true.
	Error string `json:"error,omitempty"`
}

// CheckORTAvailability probes for a usable ONNX Runtime installation.
// configuredPath is the user-configured library path (may be empty).
// The function never mutates global ORT state.
func CheckORTAvailability(configuredPath string) ORTStatus {
	// A library that loaded but is below the minimum cannot be replaced in
	// this process; report it until the process restarts.
	if rej := rejectedORT(); rej != nil {
		return ORTStatus{
			LibraryPath: rej.path,
			Version:     rej.version,
			Error:       versionError(rej.version) + ortRestartHint,
		}
	}

	// Fast path: ORT is already loaded and running. Trust the init even if
	// GetVersion returns empty (it succeeded, so the library is compatible).
	if IsORTInitialized() {
		version := ort.Version()
		status := ORTStatus{
			Available:   true,
			Initialized: true,
			Version:     version,
		}
		// A successful init already passed the version floor, so this only
		// guards odd version strings.
		if meets, known := ortVersionMeetsMinimum(version); known && !meets {
			status.Available = false
			status.Error = versionError(version)
		}
		return status
	}

	// ORT is not initialized; check whether the library file exists on disk.
	libPath := resolvedORTPath(configuredPath)
	if libPath == "" || !libraryFileExists(libPath) {
		return ORTStatus{
			Error: fmt.Sprintf("ONNX Runtime shared library not found; install guide: %s", ORTInstallGuideURL),
		}
	}

	version := inferVersionFromPath(libPath)
	status := ORTStatus{
		Available:   true,
		LibraryPath: libPath,
		Version:     version,
	}

	// Reject only a version known to be too old. Unknown covers unversioned
	// names (Windows, the macOS tarball, packaged release libraries) and a
	// resolved "libonnxruntime.so.1"; the real load verifies those and fails
	// with a readable error when the library is too old.
	if meets, known := ortVersionMeetsMinimum(version); known && !meets {
		status.Available = false
		status.Error = versionError(version)
	}

	return status
}

// ORTMinimumVersion returns the oldest supported ONNX Runtime release as
// "major.minor" (e.g. "1.29"), for use as a localized message parameter.
func ORTMinimumVersion() string {
	return fmt.Sprintf("%d.%d", minORTMajor, minORTMinor)
}

// ORTRequiredVersion returns a human-readable requirement for English text
// (e.g. "1.29 or newer").
func ORTRequiredVersion() string {
	return ORTMinimumVersion() + ortOrNewerSuffix
}

// resolvedORTPath returns the filesystem path to the ORT shared library.
// It uses configuredPath if non-empty, otherwise falls back to the
// package-level findONNXRuntimeLibrary search.
func resolvedORTPath(configuredPath string) string {
	if configuredPath != "" {
		return configuredPath
	}
	return findONNXRuntimeLibrary()
}

// libraryFileExists returns true when path refers to a real file on disk.
// Bare dlopen names such as "onnxruntime" or "libonnxruntime.so" without a
// directory component return false because they are search-order hints, not
// concrete paths.
func libraryFileExists(path string) bool {
	if path == "" {
		return false
	}
	// A bare filename (no directory separator) is a dlopen hint, not a real path.
	if !strings.Contains(path, string(os.PathSeparator)) && !strings.Contains(path, "/") {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// inferVersionFromPath tries to extract a semver-style version string from the
// library filename. It first resolves symlinks so that, for example,
// /usr/lib/libonnxruntime.so -> libonnxruntime.so.1.31.0 yields "1.31.0".
// Returns empty string if the version cannot be determined.
func inferVersionFromPath(libPath string) string {
	resolved, err := filepath.EvalSymlinks(libPath)
	if err != nil {
		resolved = libPath
	}

	base := filepath.Base(resolved)

	// Linux convention: "libonnxruntime.so.1.31.0" -> "1.31.0"
	if _, after, ok := strings.Cut(base, ".so."); ok {
		return after
	}

	// macOS convention: "libonnxruntime.1.31.0.dylib" -> "1.31.0"
	if before, ok := strings.CutSuffix(base, ".dylib"); ok {
		name := before
		for i := range len(name) {
			if name[i] == '.' && i+1 < len(name) && name[i+1] >= '0' && name[i+1] <= '9' {
				return name[i+1:]
			}
		}
	}

	return ""
}

// ortVersionMeetsMinimum compares version numerically with the supported
// minimum. known is false when version cannot be parsed (empty, "1", garbage);
// meets is then false too, and callers treat an unknown version optimistically.
func ortVersionMeetsMinimum(version string) (meets, known bool) {
	major, minor, ok := ort.ParseORTVersion(version)
	if !ok {
		return false, false
	}
	if major != minORTMajor {
		return major > minORTMajor, true
	}
	return minor >= minORTMinor, true
}

// versionError returns a human-readable error message when version is empty,
// unparseable or too old. Returns empty string for supported versions.
func versionError(version string) string {
	meets, known := ortVersionMeetsMinimum(version)
	if !known {
		return fmt.Sprintf("unable to determine ONNX Runtime version; required %s; install guide: %s", ORTRequiredVersion(), ORTInstallGuideURL)
	}
	if !meets {
		return fmt.Sprintf("ONNX Runtime version %s is older than the minimum supported version; required %s; install guide: %s", version, ORTRequiredVersion(), ORTInstallGuideURL)
	}
	return ""
}

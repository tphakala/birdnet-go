package inference

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ortlib "github.com/yalue/onnxruntime_go"
)

// RequiredORTAPIMajor is the version prefix required by the current
// yalue/onnxruntime_go binding. Models that depend on ONNX Runtime
// will not work unless the installed library version starts with this.
const RequiredORTAPIMajor = "1.25"

// ORTStatus describes the availability and version compatibility of the
// ONNX Runtime shared library on the current system.
type ORTStatus struct {
	// Available is true when a compatible ORT library is present.
	Available bool `json:"available"`
	// Initialized is true when the ORT environment has already been set up
	// (i.e., InitONNXRuntime succeeded at some point).
	Initialized bool `json:"initialized"`
	// Version holds the detected library version string (e.g. "1.25.1"),
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
	// Fast path: ORT is already loaded and running.
	if IsORTInitialized() {
		version := ortlib.GetVersion()
		status := ORTStatus{
			Initialized: true,
			Version:     version,
		}
		if msg := versionError(version); msg != "" {
			status.Error = msg
			return status
		}
		status.Available = true
		return status
	}

	// ORT is not initialized; check whether the library file exists on disk.
	libPath := resolvedORTPath(configuredPath)
	if libPath == "" || !libraryFileExists(libPath) {
		return ORTStatus{
			Error: "ONNX Runtime shared library not found",
		}
	}

	version := inferVersionFromPath(libPath)
	status := ORTStatus{
		LibraryPath: libPath,
		Version:     version,
	}

	if msg := versionError(version); msg != "" {
		status.Error = msg
		return status
	}

	status.Available = true
	return status
}

// ORTRequiredVersion returns a human-readable version string describing the
// required ONNX Runtime version (e.g. "1.25.x").
func ORTRequiredVersion() string {
	return RequiredORTAPIMajor + ".x"
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
// /usr/lib/libonnxruntime.so -> libonnxruntime.so.1.25.1 yields "1.25.1".
// Returns empty string if the version cannot be determined.
func inferVersionFromPath(libPath string) string {
	resolved, err := filepath.EvalSymlinks(libPath)
	if err != nil {
		resolved = libPath
	}

	base := filepath.Base(resolved)

	// Look for a version suffix after ".so." (Linux convention).
	// Example: "libonnxruntime.so.1.25.1" -> "1.25.1"
	if idx := strings.Index(base, ".so."); idx >= 0 {
		return base[idx+len(".so."):]
	}

	return ""
}

// isVersionCompatible checks whether version starts with RequiredORTAPIMajor
// followed by a dot (ensuring "1.25" matches "1.25.x" but not "1.250").
// Also accepts an exact major.minor match without a patch version.
func isVersionCompatible(version string) bool {
	if version == "" {
		return false
	}
	if version == RequiredORTAPIMajor {
		return true
	}
	return strings.HasPrefix(version, RequiredORTAPIMajor+".")
}

// versionError returns a human-readable error message when version is empty or
// incompatible. Returns empty string for compatible versions.
func versionError(version string) string {
	if version == "" {
		return fmt.Sprintf("unable to determine ONNX Runtime version; required %s", ORTRequiredVersion())
	}
	if !isVersionCompatible(version) {
		return fmt.Sprintf("ONNX Runtime version %s is incompatible; required %s", version, ORTRequiredVersion())
	}
	return ""
}

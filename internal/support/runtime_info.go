package support

import (
	"context"
	"path/filepath"
	"strings"
)

// runtimeInfoUnavailableReason is reported when no provider is wired, as in the
// CLI collector, which has no running instance to inspect.
const runtimeInfoUnavailableReason = "no running instance to inspect"

// builtInModelFile is reported as the model file of a model with no file on disk.
const builtInModelFile = "built-in"

// Values of the library_source field.
const (
	// LibrarySourceConfigured means the library path came from configuration.
	LibrarySourceConfigured = "configured"
	// LibrarySourceAuto means the library was found by the automatic search.
	LibrarySourceAuto = "auto"
	// LibrarySourceLoaderSearch means the dynamic loader resolved a bare name.
	LibrarySourceLoaderSearch = "loader_search"
)

// LibraryDirCustom is reported as the library directory when it is not one of
// the well-known system directories, so a home directory never ships.
const LibraryDirCustom = "custom"

// knownLibraryDirs are the system directories a library directory may be named
// by. Any other directory is reported as LibraryDirCustom.
var knownLibraryDirs = []string{
	"/usr/lib",
	"/usr/local/lib",
	"/usr/lib/aarch64-linux-gnu",
	"/usr/lib/x86_64-linux-gnu",
	"/usr/lib/arm-linux-gnueabihf",
	"/opt/homebrew/lib",
}

// SetRuntimeInfoProvider configures the inference runtime state provider.
func (c *Collector) SetRuntimeInfoProvider(provider RuntimeInfoProvider) {
	c.runtimeInfoProvider = provider
}

// collectRuntimeInfo returns the runtime info for the dump. Without a provider it
// returns an unavailable marker, so a reader can tell "not collected here" from
// "the dump predates this file".
func (c *Collector) collectRuntimeInfo(ctx context.Context) *RuntimeInfo {
	var info *RuntimeInfo
	if c.runtimeInfoProvider != nil {
		info = c.runtimeInfoProvider.CollectRuntimeInfo(ctx)
	}
	if info == nil {
		return &RuntimeInfo{Available: false, Reason: runtimeInfoUnavailableReason}
	}
	finalizeRuntimeInfo(info)
	return info
}

// finalizeRuntimeInfo converts the raw paths the provider filled into the shipped
// fields: a basename and a location class for each library, and a basename for
// each model file. The raw fields are cleared, so nothing downstream can ship them.
func finalizeRuntimeInfo(info *RuntimeInfo) {
	if ort := info.ONNXRuntime; ort != nil {
		ort.LibraryFile, ort.LibraryDir, ort.LibrarySource = libraryLocation(ort.LibraryPath, ort.LibraryConfigured)
		ort.LibraryPath, ort.LibraryConfigured = "", false
	}
	if ov := info.OpenVINO; ov != nil {
		ov.LibraryFile, ov.LibraryDir, ov.LibrarySource = libraryLocation(ov.LibraryPath, ov.LibraryPath != "")
		ov.LibraryPath = ""
	}
	for i := range info.Models {
		m := &info.Models[i]
		m.ModelFile = builtInModelFile
		if m.ModelPath != "" {
			m.ModelFile = filepath.Base(m.ModelPath)
		}
		m.ModelPath = ""
	}
}

// libraryLocation reduces a library path to what is safe to ship: the basename
// (after resolving symlinks, which gives the versioned file name), a directory
// class, and how the library was found. A bare name has no directory and is
// resolved by the dynamic loader. An empty path gives three empty strings.
func libraryLocation(raw string, configured bool) (file, dir, source string) {
	if raw == "" {
		return "", "", ""
	}
	if !strings.ContainsRune(raw, '/') && !strings.ContainsRune(raw, filepath.Separator) {
		// Never resolve a bare name: it would be read relative to the working directory.
		return raw, "", LibrarySourceLoaderSearch
	}
	resolved := raw
	if target, err := filepath.EvalSymlinks(raw); err == nil {
		resolved = target
	}
	file = filepath.Base(resolved)
	dir = LibraryDirCustom
	for _, known := range knownLibraryDirs {
		if filepath.Dir(raw) == filepath.FromSlash(known) {
			dir = known
			break
		}
	}
	source = LibrarySourceAuto
	if configured {
		source = LibrarySourceConfigured
	}
	return file, dir, source
}

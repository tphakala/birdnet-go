//go:build openvino && windows

package openvino

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// This file is the Windows loader: pure Go on golang.org/x/sys/windows, so the
// cgo preamble never includes <windows.h>. It never calls SetDefaultDllDirectories
// or SetDllDirectory: those change how every other DLL in the process is found.
// Instead each load passes its own LOAD_LIBRARY_SEARCH_* flags, and the one
// directory it adds (the bundled oneTBB) is removed again when the load or the
// symbol binding fails.

const (
	// loadFlagsAbsolute searches the DLL's own directory (its plugins and the
	// ONNX frontend sit there) and the default directories, which include any
	// directory added with AddDllDirectory.
	loadFlagsAbsolute = windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS
	// loadFlagsBareName searches only the default directories (application
	// directory, System32 and AddDllDirectory entries). The DLL_LOAD_DIR flag
	// requires a fully qualified path, so it cannot be used for a bare name.
	loadFlagsBareName = windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS

	// moduleNotFoundText is the fixed English text reported when the DLL or one
	// of its dependencies cannot be found. It is the text isLibraryAbsent
	// (internal/classifier) matches, independent of the Windows display language.
	moduleNotFoundText = "the specified module could not be found"

	// packageRepositoryKey lists the installed MSIX packages. Its subkeys are
	// readable by every account, unlike the WindowsApps directory.
	packageRepositoryKey = `SOFTWARE\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\PackageRepository\Packages`
	// packageRootValue is the value under a package subkey holding its root.
	packageRootValue = "Path"
)

var (
	// dllDirCookies maps a loaded handle to the AddDllDirectory cookie added for
	// it, so closeLibrary can remove it again.
	dllDirCookies   = map[uintptr]uintptr{}
	dllDirCookiesMu sync.Mutex
)

// openLibrary loads the OpenVINO C runtime. An absolute path is loaded with its
// own directory and the bundled oneTBB directory (when the standard layout has
// one) in the DLL search; a bare name, or "" for DefaultLibraryName, is loaded
// from the default directories only. The error text for a missing DLL is
// moduleNotFoundText.
func openLibrary(path string) (uintptr, error) {
	if path == "" {
		path = DefaultLibraryName
	}
	if !filepath.IsAbs(path) {
		h, err := windows.LoadLibraryEx(path, 0, loadFlagsBareName)
		if err != nil {
			return 0, loadError(err)
		}
		return uintptr(h), nil
	}

	var cookie uintptr
	if tbb := tbbDirFor(path, platformDiscoveryDeps()); tbb != "" {
		p, err := windows.UTF16PtrFromString(tbb)
		if err != nil {
			return 0, errors.NewStd(fmt.Sprintf("invalid oneTBB directory: %v", err))
		}
		// A failure to add the directory is not fatal: a runtime with a flat
		// layout, or one whose dependencies are already resolvable, still loads.
		if c, addErr := windows.AddDllDirectory(p); addErr == nil {
			cookie = c
		}
	}
	h, err := windows.LoadLibraryEx(path, 0, loadFlagsAbsolute)
	if err != nil {
		removeDllDirectory(cookie)
		return 0, loadError(err)
	}
	if cookie != 0 {
		dllDirCookiesMu.Lock()
		dllDirCookies[uintptr(h)] = cookie
		dllDirCookiesMu.Unlock()
	}
	return uintptr(h), nil
}

// loadError maps a LoadLibraryEx failure to the error InitOV reports. A missing
// module, file or path becomes the fixed English text; anything else keeps the
// Win32 error number and message.
func loadError(err error) error {
	if isModuleNotFound(err) {
		return errors.NewStd(moduleNotFoundText)
	}
	return errors.NewStd(fmt.Sprintf("LoadLibraryEx failed: %v", err))
}

// isModuleNotFound reports whether err is one of the Win32 errors for a DLL (or
// a dependency of it) that cannot be located.
func isModuleNotFound(err error) bool {
	return errors.Is(err, windows.ERROR_MOD_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

// lookupSymbol returns the address of the exported symbol name in the library
// handle. The error names the symbol.
func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	addr, err := windows.GetProcAddress(windows.Handle(handle), name)
	if err != nil {
		return 0, errors.NewStd(fmt.Sprintf("GetProcAddress failed for %s: %v", name, err))
	}
	return addr, nil
}

// closeLibrary releases a handle returned by openLibrary and removes the DLL
// directory added for it. bindLibrary calls it only when symbol resolution
// fails; a successfully bound library is never closed.
func closeLibrary(handle uintptr) {
	_ = windows.FreeLibrary(windows.Handle(handle))
	dllDirCookiesMu.Lock()
	cookie := dllDirCookies[handle]
	delete(dllDirCookies, handle)
	dllDirCookiesMu.Unlock()
	removeDllDirectory(cookie)
}

// removeDllDirectory removes a directory added with AddDllDirectory; a zero
// cookie means none was added.
func removeDllDirectory(cookie uintptr) {
	if cookie != 0 {
		_ = windows.RemoveDllDirectory(cookie)
	}
}

// platformDiscoveryDeps returns the real lookups for resolveLibraryPath and
// tbbDirFor.
func platformDiscoveryDeps() discoveryDeps {
	return discoveryDeps{
		goos:   runtime.GOOS,
		getenv: os.Getenv,
		executableDir: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.Dir(exe), nil
		},
		stat: os.Stat,
		readDir: func(dir string) ([]string, error) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return names, nil
		},
		absPath:          filepath.Abs,
		registryPackages: registryMSIXPackages,
	}
}

// registryMSIXPackages lists the OpenVINO Toolkit MSIX packages recorded in the
// system package repository, with their root directories. Any registry error
// yields the packages read so far, never an error: discovery then simply finds
// nothing here.
func registryMSIXPackages() []installRoot {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, packageRepositoryKey,
		registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer func() { _ = key.Close() }()
	names, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []installRoot
	for _, name := range names {
		if !isOpenVINOMSIXName(name) {
			continue
		}
		sub, err := registry.OpenKey(key, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		root, _, err := sub.GetStringValue(packageRootValue)
		_ = sub.Close()
		if err != nil {
			continue
		}
		out = append(out, installRoot{Name: name, Root: root})
	}
	return out
}

package discovery

import (
	"path/filepath"
	"runtime"

	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// osLinux is the runtime.GOOS value for Linux; native discovery and guidance are
// implemented for Linux only.
const osLinux = "linux"

// Root is a directory the scanner should search, with its display kind.
type Root struct {
	// Path is the absolute directory to search.
	Path string
	// Kind is the display classification for candidates found beneath it.
	Kind Kind
}

// LocationProvider yields the roots to scan for the current environment.
type LocationProvider interface {
	// Roots returns the directories to search.
	Roots() []Root
}

type staticProvider struct{ roots []Root }

func (p staticProvider) Roots() []Root { return p.roots }

// nativeLinuxLocalDirs are the fixed local directories that commonly hold a
// BirdNET-Pi install. home is expanded to <home>/BirdNET-Pi and <home>.
func nativeLinuxLocalDirs(home string) []string {
	dirs := []string{"/opt/birdnet-pi", "/root"}
	if home != "" {
		dirs = append([]string{filepath.Join(home, "BirdNET-Pi"), home}, dirs...)
	}
	return dirs
}

// nativeLinuxRemovableDirs are the parents under which removable media mounts.
var nativeLinuxRemovableDirs = []string{"/media", "/run/media", "/mnt"}

// SelectProvider returns the LocationProvider for the given runtime environment.
// home is the BirdNET-Go process user's home directory ("" if unknown).
func SelectProvider(envType, home string) LocationProvider {
	if sysinfo.IsContainerEnv(envType) {
		return staticProvider{roots: []Root{
			{Path: sysinfo.DefaultExternalMountPath, Kind: KindLocal},
		}}
	}
	if runtime.GOOS != osLinux {
		return staticProvider{}
	}
	// Deduplicate by cleaned path so an overlapping entry (e.g. home == /root,
	// which the default list also contains) is not walked twice.
	var roots []Root
	seen := make(map[string]struct{})
	addRoot := func(path string, kind Kind) {
		clean := filepath.Clean(path)
		if _, dup := seen[clean]; dup {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, Root{Path: clean, Kind: kind})
	}
	for _, d := range nativeLinuxLocalDirs(home) {
		addRoot(d, KindLocal)
	}
	for _, d := range nativeLinuxRemovableDirs {
		addRoot(d, KindRemovable)
	}
	return staticProvider{roots: roots}
}

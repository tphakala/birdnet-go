//go:build !windows

package openvino

// DefaultLibraryName is the soname the backend loads when no library path is
// configured. It is the fallback in openLibrary (loader_unix.go) and is used
// only to report which library a path-less load resolved to.
const DefaultLibraryName = "libopenvino_c.so"

//go:build windows

package openvino

// DefaultLibraryName is the file name the backend loads when no library path is
// configured and automatic discovery finds nothing. It is a bare name, so the
// loader searches the standard directories (application directory, System32).
// It is also used to report which library a path-less load resolved to.
const DefaultLibraryName = windowsLibraryName

//go:build unix

package hwprofile

import "golang.org/x/sys/unix"

// canAccessReadWrite reports whether this process may open path for reading and
// writing, without opening it.
//
// Opening a DRM render node would answer the same question, but it is a real
// device open: on a host with runtime power management it resumes a suspended
// discrete GPU, which is a poor trade for drawing a diagnostics panel on an
// always-on machine. It would also run on every developer's box during
// `go test ./...`. Faccessat asks the kernel the permission question directly.
//
// AT_EACCESS makes the check use the effective UID and GID, which is what
// actually governs the open; the default would test the real IDs and give the
// wrong answer under setuid or under a container that drops privileges after
// start.
func canAccessReadWrite(path string) bool {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.R_OK|unix.W_OK, unix.AT_EACCESS) == nil
}

//go:build linux

package staging

import (
	"os"
	"syscall"
)

// openNoFollow opens path read-only, failing if the final component is a
// symlink. This re-validates the source after the unprivileged API-side check,
// closing the TOCTOU window where a symlink to a sensitive file (e.g.
// /etc/shadow) is swapped in before the root subcommand reads it.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// chownTo changes ownership of path to uid:gid.
func chownTo(path string, uid, gid int) error {
	return os.Chown(path, uid, gid)
}

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

// createNoFollow creates path for writing, failing if it already exists
// (O_EXCL) or is a symlink (O_NOFOLLOW). The destination directory is owned by
// the unprivileged service user, so without these flags the service user could
// pre-plant dst/birds.db as a symlink to an arbitrary path (e.g. /etc/cron.d)
// and have this root process write through it. O_EXCL also rejects a file the
// attacker raced in ahead of us.
func createNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
}

// chownTo changes ownership of path to uid:gid WITHOUT dereferencing symlinks.
// Lchown (not Chown) is essential here: the staging directory is owned by the
// unprivileged service user, who could swap a staged regular file for a symlink
// to a sensitive file (e.g. /etc/shadow) between the copy and this chown. Chown
// would follow it and hand that file to the service user (privilege escalation);
// Lchown changes only the link itself.
func chownTo(path string, uid, gid int) error {
	return os.Lchown(path, uid, gid)
}

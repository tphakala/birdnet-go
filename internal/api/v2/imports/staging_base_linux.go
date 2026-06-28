//go:build linux

package importsapi

import (
	"os"
	"syscall"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// freeBytes returns the bytes available to an unprivileged user on the
// filesystem that holds path.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil //nolint:gosec // Bavail and Bsize are non-negative filesystem counters.
}

// assertTrustedBase confirms path is a directory owned by root (uid 0) with the
// sticky bit set, i.e. a system temp dir like /var/tmp that an unprivileged local
// user cannot rename or replace. This is what makes it safe to hand
// <base>/<random> to the root import-stage subcommand: import-stage rejects a
// pre-planted symlink at the terminal component, and a root-owned sticky parent
// cannot be swapped to redirect that creation elsewhere.
func assertTrustedBase(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New(err).Component("imports").Category(errors.CategoryFileIO).
			Context("op", "lstat-staging-base").Build()
	}
	if !info.IsDir() || info.Mode()&os.ModeSticky == 0 {
		return ErrStagingBaseUnavailable
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return ErrStagingBaseUnavailable
	}
	return nil
}

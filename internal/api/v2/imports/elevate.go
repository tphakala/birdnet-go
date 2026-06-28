package importsapi

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// stagingParentDir is the root-owned, sticky system directory under which each
	// per-import staging directory is created. /var/tmp (mode 1777) cannot be
	// renamed or replaced by an unprivileged local user, so handing
	// <stagingParentDir>/<random> to the root import-stage subcommand cannot be
	// redirected via a parent-path symlink swap.
	stagingParentDir = "/var/tmp"
	// stagingDirPrefix names the per-import staging subdirectory.
	stagingDirPrefix = "birdnet-go-import-"
	// stagingTokenBytes is the length of the random staging-subdir name suffix.
	stagingTokenBytes = 16
)

var (
	// ErrDiskCheckUnsupported is returned when free-space checks are unavailable.
	ErrDiskCheckUnsupported = errors.NewStd("disk space check unsupported on this platform")
	// ErrInsufficientSpace is returned when the staging base lacks room for the copy.
	ErrInsufficientSpace = errors.NewStd("insufficient disk space for staging copy")
	// ErrStagingBaseUnavailable is returned when no trusted base directory is available.
	ErrStagingBaseUnavailable = errors.NewStd("trusted staging base directory unavailable")
)

// resolveStagingBase returns the trusted parent directory for per-import staging
// dirs. It does NOT create the directory: the parent is a pre-existing root-owned
// sticky system dir, and import-stage creates the random child. Tests override
// via c.stagingBase.
func (c *Handler) resolveStagingBase() (string, error) {
	if c.stagingBase != "" {
		return c.stagingBase, nil
	}
	return stagingParentDir, nil
}

// newStagingDst verifies the base is a trusted (root-owned, sticky) directory,
// then returns a fresh, non-existent <base>/birdnet-go-import-<random> path for
// import-stage to create root-owned 0700 and chown to the service user. The
// random terminal name means a local attacker cannot pre-plant a symlink at the
// path (and even a lucky pre-plant fails import-stage's os.Mkdir with EEXIST).
func (c *Handler) newStagingDst(base string) (string, error) {
	if err := c.verifyTrustedBase(base); err != nil {
		return "", err
	}
	buf := make([]byte, stagingTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New(err).Component("imports").Category(errors.CategoryGeneric).
			Context("op", "staging-token").Build()
	}
	return filepath.Join(base, stagingDirPrefix+hex.EncodeToString(buf)), nil
}

// preflightDiskSpace fails when the staging base filesystem cannot hold the copy.
func (c *Handler) preflightDiskSpace(base string, requiredBytes uint64) error {
	free, err := c.freeBytesFn(base)
	if err != nil {
		return err
	}
	if free < requiredBytes {
		return ErrInsufficientSpace
	}
	return nil
}

// dirSizeBounded walks dir summing regular-file sizes, returning early when the
// running total exceeds capBytes or the node count reaches nodeCap. It returns 0
// when dir is empty or absent. The bounds prevent a large or hostile audio tree
// from stalling the HTTP handler goroutine.
func dirSizeBounded(dir string, capBytes uint64, nodeCap int) uint64 {
	if dir == "" {
		return 0
	}
	var total uint64
	nodes := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // skip unreadable entries; we are estimating disk usage, not auditing
		}
		nodes++
		if nodes > nodeCap {
			return fs.SkipAll
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil //nolint:nilerr // skip entries whose info cannot be retrieved during estimation
		}
		total += uint64(info.Size()) //nolint:gosec // file size is non-negative
		if capBytes > 0 && total >= capBytes {
			return fs.SkipAll
		}
		return nil
	})
	return total
}

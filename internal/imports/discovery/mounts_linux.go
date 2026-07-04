//go:build linux

package discovery

import (
	"bufio"
	"os"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// networkFSTypes is the set of /proc/mounts fstypes treated as network mounts;
// a stat on a dead one of these can block uninterruptibly, so the scanner skips
// any path under such a mount.
var networkFSTypes = map[string]struct{}{
	"nfs": {}, "nfs4": {}, "cifs": {}, "smbfs": {}, "smb3": {},
	"fuse.sshfs": {}, "fuse.rclone": {}, "afs": {}, "ncpfs": {},
	"glusterfs": {}, "ceph": {}, "9p": {},
}

// networkMountPrefixes parses a mounts file (e.g. /proc/mounts) and returns the
// mount points whose filesystem type is a known network type.
func networkMountPrefixes(mountsFile string) ([]string, error) {
	f, err := os.Open(mountsFile) //nolint:gosec // fixed system path / test path
	if err != nil {
		return nil, errors.New(err).
			Component("imports/discovery").
			Category(errors.CategoryFileIO).
			Context("operation", "open_mounts").
			Build()
	}
	defer func() { _ = f.Close() }()

	var prefixes []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		const minMountFields = 3 // device, mountpoint, fstype, ...
		if len(fields) < minMountFields {
			continue
		}
		if _, ok := networkFSTypes[fields[2]]; ok {
			prefixes = append(prefixes, unescapeMountField(fields[1]))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, errors.New(err).
			Component("imports/discovery").
			Category(errors.CategoryFileIO).
			Context("operation", "scan_mounts").
			Build()
	}
	return prefixes, nil
}

// unescapeMountField decodes the octal escapes (\040 space, \011 tab, \012
// newline, \134 backslash) that /proc/mounts uses in path fields.
func unescapeMountField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	replacer := strings.NewReplacer(
		`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`,
	)
	return replacer.Replace(s)
}

// defaultNetworkMountPrefixes returns network mount points from /proc/mounts,
// or nil on any error (best-effort: a failure just means no paths are skipped).
func defaultNetworkMountPrefixes() []string {
	prefixes, err := networkMountPrefixes("/proc/mounts")
	if err != nil {
		return nil
	}
	return prefixes
}

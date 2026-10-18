package diagnostics

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

const (
	// ownMountInfoPath is the mount table of the namespace this process opens
	// files in. Unlike the /proc/1 table it stays readable when /proc is
	// mounted with hidepid.
	ownMountInfoPath = "/proc/self/mountinfo"
	// mountTableSeparatorMinIdx is the first field index at which the "-"
	// separator can appear: six fixed fields come before the optional ones.
	mountTableSeparatorMinIdx = 6
	// mountTableFieldsAfterSeparator is the number of fields after the "-"
	// separator: fstype, mount source and super options.
	mountTableFieldsAfterSeparator = 3
	// mountTableRootIdx is the index of the mountinfo root field.
	mountTableRootIdx = 3
	// mountTableDestIdx is the index of the mountinfo mount point field.
	mountTableDestIdx = 4
	// maxSymlinkHops bounds the symlinks followed while resolving a path whose
	// leaf does not exist yet.
	maxSymlinkHops = 40
	// rootDestination is the mount destination of the container root.
	rootDestination = "/"
)

// Filesystem types the matcher classifies on.
const (
	fsTypeTmpfs         = "tmpfs"
	fsTypeRamfs         = "ramfs"
	fsTypeOverlay       = "overlay"
	fsTypeAufs          = "aufs"
	fsTypeFuseOverlayfs = "fuse.fuse-overlayfs"
)

// Labels of the key storage paths, in the order they are reported.
const (
	// StorageLabelConfig labels the directory or file holding config.yaml.
	StorageLabelConfig = "config"
	// StorageLabelDatabase labels the SQLite database file.
	StorageLabelDatabase = "database"
	// StorageLabelModels labels the model gallery directory.
	StorageLabelModels = "models"
	// StorageLabelClips labels the audio clip export directory.
	StorageLabelClips = "clips"
)

// mountInfoUnescaper decodes the octal escapes the kernel writes into mountinfo
// paths. A single Replacer pass never rescans its own output, so an escaped
// backslash followed by digits stays literal.
var mountInfoUnescaper = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

// anonymousVolumeRootRE matches the host-side subtree of an anonymous volume:
// Docker's volumes/<64 hex>/_data, Podman's storage/volumes/<64 hex>/_data, the
// same directory on a dedicated volumes partition (/<64 hex>/_data), and a
// subpath of any of them.
var anonymousVolumeRootRE = regexp.MustCompile(`(?:^|/volumes)/[0-9a-f]{64}/_data(?:/|$)`)

// MountTableEntry is one line of a mountinfo table. Destination is clean when
// the entry comes from ParseMountTable.
type MountTableEntry struct {
	// Destination is the mount point inside the container, clean.
	Destination string
	// Root is the mountinfo root field: the subtree of the source filesystem
	// that is mounted. For a bind mount it is a host directory, so it is used
	// to classify a mount and is never shown to the user.
	Root string
	// FSType is the filesystem type.
	FSType string
}

// ParseMountTable parses mountinfo content and keeps every entry, including the
// root mount and the system mounts that ParseMountInfo drops. Absolute
// destinations are cleaned. Malformed lines are skipped.
func ParseMountTable(content string) []MountTableEntry {
	var table []MountTableEntry
	for line := range strings.SplitSeq(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < mountTableSeparatorMinIdx+1+mountTableFieldsAfterSeparator {
			continue
		}
		sep := slices.Index(fields[mountTableSeparatorMinIdx:], "-")
		if sep < 0 {
			continue
		}
		sep += mountTableSeparatorMinIdx
		if sep+mountTableFieldsAfterSeparator >= len(fields) {
			continue
		}
		table = append(table, MountTableEntry{
			Destination: cleanDestination(mountInfoUnescaper.Replace(fields[mountTableDestIdx])),
			Root:        mountInfoUnescaper.Replace(fields[mountTableRootIdx]),
			FSType:      fields[sep+1],
		})
	}
	return table
}

// cleanDestination cleans an absolute mount point so that ClassifyPath can
// compare destinations without cleaning them again; a relative one is kept as
// is and never matches.
func cleanDestination(dest string) string {
	if !strings.HasPrefix(dest, "/") {
		return dest
	}
	return path.Clean(dest)
}

// PersistenceClass says whether files at a path outlive the container.
type PersistenceClass string

// The persistence classes of a path.
const (
	// PersistencePersistent is a mount that outlives the container.
	PersistencePersistent PersistenceClass = "persistent"
	// PersistenceContainerLayer is the container's own writable layer, deleted
	// with the container.
	PersistenceContainerLayer PersistenceClass = "container_layer"
	// PersistenceMemory is a tmpfs or ramfs mount, lost on restart.
	PersistenceMemory PersistenceClass = "memory"
	// PersistenceAnonymousVolume is a volume the runtime created because none
	// was mapped. It survives a restart but a fresh container gets a new one.
	PersistenceAnonymousVolume PersistenceClass = "anonymous_volume"
	// PersistenceUnknown means the path could not be judged.
	PersistenceUnknown PersistenceClass = "unknown"
)

// ClassifyPath finds the mount covering realPath and classifies it. realPath
// must be absolute with symlinks already resolved. The covering mount is the one
// with the longest destination that equals the cleaned path or is a whole-path
// segment prefix of it (destinations must be clean, as ParseMountTable makes
// them); among equal destinations the later table line wins,
// because it is mounted over the earlier one. A relative path, or a path no
// entry covers, is unknown.
func ClassifyPath(table []MountTableEntry, realPath string) (MountTableEntry, PersistenceClass) {
	if !strings.HasPrefix(realPath, "/") {
		return MountTableEntry{}, PersistenceUnknown
	}
	p := path.Clean(realPath)
	var best MountTableEntry
	found := false
	for i := range table {
		dest := table[i].Destination
		if !strings.HasPrefix(dest, "/") || !coversPath(dest, p) {
			continue
		}
		if !found || len(dest) >= len(best.Destination) {
			best = table[i]
			found = true
		}
	}
	if !found {
		return MountTableEntry{}, PersistenceUnknown
	}
	return best, classifyMount(best)
}

// coversPath reports whether a mount at dest contains p. Both are clean.
func coversPath(dest, p string) bool {
	if dest == rootDestination || dest == p {
		return true
	}
	return strings.HasPrefix(p, dest+"/")
}

// classifyMount applies the persistence rules to one mount, in order.
func classifyMount(m MountTableEntry) PersistenceClass {
	switch m.FSType {
	case fsTypeTmpfs, fsTypeRamfs:
		return PersistenceMemory
	case fsTypeOverlay, fsTypeAufs, fsTypeFuseOverlayfs:
		return PersistenceContainerLayer
	}
	// The container root is deleted with the container whatever its filesystem
	// type (the btrfs, zfs and devicemapper storage drivers).
	if m.Destination == rootDestination {
		return PersistenceContainerLayer
	}
	if anonymousVolumeRootRE.MatchString(m.Root) {
		return PersistenceAnonymousVolume
	}
	return PersistencePersistent
}

// StoragePath is a path to judge.
type StoragePath struct {
	// Label names what the path is for (the StorageLabel constants).
	Label string
	// Path is the path as the application uses it.
	Path string
	// IsFile marks a file path. The file is resolved first and the directory
	// that holds it is judged, because the file may not exist yet.
	IsFile bool
}

// StorageVerdict is the persistence result for one StoragePath. It carries only
// container-side paths.
type StorageVerdict struct {
	// Label is the StoragePath label.
	Label string
	// Path is the configured path.
	Path string
	// ResolvedPath is the absolute path with symlinks resolved, empty when it
	// could not be resolved.
	ResolvedPath string
	// MountPoint is the destination of the mount that covers the path.
	MountPoint string
	// FSType is the filesystem type of that mount.
	FSType string
	// Class is the persistence class.
	Class PersistenceClass
}

// StorageSeverity ranks a verdict.
type StorageSeverity int

// The severities of a verdict, in increasing order.
const (
	// StorageSeverityNone needs no attention.
	StorageSeverityNone StorageSeverity = iota
	// StorageSeverityWarning is data that is lost (models or clips) or can seem
	// lost (any anonymous volume).
	StorageSeverityWarning
	// StorageSeverityCritical is the configuration or the database on a path
	// lost when the container is recreated.
	StorageSeverityCritical
)

// Severity ranks the verdict. A lost config or database is critical, a lost
// models or clips path and any anonymous volume is a warning, and a persistent
// or unknown path needs nothing.
func (v *StorageVerdict) Severity() StorageSeverity {
	switch {
	case v.Lost():
		if v.Label == StorageLabelConfig || v.Label == StorageLabelDatabase {
			return StorageSeverityCritical
		}
		return StorageSeverityWarning
	case v.Class == PersistenceAnonymousVolume:
		return StorageSeverityWarning
	}
	return StorageSeverityNone
}

// Lost reports whether the path is lost when the container is recreated.
func (v *StorageVerdict) Lost() bool {
	return v.Class == PersistenceContainerLayer || v.Class == PersistenceMemory
}

// ResolveRealPath makes p absolute and resolves its symlinks. When the leaf does
// not exist yet it resolves the nearest existing ancestor, following dangling
// symlinks hop by hop, and appends the missing suffix, which is where the path
// would be created. An error other than "does not exist" (permission denied, a
// link loop) is returned and never walked past.
func ResolveRealPath(p string) (string, error) {
	cur, err := filepath.Abs(p)
	if err != nil {
		return "", resolveError(err)
	}
	for hops := 0; hops <= maxSymlinkHops; hops++ {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", resolveError(err)
		}
		next, done, err := stepMissingPath(cur)
		if err != nil {
			return "", resolveError(err)
		}
		if done {
			return next, nil
		}
		cur = next
	}
	return "", resolveError(errors.NewStd("too many symbolic links"))
}

// stepMissingPath handles a path EvalSymlinks reported as missing. It finds the
// nearest existing ancestor with Lstat. A real ancestor yields the resolved
// ancestor plus the missing suffix, and done is true. A symlink ancestor yields
// its target plus the suffix, to be resolved on the next pass.
func stepMissingPath(missing string) (next string, done bool, err error) {
	cur := missing
	var suffix []string
	for {
		info, err := os.Lstat(cur)
		if err == nil {
			next, err = spliceExisting(cur, info, suffix)
			return next, info.Mode()&os.ModeSymlink == 0, err
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", false, err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false, errors.NewStd("no existing ancestor")
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

// spliceExisting joins the missing suffix (deepest component first) onto the
// existing path cur: its link target when cur is a symlink, else its resolved
// form.
func spliceExisting(cur string, info os.FileInfo, suffix []string) (string, error) {
	var base string
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(cur)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			realParent, err := filepath.EvalSymlinks(filepath.Dir(cur))
			if err != nil {
				return "", err
			}
			target = filepath.Join(realParent, target)
		}
		base = target
	} else {
		resolved, err := filepath.EvalSymlinks(cur)
		if err != nil {
			return "", err
		}
		base = resolved
	}
	for _, name := range slices.Backward(suffix) {
		base = filepath.Join(base, name)
	}
	return base, nil
}

// resolveError wraps a path resolution failure.
func resolveError(err error) error {
	return errors.New(err).
		Component("diagnostics").
		Category(errors.CategoryFileIO).
		Context("operation", "resolve_real_path").
		Build()
}

// EvaluateStoragePersistence classifies each path against the mount table and
// returns one verdict per input, in input order. resolve turns a configured path
// into the real path (ResolveRealPath in production); a resolve error gives an
// unknown verdict.
func EvaluateStoragePersistence(table []MountTableEntry, paths []StoragePath, resolve func(string) (string, error)) []StorageVerdict {
	verdicts := make([]StorageVerdict, 0, len(paths))
	for _, sp := range paths {
		v := StorageVerdict{Label: sp.Label, Path: sp.Path, Class: PersistenceUnknown}
		resolved, err := resolve(sp.Path)
		if err != nil {
			verdicts = append(verdicts, v)
			continue
		}
		v.ResolvedPath = resolved
		judged := filepath.ToSlash(resolved)
		if sp.IsFile {
			judged = path.Dir(judged)
		}
		entry, class := ClassifyPath(table, judged)
		v.Class = class
		v.MountPoint = entry.Destination
		v.FSType = entry.FSType
		verdicts = append(verdicts, v)
	}
	return verdicts
}

// StoragePersistenceAppliesTo reports whether the persistence check is
// meaningful in the given environment type: only Docker and Podman application
// containers, where the container root is deleted with the container. LXC and
// systemd-nspawn containers keep their root on a persistent disk.
func StoragePersistenceAppliesTo(envType string) bool {
	return envType == sysinfo.EnvDocker || envType == sysinfo.EnvPodman
}

// ReadOwnMountTable reads and parses the mount table of this process.
func ReadOwnMountTable() ([]MountTableEntry, error) {
	data, err := os.ReadFile(ownMountInfoPath) //nolint:gosec // Constant path
	if err != nil {
		return nil, errors.New(err).
			Component("diagnostics").
			Category(errors.CategoryFileIO).
			Context("operation", "read_own_mountinfo").
			Build()
	}
	return ParseMountTable(string(data)), nil
}

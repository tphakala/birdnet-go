package diagnostics

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// hexID is a 64 character volume identifier like the ones the container
// runtimes give anonymous volumes.
const hexID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// mountLine builds one mountinfo line for the given root, destination and
// fstype, with one optional field before the "-" separator.
func mountLine(id int, root, dest, fstype string) string {
	return fmt.Sprintf("%d 1 8:1 %s %s rw,relatime shared:1 - %s /dev/sda1 rw", id, root, dest, fstype)
}

func TestParseMountTable_KeepsRootAndSystemMounts(t *testing.T) {
	t.Parallel()
	content := mountLine(1, "/", "/", "overlay") + "\n" +
		mountLine(2, "/", "/proc", "proc") + "\n" +
		mountLine(3, "/", "/tmp", "tmpfs") + "\n" +
		mountLine(4, "/", "/dev/shm", "tmpfs") + "\n"

	table := ParseMountTable(content)

	require.Len(t, table, 4)
	assert.Equal(t, MountTableEntry{Destination: "/", Root: "/", FSType: "overlay"}, table[0])
	assert.Equal(t, "/proc", table[1].Destination)
	assert.Equal(t, "/tmp", table[2].Destination)
	assert.Equal(t, "/dev/shm", table[3].Destination)
}

func TestParseMountTable_UnescapesOctal(t *testing.T) {
	t.Parallel()
	content := mountLine(1, `/My\040Data`, `/mnt/My\040Data`, "ext4") + "\n" +
		mountLine(2, `/a`, `/mnt/lit\134040`, "ext4") + "\n"

	table := ParseMountTable(content)

	require.Len(t, table, 2)
	assert.Equal(t, "/mnt/My Data", table[0].Destination)
	assert.Equal(t, "/My Data", table[0].Root)
	// A backslash escape followed by literal digits must not be decoded twice.
	assert.Equal(t, `/mnt/lit\040`, table[1].Destination)
}

func TestParseMountTable_CleansDestinations(t *testing.T) {
	t.Parallel()
	table := ParseMountTable(mountLine(1, "/", "/config//hls/", "tmpfs") + "\n" +
		mountLine(2, "/", "relative/dir", "ext4") + "\n")

	require.Len(t, table, 2)
	assert.Equal(t, "/config/hls", table[0].Destination)
	assert.Equal(t, "relative/dir", table[1].Destination, "a relative destination is kept and never matches")
	_, class := ClassifyPath(table, "/config/hls/x")
	assert.Equal(t, PersistenceMemory, class)
}

func TestParseMountTable_OptionalFieldsAndMalformedLines(t *testing.T) {
	t.Parallel()
	content := "10 1 8:1 / /a rw shared:1 master:2 - ext4 /dev/sda1 rw\n" +
		"11 1 8:1 / /b rw - ext4 /dev/sda1 rw\n" +
		"short line\n" +
		"12 1 8:1 / /c rw shared:1 ext4 /dev/sda1 rw\n" +
		"13 1 - 8:1 / /d rw ext4 /dev/sda1 rw\n" +
		"14 1 8:1 / /e rw - ext4\n"

	table := ParseMountTable(content)

	require.Len(t, table, 2)
	assert.Equal(t, "/a", table[0].Destination)
	assert.Equal(t, "ext4", table[0].FSType)
	assert.Equal(t, "/b", table[1].Destination)
}

// containerTable is a fixture shaped like a Docker container with an overlay
// root, a bind mounted config dir that holds a nested tmpfs, and assorted
// volumes.
func containerTable() []MountTableEntry {
	return []MountTableEntry{
		{Destination: "/", Root: "/var/lib/docker/overlay2/x/merged", FSType: "overlay"},
		{Destination: "/config", Root: "/home/alice/birdnet/config", FSType: "ext4"},
		{Destination: "/config/hls", Root: "/", FSType: "tmpfs"},
		{Destination: "/data", Root: "/var/lib/docker/volumes/birdnet_data/_data", FSType: "ext4"},
		{Destination: "/anon-docker", Root: "/var/lib/docker/volumes/" + hexID + "/_data", FSType: "ext4"},
		{Destination: "/anon-podman", Root: "/home/u/.local/share/containers/storage/volumes/" + hexID + "/_data", FSType: "ext4"},
		{Destination: "/anon-partition", Root: "/" + hexID + "/_data", FSType: "ext4"},
		{Destination: "/anon-sub", Root: "/" + hexID + "/_data/sub", FSType: "ext4"},
		{Destination: "/podman-overlay/data", Root: "/", FSType: "overlay"},
		{Destination: "/scratch", Root: "/", FSType: "tmpfs"},
		{Destination: "/over", Root: "/first", FSType: "ext4"},
		{Destination: "/over", Root: "/second", FSType: "tmpfs"},
	}
}

func TestClassifyPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		table     []MountTableEntry
		path      string
		wantClass PersistenceClass
		wantMount string
	}{
		{"bind config", containerTable(), "/config/config.yaml", PersistencePersistent, "/config"},
		{"named volume", containerTable(), "/data/birdnet.db", PersistencePersistent, "/data"},
		{"anonymous docker volume", containerTable(), "/anon-docker/x", PersistenceAnonymousVolume, "/anon-docker"},
		{"anonymous podman volume", containerTable(), "/anon-podman", PersistenceAnonymousVolume, "/anon-podman"},
		{"anonymous volume on own partition", containerTable(), "/anon-partition/y", PersistenceAnonymousVolume, "/anon-partition"},
		{"anonymous volume subpath", containerTable(), "/anon-sub", PersistenceAnonymousVolume, "/anon-sub"},
		{"overlay root only", containerTable(), "/opt/models", PersistenceContainerLayer, "/"},
		{"btrfs root", []MountTableEntry{{Destination: "/", Root: "/", FSType: "btrfs"}}, "/opt/models", PersistenceContainerLayer, "/"},
		{"overlay mount at data", containerTable(), "/podman-overlay/data/db", PersistenceContainerLayer, "/podman-overlay/data"},
		{"tmpfs scratch", containerTable(), "/scratch/a", PersistenceMemory, "/scratch"},
		{"nested tmpfs", containerTable(), "/config/hls/x", PersistenceMemory, "/config/hls"},
		{"models beside nested tmpfs", containerTable(), "/config/models", PersistencePersistent, "/config"},
		{"sibling of nested tmpfs", containerTable(), "/config/hlsx", PersistencePersistent, "/config"},
		{"cleaned parent traversal", containerTable(), "/config/hls/../models", PersistencePersistent, "/config"},
		{"overmount last wins", containerTable(), "/over/z", PersistenceMemory, "/over"},
		{"no root entry", []MountTableEntry{{Destination: "/config", Root: "/", FSType: "ext4"}}, "/opt/models", PersistenceUnknown, ""},
		{"relative path", containerTable(), "config/models", PersistenceUnknown, ""},
		{"empty table", nil, "/config", PersistenceUnknown, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry, class := ClassifyPath(tt.table, tt.path)
			assert.Equal(t, tt.wantClass, class)
			assert.Equal(t, tt.wantMount, entry.Destination)
		})
	}
}

func TestResolveRealPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	// Resolve the temp dir once so comparisons are independent of a symlinked
	// temp directory (macOS).
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	mustMkdir := func(p string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(p, 0o750))
	}
	mustLink := func(target, link string) {
		t.Helper()
		require.NoError(t, os.Symlink(target, link))
	}

	t.Run("entrypoint style symlink with missing leaf", func(t *testing.T) {
		mustMkdir(filepath.Join(base, "cfg"))
		mustMkdir(filepath.Join(base, "home", ".config"))
		mustLink(filepath.Join(base, "cfg"), filepath.Join(base, "home", ".config", "birdnet-go"))

		got, err := ResolveRealPath(filepath.Join(base, "home", ".config", "birdnet-go", "models"))

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(base, "cfg", "models"), got)
	})

	t.Run("relative path becomes absolute", func(t *testing.T) {
		wd, err := os.Getwd()
		require.NoError(t, err)

		got, err := ResolveRealPath("does-not-exist-xyz")

		require.NoError(t, err)
		realWd, err := filepath.EvalSymlinks(wd)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realWd, "does-not-exist-xyz"), got)
	})

	t.Run("two link chain", func(t *testing.T) {
		mustMkdir(filepath.Join(base, "chain", "real"))
		mustLink(filepath.Join(base, "chain", "real"), filepath.Join(base, "chain", "l1"))
		mustLink(filepath.Join(base, "chain", "l1"), filepath.Join(base, "chain", "l2"))

		got, err := ResolveRealPath(filepath.Join(base, "chain", "l2", "new"))

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(base, "chain", "real", "new"), got)
	})

	t.Run("dangling relative link", func(t *testing.T) {
		mustMkdir(filepath.Join(base, "dang", "home"))
		mustLink(filepath.Join("..", "vol", "clips"), filepath.Join(base, "dang", "home", "clips"))

		got, err := ResolveRealPath(filepath.Join(base, "dang", "home", "clips", "a.wav"))

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(base, "dang", "vol", "clips", "a.wav"), got)
	})

	t.Run("link loop errors", func(t *testing.T) {
		mustMkdir(filepath.Join(base, "loop"))
		mustLink(filepath.Join(base, "loop", "b"), filepath.Join(base, "loop", "a"))
		mustLink(filepath.Join(base, "loop", "a"), filepath.Join(base, "loop", "b"))

		_, err := ResolveRealPath(filepath.Join(base, "loop", "a", "x"))

		require.Error(t, err)
	})

	t.Run("chain of dangling links beyond the hop cap errors", func(t *testing.T) {
		const chainLen = 45 // above maxSymlinkHops, below EvalSymlinks' own limit
		dir := filepath.Join(base, "chain45")
		mustMkdir(dir)
		linkPath := func(i int) string { return filepath.Join(dir, fmt.Sprintf("l%d", i)) }
		for i := range chainLen {
			mustLink(linkPath(i+1), linkPath(i))
		}

		_, err := ResolveRealPath(linkPath(0))

		require.Error(t, err)
	})

	t.Run("unreadable directory errors", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		locked := filepath.Join(base, "locked")
		mustMkdir(filepath.Join(locked, "inner"))
		require.NoError(t, os.Chmod(locked, 0o000))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

		_, err := ResolveRealPath(filepath.Join(locked, "inner", "leaf"))

		require.Error(t, err)
	})
}

func TestEvaluateStoragePersistence_SymlinkedDefaultModelsDir(t *testing.T) {
	t.Parallel()
	table := []MountTableEntry{
		{Destination: "/", Root: "/", FSType: "overlay"},
		{Destination: "/config", Root: "/host/config", FSType: "ext4"},
	}
	resolve := func(p string) (string, error) {
		switch p {
		case "/root/.config/birdnet-go/models":
			return "/config/models", nil
		case "/broken":
			return "", errors.NewStd("boom")
		}
		return p, nil
	}
	paths := []StoragePath{
		{Label: StorageLabelModels, Path: "/root/.config/birdnet-go/models"},
		{Label: StorageLabelClips, Path: "/broken"},
		{Label: StorageLabelDatabase, Path: "/opt/db/birdnet.db", IsFile: true},
	}

	got := EvaluateStoragePersistence(table, paths, resolve)

	require.Len(t, got, 3)
	assert.Equal(t, StorageLabelModels, got[0].Label)
	assert.Equal(t, "/config/models", got[0].ResolvedPath)
	assert.Equal(t, PersistencePersistent, got[0].Class)
	assert.Equal(t, "/config", got[0].MountPoint)
	assert.Equal(t, StorageLabelClips, got[1].Label)
	assert.Equal(t, PersistenceUnknown, got[1].Class)
	assert.Empty(t, got[1].ResolvedPath)
	assert.Equal(t, StorageLabelDatabase, got[2].Label)
	assert.Equal(t, PersistenceContainerLayer, got[2].Class)
}

func TestEvaluateStoragePersistence_FilePathsJudgedAfterResolve(t *testing.T) {
	t.Parallel()
	table := []MountTableEntry{
		{Destination: "/", Root: "/", FSType: "overlay"},
		{Destination: "/data", Root: "/host/data", FSType: "ext4"},
	}
	resolve := func(p string) (string, error) {
		if p == "/app/birdnet.db" {
			return "/data/birdnet.db", nil
		}
		return p, nil
	}

	got := EvaluateStoragePersistence(table,
		[]StoragePath{{Label: StorageLabelDatabase, Path: "/app/birdnet.db", IsFile: true}}, resolve)

	require.Len(t, got, 1)
	assert.Equal(t, PersistencePersistent, got[0].Class)
	assert.Equal(t, "/data", got[0].MountPoint)
	assert.Equal(t, "/data/birdnet.db", got[0].ResolvedPath)
}

func TestEvaluateStoragePersistence_VerdictIndependentOfOtherPaths(t *testing.T) {
	t.Parallel()
	table := containerTable()
	identity := func(p string) (string, error) { return p, nil }
	paths := []StoragePath{
		{Label: StorageLabelConfig, Path: "/config/config.yaml", IsFile: true},
		{Label: StorageLabelDatabase, Path: "/data/birdnet.db", IsFile: true},
		{Label: StorageLabelModels, Path: "/config/models"},
		{Label: StorageLabelClips, Path: "/config/hls/clips"},
	}
	alone := make(map[string]StorageVerdict, len(paths))
	for _, p := range paths {
		alone[p.Label] = EvaluateStoragePersistence(table, []StoragePath{p}, identity)[0]
	}

	rng := rand.New(rand.NewPCG(1, 2))
	for range 20 {
		shuffled := slices.Clone(paths)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := EvaluateStoragePersistence(table, shuffled, identity)
		require.Len(t, got, len(shuffled))
		for i, v := range got {
			assert.Equal(t, shuffled[i].Label, v.Label, "input order is kept")
			assert.Equal(t, alone[v.Label], v)
		}
	}
}

func TestStorageVerdictSeverity(t *testing.T) {
	t.Parallel()
	labels := []string{StorageLabelConfig, StorageLabelDatabase, StorageLabelModels, StorageLabelClips}
	want := func(label string, class PersistenceClass) StorageSeverity {
		critical := label == StorageLabelConfig || label == StorageLabelDatabase
		switch class {
		case PersistenceContainerLayer, PersistenceMemory:
			if critical {
				return StorageSeverityCritical
			}
			return StorageSeverityWarning
		case PersistenceAnonymousVolume:
			return StorageSeverityWarning
		case PersistencePersistent, PersistenceUnknown:
			return StorageSeverityNone
		}
		return StorageSeverityNone
	}
	classes := []PersistenceClass{
		PersistencePersistent, PersistenceContainerLayer, PersistenceMemory,
		PersistenceAnonymousVolume, PersistenceUnknown,
	}
	for _, label := range labels {
		for _, class := range classes {
			t.Run(label+"/"+string(class), func(t *testing.T) {
				t.Parallel()
				v := StorageVerdict{Label: label, Class: class}
				assert.Equal(t, want(label, class), v.Severity())
				assert.Equal(t, class == PersistenceContainerLayer || class == PersistenceMemory, v.Lost())
			})
		}
	}
}

func TestStoragePersistenceAppliesTo(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		sysinfo.EnvDocker:       true,
		sysinfo.EnvPodman:       true,
		sysinfo.EnvLXC:          false,
		sysinfo.EnvNspawn:       false,
		sysinfo.EnvContainerGen: false,
		"":                      false,
		"Bare Metal":            false,
		"VM":                    false,
	}
	for env, want := range tests {
		assert.Equal(t, want, StoragePersistenceAppliesTo(env), "env %q", env)
	}
}

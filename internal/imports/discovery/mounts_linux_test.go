//go:build linux

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkMountPrefixes_DetectsNetworkFilesystems(t *testing.T) {
	t.Parallel()
	content := "" +
		"proc /proc proc rw,relatime 0 0\n" +
		"/dev/sda1 / ext4 rw,relatime 0 0\n" +
		"server:/export /mnt/nas nfs4 rw,relatime 0 0\n" +
		"//server/share /mnt/win cifs rw 0 0\n" +
		"tmpfs /run tmpfs rw 0 0\n"
	dir := t.TempDir()
	f := filepath.Join(dir, "mounts")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o600))

	got, err := networkMountPrefixes(f)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"/mnt/nas", "/mnt/win"}, got)
}

func TestNetworkMountPrefixes_HandlesOctalEscapesInPath(t *testing.T) {
	t.Parallel()
	// /proc/mounts encodes spaces as \040.
	content := "server:/export /mnt/has\\040space nfs rw 0 0\n"
	dir := t.TempDir()
	f := filepath.Join(dir, "mounts")
	require.NoError(t, os.WriteFile(f, []byte(content), 0o600))

	got, err := networkMountPrefixes(f)
	require.NoError(t, err)
	assert.Equal(t, []string{"/mnt/has space"}, got)
}

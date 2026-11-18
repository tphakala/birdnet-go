//go:build openvino && windows

package openvino

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// systemLibrary is a DLL every Windows host has.
	systemLibrary = "kernel32.dll"
	// knownExport is an export of systemLibrary.
	knownExport = "GetCurrentProcessId"
	// absentText is the text isLibraryAbsent (internal/classifier) matches for a
	// missing runtime, spelled out here so a change to moduleNotFoundText that
	// breaks that match fails this package's tests.
	absentText = "the specified module could not be found"
	// missingExport is not exported by any system DLL.
	missingExport = "ov_no_such_symbol_exported"
)

func systemLibraryPath(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SystemRoot")
	require.NotEmpty(t, root, "SystemRoot must be set on Windows")
	return filepath.Join(root, "System32", systemLibrary)
}

// TestOpenLibrary_AbsolutePathAndSymbols loads a system DLL by absolute path,
// resolves a real export, and rejects a missing one.
func TestOpenLibrary_AbsolutePathAndSymbols(t *testing.T) {
	h, err := openLibrary(systemLibraryPath(t))
	require.NoError(t, err)
	t.Cleanup(func() { closeLibrary(h) })

	addr, err := lookupSymbol(h, knownExport)
	require.NoError(t, err)
	assert.NotZero(t, addr)

	_, err = lookupSymbol(h, missingExport)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missingExport)
}

// TestOpenLibrary_BareName loads a system DLL by bare name through the default
// directories.
func TestOpenLibrary_BareName(t *testing.T) {
	h, err := openLibrary(systemLibrary)
	require.NoError(t, err)
	closeLibrary(h)
}

// TestOpenLibrary_MissingDLLReportsFixedText pins the text isLibraryAbsent
// matches, whatever the Windows display language, for both a missing absolute
// path and a missing bare name.
func TestOpenLibrary_MissingDLLReportsFixedText(t *testing.T) {
	for _, path := range []string{
		filepath.Join(t.TempDir(), "openvino_c.dll"),
		"openvino_c_no_such_library.dll",
	} {
		_, err := openLibrary(path)
		require.Error(t, err, path)
		assert.Equal(t, absentText, err.Error(), path)
	}
}

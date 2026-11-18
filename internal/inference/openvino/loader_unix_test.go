//go:build openvino && linux

package openvino

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// nonOpenVINOLibrary is a real shared library every glibc Linux host has.
	// It loads fine but exports no OpenVINO symbol, so it exercises the
	// symbol-resolution failure path without an OpenVINO install.
	nonOpenVINOLibrary = "libm.so.6"
	// firstSymbolName is the first symbol the loader resolves.
	firstSymbolName = "ov_core_create"
	// missingLibraryText is the dlerror text for a path that does not exist.
	missingLibraryText = "cannot open shared object file"
)

// TestInitOV_SymbolFailureLeavesNothingBound pins that a library which loads
// but lacks the OpenVINO symbols is rejected without binding it, and that a
// later attempt with another path starts clean and reports the loader text.
func TestInitOV_SymbolFailureLeavesNothingBound(t *testing.T) {
	// The binding is process-wide and permanent once it succeeds, so a round
	// trip test that ran earlier in this process leaves nothing to test here.
	if _, loaded := LoadedLibraryPath(); loaded {
		t.Skip("an OpenVINO library is already bound in this process")
	}
	err := InitOV(nonOpenVINOLibrary)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openvino: failed to load libopenvino_c")
	assert.Contains(t, err.Error(), firstSymbolName)

	_, loaded := LoadedLibraryPath()
	assert.False(t, loaded, "a failed symbol resolution must not record a loaded library")

	err = InitOV("/nonexistent/libopenvino_c.so")
	require.Error(t, err)
	assert.Contains(t, err.Error(), missingLibraryText,
		"a retry after a failed load must reach dlopen again and report its text")
	assert.NotContains(t, err.Error(), "/nonexistent/", "the library path must be scrubbed")
}

package classifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFindModelPathInStandardPaths verifies the path-only finder used by the
// arm64 ONNX default: it returns the on-disk location of a model in the standard
// search paths without reading the file, and reports absence for unknown models.
func TestFindModelPathInStandardPaths(t *testing.T) {
	// Cannot run in parallel: uses os.Chdir via withTempWorkDir.

	withTempWorkDir(t, func(_ string) {
		// A name that exists in no standard path resolves to not-found.
		_, ok := findModelPathInStandardPaths("definitely-not-a-real-model-zzz.onnx")
		require.False(t, ok, "expected unknown model to be absent")

		// Once present in the relative model directory, it is found.
		const name = DefaultBirdNETINT8ONNXModelName
		modelPath := filepath.Join(DefaultModelDirectory, name)
		require.NoError(t, os.MkdirAll(DefaultModelDirectory, 0o750))
		require.NoError(t, os.WriteFile(modelPath, []byte("x"), 0o600))

		path, ok := findModelPathInStandardPaths(name)
		require.True(t, ok, "expected model found after creation")
		require.True(t, strings.HasSuffix(path, modelPath), "path %s should end with %s", path, modelPath)
	})
}

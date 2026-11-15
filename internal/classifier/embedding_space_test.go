package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEmbeddingSpace(t *testing.T) {
	t.Parallel()

	modelsDir := t.TempDir()
	catalogPath := filepath.Join(modelsDir, sharedDirName, "birdnet-v24-embeddings.onnx")

	customDir := t.TempDir()
	writeFile := func(name, content string) string {
		p := filepath.Join(customDir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		return p
	}
	first := writeFile("a.onnx", "backbone one")
	second := writeFile("b.onnx", "backbone two")
	sameBytes := writeFile("c.onnx", "backbone one")

	t.Run("catalog path gives the named space without reading the file", func(t *testing.T) {
		t.Parallel()
		// The file does not exist: a named space must never depend on its bytes.
		space, err := resolveEmbeddingSpace(modelsDir, catalogPath)
		require.NoError(t, err)
		assert.Equal(t, EmbeddingSpaceBirdNETV24Bat, space)
	})

	t.Run("a file outside the catalog is its own content-addressed space", func(t *testing.T) {
		t.Parallel()
		a, err := resolveEmbeddingSpace(modelsDir, first)
		require.NoError(t, err)
		b, err := resolveEmbeddingSpace(modelsDir, second)
		require.NoError(t, err)
		assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, a)
		assert.NotEqual(t, a, b, "different bytes must be different spaces")
		assert.NotEqual(t, EmbeddingSpaceBirdNETV24Bat, a)
	})

	t.Run("the same bytes at two paths give the same space", func(t *testing.T) {
		t.Parallel()
		a, err := resolveEmbeddingSpace(modelsDir, first)
		require.NoError(t, err)
		c, err := resolveEmbeddingSpace(modelsDir, sameBytes)
		require.NoError(t, err)
		assert.Equal(t, a, c)
	})

	t.Run("an unreadable custom file is an error", func(t *testing.T) {
		t.Parallel()
		_, err := resolveEmbeddingSpace(modelsDir, filepath.Join(customDir, "missing.onnx"))
		require.Error(t, err)
	})
}

package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResults_CopyDeepCopiesEmbeddings(t *testing.T) {
	orig := Results{Embeddings: []float32{1, 2, 3}}
	cp := orig.Copy()
	require.Equal(t, []float32{1, 2, 3}, cp.Embeddings)

	cp.Embeddings[0] = 99
	assert.InDelta(t, float32(1), orig.Embeddings[0], 0.0001, "Copy must deep-copy Embeddings")
}

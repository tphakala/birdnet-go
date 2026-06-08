package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestEmbeddingsConfig_DefaultDisabled(t *testing.T) {
	t.Parallel()
	var s Settings
	assert.False(t, s.Embeddings.Enabled)
}

func TestEmbeddingsConfig_YAMLRoundTrip(t *testing.T) {
	t.Parallel()
	var s Settings
	require.NoError(t, yaml.Unmarshal([]byte("embeddings:\n  enabled: true\n"), &s))
	assert.True(t, s.Embeddings.Enabled)
}

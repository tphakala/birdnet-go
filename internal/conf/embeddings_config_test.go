package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestEmbeddingsConfig_ZeroValueDisabled verifies that the Go zero value of
// Settings has Embeddings.Enabled == false. This tests the struct default,
// not the viper default.
func TestEmbeddingsConfig_ZeroValueDisabled(t *testing.T) {
	t.Parallel()
	var s Settings
	assert.False(t, s.Embeddings.Enabled)
}

// TestEmbeddingsConfig_ViperDefaultDisabled verifies that the configured viper
// default for "embeddings.enabled" is false. This exercises the actual default
// set by setDefaultConfig(), not just the Go zero value.
// Not parallel: reads/writes global viper defaults set by setDefaultConfig.
func TestEmbeddingsConfig_ViperDefaultDisabled(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaultConfig()
	assert.False(t, viper.GetBool("embeddings.enabled"),
		"embeddings.enabled viper default must be false")
}

// TestEmbeddingsConfig_YAMLExplicitFalseRoundTrip verifies that an explicit
// false in YAML unmarshals correctly, covering the case where a config file
// explicitly sets the feature off.
func TestEmbeddingsConfig_YAMLExplicitFalseRoundTrip(t *testing.T) {
	t.Parallel()
	var s Settings
	require.NoError(t, yaml.Unmarshal([]byte("embeddings:\n  enabled: false\n"), &s))
	assert.False(t, s.Embeddings.Enabled)
}

func TestEmbeddingsConfig_YAMLRoundTrip(t *testing.T) {
	t.Parallel()
	var s Settings
	require.NoError(t, yaml.Unmarshal([]byte("embeddings:\n  enabled: true\n"), &s))
	assert.True(t, s.Embeddings.Enabled)
}

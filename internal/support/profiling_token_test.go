package support

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/yaml.v3"
)

// profilingTokenSecret is a value distinctive enough that finding it anywhere in
// a generated dump is unambiguous.
const profilingTokenSecret = "PROFILING-TOKEN-MUST-NOT-LEAK-8b41c7"

// TestSupportDumpRedactsProfilingToken asserts the profiling token is redacted
// from a collected support dump.
//
// This is the trap that made the config key name a design decision rather than
// a detail. Support dumps upload to Sentry, and scrubbing matches sensitive keys
// on word boundaries (isSensitiveKey), so "token" and "profiling_token" are
// redacted but a squashed "profilingtoken" is not. The project's YAML house
// style is squashed lowercase, which is exactly the form that fails, so the leaf
// key is "token" and this test is what keeps it that way.
//
// It marshals a real conf.Settings rather than a hand-written map, so the yaml
// tag on the struct field is part of what is being asserted.
func TestSupportDumpRedactsProfilingToken(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.Diagnostics.Profiling.Enabled = true
	settings.Diagnostics.Profiling.Token = profilingTokenSecret

	data, err := yaml.Marshal(settings)
	require.NoError(t, err)
	require.Contains(t, string(data), profilingTokenSecret,
		"test setup must actually write the token into the config file")

	configDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0o600))

	collector := NewCollector(configDir, t.TempDir(), "test-system", "test-version")

	scrubbed, err := collector.collectConfig(true)
	require.NoError(t, err)

	rendered, err := yaml.Marshal(scrubbed)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), profilingTokenSecret,
		"the profiling token must never reach a support dump")

	diagnostics, ok := scrubbed["diagnostics"].(map[string]any)
	require.True(t, ok, "diagnostics section should survive scrubbing: %#v", scrubbed)
	profiling, ok := diagnostics["profiling"].(map[string]any)
	require.True(t, ok, "profiling section should survive scrubbing: %#v", diagnostics)

	assert.Equal(t, "[redacted]", profiling["token"])
	assert.Equal(t, true, profiling["enabled"],
		"only the token is sensitive; the enable flag is diagnostic signal worth keeping")
}

// TestProfilingTokenKeyIsRedactable pins the naming rule itself, independent of
// the collector. The negative case is the point: it records why the leaf key
// cannot be renamed to the squashed form the rest of the config file uses.
func TestProfilingTokenKeyIsRedactable(t *testing.T) {
	t.Parallel()

	keys := DefaultSensitiveKeys()

	assert.True(t, MatchesSensitiveKey("diagnostics.profiling.token", keys),
		"the configured key path must be redacted")
	assert.False(t, MatchesSensitiveKey("diagnostics.profiling.profilingtoken", keys),
		"squashed naming is NOT redacted; renaming the leaf key would leak the credential")
}

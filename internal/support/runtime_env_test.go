package support

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// newTestCollector builds a Collector carrying the default sensitive-key list,
// which is what collectRuntimeEnv consults to redact by key.
func newTestCollector() *Collector {
	return &Collector{sensitiveKeys: DefaultSensitiveKeys()}
}

// TestCollectRuntimeEnvRedactsASensitiveKey pins the defense-in-depth layer:
// the allowlist is supposed to make this unreachable, but SystemInfo is the one
// dump section no scrubber touches, so a future allowlist entry naming a
// credential must still fail safe rather than upload the value.
func TestCollectRuntimeEnvRedactsASensitiveKey(t *testing.T) {
	const sensitive = "BIRDNET_TEST_API_TOKEN"
	t.Setenv(sensitive, "super-secret-value")

	// Inject the entry a future maintainer might wrongly add, to prove the
	// redaction net catches what the allowlist review missed. SystemInfo is the
	// one dump section no scrubber touches, so this net is the last line.
	env := newTestCollector().collectRuntimeEnv(append(conf.SupportEnvAllowlist(), sensitive))

	assert.NotContains(t, env[sensitive], "super-secret-value",
		"a sensitive-looking key must never reach the dump verbatim")
	assert.Equal(t, redactedPlaceholder, env[sensitive])
}

// TestCollectRuntimeEnvCapturesAllowlistedGates covers the case the feature
// exists for: a dump that arrives without a clip export in the captured log
// window must still reveal whether a native encoder gate was on.
func TestCollectRuntimeEnvCapturesAllowlistedGates(t *testing.T) {
	t.Setenv(conf.EnvNativeAACEncoder, "native")

	env := newTestCollector().collectRuntimeEnv(conf.SupportEnvAllowlist())

	require.NotNil(t, env)
	assert.Equal(t, "native", env[conf.EnvNativeAACEncoder])
	assert.NotContains(t, env, conf.EnvNativeOpusEncoder,
		"an unset gate must be absent, not present with an empty value")
}

// TestCollectRuntimeEnvIgnoresNonAllowlisted is the privacy half of the
// contract. The allowlist exists because the process environment can hold
// credentials, so a BIRDNET_-prefixed variable that nobody vetted must not be
// swept into a file the user uploads to a third party.
func TestCollectRuntimeEnvIgnoresNonAllowlisted(t *testing.T) {
	t.Setenv("BIRDNET_SOMETHING_SECRET", "hunter2")

	env := newTestCollector().collectRuntimeEnv(conf.SupportEnvAllowlist())

	assert.NotContains(t, env, "BIRDNET_SOMETHING_SECRET")
	for _, v := range env {
		assert.NotEqual(t, "hunter2", v)
	}
}

// TestCollectRuntimeEnvEmptyWhenNoGatesSet keeps the common install clean: with
// nothing set the map is nil, so runtime_env is omitted from the JSON entirely
// rather than adding an empty block to every dump.
func TestCollectRuntimeEnvEmptyWhenNoGatesSet(t *testing.T) {
	for _, name := range conf.SupportEnvAllowlist() {
		t.Setenv(name, "")
	}

	env := newTestCollector().collectRuntimeEnv(conf.SupportEnvAllowlist())
	assert.Empty(t, env)

	encoded, err := json.Marshal(SystemInfo{RuntimeEnv: env})
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "runtime_env")
}

// TestCollectSystemInfoIncludesRuntimeEnv checks the collector actually wires
// the helper in, since the helper being correct in isolation is not what the
// support dump depends on.
func TestCollectSystemInfoIncludesRuntimeEnv(t *testing.T) {
	t.Setenv(conf.EnvNativeOpusEncoder, "native")

	c := &Collector{}
	info := c.collectSystemInfo()

	assert.Equal(t, "native", info.RuntimeEnv[conf.EnvNativeOpusEncoder])
}

package backup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestSanitizeConfigStripsEverySecret asserts that sanitizeConfig removes every
// credential before the config is written into a backup archive.
//
// Backup archives leave the machine, so a credential that survives this
// function is a credential shipped to whatever target the user configured.
// The check is written against the SERIALIZED output rather than field by
// field, so a newly added credential that nobody remembered to register here
// fails this test instead of silently riding along: that is exactly how
// diagnostics.profiling.token was missed when it was introduced.
func TestSanitizeConfigStripsEverySecret(t *testing.T) {
	t.Parallel()

	// Each value is distinctive so finding it in the output is unambiguous.
	secrets := map[string]string{
		"basic auth password":  "SECRET-basic-auth-password",
		"basic auth client":    "SECRET-basic-auth-client",
		"google client secret": "SECRET-google-client",
		"github client secret": "SECRET-github-client",
		"session secret":       "SECRET-session",
		"profiling token":      "SECRET-profiling-token",
		"mysql password":       "SECRET-mysql",
		"mqtt password":        "SECRET-mqtt",
		"openweather api key":  "SECRET-openweather",
	}

	settings := &conf.Settings{}
	settings.Security.BasicAuth.Password = secrets["basic auth password"]
	settings.Security.BasicAuth.ClientSecret = secrets["basic auth client"]
	settings.Security.GoogleAuth.ClientSecret = secrets["google client secret"]
	settings.Security.GithubAuth.ClientSecret = secrets["github client secret"]
	settings.Security.SessionSecret = secrets["session secret"]
	settings.Diagnostics.Profiling.Token = secrets["profiling token"]
	settings.Output.MySQL.Password = secrets["mysql password"]
	settings.Realtime.MQTT.Password = secrets["mqtt password"]
	settings.Realtime.Weather.OpenWeather.APIKey = secrets["openweather api key"]

	// Prove the setup actually plants every secret, so a renamed field cannot
	// turn this into a test that passes because it asserts on nothing.
	original, err := yaml.Marshal(settings)
	require.NoError(t, err)
	for name, value := range secrets {
		require.Contains(t, string(original), value,
			"test setup failed to plant the %s", name)
	}

	sanitized := sanitizeConfig(settings)
	require.NotNil(t, sanitized)

	rendered, err := yaml.Marshal(sanitized)
	require.NoError(t, err)

	for name, value := range secrets {
		assert.NotContains(t, string(rendered), value,
			"the %s must not reach a backup archive", name)
	}

	// The original must be untouched: callers keep using it after sanitizing.
	untouched, err := yaml.Marshal(settings)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(untouched),
		"sanitizeConfig must not mutate the config it was given")
}

// TestSanitizeConfigKeepsNonSecrets guards against over-redaction: a backup's
// config copy is only useful if the non-sensitive settings survive.
func TestSanitizeConfigKeepsNonSecrets(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.Main.Name = "test-node"
	settings.Diagnostics.Profiling.Enabled = true
	settings.Diagnostics.Profiling.Token = "SECRET-profiling-token"

	sanitized := sanitizeConfig(settings)
	require.NotNil(t, sanitized)

	assert.Equal(t, "test-node", sanitized.Main.Name)
	assert.True(t, sanitized.Diagnostics.Profiling.Enabled,
		"whether profiling is on is diagnostic signal, not a secret")
	assert.Empty(t, sanitized.Diagnostics.Profiling.Token)

	rendered, err := yaml.Marshal(sanitized)
	require.NoError(t, err)
	assert.NotContains(t, string(rendered), "SECRET-",
		"no planted secret should survive")
}

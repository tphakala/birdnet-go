package imageprovider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestAppUserAgent_DoesNotLatchAVersionlessString covers the behaviour the
// memoization exists for.
//
// The provider used to build its User-Agent once at construction, so one built
// before main.go published Version sent "BirdNETGo/unknown" for the whole
// process lifetime. appUserAgent memoizes only a non-empty version, so an early
// call cannot poison the value.
//
// No t.Parallel(): mutates the settings global and the package-level memo.
func TestAppUserAgent_DoesNotLatchAVersionlessString(t *testing.T) {
	prevSettings := conf.GetSettings()
	prevUA := cachedAppUserAgent.Load()
	t.Cleanup(func() {
		conftest.SetTestSettings(prevSettings)
		cachedAppUserAgent.Store(prevUA)
	})

	cachedAppUserAgent.Store(nil)

	versionless := conftest.NewTestSettings().Build()
	versionless.Version = ""
	conftest.SetTestSettings(versionless)

	early := appUserAgent()
	require.Contains(t, early, userAgentName)
	assert.Contains(t, early, "unknown", "with no version published the token is a placeholder")
	assert.Nil(t, cachedAppUserAgent.Load(), "a placeholder must not be memoized")

	published := conftest.NewTestSettings().Build()
	published.Version = "1.2.3-test"
	conftest.SetTestSettings(published)

	assert.Contains(t, appUserAgent(), "1.2.3-test",
		"the User-Agent must pick up the version once it is published")
	assert.NotContains(t, appUserAgent(), "unknown")
	assert.Equal(t, userAgentName, strings.Split(appUserAgent(), "/")[0])
}

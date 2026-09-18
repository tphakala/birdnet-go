package useragent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestDoesNotLatchAVersionlessString covers the behaviour the memoization exists
// for.
//
// A provider used to build its User-Agent once at construction, so one built
// before main.go published Version sent "unknown" as its version for the whole
// process lifetime. Only a non-empty version is memoized, so an early call
// cannot poison the value.
//
// No t.Parallel(): mutates the settings global and the package-level memos.
func TestDoesNotLatchAVersionlessString(t *testing.T) {
	for _, tc := range []struct {
		name  string
		memo  *cached
		get   func() string
		token string
	}{
		{"Product", &productCache, Product, ProductName},
		{"PoliteBot", &politeBotCache, PoliteBot, PoliteBotName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prevSettings := conf.GetSettings()
			prev := tc.memo.value.Load()
			t.Cleanup(func() {
				conftest.SetTestSettings(prevSettings)
				tc.memo.value.Store(prev)
			})
			tc.memo.value.Store(nil)

			versionless := conftest.NewTestSettings().Build()
			versionless.Version = ""
			conftest.SetTestSettings(versionless)

			early := tc.get()
			require.Contains(t, early, tc.token)
			assert.Contains(t, early, UnknownVersion, "with no version published the token is a placeholder")
			assert.Nil(t, tc.memo.value.Load(), "a placeholder must not be memoized")

			published := conftest.NewTestSettings().Build()
			published.Version = "1.2.3-test"
			conftest.SetTestSettings(published)

			assert.Contains(t, tc.get(), "1.2.3-test",
				"the User-Agent must pick up the version once it is published")
			assert.NotContains(t, tc.get(), UnknownVersion)
			assert.NotNil(t, tc.memo.value.Load(), "a real version must be memoized")
		})
	}
}

// TestFormsStayDistinct pins the two hard-won constraints that are the reason
// this package exports two forms rather than one. Both were established against
// the live Wikimedia edge and cannot be re-derived from the source.
func TestFormsStayDistinct(t *testing.T) {
	t.Parallel()

	product := BuildProduct("1.2.3")
	politeBot := BuildPoliteBot("1.2.3")

	// The edge refuses any User-Agent whose LEADING token is "birdnet-go".
	leading := strings.ToLower(strings.Split(product, "/")[0])
	assert.NotEqual(t, "birdnet-go", leading, "the product form must not lead with the hyphenated name")

	// UA-policy enforcement (phab T400119) 403s a bare "App/1.0 (url)" agent, so
	// the polite-bot form must keep its Mozilla-compatible wrapper.
	assert.True(t, strings.HasPrefix(politeBot, "Mozilla/5.0 (compatible; "),
		"the polite-bot form must keep its wrapper, got %q", politeBot)

	// Both must carry a contact URL, which is what the policy relies on to reach
	// the operator — and it must be the branded one so a fork identifies itself.
	for _, ua := range []string{product, politeBot} {
		assert.Contains(t, ua, branding.RepoURL())
		assert.Contains(t, ua, "1.2.3")
	}
}

func TestBuildSubstitutesUnknownVersion(t *testing.T) {
	t.Parallel()
	assert.Contains(t, BuildProduct(""), "/"+UnknownVersion+" ")
	assert.Contains(t, BuildPoliteBot(""), "/"+UnknownVersion+";")
}

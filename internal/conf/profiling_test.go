package conf

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// settingsWithProfiling builds settings with the profiling section configured
// and, optionally, an authentication provider enabled.
func settingsWithProfiling(enabled bool, token string, withAuth bool) *Settings {
	settings := &Settings{}
	settings.Diagnostics.Profiling.Enabled = enabled
	settings.Diagnostics.Profiling.Token = token
	settings.Security.BasicAuth.Enabled = withAuth
	return settings
}

// TestEnsureProfilingToken covers when a token is minted and, more importantly,
// when it is not. Minting one on an instance that already has authentication
// would add a second, weaker way into the profiling endpoints for no benefit.
func TestEnsureProfilingToken(t *testing.T) {
	tests := []struct {
		name          string
		settings      *Settings
		wantGenerated bool
	}{
		{
			name:          "enabled without an auth provider mints a token",
			settings:      settingsWithProfiling(true, "", false),
			wantGenerated: true,
		},
		{
			name:     "disabled mints nothing",
			settings: settingsWithProfiling(false, "", false),
		},
		{
			name:     "an existing token is left alone",
			settings: settingsWithProfiling(true, "already-set", false),
		},
		{
			name:     "a configured auth provider means no token is needed",
			settings: settingsWithProfiling(true, "", true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// viper.Set is global state, so these cannot run in parallel.
			viper.Reset()
			t.Cleanup(viper.Reset)

			before := tt.settings.Diagnostics.Profiling.Token

			generated, err := EnsureProfilingToken(tt.settings)
			require.NoError(t, err)
			assert.Equal(t, tt.wantGenerated, generated)

			got := tt.settings.Diagnostics.Profiling.Token
			if !tt.wantGenerated {
				assert.Equal(t, before, got, "the token must not change")
				return
			}

			assert.NotEmpty(t, got)
			// Length is derived, not hardcoded, so raising the entropy in
			// GenerateRandomSecret does not silently leave this assertion
			// pinning the old size.
			expected, err := GenerateRandomSecret()
			require.NoError(t, err)
			assert.Len(t, got, len(expected),
				"a token should carry the same entropy as any other generated secret")

			// The token is deliberately NOT mirrored into viper: nothing in
			// this repository persists through viper (SaveYAMLConfig marshals
			// the struct), and viper.Set is not goroutine-safe. Asserting the
			// absence keeps a well-meaning "mirror it like ensureSessionSecret
			// does" from creeping back in.
			assert.Empty(t, viper.GetString("diagnostics.profiling.token"),
				"the token must not be staged into viper")
		})
	}
}

// TestEnsureProfilingTokenIsUnique guards against a fixed or seeded value: two
// instances must never end up sharing a credential.
func TestEnsureProfilingTokenIsUnique(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	first := settingsWithProfiling(true, "", false)
	second := settingsWithProfiling(true, "", false)

	generated, err := EnsureProfilingToken(first)
	require.NoError(t, err)
	require.True(t, generated)

	generated, err = EnsureProfilingToken(second)
	require.NoError(t, err)
	require.True(t, generated)

	assert.NotEqual(t, first.Diagnostics.Profiling.Token, second.Diagnostics.Profiling.Token)
}

// TestEnsureProfilingTokenIsIdempotent verifies a second pass over already
// backfilled settings is a no-op, so restarts do not churn the config file.
func TestEnsureProfilingTokenIsIdempotent(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	settings := settingsWithProfiling(true, "", false)

	generated, err := EnsureProfilingToken(settings)
	require.NoError(t, err)
	require.True(t, generated)
	minted := settings.Diagnostics.Profiling.Token

	generated, err = EnsureProfilingToken(settings)
	require.NoError(t, err)
	assert.False(t, generated, "a second pass must report no change")
	assert.Equal(t, minted, settings.Diagnostics.Profiling.Token,
		"the token must be stable across restarts; a profiling session outlives one")
}

// TestIsAuthProviderConfigured pins the predicate that decides whether the
// profiling routes rely on the auth middleware or on their own token.
func TestIsAuthProviderConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*Settings)
		expected bool
	}{
		{
			name:   "nothing configured",
			mutate: func(*Settings) {},
		},
		{
			name:     "basic auth enabled",
			mutate:   func(s *Settings) { s.Security.BasicAuth.Enabled = true },
			expected: true,
		},
		{
			name: "fully configured OAuth provider",
			mutate: func(s *Settings) {
				s.Security.OAuthProviders = []OAuthProviderConfig{{
					Provider:     "google",
					Enabled:      true,
					ClientID:     "id",
					ClientSecret: "secret",
				}}
			},
			expected: true,
		},
		{
			name: "OAuth provider enabled but missing credentials does not count",
			mutate: func(s *Settings) {
				s.Security.OAuthProviders = []OAuthProviderConfig{{
					Provider: "google",
					Enabled:  true,
				}}
			},
		},
		{
			name: "fully credentialed but disabled OAuth provider does not count",
			mutate: func(s *Settings) {
				s.Security.OAuthProviders = []OAuthProviderConfig{{
					Provider:     "google",
					ClientID:     "id",
					ClientSecret: "secret",
				}}
			},
		},
		{
			name: "subnet bypass is a per-request concern and does not count",
			mutate: func(s *Settings) {
				s.Security.AllowSubnetBypass.Enabled = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &Settings{}
			tt.mutate(settings)
			assert.Equal(t, tt.expected, settings.IsAuthProviderConfigured())
		})
	}
}

// TestConfigTemplateShipsProfilingDisabled guards the embedded config template.
// A token committed to the repository would be identical on every install, which
// is worse than having none.
func TestConfigTemplateShipsProfilingDisabled(t *testing.T) {
	t.Parallel()

	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)

	var settings Settings
	require.NoError(t, yaml.Unmarshal(template, &settings))

	assert.False(t, settings.Diagnostics.Profiling.Enabled,
		"profiling must ship disabled")
	assert.Empty(t, settings.Diagnostics.Profiling.Token,
		"the shipped template must not carry a profiling token")
	assert.Zero(t, settings.Diagnostics.Profiling.BlockRate,
		"block profiling must ship off; sampling costs CPU whether or not a profile is ever fetched")
	assert.Zero(t, settings.Diagnostics.Profiling.MutexFraction,
		"mutex profiling must ship off for the same reason")

	// Zero is also what an absent, misspelled or misnested key produces, so
	// assert the keys are actually present and land where they are meant to.
	// Without this the two assertions above pass just as happily against a
	// template that stopped shipping them at all.
	assert.Contains(t, string(template), "blockrate:", "the template must ship the blockrate key")
	assert.Contains(t, string(template), "mutexfraction:", "the template must ship the mutexfraction key")
}

// TestResolvedRates covers the clamping the two runtime setters need.
//
// The mutex case is the one that matters. runtime.SetMutexProfileFraction reads
// the current fraction and leaves it alone when handed a negative, so passing a
// negative config value through unclamped would quietly keep mutex profiling at
// whatever it already was rather than turning it off.
func TestResolvedRates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		blockRate         int
		mutexFraction     int
		wantBlockRate     int
		wantMutexFraction int
	}{
		{
			name: "unset resolves to off",
		},
		{
			name:              "an arbitrary user value passes through",
			blockRate:         50000,
			mutexFraction:     250,
			wantBlockRate:     50000,
			wantMutexFraction: 250,
		},
		{
			name:              "the recommended rates pass through unchanged",
			blockRate:         RecommendedBlockProfileRate,
			mutexFraction:     RecommendedMutexProfileFraction,
			wantBlockRate:     RecommendedBlockProfileRate,
			wantMutexFraction: RecommendedMutexProfileFraction,
		},
		{
			name:              "a block rate above the ceiling is clamped",
			blockRate:         maxBlockProfileRate + 1,
			mutexFraction:     1,
			wantBlockRate:     maxBlockProfileRate,
			wantMutexFraction: 1,
		},
		{
			name:              "math.MaxInt block rate is clamped rather than wrapping negative",
			blockRate:         math.MaxInt,
			wantBlockRate:     maxBlockProfileRate,
			wantMutexFraction: 0,
		},
		{
			name:              "the mutex fraction has no ceiling; a huge value degrades to never sampling",
			mutexFraction:     math.MaxInt,
			wantMutexFraction: math.MaxInt,
		},
		{
			name:              "rate 1 is honoured rather than overridden",
			blockRate:         1,
			mutexFraction:     1,
			wantBlockRate:     1,
			wantMutexFraction: 1,
		},
		{
			name:          "negatives clamp to off",
			blockRate:     -1,
			mutexFraction: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			profiling := &ProfilingConfig{
				BlockRate:     tt.blockRate,
				MutexFraction: tt.mutexFraction,
			}

			assert.Equal(t, tt.wantBlockRate, profiling.ResolvedBlockRate())
			assert.Equal(t, tt.wantMutexFraction, profiling.ResolvedMutexFraction())
		})
	}
}

// TestResolvedRatesNilReceiver pins the nil guard.
//
// Not reachable from production today: every caller takes the address of a
// ProfilingConfig field on a non-nil Settings, so the pointer cannot be nil.
// These are exported methods on an exported config type, though, so the guard
// is the contract for any future caller that holds the section on its own, and
// a panic in a diagnostics accessor would be a poor trade for two comparisons.
func TestResolvedRatesNilReceiver(t *testing.T) {
	t.Parallel()

	var profiling *ProfilingConfig

	assert.Zero(t, profiling.ResolvedBlockRate())
	assert.Zero(t, profiling.ResolvedMutexFraction())
}

// TestSamplingDefaultsAreNotRateOne is the regression guard for the change that
// introduced these constants. Both rates used to be set to 1 by debug: true,
// which records essentially every blocking and contention event. Anyone tempted
// to "simplify" these constants back toward the Go documentation's rate 1 has to
// delete this test to do it.
func TestSamplingDefaultsAreNotRateOne(t *testing.T) {
	t.Parallel()

	assert.Greater(t, RecommendedBlockProfileRate, 1,
		"the recommended block rate must sample, not record every blocking event")
	assert.Greater(t, RecommendedMutexProfileFraction, 1,
		"the recommended mutex fraction must sample, not record every contention event")
}

// TestRecommendedRatesMatchShippedConfig keeps the constants in step with the
// numbers quoted to users in the shipped config template.
//
// It reads config.yaml rather than restating the constants, which is what makes
// it a drift guard rather than a second copy of the same literal: editing
// either the constant or the template alone fails it.
//
// Every published copy now has a guard; see the enumeration on
// TestRecommendedRatesMatchProfilingDoc for which one covers which. In
// particular TestSchemaUpToDate is NOT one of them for the struct comments: it
// generates the schema from those comments and compares against the committed
// file, so the comment is on both sides. That copy is covered by
// TestRecommendedRatesMatchSchemaDescription.
//
// The failure messages below still name doc/PROFILING.md, which is now belt and
// braces rather than the only warning: a change to the constant trips this test,
// the doc test and the schema test together.
func TestRecommendedRatesMatchShippedConfig(t *testing.T) {
	t.Parallel()

	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)

	// HasSuffix, scoped to the line carrying each key, rather than a whole-file
	// Contains. Two distinct prefix hazards make the obvious spelling useless:
	// across keys, "Try 100" is contained in the blockrate line's "Try 10000",
	// so the mutex guard passes with its comment deleted outright; and within a
	// key, "Try 100" is a prefix of "Try 1000", so a drift that lengthens the
	// number is invisible. Both template lines end with the number, so
	// anchoring at the end closes both.
	assert.True(t,
		strings.HasSuffix(shippedLineFor(t, template, "blockrate:"),
			"Try "+strconv.Itoa(RecommendedBlockProfileRate)),
		"config.yaml must recommend the same block rate as RecommendedBlockProfileRate; update doc/PROFILING.md too")
	assert.True(t,
		strings.HasSuffix(shippedLineFor(t, template, "mutexfraction:"),
			"Try "+strconv.Itoa(RecommendedMutexProfileFraction)),
		"config.yaml must recommend the same mutex fraction as RecommendedMutexProfileFraction; update doc/PROFILING.md too")
}

// TestViperDecodesProfilingSection pins the field-name matching contract that
// actually governs config.yaml, which is NOT the yaml tag.
//
// It exercises that contract on a local viper without conf.Load's DurationDecodeHook,
// because the hook plays no part in how a key is matched to a field. It is
// therefore a targeted guard on the naming invariant, not an end-to-end test of
// the loader.
//
// conf.Load calls viper.Unmarshal, and viper passes no TagName, so mapstructure
// defaults to looking for `mapstructure` tags. ProfilingConfig has none, so
// matching falls back to the Go FIELD NAME compared case-insensitively against
// the lowercased config key. That makes `strings.ToLower(FieldName) == yamlKey`
// a load-bearing invariant of this struct, held today only by coincidence of
// naming, and invisible to anyone reading the tags.
//
// Renaming BlockRate to BlockRateNanos, keeping both tags byte-identical, was
// tried and reverted: it silently decoded to 0 for every user with a configured
// blockrate, while SaveSettings (yaml tags) and the settings API (json tags)
// both kept working, so the value round-tripped correctly and only went missing
// across a restart. Nothing in the suite caught it, because every other test
// assigns these fields programmatically and never drives viper.
//
// The rate must be NON-ZERO here. Zero is what a failed match produces, so a
// zero fixture would pass just as happily against a field viper cannot find.
func TestViperDecodesProfilingSection(t *testing.T) {
	// viper.Reset is global state, so this cannot run in parallel.
	viper.Reset()
	t.Cleanup(viper.Reset)

	const configYAML = `
diagnostics:
  profiling:
    enabled: true
    token: decoder-probe
    blockrate: 10000
    mutexfraction: 100
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(configYAML)))

	var settings Settings
	require.NoError(t, v.Unmarshal(&settings))

	got := settings.Diagnostics.Profiling
	assert.True(t, got.Enabled, "enabled must decode")
	assert.Equal(t, "decoder-probe", got.Token, "token must decode")
	assert.Equal(t, 10000, got.BlockRate,
		"blockrate must decode; if this is 0 the Go field name no longer matches the config key, and viper cannot find it")
	assert.Equal(t, 100, got.MutexFraction,
		"mutexfraction must decode; same field-name contract as blockrate")
}

// profilingDocPath is doc/PROFILING.md relative to this package's directory,
// which is the working directory `go test` uses.
const profilingDocPath = "../../doc/PROFILING.md"

// TestRecommendedRatesMatchProfilingDoc guards the doc/PROFILING.md copies of
// the two recommended rates.
//
// The numbers are published in SIX places, not four, and the enumeration is
// worth stating because the obvious count misses the one that mattered:
//
//  1. internal/conf/profiling.go     the constants themselves, the source
//  2. internal/conf/config.go        the ProfilingConfig field comments
//  3. internal/conf/config.yaml      guarded by TestRecommendedRatesMatchShippedConfig
//  4. config.schema.json            ) generated FROM 2, byte-compared by
//  5. doc/wiki/configuration-reference.md ) cmd/gen-schema's TestSchemaUpToDate
//  6. doc/PROFILING.md               guarded here, twice per number
//
// TestSchemaUpToDate does NOT pin 2: it regenerates 4 and 5 from that comment
// and compares against the committed files, so the comment sits on both sides of
// the equality and can say anything. Verified by editing the comment to 20000
// and watching every test stay green while the wiki reference told users 20000.
// TestRecommendedRatesMatchSchemaDescription below closes that, by asserting on
// the generated artifact, which pins the comment transitively.
//
// This test matches on the key rather than the bare number, and asserts on every
// occurrence rather than the first, so neither a stale copy left behind nor a
// value that merely shares a prefix with the right one survives. Requiring at
// least one match is what makes deleting the line fail too.
//
// Scope, stated because the doc comment is otherwise easy to over-trust: it sees
// the two example syntaxes the doc actually uses, a YAML key at the start of a
// line and a JSON key. A number written into prose, a markdown table, or a list
// item is invisible to it.
func TestRecommendedRatesMatchProfilingDoc(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(profilingDocPath)
	require.NoError(t, err, "doc/PROFILING.md must be readable from the package directory")
	text := string(doc)

	tests := []struct {
		name    string
		pattern *regexp.Regexp
		want    int
	}{
		{"yaml blockrate", regexp.MustCompile(`(?m)^[ \t]*blockrate:[ \t]*(\d+)`), RecommendedBlockProfileRate},
		{"json blockRate", regexp.MustCompile(`"blockRate"[ \t]*:[ \t]*(\d+)`), RecommendedBlockProfileRate},
		{"yaml mutexfraction", regexp.MustCompile(`(?m)^[ \t]*mutexfraction:[ \t]*(\d+)`), RecommendedMutexProfileFraction},
		{"json mutexFraction", regexp.MustCompile(`"mutexFraction"[ \t]*:[ \t]*(\d+)`), RecommendedMutexProfileFraction},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			matches := tt.pattern.FindAllStringSubmatch(text, -1)
			require.NotEmpty(t, matches,
				"doc/PROFILING.md must still quote %s; the drift guard is useless if the example is gone", tt.name)

			for _, m := range matches {
				got, err := strconv.Atoi(m[1])
				require.NoError(t, err)
				assert.Equal(t, tt.want, got,
					"doc/PROFILING.md quotes %s as %d; update the doc to match the constant", tt.name, got)
			}
		})
	}
}

// TestRecommendedRatesMatchSchemaDescription closes the copy in the
// ProfilingConfig field comments, which was the one genuinely unguarded
// publication of these numbers.
//
// It asserts on config.schema.json rather than on the Go comment because the
// schema is generated verbatim from that comment and byte-compared by
// TestSchemaUpToDate. Pinning the generated artifact therefore pins the comment,
// and it does so through the file users and API clients actually read. Editing
// the comment alone now fails here; editing it and regenerating fails here too.
func TestRecommendedRatesMatchSchemaDescription(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "config.schema.json"))
	require.NoError(t, err, "config.schema.json must be readable from the package directory")

	var doc struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(schema, &doc))

	profiling := doc.Defs["ProfilingConfig"].Properties
	require.NotEmpty(t, profiling, "the schema must still define ProfilingConfig")

	for _, tt := range []struct {
		key  string
		want int
	}{
		{"blockrate", RecommendedBlockProfileRate},
		{"mutexfraction", RecommendedMutexProfileFraction},
	} {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()

			description := profiling[tt.key].Description
			require.NotEmpty(t, description, "the schema must still describe %s", tt.key)
			// Anchored on the trailing period so a shorter number that is a
			// prefix of the right one ("1000" inside "10000") cannot satisfy it.
			assert.Contains(t, description,
				"Recommended starting point: "+strconv.Itoa(tt.want)+".",
				"the %s description in config.schema.json must recommend the constant's value; it is generated from the ProfilingConfig field comment in config.go, so fix the comment and re-run 'task generate-schema'", tt.key)
		})
	}
}

// shippedLineFor returns the single line of the embedded template that declares
// the given key, failing the test if it is absent or ambiguous.
func shippedLineFor(t *testing.T, template []byte, key string) string {
	t.Helper()

	var found []string
	for line := range strings.Lines(string(template)) {
		if strings.Contains(line, key) {
			// Trim both \r and \n: on a Windows checkout the embedded template
			// carries CRLF line endings, and a lingering trailing \r would break
			// the HasSuffix checks in TestRecommendedRatesMatchShippedConfig.
			found = append(found, strings.TrimRight(line, "\r\n"))
		}
	}

	require.Len(t, found, 1, "expected exactly one line declaring %q in the shipped config template", key)
	return found[0]
}

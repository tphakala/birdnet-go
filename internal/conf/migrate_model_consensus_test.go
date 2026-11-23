package conf

import (
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestMigrateModelConsensusRules pins the version 4 one-shot conversion of the
// removed allspecies, listedspecies, species, minmodels and alleligible keys into at
// most one rule.
func TestMigrateModelConsensusRules(t *testing.T) {
	t.Parallel()

	existing := ModelConsensusRule{Species: []string{"Fulica atra"}, Models: []string{"perch_v2"}}
	tests := []struct {
		name    string
		version int
		rules   []ModelConsensusRule
		legacy  legacyModelConsensus
		want    []ModelConsensusRule
	}{
		{"all off makes no rule", 3, nil, legacyModelConsensus{MinModels: 3}, nil},
		{"allspecies makes an every-species rule carrying the settings", 3, nil,
			legacyModelConsensus{AllSpecies: true, Species: []string{"Parus major"}, MinModels: 3, AllEligible: true},
			[]ModelConsensusRule{{MinModels: 3, AllEligible: true}}},
		{"allspecies wins over listedspecies", 3, nil,
			legacyModelConsensus{AllSpecies: true, ListedSpecies: true, Species: []string{"Parus major"}, MinModels: 2},
			[]ModelConsensusRule{{MinModels: 2}}},
		{"listedspecies makes a species rule", 3, nil,
			legacyModelConsensus{ListedSpecies: true, Species: []string{"Parus major", "Fulica atra"}, MinModels: 4},
			[]ModelConsensusRule{{Species: []string{"Parus major", "Fulica atra"}, MinModels: 4}}},
		{"listedspecies with an empty list makes no rule", 3, nil,
			legacyModelConsensus{ListedSpecies: true, MinModels: 2}, nil},
		{"listedspecies with only blank entries makes no rule", 3, nil,
			legacyModelConsensus{ListedSpecies: true, Species: []string{"", "  "}, MinModels: 2}, nil},
		{"blank entries are dropped and the others kept as written", 3, nil,
			legacyModelConsensus{ListedSpecies: true, Species: []string{" Parus major", ""}, MinModels: 2},
			[]ModelConsensusRule{{Species: []string{" Parus major"}, MinModels: 2}}},
		{"a list with listedspecies off makes no rule", 3, nil,
			legacyModelConsensus{Species: []string{"Parus major"}, MinModels: 2}, nil},
		{"minmodels 0 becomes the default", 3, nil,
			legacyModelConsensus{AllSpecies: true},
			[]ModelConsensusRule{{MinModels: DefaultModelConsensusMinModels}}},
		{"rules already present are kept and the legacy rule is appended", 3,
			[]ModelConsensusRule{existing},
			legacyModelConsensus{AllSpecies: true, MinModels: 2},
			[]ModelConsensusRule{existing, {MinModels: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{ConfigVersion: tt.version}
			s.Realtime.ModelConsensus.Rules = tt.rules

			changed := s.MigrateModelConsensusRules(func() (legacyModelConsensus, error) { return tt.legacy, nil })

			assert.True(t, changed)
			assert.Equal(t, tt.want, s.Realtime.ModelConsensus.Rules)
			assert.Equal(t, configVersionModelConsensusRules, s.ConfigVersion)
		})
	}

	t.Run("version 4 is untouched and does not read", func(t *testing.T) {
		t.Parallel()
		s := &Settings{ConfigVersion: configVersionModelConsensusRules}
		changed := s.MigrateModelConsensusRules(func() (legacyModelConsensus, error) {
			t.Error("read must not be called at the current version")
			return legacyModelConsensus{AllSpecies: true}, nil
		})
		assert.False(t, changed)
		assert.Empty(t, s.Realtime.ModelConsensus.Rules)
	})

	t.Run("a read error makes no rule, warns and still stamps", func(t *testing.T) {
		t.Parallel()
		s := &Settings{ConfigVersion: 3}
		changed := s.MigrateModelConsensusRules(func() (legacyModelConsensus, error) {
			return legacyModelConsensus{}, errors.NewStd("decode failed")
		})
		assert.True(t, changed)
		assert.Empty(t, s.Realtime.ModelConsensus.Rules)
		assert.Equal(t, configVersionModelConsensusRules, s.ConfigVersion)
		assert.Contains(t, warningsText(s), "could not be read")
	})
}

// TestMigrateModelConsensusRules_ThroughLoad runs the conversion through the full
// Load pipeline, which pins the decode of the removed keys with Load's decode hook,
// the persist step and the round trip of rules. Not parallel: Load publishes the
// global snapshot.
func TestMigrateModelConsensusRules_ThroughLoad(t *testing.T) {
	t.Run("version 3 listedspecies block becomes one rule on disk and in memory", func(t *testing.T) {
		cfg := "configversion: 3\ndebug: false\nrealtime:\n  modelconsensus:\n    listedspecies: true\n    species:\n      - Parus major\n      - Fulica atra\n    minmodels: 3\n    alleligible: true\n"
		settings, configPath := loadConfigFromYAML(t, cfg)

		// An empty Models list is nil in memory and [] after a YAML round trip, so compare fields.
		assertRule := func(rules []ModelConsensusRule) {
			t.Helper()
			require.Len(t, rules, 1)
			assert.Equal(t, []string{"Parus major", "Fulica atra"}, rules[0].Species)
			assert.Empty(t, rules[0].Models)
			assert.Equal(t, 3, rules[0].MinModels)
			assert.True(t, rules[0].AllEligible)
		}
		assertRule(settings.Realtime.ModelConsensus.Rules)
		assert.Equal(t, currentConfigVersion, settings.ConfigVersion)

		persisted := readPersistedSettings(t, configPath)
		assertRule(persisted.Realtime.ModelConsensus.Rules)
		assert.Equal(t, currentConfigVersion, persisted.ConfigVersion)

		data, err := os.ReadFile(configPath) //nolint:gosec // test-controlled path
		require.NoError(t, err)
		assert.NotContains(t, string(data), "listedspecies", "the legacy keys must leave the file")
		assert.NotContains(t, string(data), "allspecies")

		viper.Reset()
		ConfigPath = configPath
		reloaded, err := Load()
		require.NoError(t, err)
		assertRule(reloaded.Realtime.ModelConsensus.Rules)
	})

	t.Run("version 3 listedspecies with a comma scalar species decodes like the shipped binary", func(t *testing.T) {
		cfg := "configversion: 3\ndebug: false\nrealtime:\n  modelconsensus:\n    listedspecies: true\n    species: \"Parus major,Fulica atra\"\n    minmodels: 2\n"
		settings, _ := loadConfigFromYAML(t, cfg)
		require.Len(t, settings.Realtime.ModelConsensus.Rules, 1)
		assert.Equal(t, []string{"Parus major", "Fulica atra"}, settings.Realtime.ModelConsensus.Rules[0].Species)
	})

	t.Run("version 4 file with hand-added legacy keys makes no rule", func(t *testing.T) {
		cfg := "configversion: 4\ndebug: false\nrealtime:\n  modelconsensus:\n    allspecies: true\n    minmodels: 2\n"
		settings, _ := loadConfigFromYAML(t, cfg)
		assert.Empty(t, settings.Realtime.ModelConsensus.Rules)
	})
}

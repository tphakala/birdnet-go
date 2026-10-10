package classifier

import (
	"iter"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// legacyEffectiveEnabledModels is a FROZEN copy of the pre-collapse effectiveEnabledModels
// (BirdNET v2.4 always yielded first, deduplicated). It is the load-order oracle for the
// models-enabled-authoritative migration: after Part A moves v2.4 to the front of
// models.enabled, walking the config-order enabledModels over the migrated list must
// reproduce this exact distinct load order for every install. That equivalence is what makes
// collapsing effectiveEnabledModels into enabledModels zero-regression, so this copy is kept
// verbatim even after the production effectiveEnabledModels is deleted.
func legacyEffectiveEnabledModels(settings *conf.Settings) iter.Seq[enabledModel] {
	return func(yield func(enabledModel) bool) {
		v24ConfigID := conf.ModelIDBirdNET
		for _, configID := range settings.Models.Enabled {
			if registryID, _ := ResolveConfigModelID(configID); registryID == RegistryIDBirdNETV24 {
				v24ConfigID = configID
				break
			}
		}
		if !yield(enabledModel{configID: v24ConfigID, registryID: RegistryIDBirdNETV24, known: true}) {
			return
		}
		for _, configID := range settings.Models.Enabled {
			registryID, known := ResolveConfigModelID(configID)
			if known && registryID == RegistryIDBirdNETV24 {
				continue
			}
			if !yield(enabledModel{configID: configID, registryID: registryID, known: known}) {
				return
			}
		}
	}
}

// distinctKnownOrder collects the first-occurrence order of distinct KNOWN registry IDs from
// a resolved-enable sequence. This is the effective load order the callers realize:
// loadEnabledModels skips an already-registered model, computeThreadAllocation tracks a
// seen-set, and both ignore unknown IDs.
func distinctKnownOrder(seq iter.Seq[enabledModel]) []string {
	var order []string
	seen := make(map[string]bool)
	for m := range seq {
		if !m.known || seen[m.registryID] {
			continue
		}
		seen[m.registryID] = true
		order = append(order, m.registryID)
	}
	return order
}

// TestEnabledModels_MigratedListMatchesLegacyEffectiveOrder pins the zero-regression core of
// the collapse: for every list shape, the distinct load order produced by walking the
// config-order enabledModels over the MIGRATED list equals the order the frozen legacy
// effectiveEnabledModels produced over the ORIGINAL list. Written before the collapse so the
// oracle is the real pre-collapse behavior.
func TestEnabledModels_MigratedListMatchesLegacyEffectiveOrder(t *testing.T) {
	t.Parallel()

	shapes := [][]string{
		nil,
		{},
		{"birdnet"},
		{"perch_v2"},
		{"perch_v2", "birdnet"},
		{"birdnet", "perch_v2"},
		{"perch_v2", "birdnet", "birdnet_v3.0"},
		{"birdnet", "perch_v2", "birdnet_v3.0"},
		{"perch_v2", "birdnet-v2.4"},            // catalog spelling
		{"perch_v2", "birdnet", "birdnet-v2.4"}, // duplicate v2.4 spellings, canonical first
		{"birdnet-v2.4", "perch_v2", "birdnet"}, // duplicate v2.4 spellings, catalog first
	}

	for _, shape := range shapes {
		// Legacy effective load order over the ORIGINAL list.
		orig := &conf.Settings{}
		orig.Models.Enabled = slices.Clone(shape)
		want := distinctKnownOrder(legacyEffectiveEnabledModels(orig))

		// Post-migration config-order load order over the MIGRATED list (the collapse target).
		migrated := &conf.Settings{ConfigVersion: 1}
		migrated.Models.Enabled = slices.Clone(shape)
		migrated.MigrateModelsEnabledAuthoritative()
		got := distinctKnownOrder(enabledModels(migrated))

		assert.Equalf(t, want, got,
			"shape %v: migrated config-order load must equal legacy effective load order", shape)
	}
}

// TestModelRegistry_V24AliasesMatchConf keeps the two BirdNET v2.4 config spellings in
// lockstep across the package boundary. isBirdNETV24ConfigID in the conf package hardcodes
// exactly {ModelIDBirdNET, ModelIDBirdNETCatalog} because conf cannot import the classifier
// registry (import cycle). If a v2.4 alias is added to or removed from the registry here, this
// test fails as the reminder to update isBirdNETV24ConfigID (and the migration that depends on
// it) to match.
func TestModelRegistry_V24AliasesMatchConf(t *testing.T) {
	t.Parallel()

	info, ok := ModelRegistry[RegistryIDBirdNETV24]
	require.True(t, ok, "the BirdNET v2.4 registry entry must exist")
	assert.ElementsMatch(t, []string{conf.ModelIDBirdNET, conf.ModelIDBirdNETCatalog}, info.ConfigAliases,
		"conf.isBirdNETV24ConfigID recognizes exactly these two v2.4 spellings; keep them in lockstep with the registry")
}

// TestModelIDEnabled_ConfigAuthoritative_NoImplicitV24 pins the collapse of
// effectiveEnabledModels into config-order enabledModels: models.enabled is authoritative, so
// v2.4 is enabled only when the list names it. Before the collapse effectiveEnabledModels
// prepended v2.4, so it was implicitly enabled regardless of the list. Not parallel: publishes
// the global settings snapshot.
func TestModelIDEnabled_ConfigAuthoritative_NoImplicitV24(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{conf.ModelIDPerchV2} // v2.4 NOT listed
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := &Orchestrator{Settings: settings, models: map[string]*modelEntry{}, modelRSS: make(map[string]int64)}
	assert.False(t, o.modelIDEnabled(RegistryIDBirdNETV24),
		"v2.4 is not implicitly enabled once models.enabled is authoritative")
	assert.True(t, o.modelIDEnabled(RegistryIDPerchV2), "an explicitly listed model is enabled")
}

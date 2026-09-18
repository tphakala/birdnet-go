package classifier

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// registerTestV24 registers inst as the loaded BirdNET v2.4 entry (the range-filter
// anchor and default model) in a hand-built test Orchestrator, standing in for the
// removed o.primary field so the anchor-based accessors resolve it. It creates the
// models map if needed and preserves any entries already present.
func registerTestV24(o *Orchestrator, inst ModelInstance) {
	if o.models == nil {
		o.models = map[string]*modelEntry{}
	}
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: inst}
}

// requireV24Loaded skips the test when BirdNET v2.4 is not in the loaded set. A
// real-load test builds an orchestrator through NewOrchestrator and asserts against the
// loaded v2.4 instance. Under the noembed build tag the embedded model is compiled out,
// so v2.4 fails to load and (models.enabled being authoritative, Phase 4) construction
// now SUCCEEDS at N=0 instead of returning an error. Without this skip those tests would
// fail on the absent model rather than skip, so every real-load site calls it right
// after construction.
func requireV24Loaded(t *testing.T, o *Orchestrator) {
	t.Helper()
	if !o.IsModelLoaded(RegistryIDBirdNETV24) {
		t.Skip("BirdNET v2.4 not loaded (embedded model unavailable, e.g. noembed build); skipping real-load test")
	}
}

// enableBirdNETV24 names BirdNET v2.4 in models.enabled so a real-load test (one that
// builds an orchestrator through NewOrchestrator and expects the embedded model to
// load) gets N >= 1. Before Phase 4 v2.4 was implicitly enabled; models.enabled is now
// authoritative (model de-privilege epic, Phase 4), so a test that expects v2.4 must
// name it. Tests that deliberately exercise N=0 leave the list empty instead.
func enableBirdNETV24(settings *conf.Settings) {
	settings.Models.Enabled = []string{conf.ModelIDBirdNET}
}

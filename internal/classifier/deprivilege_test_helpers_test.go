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

// requireV24Loaded guards a real-load test that builds an orchestrator through
// NewOrchestrator and asserts against the loaded v2.4 instance. When v2.4 is loaded it
// returns. When it is not, the outcome depends on the build: under the noembed tag the
// embedded model is compiled out, so v2.4 legitimately cannot load and the test SKIPS;
// on a normal (embedded) build the model is compiled in and always available, so a
// missing v2.4 is a real regression and the test FAILS with the recorded load error,
// rather than being masked by a skip.
//
// It also releases the orchestrator before skipping/failing: call sites register their
// own t.Cleanup(o.Delete()) only AFTER this call (or, in one case, delete explicitly
// later), so on the not-loaded path this Delete is the one that prevents a leak. On the
// loaded path it does not delete; the caller's cleanup owns that.
func requireV24Loaded(t *testing.T, o *Orchestrator) {
	t.Helper()
	if o.IsModelLoaded(RegistryIDBirdNETV24) {
		return
	}
	loadErr := o.LoadErrors()[RegistryIDBirdNETV24]
	if loadErr == "" {
		loadErr = "no load error was recorded"
	}
	o.Delete()
	if !hasEmbeddedModels {
		t.Skip("BirdNET v2.4 not loaded (embedded model compiled out, noembed build); skipping real-load test")
	}
	t.Fatalf("BirdNET v2.4 failed to load on an embedded build (real regression, not an environment gap): %s", loadErr)
}

// enableBirdNETV24 names BirdNET v2.4 in models.enabled so a real-load test (one that
// builds an orchestrator through NewOrchestrator and expects the embedded model to
// load) gets N >= 1. Before Phase 4 v2.4 was implicitly enabled; models.enabled is now
// authoritative (model de-privilege epic, Phase 4), so a test that expects v2.4 must
// name it. Tests that deliberately exercise N=0 leave the list empty instead.
func enableBirdNETV24(settings *conf.Settings) {
	settings.Models.Enabled = []string{conf.ModelIDBirdNET}
}

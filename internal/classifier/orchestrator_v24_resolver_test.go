package classifier

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// fakeNameResolver is a stand-in for a non-v2.4 resolver (e.g. the taxonomy resolver) so a
// test can assert the v2.4 chain rewrite preserves the relative order of other resolvers.
type fakeNameResolver struct{ tag string }

func (fakeNameResolver) Resolve(_, _ string) string { return "" }

// countV24Resolvers reports how many BirdNET v2.4 label resolvers are in the chain and
// whether OpenFauna leads it, read under the same RLock ResolveName uses.
func countV24Resolvers(o *Orchestrator) (v24 int, openfaunaLeads bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	openfaunaLeads = len(o.nameResolvers) > 0 && o.nameResolvers[0] == o.openfauna
	for _, r := range o.nameResolvers {
		if _, ok := r.(*BirdNETLabelResolver); ok {
			v24++
		}
	}
	return v24, openfaunaLeads
}

// TestOrchestrator_V24LabelResolverChain verifies the copy-on-write chain writer inserts the
// v2.4 label resolver right after OpenFauna, replaces it in place (never duplicating it),
// removes it on unload, and keeps every other resolver in its relative order.
func TestOrchestrator_V24LabelResolverChain(t *testing.T) {
	o := &Orchestrator{}
	o.openfauna = openfauna.NewResolver()
	tax := fakeNameResolver{tag: "taxonomy"}
	o.nameResolvers = []NameResolver{o.openfauna, tax}

	// Fabricated binomials OpenFauna cannot know, so resolution is decided by the v2.4
	// resolver alone.
	o.setV24LabelResolver([]string{"Testus alpha_Alpha Bird"})
	require.Len(t, o.nameResolvers, 3)
	assert.Same(t, o.openfauna, o.nameResolvers[0], "OpenFauna leads the chain")
	_, isV24 := o.nameResolvers[1].(*BirdNETLabelResolver)
	assert.True(t, isV24, "the v2.4 resolver sits directly after OpenFauna")
	assert.Equal(t, tax, o.nameResolvers[2], "the taxonomy resolver keeps its relative order")
	assert.Equal(t, "Alpha Bird", o.ResolveName("Testus alpha", ""))

	// Replace with new labels: exactly one v2.4 resolver, resolving the new species only.
	o.setV24LabelResolver([]string{"Testus beta_Beta Bird"})
	require.Len(t, o.nameResolvers, 3)
	n, _ := countV24Resolvers(o)
	assert.Equal(t, 1, n, "replacing must not duplicate the v2.4 resolver")
	assert.Empty(t, o.ResolveName("Testus alpha", ""), "the old v2.4 label no longer resolves")
	assert.Equal(t, "Beta Bird", o.ResolveName("Testus beta", ""))

	// Remove (v2.4 unloaded): OpenFauna and taxonomy remain, no v2.4 resolver.
	o.setV24LabelResolver(nil)
	require.Len(t, o.nameResolvers, 2)
	n, leads := countV24Resolvers(o)
	assert.Equal(t, 0, n, "no v2.4 label resolver after removal")
	assert.True(t, leads, "OpenFauna still leads")
	assert.Equal(t, tax, o.nameResolvers[1], "the taxonomy resolver survives removal")
}

// TestOrchestrator_ConcurrentV24ResolverSwapAndResolve_NoRace proves the copy-on-write chain
// writer is race-free against the lock-free ResolveName reader (run with -race).
func TestOrchestrator_ConcurrentV24ResolverSwapAndResolve_NoRace(t *testing.T) {
	o := &Orchestrator{}
	o.openfauna = openfauna.NewResolver()
	o.nameResolvers = []NameResolver{o.openfauna}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = o.ResolveName("Testus alpha", "")
			}
		}
	})
	wg.Go(func() {
		defer close(stop)
		for i := range 2000 {
			if i%3 == 0 {
				o.setV24LabelResolver(nil)
			} else {
				o.setV24LabelResolver([]string{"Testus alpha_Alpha Bird"})
			}
		}
	})
	wg.Wait()
}

// failV24LoaderOnce makes the BirdNET v2.4 loader fail on its FIRST call (construction) and
// delegate to the real loader on every later call (the LoadModel retry), restoring the
// original loader on cleanup. It reproduces a transient construction failure that clears by
// the time the model is retried.
func failV24LoaderOnce(t *testing.T) {
	t.Helper()
	orig, had := modelLoaders[RegistryIDBirdNETV24]
	require.True(t, had, "the v2.4 loader must be registered")
	var failed atomic.Bool
	modelLoaders[RegistryIDBirdNETV24] = func(o *Orchestrator, threads int) error {
		if failed.CompareAndSwap(false, true) {
			return errors.NewStd("simulated transient v2.4 load failure")
		}
		return orig(o, threads)
	}
	t.Cleanup(func() { modelLoaders[RegistryIDBirdNETV24] = orig })
}

// TestLoadModel_V24RetryAfterConstructionFailure exercises the post-construction v2.4 load
// hardening: v2.4 fails at construction and is loaded later via LoadModel (as
// loadInstalledModels does). The v2.4 label resolver must be wired on that retry path, and its
// labels must be visible to the range filter so the inclusion list is NOT empty (a fail-closed
// guard).
// Skips on a noembed build where the retry cannot load the embedded model.
func TestLoadModel_V24RetryAfterConstructionFailure(t *testing.T) {
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	enableBirdNETV24(settings)
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	failV24LoaderOnce(t)

	o, err := NewOrchestrator(settings)
	require.NoError(t, err, "a v2.4 construction failure must not be fatal")
	t.Cleanup(func() { o.Delete() })
	require.False(t, o.IsModelLoaded(RegistryIDBirdNETV24), "v2.4 fails at construction as designed")

	// Retry the load exactly as loadInstalledModels does.
	require.NoError(t, o.LoadModel(RegistryIDBirdNETV24))
	requireV24Loaded(t, o) // skips under noembed, where the retry cannot succeed

	// Finding 1: the v2.4 label resolver is wired into the chain on the retry path.
	n, leads := countV24Resolvers(o)
	assert.Equal(t, 1, n, "exactly one v2.4 label resolver must be in the chain after the retry")
	assert.True(t, leads, "OpenFauna must lead the chain")

	// Finding 2: v2.4 labels are visible through the covered-label space, so rarity context
	// and the inclusion list are not empty.
	rc, err := o.GetRarityContext(time.Now())
	require.NoError(t, err)
	assert.NotEmpty(t, rc.ClassifierLabels, "v2.4 labels must be visible after a post-construction load")

	status := o.RangeFilterStatus()
	c, ok := classifierCoverageByID(&status, RegistryIDBirdNETV24)
	require.True(t, ok, "v2.4 must appear as a loaded range-filter participant")
	assert.Positive(t, c.TotalSpecies, "the v2.4 participant has labels after the retry")

	require.NoError(t, BuildRangeFilter(o))
	assert.NotEmpty(t, conf.GetSettings().GetIncludedSpecies(),
		"the inclusion list must not be empty after a v2.4 retry (fail-closed regression guard)")
}

// TestRangeFilterView_CoveredLabels_FallsBackToInstanceLabels covers the covered-label
// fallback: when v2.4 is loaded but the published settings snapshot has no labels (the retry path),
// coveredLabels falls back to the loaded instance's own labels instead of returning an empty
// set that would fail the inclusion list closed.
func TestRangeFilterView_CoveredLabels_FallsBackToInstanceLabels(t *testing.T) {
	v24 := []string{"Turdus merula_Common Blackbird"}
	view := rangeFilterView{
		v24Labels:    v24,
		participants: []participantLabels{{id: RegistryIDBirdNETV24, labels: v24}},
	}

	// Published labels present: use them (byte-identical to the pre-decouple behavior).
	published := &conf.Settings{}
	published.BirdNET.Labels = []string{"Published_Label"}
	assert.Equal(t, []string{"Published_Label"}, view.coveredLabels(published))

	// Published labels empty (post-construction retry): fall back to the instance labels.
	assert.Equal(t, v24, view.coveredLabels(&conf.Settings{}))
}

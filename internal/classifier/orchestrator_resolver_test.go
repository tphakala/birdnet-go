package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// newResolverTestOrchestrator builds a minimal Orchestrator by hand (no model
// files) with OpenFauna prepended ahead of a deliberately-wrong BirdNET label
// resolver, so tests can assert OpenFauna wins on the chain.
func newResolverTestOrchestrator(t *testing.T, wrongLabel string) *Orchestrator {
	t.Helper()
	of := openfauna.NewResolver()
	o := &Orchestrator{
		nameResolvers: []NameResolver{of, NewBirdNETLabelResolver([]string{wrongLabel})},
		openfauna:     of,
	}
	s := &conf.Settings{}
	s.BirdNET.Locale = "en"
	o.settingsAtomic.Store(s)
	return o
}

func TestRebuildNameResolver_OpenFaunaOverridesLabel(t *testing.T) {
	// "Turdus merula" is a stable BirdNET species present in the vendored
	// OpenFauna dataset. Do NOT assert the exact common name (the dataset is
	// refreshed on main); assert only that OpenFauna's name wins over the label.
	const sci = "Turdus merula"
	o := newResolverTestOrchestrator(t, sci+"_WRONG-LABEL-NAME")

	// Working set is a label string; RebuildNameResolver must extract the scientific part.
	require.NoError(t, o.RebuildNameResolver([]string{sci + "_WRONG-LABEL-NAME"}))

	got := o.ResolveName(sci, "")
	assert.NotEmpty(t, got, "OpenFauna should resolve a known species")
	assert.NotEqual(t, "WRONG-LABEL-NAME", got, "OpenFauna (chain[0]) must override the label resolver")
}

func TestRebuildNameResolver_EmptyWorkingSetDoesNotPanic(t *testing.T) {
	o := newResolverTestOrchestrator(t, "Turdus merula_x")
	o.primary = nil // exercise the empty-working-set guard without a model
	assert.NoError(t, o.RebuildNameResolver(nil))
}

func TestOpenFaunaResolver_ReturnsOwnedInstance(t *testing.T) {
	o := newResolverTestOrchestrator(t, "Turdus merula_x")
	assert.Same(t, o.openfauna, o.OpenFaunaResolver())
}

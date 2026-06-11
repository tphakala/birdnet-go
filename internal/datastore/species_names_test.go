package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeResolver misses ResolveLocal for everything (the branch that broke localized search) and
// resolves scientific-only labels only through the batch seam, exactly like the real
// resolver does for out-of-working-set secondary-model species.
type fakeResolver struct {
	batch map[string]string
}

func (f *fakeResolver) Resolve(string, string) string      { return "" }
func (f *fakeResolver) ResolveLocal(string) (string, bool) { return "", false }
func (f *fakeResolver) ResolveLocalizedBatch(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v, ok := f.batch[n]; ok {
			out[n] = v
		}
	}
	return out
}

func TestResolveLabelNames_BatchResolvesScientificOnlyLabel(t *testing.T) {
	t.Parallel()

	r := &fakeResolver{batch: map[string]string{"Barbastella barbastellus": "mopsilepakko"}}
	got := ResolveLabelNames([]string{"Barbastella barbastellus"}, r)

	assert.Equal(t, []SpeciesName{{Scientific: "Barbastella barbastellus", Common: "mopsilepakko"}}, got)
}

func TestResolveLabelNames_EmbeddedCommonNameUsedDirectly(t *testing.T) {
	t.Parallel()

	got := ResolveLabelNames([]string{"Turdus merula_Common Blackbird"}, nil)
	assert.Equal(t, []SpeciesName{{Scientific: "Turdus merula", Common: "Common Blackbird"}}, got)
}

func TestResolveLabelNames_UnresolvableScientificOnlyLabelDropped(t *testing.T) {
	t.Parallel()

	r := &fakeResolver{batch: map[string]string{}}
	got := ResolveLabelNames([]string{"Genus species"}, r)
	assert.Empty(t, got)
}

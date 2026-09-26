package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// fakeIndexSetter records the species-name index installed on it, standing in for
// the v2-only datastore (which implements speciesIndexSetter).
type fakeIndexSetter struct {
	got   *speciesindex.Service
	calls int
}

func (f *fakeIndexSetter) SetSpeciesIndex(svc *speciesindex.Service) {
	f.got = svc
	f.calls++
}

// plainDatastore does NOT implement speciesIndexSetter, standing in for the legacy
// DataStore, which keeps its own label-seeded maps.
type plainDatastore struct{}

// TestInstallSpeciesIndex_InjectsWhenSupported proves the orchestrator-owned index
// is handed to a datastore that implements the optional setter, so its common-name
// resolution reads the shared snapshot rather than a private copy.
func TestInstallSpeciesIndex_InjectsWhenSupported(t *testing.T) {
	t.Parallel()

	svc := speciesindex.New(nil)
	f := &fakeIndexSetter{}
	installSpeciesIndex(f, svc)

	assert.Equal(t, 1, f.calls, "the setter must be called exactly once")
	assert.Same(t, svc, f.got, "the exact orchestrator-owned service must be installed")
}

// TestInstallSpeciesIndex_NoopWhenUnsupported proves a datastore that does not
// implement the setter (the legacy DataStore) is skipped without panicking.
func TestInstallSpeciesIndex_NoopWhenUnsupported(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		installSpeciesIndex(plainDatastore{}, speciesindex.New(nil))
	})
}

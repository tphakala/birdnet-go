package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

const (
	wiringSpeciesSci   = "Turdus merula"       // seeded into the resolver's working set
	wiringSpeciesLabel = "Turdus merula_WRONG" // label whose common name the resolver must override
	wiringLabelCommon  = "WRONG"
	wiringLocale       = "en"
)

// nameWiringRecorder captures the externally-observable effect of the two
// name-wiring calls on one surface: the order they ran (setAt < updateAt) and the
// localized name the live resolver would produce at map-build time. It mirrors
// production: SetNameResolver no-ops on a nil/typed-nil resolver
// (datastore.IsNilResolver), and the map rebuild localizes via ResolveLocal.
type nameWiringRecorder struct {
	calls         int
	resolver      datastore.SpeciesNameResolver
	setAt         int
	updateAt      int
	localizedName string
	localizedOK   bool
}

func (r *nameWiringRecorder) setResolver(res datastore.SpeciesNameResolver) {
	r.calls++
	r.setAt = r.calls
	if !datastore.IsNilResolver(res) { // mirror production no-op-on-nil
		r.resolver = res
	}
}

func (r *nameWiringRecorder) rebuildMaps() {
	r.calls++
	r.updateAt = r.calls
	if r.resolver != nil {
		r.localizedName, r.localizedOK = r.resolver.ResolveLocal(wiringSpeciesSci)
	}
}

// spyDatastore is a minimal datastore.Interface: only SetNameResolver and
// UpdateNameMaps are exercised; any other call hits the nil embedded Interface and
// panics loudly (surfacing an unexpected new dependency).
type spyDatastore struct {
	datastore.Interface
	nameWiringRecorder
}

func (s *spyDatastore) SetNameResolver(r datastore.SpeciesNameResolver) { s.setResolver(r) }
func (s *spyDatastore) UpdateNameMaps(_ []string)                       { s.rebuildMaps() }

// spyController implements commonNameController (the api-controller surface).
type spyController struct {
	nameWiringRecorder
}

func (s *spyController) SetNameResolver(r datastore.SpeciesNameResolver) { s.setResolver(r) }
func (s *spyController) UpdateCommonNameMap(_ []string)                  { s.rebuildMaps() }

func newSeededResolver(t *testing.T) *openfauna.Resolver {
	t.Helper()
	of := openfauna.NewResolver()
	require.NoError(t, of.Rebuild([]string{wiringSpeciesSci}, wiringLocale))
	return of
}

// wiringObservation captures the externally-visible result of installNameResolver
// on one surface so both surfaces can be asserted identically.
type wiringObservation struct {
	surface       string
	setAt         int
	updateAt      int
	localizedName string
	localizedOK   bool
}

// TestInstallNameResolver_InstallsBeforeRebuild proves NewControlMonitor's wiring
// helper installs the shared resolver BEFORE rebuilding the cached name maps, on
// both the datastore and the api-controller surface. Swapping the order or
// dropping SetNameResolver leaves the resolver nil at map-build time, so the
// localized capture is empty and the test fails.
func TestInstallNameResolver_InstallsBeforeRebuild(t *testing.T) {
	of := newSeededResolver(t)
	ds := &spyDatastore{}
	api := &spyController{}

	installNameResolver(of, []string{wiringSpeciesLabel}, ds, api)

	observations := []wiringObservation{
		{"datastore", ds.setAt, ds.updateAt, ds.localizedName, ds.localizedOK},
		{"controller", api.setAt, api.updateAt, api.localizedName, api.localizedOK},
	}
	for _, obs := range observations {
		t.Run(obs.surface, func(t *testing.T) {
			require.Positive(t, obs.setAt, "SetNameResolver must be called")
			require.Positive(t, obs.updateAt, "the name-map rebuild must be called")
			assert.Less(t, obs.setAt, obs.updateAt, "SetNameResolver must precede the map rebuild")
			assert.True(t, obs.localizedOK, "resolver must be live at map-build time")
			assert.NotEmpty(t, obs.localizedName)
			assert.NotEqual(t, wiringSpeciesSci, obs.localizedName, "name must be localized, not the scientific name")
			assert.NotEqual(t, wiringLabelCommon, obs.localizedName, "resolver must override the label common name")
		})
	}
}

// TestInstallNameResolver_NilResolverStillRebuildsMaps pins the behavior that the
// map rebuild must run even without a resolver (otherwise search/insights start
// with empty maps). A typed-nil *openfauna.Resolver is treated as absent by
// SetNameResolver, but UpdateNameMaps/UpdateCommonNameMap still fire.
func TestInstallNameResolver_NilResolverStillRebuildsMaps(t *testing.T) {
	var typedNil *openfauna.Resolver // nil pointer wrapped into the interface
	ds := &spyDatastore{}
	api := &spyController{}

	installNameResolver(typedNil, []string{wiringSpeciesLabel}, ds, api)

	assert.Positive(t, ds.updateAt, "datastore maps must be rebuilt even with no resolver")
	assert.False(t, ds.localizedOK, "no localization without a resolver")
	assert.Positive(t, api.updateAt, "controller maps must be rebuilt even with no resolver")
	assert.False(t, api.localizedOK, "no localization without a resolver")
}

// TestInstallNameResolver_NilSurfacesTolerated proves a missing surface is skipped
// without panicking while the other surface is still wired.
func TestInstallNameResolver_NilSurfacesTolerated(t *testing.T) {
	of := newSeededResolver(t)

	t.Run("nil datastore", func(t *testing.T) {
		api := &spyController{}
		installNameResolver(of, []string{wiringSpeciesLabel}, nil, api)
		assert.Positive(t, api.updateAt, "controller still wired when datastore is nil")
		assert.True(t, api.localizedOK)
	})

	t.Run("nil controller", func(t *testing.T) {
		ds := &spyDatastore{}
		installNameResolver(of, []string{wiringSpeciesLabel}, ds, nil)
		assert.Positive(t, ds.updateAt, "datastore still wired when controller is nil")
		assert.True(t, ds.localizedOK)
	})
}

package speciesindex

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_SnapshotNeverNil(t *testing.T) {
	t.Parallel()

	// Before any Rebuild, Snapshot returns the empty snapshot with non-nil maps.
	s := New(nil)
	snap := s.Snapshot()
	require.NotNil(t, snap)
	assert.NotNil(t, snap.SciToCommon)
	assert.Empty(t, snap.SciToCommon)

	// After Rebuild, the published snapshot reflects the labels.
	s.Rebuild([]string{"Turdus merula_Blackbird"}, "en")
	snap = s.Snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, "Blackbird", snap.SciToCommon["Turdus merula"])
	assert.Equal(t, "en", snap.Locale)

	// A zero-value Service (no New) still returns a non-nil snapshot.
	var zero Service
	assert.NotNil(t, zero.Snapshot())
}

func TestService_SetResolverIgnoresNil(t *testing.T) {
	t.Parallel()

	r := localResolver{m: map[string]string{"Myotis myotis": "Localized"}}
	s := New(r)
	require.NotNil(t, s.Resolver())

	// A nil resolver is ignored, leaving the existing one in place.
	s.SetResolver(nil)
	require.NotNil(t, s.Resolver())

	// A typed-nil interface value is also ignored (IsNilResolver semantics).
	var typedNil *localResolverPtr
	s.SetResolver(typedNil)
	require.NotNil(t, s.Resolver())

	// The resolver localizes the reverse map after a rebuild.
	s.Rebuild([]string{"Myotis myotis"}, "")
	assert.Equal(t, "Myotis myotis", s.Snapshot().CommonToSci["localized"])
}

// localResolverPtr is a pointer-receiver resolver used to exercise the typed-nil
// path of SetResolver (datastore.IsNilResolver reflects on a nil pointer).
type localResolverPtr struct{ m map[string]string }

func (r *localResolverPtr) Resolve(sci, _ string) string { return r.m[sci] }
func (r *localResolverPtr) ResolveLocal(sci string) (string, bool) {
	v, ok := r.m[sci]
	return v, ok
}

func TestService_ConcurrentReadersDuringRebuild(t *testing.T) {
	t.Parallel()

	s := New(nil)
	labels := loadTestdata(t, "bat_like_labels.txt")

	const readers = 8
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers hammer Snapshot() and read its maps while rebuilds churn.
	for range readers {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					snap := s.Snapshot()
					_ = snap.SciToCommon["Nyctalus noctula"]
					_ = snap.CommonToSci["noctule"]
					for range snap.SciToCommonFolded { //nolint:revive // draining the map is the point
					}
				}
			}
		})
	}

	// Writer loops rebuilds; the atomic swap must never expose a torn map.
	wg.Go(func() {
		for range 200 {
			s.Rebuild(labels, "en")
		}
		close(stop)
	})

	wg.Wait()
	// Final state is the last rebuild.
	assert.Equal(t, "Noctule", s.Snapshot().SciToCommon["Nyctalus noctula"])
}

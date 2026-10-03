package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe verifies the guard: with no
// API controller or metrics wired, the reconfigure handler logs and returns
// without panicking (rather than dereferencing a nil controller). The full swap
// orchestration is exercised by the QA hot-reload integration suite, since it
// requires a live *apiv2.Controller; its building blocks (initGuideCacheIfNeeded,
// guideProviderSetChanged, warmGuideCacheWithTopSpecies) are unit-tested here and
// in guide_cache_init_test.go.
func TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		(&ControlMonitor{}).handleReconfigureSpeciesGuide() // apiController + metrics are nil
	})
}

package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestHandleReconcileModels_NoOrchestrator pins the nil-orchestrator guard: the handler logs
// and returns without panicking when cm.bn is not set.
func TestHandleReconcileModels_NoOrchestrator(t *testing.T) {
	cm := &ControlMonitor{} // bn and apiController are nil
	assert.NotPanics(t, func() { cm.handleReconcileModels() })
}

// TestHandleReconcileModels_N0NoOp verifies that reconciling with nothing enabled loads and
// unloads nothing and never dereferences the (nil) apiController.
func TestHandleReconcileModels_N0NoOp(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // N=0
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	orch, err := classifier.NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(orch.Delete)

	cm := &ControlMonitor{bn: orch} // apiController nil: a no-op reconcile must not touch it
	assert.NotPanics(t, func() { cm.handleReconcileModels() })
}

// TestHandleReloadBirdnet_V24NotLoaded_SkipsPrimaryReload verifies the Phase 4 tolerance: on
// an N=0 runtime (BirdNET v2.4 not loaded), handleReloadBirdnet skips the primary reload
// (which would error "model not loaded") and still runs the range-filter rebuild without
// panicking or emitting an error.
func TestHandleReloadBirdnet_V24NotLoaded_SkipsPrimaryReload(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // N=0: v2.4 not loaded
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	orch, err := classifier.NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(orch.Delete)
	require.False(t, orch.IsModelLoaded(classifier.RegistryIDBirdNETV24), "v2.4 is not loaded at N=0")

	cm := &ControlMonitor{bn: orch} // apiController nil
	assert.NotPanics(t, func() { cm.handleReloadBirdnet() },
		"a BirdNET reload with v2.4 not loaded must skip the primary reload, not panic or error")
}

package analysis

import (
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/observability"
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

// TestHandleReloadBirdnet_ReplansCadence pins that a BirdNET reload, which
// re-probes model latency, re-plans the analysis cadence through the re-plan
// hook and does not run a full source reconfigure (which probes every stream).
func TestHandleReloadBirdnet_ReplansCadence(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // N=0: nothing to reload, the hook still runs
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	orch, err := classifier.NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(orch.Delete)

	var replans, reconfigures int
	cm := &ControlMonitor{
		bn:                   orch,
		replanCadenceFn:      func() { replans++ },
		reconfigureSourcesFn: func() { reconfigures++ },
	}
	cm.handleReloadBirdnet()
	assert.Equal(t, 1, replans, "the reload must re-plan the cadence once")
	assert.Zero(t, reconfigures, "the reload must not reconfigure (and probe) the sources")
}

// TestHandleReconcileModels_AllLoadsFailed_BroadcastsListChange pins that a reconcile in
// which every change failed still sends the topology SSE event, so an open model list moves
// the model from pending to failed instead of showing it as loading until a reload.
func TestHandleReconcileModels_AllLoadsFailed_BroadcastsListChange(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{classifier.RegistryIDPerchV2} // no model files in tests
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	orch, err := classifier.NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(orch.Delete)
	require.False(t, orch.IsModelLoaded(classifier.RegistryIDPerchV2), "Perch must fail to load without files")

	store := observability.NewMemoryStore(1)
	topoCh, cancel := store.SubscribeTopology()
	t.Cleanup(cancel)

	cm := &ControlMonitor{bn: orch, apiController: &apiv2.Controller{Core: &apicore.Core{MetricsStore: store}}}
	cm.handleReconcileModels()

	select {
	case <-topoCh:
	default:
		t.Fatal("a reconcile whose loads all failed must broadcast the topology change")
	}
}

// retryReconfigureWait bounds the wait for the debounced audio-source reconfigure
// (2 s debounce in api/v2) after a retry loaded a model.
const retryReconfigureWait = 5 * time.Second

// TestHandleReloadBirdnet_RetryLoadedModel_ReconfiguresAudio pins that a BirdNET reload
// whose retry of failed loads loaded a model reconciles the per-source model registration
// (the reconfigure_audio_sources control signal), not only the topology SSE event. Without
// it the model would load and receive no audio until the next unrelated reconfigure.
func TestHandleReloadBirdnet_RetryLoadedModel_ReconfiguresAudio(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // N=0: the retry is the only thing that loads
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	orch, err := classifier.NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(orch.Delete)

	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().PruneAppEvents(mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)
	controlChan := make(chan string, 1)
	controller, err := apiv2.NewWithOptions(echo.New(), mockDS, settings, nil, nil, controlChan, metrics, false)
	require.NoError(t, err)
	t.Cleanup(controller.Shutdown)

	cm := &ControlMonitor{
		bn:                 orch,
		apiController:      controller,
		retryFailedLoadsFn: func() []string { return []string{classifier.RegistryIDPerchV2} },
	}
	cm.handleReloadBirdnet()

	select {
	case signal := <-controlChan:
		assert.Equal(t, "reconfigure_audio_sources", signal)
	case <-time.After(retryReconfigureWait):
		t.Fatal("a retry that loaded a model must reconfigure the audio sources")
	}
}

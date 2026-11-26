package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestReloadSecondaryModels_RefusalKeepsConcurrentlySwappedInstance pins that a refusal
// never unloads an instance published after the refused reload started: here a variant
// swap lands while the refused build runs, so the entry holds a newer instance and the
// refusal must leave it serving, with nothing recorded.
func TestReloadSecondaryModels_RefusalKeepsConcurrentlySwappedInstance(t *testing.T) {
	setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	refusal := &errToggle{err: wrappedRefusal(RegistryIDPerchV2, ovReasonNotPerchNoDFT, "auto")}
	refusingPerchBuilder(t, refusal)

	o := newTestOrchestrator(t)
	t.Cleanup(o.stopOpenVINORefusalNotices)
	t.Cleanup(o.acousticNotice.Stop)
	old := &reloadFakeModel{id: RegistryIDPerchV2}
	swapped := &reloadFakeModel{id: RegistryIDPerchV2}
	entry := &modelEntry{instance: old, backend: secondaryBackendKey{backend: conf.BackendPrefAuto}}
	o.models[RegistryIDPerchV2] = entry
	// The refused build runs while a gallery variant swap publishes a newer instance.
	racing := openvinoCapableSecondaryBuilders[RegistryIDPerchV2]
	openvinoCapableSecondaryBuilders[RegistryIDPerchV2] = func(orc *Orchestrator, s *conf.Settings, threads int) (ModelInstance, error) {
		entry.mu.Lock()
		entry.instance = swapped
		entry.generation++
		entry.mu.Unlock()
		return racing(orc, s, threads)
	}

	require.NoError(t, o.ReloadSecondaryModels())

	assert.True(t, o.IsModelLoaded(RegistryIDPerchV2), "the newer instance must keep serving")
	assert.Equal(t, int32(0), swapped.closes.Load(), "the newer instance must stay open")
	assert.Empty(t, o.LoadFailureReasons(), "nothing is recorded for a replaced instance")
}

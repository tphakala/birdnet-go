package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// TestReloadV24_RefusalUnloadsAndPinsKey checks the v2.4 settings reload under an
// explicit selection OpenVINO cannot honor: the model is unloaded with its refusal on
// record and noticed, and the failure is stamped with the settings the build used even
// when the selection changes while the build runs (so the retry still loads it).
func TestReloadV24_RefusalUnloadsAndPinsKey(t *testing.T) {
	svc := setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDBirdNET)
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	t.Cleanup(o.stopOpenVINORefusalNotices)
	t.Cleanup(o.acousticNotice.Stop)

	refuse := wrappedRefusal(RegistryIDBirdNETV24, ovReasonNoDevice, "auto")
	err := o.reloadV24(func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
		// The user switches back to auto while the slow build is still running.
		publishRefusalSettings(t, conf.BackendPrefAuto, conf.OVDeviceAuto, conf.ModelIDBirdNET)
		return nil, refuse
	})

	require.Error(t, err)
	assert.False(t, o.IsModelLoaded(RegistryIDBirdNETV24), "the refused model must be unloaded")
	assert.Equal(t, map[string]string{RegistryIDBirdNETV24: string(ovReasonNoDevice)}, o.LoadFailureReasons())
	value, ok := o.modelLoadErrors.Load(RegistryIDBirdNETV24)
	require.True(t, ok)
	failure, ok := value.(loadFailure)
	require.True(t, ok)
	assert.Equal(t, conf.BackendPrefOpenVINO, failure.key.backend.backend, "the key must be the one the build used")
	require.Len(t, refusalNotices(t, svc), 1)
	assert.Equal(t, notification.MsgOpenVINORefusedNoDeviceMessage, refusalNotices(t, svc)[0].MessageKey)
	assert.NotEqual(t, failure.key, inferenceLoadKeyFor(o.currentSettings()),
		"the settings changed during the build, so the retry sees a different key and loads the model")
}

// TestUnloadRefusedModel_SkipsReplacedInstance checks the same guard at the unit level
// for both ways an instance gets replaced: a swap bumps the entry generation, and an
// unload plus reinstall replaces the entry.
func TestUnloadRefusedModel_SkipsReplacedInstance(t *testing.T) {
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	refusal := wrappedRefusal(RegistryIDPerchV2, ovReasonNotPerchNoDFT, "auto")
	key := inferenceLoadKeyFor(conftestSettings(t))

	t.Run("generation moved", func(t *testing.T) {
		o := newTestOrchestrator(t)
		t.Cleanup(o.acousticNotice.Stop)
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}}
		rev := o.entryRevisionOf(RegistryIDPerchV2)
		o.models[RegistryIDPerchV2].generation++ // a variant swap published a new instance

		o.unloadRefusedModel(rev, refusal, key)

		assert.True(t, o.IsModelLoaded(RegistryIDPerchV2), "the newer instance must keep serving")
		assert.Empty(t, o.LoadFailureReasons(), "nothing is recorded for a replaced instance")
	})
	t.Run("entry replaced", func(t *testing.T) {
		o := newTestOrchestrator(t)
		t.Cleanup(o.acousticNotice.Stop)
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}}
		rev := o.entryRevisionOf(RegistryIDPerchV2)
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}} // unload plus reinstall

		o.unloadRefusedModel(rev, refusal, key)

		assert.True(t, o.IsModelLoaded(RegistryIDPerchV2))
		assert.Empty(t, o.LoadFailureReasons())
	})
	t.Run("unchanged instance is unloaded", func(t *testing.T) {
		o := newTestOrchestrator(t)
		t.Cleanup(o.acousticNotice.Stop)
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}}
		rev := o.entryRevisionOf(RegistryIDPerchV2)

		o.unloadRefusedModel(rev, refusal, key)

		assert.False(t, o.IsModelLoaded(RegistryIDPerchV2))
		assert.Equal(t, map[string]string{RegistryIDPerchV2: string(ovReasonNotPerchNoDFT)}, o.LoadFailureReasons())
	})
}

// TestUnloadModel_ExpectedEntryGuard checks the guard unloadRefusedModel relies on:
// the expected entry is compared under the same o.mu write lock that detaches it, so
// an entry a concurrent LoadModel published after the caller's check is never torn
// down and the detach callback (the failure record) runs only on a real detach.
func TestUnloadModel_ExpectedEntryGuard(t *testing.T) {
	t.Run("replaced entry is kept", func(t *testing.T) {
		o := newTestOrchestrator(t)
		t.Cleanup(o.acousticNotice.Stop)
		stale := &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}}
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}}
		detached := false

		err := o.unloadModel(RegistryIDPerchV2, stale, func() { detached = true })

		require.ErrorIs(t, err, errEntryReplaced)
		assert.True(t, o.IsModelLoaded(RegistryIDPerchV2), "the replacement must keep serving")
		assert.False(t, detached, "nothing is recorded for a replaced entry")
	})
	t.Run("expected entry is detached", func(t *testing.T) {
		o := newTestOrchestrator(t)
		t.Cleanup(o.acousticNotice.Stop)
		entry := &modelEntry{instance: &reloadFakeModel{id: RegistryIDPerchV2}}
		o.models[RegistryIDPerchV2] = entry
		detached := false

		err := o.unloadModel(RegistryIDPerchV2, entry, func() { detached = true })

		require.NoError(t, err)
		assert.False(t, o.IsModelLoaded(RegistryIDPerchV2))
		assert.True(t, detached)
	})
}

// conftestSettings returns the settings snapshot the orchestrator under test reads.
func conftestSettings(t *testing.T) *conf.Settings {
	t.Helper()
	return (&Orchestrator{}).currentSettings()
}

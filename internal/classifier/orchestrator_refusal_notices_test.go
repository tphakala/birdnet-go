package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestReloadEntry_RefusalLogsNoKeepingWarning pins the log of a refused v2.4 reload: a
// refusal unloads the model (the settings callers do that), so the "keeping the previous
// model instance" warning would be false and is left out, while any other build failure
// still logs it. Not parallel: it swaps the process-global logger.
func TestReloadEntry_RefusalLogsNoKeepingWarning(t *testing.T) {
	buf := newCaptureLogger(t)
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	build := func(err error) entryBuilder {
		return func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) { return nil, err }
	}
	opts := reloadOpts{settings: conftestSettings(t)}

	_, err := o.reloadEntry(RegistryIDBirdNETV24, build(wrappedRefusal(RegistryIDBirdNETV24, ovReasonNoDevice, "auto")), opts)
	require.Error(t, err)
	assert.NotContains(t, buf.String(), "keeping the previous model instance")

	_, err = o.reloadEntry(RegistryIDBirdNETV24, build(errors.NewStd("compile failed")), opts)
	require.Error(t, err)
	assert.Contains(t, buf.String(), "keeping the previous model instance")
}

// TestLoadModel_RefusalNoticeFollowsLoad pins that a runtime load re-evaluates the
// refusal notices on its own: a load that OpenVINO refuses raises the notice, and a
// later successful load clears it.
func TestLoadModel_RefusalNoticeFollowsLoad(t *testing.T) {
	svc := setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	toggle := &errToggle{err: wrappedRefusal(RegistryIDPerchV2, ovReasonNotPerchNoDFT, "auto")}
	refusingPerchBuilder(t, toggle)
	o := newTestOrchestrator(t)
	t.Cleanup(o.stopOpenVINORefusalNotices)
	t.Cleanup(o.acousticNotice.Stop)

	require.Error(t, o.LoadModel(RegistryIDPerchV2))
	require.Len(t, refusalNotices(t, svc), 1, "a refused load raises the notice")

	toggle.err = nil
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))
	assert.Empty(t, refusalNotices(t, svc), "a successful load clears the notice")
}

// TestReconcileEnabledModels_DroppingRefusedModelClearsNotice pins that disabling a
// refused model removes its notice through the reconcile that applies the new
// models.enabled, without any other trigger.
func TestReconcileEnabledModels_DroppingRefusedModelClearsNotice(t *testing.T) {
	svc := setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	o := newTestOrchestrator(t)
	t.Cleanup(o.stopOpenVINORefusalNotices)
	t.Cleanup(o.acousticNotice.Stop)
	o.recordLoadFailure(RegistryIDPerchV2, wrappedRefusal(RegistryIDPerchV2, ovReasonNotBuilt, "auto"))
	o.syncOpenVINORefusalNotices()
	require.Len(t, refusalNotices(t, svc), 1)

	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto)
	_, _, err := o.ReconcileEnabledModels()
	require.NoError(t, err)
	assert.Empty(t, refusalNotices(t, svc))
}

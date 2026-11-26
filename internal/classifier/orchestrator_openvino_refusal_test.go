package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// publishRefusalSettings publishes test settings with the given inference selection
// and enabled models. Not for parallel tests: it swaps the global snapshot.
func publishRefusalSettings(t *testing.T, backend, device string, enabled ...string) {
	t.Helper()
	s := conftest.GetTestSettings()
	s.BirdNET.Backend = backend
	s.BirdNET.OpenVINODevice = device
	s.Models.Enabled = enabled
	conftest.SetTestSettings(s)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })
}

// wrappedRefusal builds a refusal wrapped the way the family builders wrap it.
func wrappedRefusal(modelID string, reason OpenVINOReason, device string) error {
	return errors.New(&OpenVINORefusalError{ModelID: modelID, Reason: reason, Device: device}).
		Component("classifier.orchestrator").Category(errors.CategoryModelInit).Build()
}

// refusalNotices lists the OpenVINO refusal notices in svc.
func refusalNotices(t *testing.T, svc *notification.Service) []*notification.Notification {
	t.Helper()
	all, err := svc.List(nil)
	require.NoError(t, err)
	var out []*notification.Notification
	for _, n := range all {
		if n.TitleKey == notification.MsgOpenVINORefusedTitle {
			out = append(out, n)
		}
	}
	return out
}

// TestRecordLoadFailure_OpenVINOReason checks which failures carry a reason code: a
// refusal wrapped by a builder does, a plain error does not, a later success clears
// it, and a disabled model is not reported.
func TestRecordLoadFailure_OpenVINOReason(t *testing.T) {
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2, conf.ModelIDBat)
	o := newTestOrchestrator(t)

	o.recordLoadFailure(RegistryIDPerchV2, wrappedRefusal(RegistryIDPerchV2, ovReasonNotBuilt, "auto"))
	o.recordLoadFailure(RegistryIDBat, errors.NewStd("model files missing"))
	assert.Equal(t, map[string]string{RegistryIDPerchV2: string(ovReasonNotBuilt)}, o.LoadFailureReasons())
	assert.Contains(t, o.LoadErrors(), RegistryIDBat, "a plain failure is still a load error")

	// A successful load deletes the record.
	o.modelLoadErrors.Delete(RegistryIDPerchV2)
	assert.Empty(t, o.LoadFailureReasons())

	// A disabled model's record is filtered out.
	o.recordLoadFailure(RegistryIDPerchV2, wrappedRefusal(RegistryIDPerchV2, ovReasonNotBuilt, "auto"))
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDBat)
	assert.Empty(t, o.LoadFailureReasons())
}

// TestOpenVINORefusalNotices_Sequence walks the refusal notices: one notice per
// reason group naming every model of it, replaced when the set changes, absent for
// gpu_failed (the GPU notice covers it), and cleared when the model loads, is
// disabled, or is uninstalled (its record stays but it is no longer enabled).
// Not parallel: process-global notification service and settings.
func TestOpenVINORefusalNotices_Sequence(t *testing.T) {
	svc := setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto,
		conf.ModelIDPerchV2, conf.ModelIDBirdNETV3, conf.ModelIDBat)
	o := newTestOrchestrator(t)
	t.Cleanup(o.stopOpenVINORefusalNotices)

	o.recordLoadFailure(RegistryIDPerchV2, wrappedRefusal(RegistryIDPerchV2, ovReasonNotBuilt, "auto"))
	o.recordLoadFailure(RegistryIDBirdNETV3, wrappedRefusal(RegistryIDBirdNETV3, ovReasonNotBuilt, "auto"))
	o.syncOpenVINORefusalNotices()
	notices := refusalNotices(t, svc)
	require.Len(t, notices, 1, "one notice per group however many models")
	n := notices[0]
	assert.Equal(t, notification.MsgOpenVINORefusedNotBuiltMessage, n.MessageKey)
	assert.Equal(t, notification.TypeError, n.Type)
	assert.Equal(t, notification.ComponentClassifier, n.Component)
	assert.Empty(t, n.Metadata, "the notice carries no metadata")
	assert.Contains(t, n.Message, ModelRegistry[RegistryIDPerchV2].Name)
	assert.Contains(t, n.Message, ModelRegistry[RegistryIDBirdNETV3].Name)
	assert.Equal(t, "2 models are not loaded", n.Title)

	// A sync with nothing changed raises nothing new.
	o.syncOpenVINORefusalNotices()
	require.Len(t, refusalNotices(t, svc), 1)
	assert.Equal(t, n.ID, refusalNotices(t, svc)[0].ID, "an unchanged signature keeps the notice")

	// v3.0 loads: the notice is replaced and names Perch only.
	o.modelLoadErrors.Delete(RegistryIDBirdNETV3)
	o.syncOpenVINORefusalNotices()
	notices = refusalNotices(t, svc)
	require.Len(t, notices, 1)
	assert.NotContains(t, notices[0].Message, ModelRegistry[RegistryIDBirdNETV3].Name)
	assert.Equal(t, "1 model is not loaded", notices[0].Title)

	// gpu_failed has no refusal notice; a device refusal and a load failure get theirs.
	o.recordLoadFailure(RegistryIDBat, wrappedRefusal(RegistryIDBat, ovReasonGPUFailed, "GPU"))
	o.syncOpenVINORefusalNotices()
	require.Len(t, refusalNotices(t, svc), 1, "gpu_failed is covered by the GPU notice")
	o.recordLoadFailure(RegistryIDBat, wrappedRefusal(RegistryIDBat, ovReasonLoadFailed, "GPU.1"))
	o.recordLoadFailure(RegistryIDBirdNETV3, wrappedRefusal(RegistryIDBirdNETV3, ovReasonGPUUnavailable, "gpu"))
	o.syncOpenVINORefusalNotices()
	keys := map[string]string{}
	for _, nn := range refusalNotices(t, svc) {
		keys[nn.MessageKey] = nn.Message
	}
	require.Len(t, keys, 3)
	assert.Contains(t, keys[notification.MsgOpenVINORefusedLoadFailedMessage], "GPU.1")
	assert.Contains(t, keys[notification.MsgOpenVINORefusedDeviceMissingMessage], "device gpu,")

	// Disabling Perch drops it from its group: the not-built notice goes.
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDBirdNETV3, conf.ModelIDBat)
	o.syncOpenVINORefusalNotices()
	keys = map[string]string{}
	for _, nn := range refusalNotices(t, svc) {
		keys[nn.MessageKey] = nn.Message
	}
	assert.NotContains(t, keys, notification.MsgOpenVINORefusedNotBuiltMessage)
	assert.Len(t, keys, 2)

	// Backend openvino with device auto and no device: the no-device text.
	o.modelLoadErrors.Delete(RegistryIDBat)
	o.modelLoadErrors.Delete(RegistryIDBirdNETV3)
	o.recordLoadFailure(RegistryIDBat, wrappedRefusal(RegistryIDBat, ovReasonNoDevice, ""))
	o.syncOpenVINORefusalNotices()
	notices = refusalNotices(t, svc)
	require.Len(t, notices, 1)
	assert.Equal(t, notification.MsgOpenVINORefusedNoDeviceMessage, notices[0].MessageKey)

	// After Delete-style stop nothing new is raised.
	o.modelLoadErrors.Delete(RegistryIDBat)
	o.syncOpenVINORefusalNotices()
	assert.Empty(t, refusalNotices(t, svc))
	o.stopOpenVINORefusalNotices()
	o.recordLoadFailure(RegistryIDBat, wrappedRefusal(RegistryIDBat, ovReasonNoDevice, ""))
	o.syncOpenVINORefusalNotices()
	assert.Empty(t, refusalNotices(t, svc), "a stopped latch raises nothing")
}

// errToggle holds the error a fake builder returns; nil makes it succeed.
type errToggle struct{ err error }

// refusingPerchBuilder replaces the Perch builder and loader with fakes for
// the test: the builder returns refuse (a refusal) while it is non-nil, else a fresh
// fake instance. Not for parallel tests: it swaps package globals.
func refusingPerchBuilder(t *testing.T, refuse *errToggle) {
	t.Helper()
	build := func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		if refuse.err != nil {
			return nil, refuse.err
		}
		return &reloadFakeModel{id: RegistryIDPerchV2}, nil
	}
	origBuilder, hadBuilder := openvinoCapableSecondaryBuilders[RegistryIDPerchV2]
	openvinoCapableSecondaryBuilders[RegistryIDPerchV2] = build
	origLoader, hadLoader := modelLoaders[RegistryIDPerchV2]
	modelLoaders[RegistryIDPerchV2] = func(o *Orchestrator, threads int) error {
		inst, err := build(o, o.currentSettings(), threads)
		if err != nil {
			return err
		}
		o.models[RegistryIDPerchV2] = &modelEntry{instance: inst}
		return nil
	}
	t.Cleanup(func() {
		if hadBuilder {
			openvinoCapableSecondaryBuilders[RegistryIDPerchV2] = origBuilder
		} else {
			delete(openvinoCapableSecondaryBuilders, RegistryIDPerchV2)
		}
		if hadLoader {
			modelLoaders[RegistryIDPerchV2] = origLoader
		} else {
			delete(modelLoaders, RegistryIDPerchV2)
		}
	})
}

// TestReloadSecondaryModels_RefusalUnloads checks the settings reload of an explicit
// selection OpenVINO cannot honor: the model is unloaded (not left serving on the old
// backend), its refusal is on record and raises one notice, and the reload reports no
// build error. Correcting the selection and retrying loads it again.
func TestReloadSecondaryModels_RefusalUnloads(t *testing.T) {
	svc := setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	refusal := &errToggle{err: wrappedRefusal(RegistryIDPerchV2, ovReasonNotPerchNoDFT, "auto")}
	refusingPerchBuilder(t, refusal)

	old := &reloadFakeModel{id: RegistryIDPerchV2}
	o := newTestOrchestrator(t)
	t.Cleanup(o.stopOpenVINORefusalNotices)
	t.Cleanup(o.acousticNotice.Stop)
	// Built on ONNX Runtime under auto, so the triplet differs from the explicit one.
	o.models[RegistryIDPerchV2] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: conf.BackendPrefAuto}}

	require.NoError(t, o.ReloadSecondaryModels(), "a refusal is not a build error to report")

	assert.False(t, o.IsModelLoaded(RegistryIDPerchV2), "the refused model must be unloaded")
	assert.Equal(t, int32(1), old.closes.Load(), "the old instance is closed by the unload")
	assert.Equal(t, map[string]string{RegistryIDPerchV2: string(ovReasonNotPerchNoDFT)}, o.LoadFailureReasons())
	notices := refusalNotices(t, svc)
	require.Len(t, notices, 1)
	assert.Equal(t, notification.MsgOpenVINORefusedModelUnsupportedMessage, notices[0].MessageKey)
	// Recorded before the unload, so the acoustic-model notice sees a load failure
	// rather than "nothing installed".
	all, err := svc.List(nil)
	require.NoError(t, err)
	var acoustic []*notification.Notification
	for _, n := range all {
		if n.TitleKey == notification.MsgAcousticModelsLoadFailedTitle || n.TitleKey == notification.MsgAcousticModelsNoneTitle {
			acoustic = append(acoustic, n)
		}
	}
	require.Len(t, acoustic, 1)
	assert.Equal(t, notification.MsgAcousticModelsLoadFailedTitle, acoustic[0].TitleKey)

	// The user switches the device selection: the retry loads the model and the
	// notice goes.
	refusal.err = nil
	publishRefusalSettings(t, conf.BackendPrefAuto, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	assert.Equal(t, []string{RegistryIDPerchV2}, o.RetryFailedLoadsForBackendChange())
	assert.True(t, o.IsModelLoaded(RegistryIDPerchV2))
	assert.Empty(t, refusalNotices(t, svc))
	assert.Empty(t, o.LoadFailureReasons())
}

// TestReloadForVariantSwap_RefusalKeepsOldInstance checks that a variant swap refused
// by OpenVINO stays gapless: the old instance keeps serving, nothing is recorded and
// no notice is raised, because the caller rolls the variant back.
func TestReloadForVariantSwap_RefusalKeepsOldInstance(t *testing.T) {
	svc := setupTestNotification(t)
	publishRefusalSettings(t, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, conf.ModelIDPerchV2)
	refusal := &errToggle{err: wrappedRefusal(RegistryIDPerchV2, ovReasonNotPerchNoDFT, "auto")}
	refusingPerchBuilder(t, refusal)

	old := &reloadFakeModel{id: RegistryIDPerchV2}
	o := newTestOrchestrator(t)
	t.Cleanup(o.stopOpenVINORefusalNotices)
	o.models[RegistryIDPerchV2] = &modelEntry{instance: old}

	err := o.ReloadForVariantSwap(RegistryIDPerchV2)
	_, refused := errors.AsType[*OpenVINORefusalError](err)
	require.True(t, refused, "the swap reports the refusal, got %v", err)
	assert.True(t, o.IsModelLoaded(RegistryIDPerchV2), "the old variant keeps serving")
	assert.Equal(t, int32(0), old.closes.Load())
	assert.Empty(t, o.LoadFailureReasons())
	assert.Empty(t, refusalNotices(t, svc))
}

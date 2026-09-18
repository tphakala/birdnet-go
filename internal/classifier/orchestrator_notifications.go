package classifier

import (
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// checkORTOrFail checks ONNX Runtime availability and returns a descriptive
// error with a log warning and bell notification if ORT is not available.
// Returns nil when ORT is ready. Pass empty component to omit it from the error.
func checkORTOrFail(configuredPath, modelName, modelContext, component string) error {
	ortStatus := inference.CheckORTAvailability(configuredPath)
	if ortStatus.Available {
		return nil
	}

	log := GetLogger()
	log.Warn(modelName+" requires ONNX Runtime which is not available",
		logger.String("error", ortStatus.Error))
	emitORTUnavailableNotification(modelName, ortStatus.Error)

	b := errors.Newf("%s requires ONNX Runtime %s: %s",
		modelName, inference.ORTRequiredVersion(), ortStatus.Error).
		Category(errors.CategoryModelInit).
		Context("model", modelContext).
		Context("ort_error", ortStatus.Error)
	if component != "" {
		b = b.Component(component)
	}
	return b.Build()
}

// emitORTUnavailableNotification sends a bell notification when a model
// cannot load because ONNX Runtime is missing or incompatible.
func emitORTUnavailableNotification(modelName, ortError string) {
	svc := notification.GetService()
	if svc == nil {
		return
	}

	requiredVersion := inference.ORTRequiredVersion()
	notif := notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityHigh,
		fmt.Sprintf("ONNX Runtime required for %s", modelName),
		fmt.Sprintf("ONNX Runtime %s is required for %s but is not available. %s",
			requiredVersion, modelName, ortError),
	).
		WithComponent("classifier").
		WithTitleKey(notification.MsgORTUnavailableTitle, nil).
		WithMessageKey(notification.MsgORTUnavailableMessage, map[string]any{
			"modelName":       modelName,
			"requiredVersion": requiredVersion,
			"installGuideURL": inference.ORTInstallGuideURL,
		}).
		WithDeliveryTarget("bell")

	_ = svc.CreateWithMetadata(notif)
}

// acousticModelsNotice is the single persistent "no acoustic model" bell notification
// per process: raised while AcousticModelsState is not ok and deleted on the first
// successful load. id is the live notification's ID, "" when none is outstanding.
type acousticModelsNotice struct {
	mu sync.Mutex
	id string
}

// syncAcousticModelsNotice raises the persistent bell notification when no acoustic
// model is loaded and clears it once one is (model de-privilege epic, Phase 4).
// Idempotent and nil-safe on the notification service (not yet initialized, or in
// tests): a raise that finds no service latches nothing, so the next call
// (NewOrchestrator, ScanInstalled, LoadModel, UnloadModel) retries it. Across restarts
// the in-memory store is empty, so "raised once per process" is the dedupe.
func (o *Orchestrator) syncAcousticModelsNotice() {
	svc := notification.GetService()
	if svc == nil {
		return
	}
	o.acousticNotice.mu.Lock()
	defer o.acousticNotice.mu.Unlock()
	// Read the state under the latch lock so a concurrent LoadModel/UnloadModel/
	// ScanInstalled sync cannot act on a state that disagrees with the latch (raise a
	// notice another just cleared, or clear one another just raised).
	state := o.AcousticModelsState()
	switch {
	case state != AcousticModelsOK && o.acousticNotice.id == "":
		notif := newAcousticModelsNotification(state)
		if err := svc.CreateWithMetadata(notif); err == nil {
			o.acousticNotice.id = notif.ID
		}
	case state == AcousticModelsOK && o.acousticNotice.id != "":
		_ = svc.Delete(o.acousticNotice.id) // an already user-dismissed notification is fine
		o.acousticNotice.id = ""
	}
}

// newAcousticModelsNotification builds the persistent bell notification for a not-ok
// acoustic-model state. The two states carry different remedies: none_installed points at
// the gallery to install a model; load_failed points at the inference page because a
// model is installed but failed to load (missing ONNX Runtime, a corrupt or incompatible
// file), so "install one" would misdirect the user. The message key carries the state so
// a localized surface can differentiate further.
func newAcousticModelsNotification(state AcousticModelsState) *notification.Notification {
	title := "No acoustic model installed"
	message := "No acoustic model is loaded, so audio is captured but not analyzed. Open the model gallery in Settings > Analysis to install one."
	titleKey := notification.MsgAcousticModelsNoneTitle
	messageKey := notification.MsgAcousticModelsNoneMessage
	if state == AcousticModelsLoadFailed {
		title = "Acoustic model failed to load"
		message = "An enabled acoustic model failed to load, so audio is captured but not analyzed. Check the model on the System > Inference page."
		titleKey = notification.MsgAcousticModelsLoadFailedTitle
		messageKey = notification.MsgAcousticModelsLoadFailedMessage
	}
	return notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityHigh,
		title,
		message,
	).
		WithComponent("classifier").
		WithTitleKey(titleKey, nil).
		WithMessageKey(messageKey, map[string]any{"state": string(state)}).
		WithMetadata("acoustic_models_state", string(state)).
		WithDeliveryTarget("bell")
}

// SyncAcousticModelsNotice is the exported entry point for ModelManager.ScanInstalled,
// which evaluates the notice once startup loading is complete and the notification
// service is certainly up.
func (o *Orchestrator) SyncAcousticModelsNotice() { o.syncAcousticModelsNotice() }

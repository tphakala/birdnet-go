package classifier

import (
	"fmt"

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
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(notification.MsgORTUnavailableTitle, nil).
		WithMessageKey(notification.MsgORTUnavailableMessage, map[string]any{
			"modelName":       modelName,
			"requiredVersion": requiredVersion,
			"installGuideURL": inference.ORTInstallGuideURL,
		}).
		WithDeliveryTarget(notification.DeliveryTargetBell)

	_ = svc.CreateWithMetadata(notif)
}

// syncAcousticModelsNotice raises the persistent bell notification when no acoustic model is
// loaded, replaces it when the not-ok state changes (the remedy text differs), and clears it
// once a model is loaded (model de-privilege epic, Phase 4). The latch is a
// notification.PersistentNotice keyed by the not-ok state. Idempotent and nil-safe on the
// notification service (not yet initialized, or in tests): a raise that finds no service
// latches nothing, so the next call (NewOrchestrator, ScanInstalled, LoadModel, UnloadModel)
// retries it, and a failed create is re-armed a bounded number of times. Across restarts the
// in-memory store is empty, so "raised once per process" is the dedupe.
func (o *Orchestrator) syncAcousticModelsNotice() {
	svc := notification.GetService()
	if svc == nil {
		return
	}
	o.acousticNotice.SetRetry(o.syncAcousticModelsNotice)
	// Reconcile reads the state under the latch lock, so a concurrent LoadModel/UnloadModel/
	// ScanInstalled sync cannot act on a state that disagrees with the latch (raise a notice
	// another just cleared, or clear one another just raised).
	_ = o.acousticNotice.Reconcile(svc, func() (string, func() *notification.Notification) {
		state := o.AcousticModelsState()
		if state == AcousticModelsOK {
			return "", nil
		}
		return string(state), func() *notification.Notification {
			return newAcousticModelsNotification(state)
		}
	})
}

// newAcousticModelsNotification builds the persistent bell notification for a not-ok
// acoustic-model state. The two states carry different remedies: none_installed covers both
// "nothing installed" and "nothing enabled" (models.enabled is empty), so its text offers
// enabling or installing; load_failed points at the inference page because a model is
// installed but failed to load (missing ONNX Runtime, a corrupt or incompatible file), so
// "install one" would misdirect the user. The message key carries the state so a localized
// surface can differentiate further.
func newAcousticModelsNotification(state AcousticModelsState) *notification.Notification {
	title := "No acoustic model enabled"
	message := "No acoustic model is loaded, so audio is captured but not analyzed. Enable a model in Settings > Analysis, or install one from the model gallery."
	titleKey := notification.MsgAcousticModelsNoneTitle
	messageKey := notification.MsgAcousticModelsNoneMessage
	if state == AcousticModelsLoadFailed {
		title = "Acoustic model failed to load"
		message = "An enabled acoustic model failed to load, so audio is captured but not analyzed. Check the model on the System > AI Models page."
		titleKey = notification.MsgAcousticModelsLoadFailedTitle
		messageKey = notification.MsgAcousticModelsLoadFailedMessage
	}
	return notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityHigh,
		title,
		message,
	).
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(titleKey, nil).
		WithMessageKey(messageKey, map[string]any{"state": string(state)}).
		WithMetadata("acoustic_models_state", string(state)).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// SyncAcousticModelsNotice is the exported entry point for ModelManager.ScanInstalled, which
// evaluates the notice once startup loading is complete and the notification service is
// certainly up.
func (o *Orchestrator) SyncAcousticModelsNotice() { o.syncAcousticModelsNotice() }

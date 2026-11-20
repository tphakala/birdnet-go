package classifier

import (
	"fmt"
	"strings"

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
			"requiredVersion": inference.ORTMinimumVersion(),
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
// retries it, and a failed create is re-armed a bounded number of times. A failed update is
// logged at WARN. Across restarts the in-memory store is empty, so "raised once per process"
// is the dedupe.
func (o *Orchestrator) syncAcousticModelsNotice() {
	svc := notification.GetService()
	if svc == nil {
		return
	}
	o.acousticNotice.SetRetry(o.syncAcousticModelsNotice)
	// Reconcile reads the state under the latch lock, so a concurrent LoadModel/UnloadModel/
	// ScanInstalled sync cannot act on a state that disagrees with the latch (raise a notice
	// another just cleared, or clear one another just raised).
	if err := o.acousticNotice.Reconcile(svc, func() (string, func() *notification.Notification) {
		state := o.AcousticModelsState()
		if state == AcousticModelsOK {
			return "", nil
		}
		return string(state), func() *notification.Notification {
			return newAcousticModelsNotification(state)
		}
	}); err != nil {
		GetLogger().Warn("failed to update acoustic model notification", logger.Error(err))
	}
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

const (
	// geomodelSignatureSep separates the issue kind from the model IDs in a geomodel
	// notice signature; geomodelModelIDSep separates the model IDs from each other.
	geomodelSignatureSep = ":"
	geomodelModelIDSep   = ","
	// geomodelModelNameSep joins the model names in the notice text.
	geomodelModelNameSep = ", "

	// English fallbacks for the geomodel notice. They equal the notifications.content.geomodel
	// strings in frontend/static/messages/en.json, with %s in place of {models}.
	geomodelNotInstalledTitle   = "Geomodel not installed"
	geomodelNotInstalledMessage = "These models need BirdNET Geomodel v3.0 to filter detections by location: %s. Install BirdNET Geomodel v3.0 from the model gallery in Settings > Analysis > Models."
	geomodelLoadFailedTitle     = "Geomodel failed to load"
	geomodelLoadFailedMessage   = "BirdNET Geomodel v3.0 could not be loaded, so these models are not filtered by location: %s. Check the application log, then install or reinstall BirdNET Geomodel v3.0 from the model gallery in Settings > Analysis > Models."
)

// geomodelNoticeFor derives the geomodel notice from a range filter status. It returns an
// empty signature when no notice is due: no geomodel issue, a deliberate custom range filter
// (not_selected, which has no install remedy), or no loaded model that needs the geomodel
// and is left uncovered. Otherwise it returns a signature of the issue kind and the affected
// model IDs, the issue, and the display names of those models in status order. BirdNET v2.4
// is never listed because it does not need the geomodel.
func geomodelNoticeFor(st *RangeFilterStatusResponse) (sig string, issue GeomodelIssue, models []string) {
	if st.GeomodelIssue != GeomodelIssueNotInstalled && st.GeomodelIssue != GeomodelIssueLoadFailed {
		return "", st.GeomodelIssue, nil
	}
	var ids []string
	for _, c := range st.Classifiers {
		if rangeFilterCompatFor(c.ID) == rangeFilterCompatGeomodel && !c.CoveredByBackend {
			ids = append(ids, c.ID)
			models = append(models, c.Name)
		}
	}
	if len(ids) == 0 {
		return "", st.GeomodelIssue, nil
	}
	return string(st.GeomodelIssue) + geomodelSignatureSep + strings.Join(ids, geomodelModelIDSep), st.GeomodelIssue, models
}

// newGeomodelNotification builds the persistent bell notification for a missing or failed
// BirdNET Geomodel. The text names the path in Settings because a notification has no link
// field. No metadata: the bell renders every metadata key as a raw line.
func newGeomodelNotification(issue GeomodelIssue, models []string) *notification.Notification {
	title, message := geomodelNotInstalledTitle, geomodelNotInstalledMessage
	titleKey, messageKey := notification.MsgGeomodelNotInstalledTitle, notification.MsgGeomodelNotInstalledMessage
	if issue == GeomodelIssueLoadFailed {
		title, message = geomodelLoadFailedTitle, geomodelLoadFailedMessage
		titleKey, messageKey = notification.MsgGeomodelLoadFailedTitle, notification.MsgGeomodelLoadFailedMessage
	}
	names := strings.Join(models, geomodelModelNameSep)
	return notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityMedium,
		title,
		fmt.Sprintf(message, names),
	).
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(titleKey, nil).
		WithMessageKey(messageKey, map[string]any{"models": names}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// geomodelNoticeService returns the notification service the geomodel notice is written to,
// or nil while it is not initialized. A variable so a test can substitute a service whose
// calls fail.
var geomodelNoticeService = notification.DefaultNoticeService

// syncGeomodelNotice raises the persistent bell notification when a loaded model that needs
// the BirdNET Geomodel (Perch v2, BirdNET v3.0, BirdNET+ v3.0) is not served by it, replaces
// it when the issue or the affected model set changes, and clears it once the geomodel serves
// every such model. The state is computed from RangeFilterStatus inside the latch's compute
// callback, the same status the range_filter health check reads. Triggered by the range
// filter service after every reload (rangeFilterService.afterReload), by ScanInstalled
// (SyncGeomodelNotice) and by Delete. It must never run with o.mu, a modelEntry lock or the
// range filter service's buildMu or mu held: the latch lock is taken first and
// RangeFilterStatus then takes o.mu. Callers may hold o.reloadMu (orchestrator_reload.go:66)
// or the model manager's mu (model_manager.go:1098, held across the range filter reload at
// :1206): nothing here takes either.
// The notice is written through geomodelNoticeService. Nil-safe on it: a raise that finds no
// service latches nothing, so the next trigger retries it, and a failed create or delete is
// re-armed a bounded number of times. A failed update is logged at WARN.
func (o *Orchestrator) syncGeomodelNotice() {
	svc := geomodelNoticeService()
	// NewOrchestrator always builds the range filter service (orchestrator.go:362); without
	// one there is no backend state to diagnose, so a hand-built orchestrator is skipped.
	if svc == nil || o.rangeFilter == nil {
		return
	}
	o.geomodelNotice.SetRetry(o.syncGeomodelNotice)
	if err := o.geomodelNotice.Reconcile(svc, func() (string, func() *notification.Notification) {
		st := o.RangeFilterStatus()
		sig, issue, models := geomodelNoticeFor(&st)
		if sig == "" {
			return "", nil
		}
		return sig, func() *notification.Notification {
			return newGeomodelNotification(issue, models)
		}
	}); err != nil {
		GetLogger().Warn("failed to update geomodel notification", logger.Error(err))
	}
}

// SyncGeomodelNotice is the exported entry point for ModelManager.ScanInstalled, which
// evaluates the notice once startup loading is done and the notification service is
// certainly up.
func (o *Orchestrator) SyncGeomodelNotice() { o.syncGeomodelNotice() }

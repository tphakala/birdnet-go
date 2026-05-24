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

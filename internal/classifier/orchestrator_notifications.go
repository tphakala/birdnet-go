package classifier

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// emitORTUnavailableNotification sends a bell notification when a model
// cannot load because ONNX Runtime is missing or incompatible.
func emitORTUnavailableNotification(modelName, ortError string) {
	svc := notification.GetService()
	if svc == nil {
		return
	}

	notif := notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityHigh,
		fmt.Sprintf("ONNX Runtime required for %s", modelName),
		fmt.Sprintf("ONNX Runtime %s is required for %s but is not available. %s",
			inference.ORTRequiredVersion(), modelName, ortError),
	).
		WithComponent("classifier").
		WithTitleKey(notification.MsgORTUnavailableTitle, nil).
		WithMessageKey(notification.MsgORTUnavailableMessage, map[string]any{
			"modelName":       modelName,
			"requiredVersion": inference.ORTRequiredVersion(),
		}).
		WithDeliveryTarget("bell")

	_ = svc.CreateWithMetadata(notif)
}

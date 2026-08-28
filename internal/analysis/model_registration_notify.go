package analysis

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/notification"
)

// modelNotRegisteredRenotifyInterval is how long the same source-and-models
// combination stays suppressed after being reported. Source registration runs
// on every start and on every settings-driven reconfigure, so an unresolved
// problem would otherwise raise a fresh notification on each pass, and a stream
// that reconnects in a loop would bury the notification list.
const modelNotRegisteredRenotifyInterval = 6 * time.Hour

var modelNotRegisteredSeen sync.Map // key: source + models, value: time.Time of last notification

// notifyModelsNotRegistered reports that models assigned to an audio source are
// not receiving its audio, either because they never loaded or because their
// analysis buffer could not be allocated.
//
// This condition used to be invisible. registerConsumersForSources skips such a
// model with a log warning and, when a source ends up with no targets at all,
// silently falls back to the primary model, so the pipeline starts looking
// healthy while analyzing with fewer models than configured. The reporters of
// GitHub #4201 and #4204 each lost a model for days before noticing by
// accident.
func notifyModelsNotRegistered(sourceName string, modelIDs []string) {
	if len(modelIDs) == 0 {
		return
	}
	svc := notification.GetService()
	if svc == nil {
		return
	}

	models := strings.Join(modelIDs, ", ")
	key := sourceName + "\x00" + models
	now := time.Now()
	if last, ok := modelNotRegisteredSeen.Load(key); ok {
		if t, isTime := last.(time.Time); isTime && now.Sub(t) < modelNotRegisteredRenotifyInterval {
			return
		}
	}
	modelNotRegisteredSeen.Store(key, now)

	notif := notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityHigh,
		fmt.Sprintf("Model not analyzing %s", sourceName),
		fmt.Sprintf("%s is assigned to audio source %q but is not receiving audio, so it is not producing "+
			"detections. The model is most likely not installed or failed to load. Check the model gallery "+
			"in Settings.", models, sourceName),
	).
		WithComponent("analysis.audio_pipeline").
		WithTitleKey(notification.MsgModelNotRegisteredTitle, map[string]any{
			"sourceName": sourceName,
		}).
		WithMessageKey(notification.MsgModelNotRegisteredMessage, map[string]any{
			"sourceName": sourceName,
			"models":     models,
		}).
		WithDeliveryTarget("bell")

	_ = svc.CreateWithMetadata(notif)
}

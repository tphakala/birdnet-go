package analysis

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
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

	notif := notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityMedium,
		fmt.Sprintf("Model not analyzing %s", sourceName),
		// State only what is known: the model is assigned but not currently
		// receiving audio. The old "most likely not installed or failed to load"
		// was wrong for the allocation-failure branch, and worse in the
		// primary-fallback case where it could tell the user the built-in BirdNET
		// model is not installed.
		fmt.Sprintf("%s is assigned to audio source %q but is not currently receiving audio, so it is not "+
			"producing detections. Open the model gallery in Settings to check its status.", models, sourceName),
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

	// Arm the 6h suppression window only after the notification is actually
	// created. The service rate-limits, so storing the key before the create (or
	// on a failed create) would silence a genuine condition for six hours behind a
	// notification the user never saw.
	if err := svc.CreateWithMetadata(notif); err != nil {
		GetLogger().Warn("failed to create model-not-registered notification",
			logger.String("source", sourceName),
			logger.String("models", models),
			logger.Error(err))
		return
	}
	modelNotRegisteredSeen.Store(key, now)
}

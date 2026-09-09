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

var modelNotRegisteredSeen sync.Map // key: modelNotRegisteredKey(sourceID, models), value: time.Time of last notification

// modelNotRegisteredKeySep separates the source ID from the model list in a
// suppression-map key. It is a NUL byte so it cannot occur inside a source ID (RTSP
// URLs and device IDs never contain NUL), which keeps a source-ID prefix an unambiguous
// match in clearModelNotRegistered.
const modelNotRegisteredKeySep = "\x00"

// modelNotRegisteredKey composes the suppression-map key for a source and its
// unregistered-model list. notifyModelsNotRegistered (which writes keys) and
// clearModelNotRegistered (which matches them by source-ID prefix) both build their keys
// from this one definition, so the two key formats cannot drift apart.
func modelNotRegisteredKey(sourceID, models string) string {
	return sourceID + modelNotRegisteredKeySep + models
}

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
func notifyModelsNotRegistered(sourceID, sourceName string, modelIDs []string) {
	if len(modelIDs) == 0 {
		return
	}
	svc := notification.GetService()
	if svc == nil {
		return
	}

	models := strings.Join(modelIDs, ", ")
	// Key the suppression window by the unique source ID, not the display name: display
	// names can collide (two streams both named "Backyard"), and a shared key would let
	// one source's recovery clear another's window, or one source's failure gate another's
	// notification. The human-readable sourceName is used only for the notification text.
	key := modelNotRegisteredKey(sourceID, models)
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
			"producing detections. Open System > AI Models to check its per-source status, and confirm the "+
			"audio source is connected and sending audio.", models, sourceName),
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

// clearModelNotRegistered drops every suppression entry for a source, so the next genuine
// "model not analyzing" condition re-notifies immediately instead of being silenced for the
// remainder of the 6h window. Call it when a source registers all its assigned models
// successfully (recovery) and when a source is removed. The suppression key is
// sourceID + "\x00" + models and the previously reported model set is not known here, so
// every entry under the sourceID prefix is cleared. Deleting during Range is safe for
// sync.Map.
func clearModelNotRegistered(sourceID string) {
	prefix := sourceID + modelNotRegisteredKeySep
	modelNotRegisteredSeen.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && strings.HasPrefix(key, prefix) {
			modelNotRegisteredSeen.Delete(key)
		}
		return true
	})
}

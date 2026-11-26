package classifier

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// openVINORefusalGroup is one bell notice for a class of OpenVINO refusals that
// share a fix, so one misconfiguration raises one notice however many models it
// stops. A refusal whose reason has no group (gpu_failed) is covered by the GPU
// notice instead.
type openVINORefusalGroup int

const (
	refusalGroupNotBuilt openVINORefusalGroup = iota
	refusalGroupDeviceMissing
	refusalGroupNoDevice
	refusalGroupModelUnsupported
	refusalGroupLoadFailed
	refusalGroupCount
)

const (
	// refusalSignatureSep separates the parts of a refusal notice signature, and
	// refusalModelIDSep the model IDs within it.
	refusalSignatureSep = "|"
	refusalModelIDSep   = ","
	// refusalModelNameSep joins the model names in the notice text.
	refusalModelNameSep = ", "
	// refusalTitleOne and refusalTitleMany are the English fallbacks of the plural
	// title notifications.content.openvinoRefused.title; the count replaces %d.
	refusalTitleOne  = "1 model is not loaded"
	refusalTitleMany = "%d models are not loaded"

	// English fallbacks for the refusal notices. They equal the
	// notifications.content.openvinoRefused strings in frontend/static/messages/en.json,
	// with {models} and {device} filled in by refusalMessageReplacer.
	refusalNotBuiltMessage      = "These models are set to run on OpenVINO, but this build of BirdNET-Go has no OpenVINO support, so they are not loaded: {models}. Set the inference backend and the OpenVINO device to Auto in Settings > Analysis > Inference."
	refusalDeviceMissingMessage = "These models are set to run on the OpenVINO device {device}, which cannot be used on this system, so they are not loaded: {models}. Choose another OpenVINO device, or set the backend and the OpenVINO device to Auto, in Settings > Analysis > Inference."
	refusalNoDeviceMessage      = "The inference backend is set to OpenVINO, but this system has no OpenVINO device that can run these models, so they are not loaded: {models}. Set the backend to Auto or ONNX Runtime in Settings > Analysis > Inference."
	refusalModelUnsupportedMsg  = "These models are set to run on OpenVINO, but their model files cannot run on OpenVINO, so they are not loaded: {models}. Install their OpenVINO variant from the model gallery in Settings > Analysis > Models, or set the backend and the OpenVINO device to Auto in Settings > Analysis > Inference."
	refusalLoadFailedMessage    = "OpenVINO could not load these models on {device}, so they are not loaded: {models}. The application log names the OpenVINO error. Choose another OpenVINO device, or set the backend and the OpenVINO device to Auto, in Settings > Analysis > Inference."
	refusalPlaceholderModels    = "{models}"
	refusalPlaceholderDevice    = "{device}"
)

// refusalGroupFor maps a reason code to its notice group, or false when the reason
// has none.
func refusalGroupFor(reason OpenVINOReason) (openVINORefusalGroup, bool) {
	switch reason {
	case ovReasonNotBuilt:
		return refusalGroupNotBuilt, true
	case ovReasonGPUUnavailable, ovReasonGPUDeviceMissing, ovReasonCPUUnsupported:
		return refusalGroupDeviceMissing, true
	case ovReasonNoDevice:
		return refusalGroupNoDevice, true
	case ovReasonNotBirdNETv24, ovReasonNotPerchNoDFT:
		return refusalGroupModelUnsupported, true
	case ovReasonInitFailed, ovReasonLoadFailed:
		return refusalGroupLoadFailed, true
	default:
		return 0, false
	}
}

// namesDevice reports whether the notice text of the group names the device, so
// the device belongs in its signature.
func (g openVINORefusalGroup) namesDevice() bool {
	return g == refusalGroupDeviceMissing || g == refusalGroupLoadFailed
}

// openVINORefusalNotices holds one latched bell notice per refusal group. mu
// serializes whole syncs and is taken with no orchestrator lock held; nothing
// orchestrator-side is acquired under it except the lock-free reads of the load
// failure records and the published settings.
type openVINORefusalNotices struct {
	mu     sync.Mutex
	groups [refusalGroupCount]notification.PersistentNotice
}

// refusalGroupState is the models one group's notice currently names.
type refusalGroupState struct {
	ids    []string
	names  []string
	device string // device of the first model in ID order; "" when no model
}

// openVINORefusalState groups the enabled models whose last load was an OpenVINO
// refusal, in registry ID order within each group. A model that loaded, was
// disabled or failed for another reason is absent.
func (o *Orchestrator) openVINORefusalState() [refusalGroupCount]refusalGroupState {
	type entry struct {
		id      string
		group   openVINORefusalGroup
		device  string
		display string
	}
	var entries []entry
	o.modelLoadErrors.Range(func(key, value any) bool {
		id, idOK := key.(string)
		failure, failureOK := value.(loadFailure)
		if !idOK || !failureOK || failure.reason == "" || !o.modelIDEnabled(id) {
			return true
		}
		group, grouped := refusalGroupFor(failure.reason)
		if !grouped {
			return true
		}
		display := id
		if info, known := ModelRegistry[id]; known && info.Name != "" {
			display = info.Name
		}
		entries = append(entries, entry{id: id, group: group, device: failure.device, display: display})
		return true
	})
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.id, b.id) })

	var state [refusalGroupCount]refusalGroupState
	for _, e := range entries {
		st := &state[e.group]
		if len(st.ids) == 0 {
			st.device = e.device
		}
		st.ids = append(st.ids, e.id)
		st.names = append(st.names, e.display)
	}
	return state
}

// signature identifies the notice for the group: the device (only where the text
// names it) and the model IDs. "" means no notice is due.
func (st *refusalGroupState) signature(g openVINORefusalGroup) string {
	if len(st.ids) == 0 {
		return ""
	}
	device := ""
	if g.namesDevice() {
		device = st.device
	}
	return fmt.Sprintf("%d%s%s%s%s", int(g), refusalSignatureSep, device, refusalSignatureSep, strings.Join(st.ids, refusalModelIDSep))
}

// newOpenVINORefusalNotification builds the bell notice for one refusal group. The
// text names the setting to change because a notification has no link field. No
// metadata: the bell renders every metadata key as a raw line.
func newOpenVINORefusalNotification(g openVINORefusalGroup, st *refusalGroupState) *notification.Notification {
	var message, messageKey string
	switch g {
	case refusalGroupNotBuilt:
		message, messageKey = refusalNotBuiltMessage, notification.MsgOpenVINORefusedNotBuiltMessage
	case refusalGroupDeviceMissing:
		message, messageKey = refusalDeviceMissingMessage, notification.MsgOpenVINORefusedDeviceMissingMessage
	case refusalGroupNoDevice:
		message, messageKey = refusalNoDeviceMessage, notification.MsgOpenVINORefusedNoDeviceMessage
	case refusalGroupModelUnsupported:
		message, messageKey = refusalModelUnsupportedMsg, notification.MsgOpenVINORefusedModelUnsupportedMessage
	case refusalGroupLoadFailed:
		message, messageKey = refusalLoadFailedMessage, notification.MsgOpenVINORefusedLoadFailedMessage
	case refusalGroupCount:
		return nil
	}
	names := strings.Join(st.names, refusalModelNameSep)
	title := refusalTitleOne
	if len(st.ids) != 1 {
		title = fmt.Sprintf(refusalTitleMany, len(st.ids))
	}
	text := strings.NewReplacer(refusalPlaceholderModels, names, refusalPlaceholderDevice, st.device).Replace(message)
	return notification.NewNotification(
		notification.TypeError,
		notification.PriorityHigh,
		title,
		text,
	).
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(notification.MsgOpenVINORefusedTitle, map[string]any{"count": len(st.ids)}).
		WithMessageKey(messageKey, map[string]any{"count": len(st.ids), "models": names, "device": st.device}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// syncOpenVINORefusalNotices raises one persistent bell notice per refusal group
// that has models, replaces it when the group's models or device change, and
// clears it when the group empties (the model loaded, was disabled or uninstalled).
// Nil-safe on the notification service: a raise that finds no service latches
// nothing, so the next trigger retries it, and a failed create is re-armed a bounded
// number of times. Call it with no orchestrator lock held, after the load failure
// records change (startup load, LoadModel, a reload that unloaded a refused model,
// ScanInstalled and an uninstall).
func (o *Orchestrator) syncOpenVINORefusalNotices() {
	svc := notification.DefaultNoticeService()
	if svc == nil {
		return
	}
	n := &o.openVINORefusals
	n.mu.Lock()
	defer n.mu.Unlock()
	state := o.openVINORefusalState()
	for g := range refusalGroupCount {
		st := &state[g]
		n.groups[g].SetRetry(o.syncOpenVINORefusalNotices)
		if err := n.groups[g].Reconcile(svc, func() (string, func() *notification.Notification) {
			sig := st.signature(g)
			if sig == "" {
				return "", nil
			}
			return sig, func() *notification.Notification { return newOpenVINORefusalNotification(g, st) }
		}); err != nil {
			GetLogger().Warn("failed to update OpenVINO refusal notification",
				logger.Int("group", int(g)), logger.Error(err))
		}
	}
}

// SyncOpenVINORefusalNotices is the exported entry point for the model manager,
// which evaluates the notices once startup loading is complete and the notification
// service is certainly up, and after an uninstall.
func (o *Orchestrator) SyncOpenVINORefusalNotices() { o.syncOpenVINORefusalNotices() }

// stopOpenVINORefusalNotices stops the retries and the raising of new notices; a
// notice already raised stays in the store, like the other persistent notices.
func (o *Orchestrator) stopOpenVINORefusalNotices() {
	n := &o.openVINORefusals
	n.mu.Lock()
	defer n.mu.Unlock()
	for g := range refusalGroupCount {
		n.groups[g].Stop()
	}
}

package classifier

// orchestrator_inference_health.go tracks, per loaded model, whether its
// analysis windows are succeeding, and surfaces a model that fails every window
// (#4423: a Raspberry Pi 5 whose stock model returned non-finite scores on every
// window looked healthy everywhere but the log). PredictModel records each call
// in lock-free atomics; the transitions into and out of the failing state kick
// an asynchronous reconcile that raises or clears a persistent bell notice per
// model and tells observers (the dashboard banner) that the state changed. The
// same record backs the per-model telemetry on the inference status API and the
// inference_failures health check.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// InferenceFailureNoticeThreshold is the number of consecutive failed analysis
// windows after which a loaded model is reported as failing: the bell notice,
// the status API state, the dashboard banner and the health check all use it.
// At a few seconds per window it is well under a minute of total failure, and
// long enough that one bad window (a transient backend hiccup) never raises it.
const InferenceFailureNoticeThreshold = 10

// inferenceFailureLogEvery is the interval, in consecutive failures of one
// model, at which PredictModel repeats its ERROR log after the first failure.
// While a model is failing, the same interval re-kicks the failure notice
// reconcile, a bounded periodic re-check if an earlier raise could not be made.
const inferenceFailureLogEvery = 100

// Stable error-class tokens for a failed analysis window. They name the class of
// failure, never the raw error text, so the notice and the status API carry no
// file paths or other details from the error.
const (
	InferenceErrorClassNonFinite = "non_finite_output"
	InferenceErrorClassOther     = "inference_error"
)

// inferenceErrorClass is the compact form of the error-class tokens stored in
// the per-model record without allocating on the hot path.
type inferenceErrorClass uint32

const (
	errorClassNone inferenceErrorClass = iota
	errorClassNonFinite
	errorClassOther
)

// String returns the error-class token, "" for errorClassNone.
func (c inferenceErrorClass) String() string {
	switch c {
	case errorClassNonFinite:
		return InferenceErrorClassNonFinite
	case errorClassOther:
		return InferenceErrorClassOther
	default:
		return ""
	}
}

// classifyInferenceError maps a PredictModel error to its class.
func classifyInferenceError(err error) inferenceErrorClass {
	if errors.Is(err, ErrNonFiniteScore) {
		return errorClassNonFinite
	}
	return errorClassOther
}

// modelInferenceHealth is the per-model inference record. All fields are atomics
// so PredictModel can update them under its own locks without adding one.
type modelInferenceHealth struct {
	streak        atomic.Int64  // consecutive failed windows; 0 after a success
	count         atomic.Int64  // analysis windows run (succeeded or failed)
	lastAtNs      atomic.Int64  // last window's completion, Unix ns; 0 = never
	lastSuccessNs atomic.Int64  // last successful window, Unix ns; 0 = never
	lastErrClass  atomic.Uint32 // inferenceErrorClass of the latest failure
}

// inferenceHealthRecords holds the per-model records keyed by registry ID
// (value: *modelInferenceHealth). A record lives as long as the model instance:
// it is dropped when the instance is unloaded or replaced, so a reloaded model
// starts fresh and stale IDs do not accumulate.
//
//nolint:gochecknoglobals // shared with globalInferenceCounters, same lifecycle
var inferenceHealthRecords sync.Map

// inferenceHealthFor returns modelID's record, creating it on first use.
func inferenceHealthFor(modelID string) *modelInferenceHealth {
	if v, ok := inferenceHealthRecords.Load(modelID); ok {
		return v.(*modelInferenceHealth) //nolint:errcheck // stored type is fixed
	}
	v, _ := inferenceHealthRecords.LoadOrStore(modelID, &modelInferenceHealth{})
	return v.(*modelInferenceHealth) //nolint:errcheck // stored type is fixed
}

// dropInferenceHealth removes modelID's record when its instance is torn down
// or replaced.
func dropInferenceHealth(modelID string) {
	inferenceHealthRecords.Delete(modelID)
}

// recordInferenceFailure records a failed window and returns the new streak.
func recordInferenceFailure(modelID string, err error, now time.Time) int64 {
	h := inferenceHealthFor(modelID)
	h.count.Add(1)
	h.lastAtNs.Store(now.UnixNano())
	h.lastErrClass.Store(uint32(classifyInferenceError(err)))
	return h.streak.Add(1)
}

// recordInferenceSuccess records a successful window and returns the streak it
// ended.
func recordInferenceSuccess(modelID string, now time.Time) int64 {
	h := inferenceHealthFor(modelID)
	h.count.Add(1)
	ns := now.UnixNano()
	h.lastAtNs.Store(ns)
	h.lastSuccessNs.Store(ns)
	return h.streak.Swap(0)
}

// isCancellation reports whether a PredictModel error came with the caller's
// context ending rather than a model fault. Only the caller's context decides: a
// backend that returns a timeout or cancellation error of its own while the
// caller is still running is a failing model. The analysis path currently passes
// context.Background(), so this only applies to callers with a cancellable
// context (tests, future per-window contexts); a caller that adds a per-window
// deadline must decide whether a timed-out window counts as a model failure.
func isCancellation(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() != nil
}

// inferenceFailureLogsAtError reports whether the streak-th consecutive failure
// of one model is logged at ERROR (the first, then every
// inferenceFailureLogEvery-th) rather than DEBUG.
func inferenceFailureLogsAtError(streak int64) bool {
	return streak == 1 || streak%inferenceFailureLogEvery == 0
}

// failureStreakNeedsSync reports whether the streak-th consecutive failure must
// reconcile the failure notice: when the model becomes failing, then every
// inferenceFailureLogEvery-th failure as a bounded periodic re-check.
func failureStreakNeedsSync(streak int64) bool {
	return streak == InferenceFailureNoticeThreshold ||
		(streak > InferenceFailureNoticeThreshold && streak%inferenceFailureLogEvery == 0)
}

// ModelInferenceHealth is the inference health of one loaded model.
type ModelInferenceHealth struct {
	// ModelID is the registry ID.
	ModelID string
	// ModelName is the display name.
	ModelName string
	// ConsecutiveFailures is the current run of failed analysis windows.
	ConsecutiveFailures int64
	// InferenceCount is the number of analysis windows the loaded instance has
	// run, succeeded or failed.
	InferenceCount int64
	// LastInferenceAt is when the last window finished; zero when none has run.
	LastInferenceAt time.Time
	// LastSuccessAt is when the last window succeeded; zero when none has.
	LastSuccessAt time.Time
	// ErrorClass is the class of the latest failure (InferenceErrorClass*), ""
	// when no failure run is in progress (ConsecutiveFailures == 0).
	ErrorClass string
	// Failing is true when ConsecutiveFailures reached
	// InferenceFailureNoticeThreshold.
	Failing bool
	// Backend, Device and Precision describe the live instance.
	Backend   string
	Device    string
	Precision string
}

// InferenceHealth returns the inference health of every loaded model, sorted by
// registry ID. A model paused by its schedule keeps the verdict of its last
// window, since it runs none while paused.
func (o *Orchestrator) InferenceHealth() []ModelInferenceHealth {
	infos := o.ModelInfos()
	out := make([]ModelInferenceHealth, 0, len(infos))
	for i := range infos {
		id := infos[i].ID
		mh := ModelInferenceHealth{ModelID: id, ModelName: infos[i].DisplayName()}
		if v, ok := inferenceHealthRecords.Load(id); ok {
			h := v.(*modelInferenceHealth) //nolint:errcheck // stored type is fixed
			mh.ConsecutiveFailures = h.streak.Load()
			mh.InferenceCount = h.count.Load()
			mh.LastInferenceAt = unixNanoTime(h.lastAtNs.Load())
			mh.LastSuccessAt = unixNanoTime(h.lastSuccessNs.Load())
			if mh.ConsecutiveFailures > 0 {
				mh.ErrorClass = inferenceErrorClass(h.lastErrClass.Load()).String()
			}
		}
		mh.Failing = mh.ConsecutiveFailures >= InferenceFailureNoticeThreshold
		mh.Device, mh.Backend, mh.Precision = o.GetModelRuntimeInfo(id)
		out = append(out, mh)
	}
	slices.SortFunc(out, func(a, b ModelInferenceHealth) int {
		switch {
		case a.ModelID < b.ModelID:
			return -1
		case a.ModelID > b.ModelID:
			return 1
		default:
			return 0
		}
	})
	return out
}

// unixNanoTime converts a stored Unix-ns timestamp, 0 meaning "never".
func unixNanoTime(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// inferenceHealthState is the orchestrator's failure-notice bookkeeping.
type inferenceHealthState struct {
	// pending coalesces kicks: set while a reconcile goroutine is queued.
	pending atomic.Bool
	// inFlight tracks queued or running kick goroutines, so tests can wait them
	// out; inFlightN counts them for kickInferenceHealthSyncIfTracked.
	inFlight  sync.WaitGroup
	inFlightN atomic.Int32
	// tracked is the number of latched notices plus failing models at the end
	// of the last reconcile, written before that pass leaves inFlightN.
	tracked atomic.Int32
	// changed is the observer called when the set of failing models changes.
	changed atomic.Pointer[func()]

	// mu serializes whole reconciles and guards the fields below.
	mu      sync.Mutex
	notices map[string]*notification.PersistentNotice // per model ID
	failing map[string]bool                           // failing set at the last reconcile
}

// SetInferenceHealthChangedCallback registers cb, called holding no
// orchestrator lock (on a background goroutine, except for the final pass that
// Orchestrator.Delete runs inline) whenever the set of failing models
// changes. The api facade wires it to the inference topology SSE broadcast so
// the dashboard re-reads the status snapshot. A nil cb disables it.
func (o *Orchestrator) SetInferenceHealthChangedCallback(cb func()) {
	if cb == nil {
		o.inferenceHealth.changed.Store(nil)
		return
	}
	o.inferenceHealth.changed.Store(&cb)
}

// kickInferenceHealthSync schedules a reconcile of the failure notices on a
// background goroutine. It takes no lock and never blocks, so it is safe on the
// inference hot path and under any orchestrator lock. Kicks that arrive while
// one is queued coalesce into it.
func (o *Orchestrator) kickInferenceHealthSync() {
	st := &o.inferenceHealth
	if !st.pending.CompareAndSwap(false, true) {
		return
	}
	st.inFlightN.Add(1)
	st.inFlight.Go(func() {
		defer st.inFlightN.Add(-1)
		// Clear pending before reconciling, so a kick during the reconcile queues
		// another pass that sees its change (the passes serialize on mu).
		st.pending.Store(false)
		o.syncInferenceHealth()
	})
}

// kickInferenceHealthSyncIfTracked kicks a reconcile only when one can matter:
// a notice is latched or a model was failing at the last pass, or a pass is
// queued or running (it may be about to latch a notice for a model that just
// went away). Unload and reload call it, so the common case (no failing model)
// starts no goroutine. inFlightN is read before tracked, and a pass writes
// tracked before leaving inFlightN, so a pass that latched is never missed.
func (o *Orchestrator) kickInferenceHealthSyncIfTracked() {
	st := &o.inferenceHealth
	if st.inFlightN.Load() > 0 || st.tracked.Load() > 0 {
		o.kickInferenceHealthSync()
	}
}

// syncInferenceHealth reconciles the per-model failure notices with the current
// inference health: raise a notice for a model that became failing, replace it
// when its backend, device, precision or error class changed, clear it when the
// model recovers or is unloaded. With no notification service (early startup,
// tests) nothing is latched, so a later kick retries. It then notifies the
// observer if the failing set changed.
//
// The health snapshot is read under st.mu, so overlapping passes apply in order
// and a slower pass cannot re-raise a notice from a stale snapshot. Lock order:
// st.mu -> o.mu and entry.mu (the snapshot) and st.mu -> PersistentNotice.mu; no
// caller holds o.mu or entry.mu while taking st.mu (PredictModel, UnloadModel and
// reloadEntry only kick, which runs the pass on its own goroutine, and Delete
// runs the final pass after releasing both).
func (o *Orchestrator) syncInferenceHealth() {
	st := &o.inferenceHealth
	st.mu.Lock()
	health := o.InferenceHealth()
	svc := notification.GetService()
	if st.notices == nil {
		st.notices = make(map[string]*notification.PersistentNotice)
	}
	failing := make(map[string]bool)
	loaded := make(map[string]bool, len(health))
	for i := range health {
		h := health[i]
		loaded[h.ModelID] = true
		if h.Failing {
			failing[h.ModelID] = true
		}
		p := st.notices[h.ModelID]
		if p == nil {
			if !h.Failing {
				continue
			}
			p = &notification.PersistentNotice{}
			p.SetRetry(o.kickInferenceHealthSync)
			st.notices[h.ModelID] = p
		}
		if svc == nil {
			continue
		}
		if err := p.Reconcile(svc, func() (string, func() *notification.Notification) {
			return inferenceFailureSignature(&h), func() *notification.Notification {
				return newInferenceFailureNotification(&h)
			}
		}); err != nil {
			GetLogger().Warn("failed to update inference failure notification",
				logger.String("model_id", h.ModelID), logger.Error(err))
		}
	}
	for id, p := range st.notices {
		if loaded[id] {
			continue
		}
		// Unloaded (or the orchestrator was deleted): clear its notice. A failed
		// delete keeps the latch so a later reconcile retries it.
		if svc != nil {
			if err := p.Reconcile(svc, noNotice); err != nil {
				continue
			}
		} else if p.ID() != "" {
			continue
		}
		p.Stop()
		delete(st.notices, id)
	}
	previous := st.failing
	st.failing = failing
	st.tracked.Store(int32(len(st.notices) + len(failing))) //nolint:gosec // G115: bounded by the loaded model count
	st.mu.Unlock()

	if maps.Equal(previous, failing) {
		return
	}
	logFailingTransitions(health, previous, failing)
	if cb := st.changed.Load(); cb != nil {
		(*cb)()
	}
}

// noNotice is the Reconcile compute for "no notice due".
func noNotice() (sig string, build func() *notification.Notification) { return "", nil }

// logFailingTransitions logs models entering or leaving the failing state.
func logFailingTransitions(health []ModelInferenceHealth, previous, current map[string]bool) {
	log := GetLogger()
	for i := range health {
		h := &health[i]
		switch {
		case current[h.ModelID] && !previous[h.ModelID]:
			log.Warn("model is failing every analysis window",
				logger.String("model_id", h.ModelID),
				logger.Int64("consecutive_failures", h.ConsecutiveFailures),
				logger.String("error_class", h.ErrorClass),
				logger.String("backend", h.Backend),
				logger.String("device", h.Device),
				logger.String("precision", h.Precision))
		case previous[h.ModelID] && !current[h.ModelID]:
			log.Info("model analysis recovered", logger.String("model_id", h.ModelID))
		}
	}
}

// inferenceFailureSignature identifies the failure notice a model's health calls
// for: "" when none is due, otherwise the model and the runtime and error class
// the notice text names, so a change in any of them replaces the notice.
func inferenceFailureSignature(h *ModelInferenceHealth) string {
	if !h.Failing {
		return ""
	}
	return h.ModelID + "|" + h.Backend + "|" + h.Device + "|" + h.Precision + "|" + h.ErrorClass
}

// newInferenceFailureNotification builds the persistent bell notice for a model
// whose analysis windows all fail. The English fallbacks mirror en.json.
func newInferenceFailureNotification(h *ModelInferenceHealth) *notification.Notification {
	runtime := describeRuntime(h.Backend, h.Device, h.Precision)
	title := fmt.Sprintf("%s fails every analysis", h.ModelName)
	messageKey := notification.MsgInferenceFailingMessage
	cause := "inference returned an error"
	if h.ErrorClass == InferenceErrorClassNonFinite {
		messageKey = notification.MsgInferenceFailingNonFiniteMessage
		cause = "the model returned invalid (non-finite) scores"
	}
	message := fmt.Sprintf(
		"%s is loaded, but its last %d analyses in a row failed on %s: %s. Audio is captured but this model detects nothing. Check the model on the System > AI Models page.",
		h.ModelName, InferenceFailureNoticeThreshold, runtime, cause)
	return notification.NewNotification(
		notification.TypeError,
		notification.PriorityHigh,
		title,
		message,
	).
		WithComponent(notification.ComponentClassifier).
		WithTitleKey(notification.MsgInferenceFailingTitle, map[string]any{"modelName": h.ModelName}).
		WithMessageKey(messageKey, map[string]any{
			"modelName": h.ModelName,
			"failures":  InferenceFailureNoticeThreshold,
			"runtime":   runtime,
		}).
		WithMetadata("model_id", h.ModelID).
		WithMetadata("error_class", h.ErrorClass).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// describeRuntime renders the backend, device and precision for the notice,
// e.g. "OpenVINO CPU (f16)", skipping parts the instance did not report.
func describeRuntime(backend, device, precision string) string {
	s := backend
	if device != "" && device != deviceUnknown {
		if s != "" {
			s += " "
		}
		s += device
	}
	if s == "" {
		s = "an unknown backend"
	}
	if precision != "" {
		s += " (" + precision + ")"
	}
	return s
}

// liveness.go - LivenessWatchdog monitors audio source health with a per-source
// state machine and tiered recovery (restart, escalate, notify).
package audiocore

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// LivenessState represents the health state of a single audio source
// as tracked by the LivenessWatchdog.
type LivenessState int

const (
	// StateHealthy means the source is dispatching frames normally.
	StateHealthy LivenessState = iota
	// StateAlarmed means silence has been detected; a restart will be attempted next tick.
	StateAlarmed
	// StateRecovering means a restart has been requested and the watchdog is
	// waiting for frames to resume.
	StateRecovering
	// StateEscalated means all restart retries have been exhausted.
	StateEscalated
	// StateFailed is terminal; the escalation timeout has elapsed with no recovery.
	StateFailed
)

// String returns a human-readable label for the state.
func (s LivenessState) String() string {
	switch s {
	case StateHealthy:
		return "HEALTHY"
	case StateAlarmed:
		return "ALARMED"
	case StateRecovering:
		return "RECOVERING"
	case StateEscalated:
		return "ESCALATED"
	case StateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// LivenessConfig holds tunable parameters for the watchdog.
type LivenessConfig struct {
	// CheckInterval is the tick period for the monitoring loop.
	CheckInterval time.Duration

	// SilenceThreshold is how long a source can go without dispatching frames
	// before an alarm is raised.
	SilenceThreshold time.Duration

	// MaxRetries is the number of restart attempts before escalating.
	MaxRetries int

	// RetryBackoff is the minimum wait between successive restart attempts.
	RetryBackoff time.Duration

	// CooldownAfterRecov suppresses alarms after a successful recovery.
	CooldownAfterRecov time.Duration

	// EscalationTimeout is how long to wait in ESCALATED before transitioning
	// to FAILED.
	EscalationTimeout time.Duration
}

// DefaultLivenessConfig returns production defaults.
func DefaultLivenessConfig() LivenessConfig {
	return LivenessConfig{
		CheckInterval:      10 * time.Second,
		SilenceThreshold:   30 * time.Second,
		MaxRetries:         3,
		RetryBackoff:       5 * time.Second,
		CooldownAfterRecov: 60 * time.Second,
		EscalationTimeout:  60 * time.Second,
	}
}

// sourceHealth tracks per-source watchdog state.
type sourceHealth struct {
	state         LivenessState
	retries       int
	lastRestart   time.Time
	stateEntered  time.Time
	cooldownEnd   time.Time
	wasQuietHours bool // true if previous tick was in quiet hours for this source
}

// SourceHealthSnapshot is the read-only view of a source's health for API
// consumers. Exported field names use JSON-friendly casing via struct tags.
type SourceHealthSnapshot struct {
	SourceID     string        `json:"source_id"`
	State        string        `json:"state"`
	Retries      int           `json:"retries"`
	LastRestart  time.Time     `json:"last_restart,omitempty"`
	StateEntered time.Time     `json:"state_entered"`
	LastDispatch time.Time     `json:"last_dispatch,omitempty"`
	RawState     LivenessState `json:"-"`
}

// LivenessCallbacks contains the external actions the watchdog can trigger.
// All callbacks are optional; nil callbacks are silently skipped.
type LivenessCallbacks struct {
	// RestartSource attempts to restart the given source. Returns an error if
	// the restart could not be initiated.
	RestartSource func(sourceID string) error

	// Escalate is called when all restart retries are exhausted.
	Escalate func(sourceID string)

	// Notify sends a user-facing notification about a source state change.
	Notify func(sourceID string, state LivenessState, msg string)

	// IsQuietHours returns true when monitoring should be suppressed for the
	// given source. Per-source granularity is required because sound card and
	// RTSP sources have independent quiet hours schedules.
	IsQuietHours func(sourceID string) bool
}

// livenessAction describes a callback to execute outside the mutex.
type livenessAction struct {
	sourceID string
	kind     actionKind
	state    LivenessState
	msg      string
}

type actionKind int

const (
	actionNotify actionKind = iota
	actionRestart
	actionEscalate
)

// LivenessWatchdog monitors audio sources for silence and drives a tiered
// recovery state machine. It queries the AudioRouter for frame timestamps
// and invokes callbacks for restart/escalation/notification.
type LivenessWatchdog struct {
	cfg       LivenessConfig
	router    *AudioRouter
	callbacks LivenessCallbacks
	log       logger.Logger

	mu      sync.Mutex
	sources map[string]*sourceHealth

	cancel context.CancelFunc
	done   chan struct{}
}

// NewLivenessWatchdog creates a watchdog that is ready to start.
func NewLivenessWatchdog(cfg LivenessConfig, router *AudioRouter, cb LivenessCallbacks) *LivenessWatchdog {
	return &LivenessWatchdog{
		cfg:       cfg,
		router:    router,
		callbacks: cb,
		log:       GetLogger().With(logger.String("component", "liveness")),
		sources:   make(map[string]*sourceHealth),
	}
}

// Start launches the monitoring goroutine. It is safe to call Start only once.
func (w *LivenessWatchdog) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.run(ctx)
}

// Stop signals the monitoring goroutine to exit and waits for it to finish.
func (w *LivenessWatchdog) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	if w.done != nil {
		<-w.done
	}
}

// Snapshot returns a point-in-time view of all tracked source health states.
func (w *LivenessWatchdog) Snapshot() []SourceHealthSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()

	snaps := make([]SourceHealthSnapshot, 0, len(w.sources))
	for id, h := range w.sources {
		snaps = append(snaps, SourceHealthSnapshot{
			SourceID:     id,
			State:        h.state.String(),
			Retries:      h.retries,
			LastRestart:  h.lastRestart,
			StateEntered: h.stateEntered,
			LastDispatch: w.router.LastDispatchTime(id),
			RawState:     h.state,
		})
	}
	return snaps
}

// run is the main monitoring loop, ticking every CheckInterval.
func (w *LivenessWatchdog) run(ctx context.Context) {
	defer close(w.done)

	ticker := time.NewTicker(w.cfg.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkAll()
		}
	}
}

// checkAll evaluates every active source, collects pending actions under the
// mutex, then executes callbacks (restart, escalate, notify) outside the mutex
// so that long-running operations like RestartSource do not block Snapshot().
func (w *LivenessWatchdog) checkAll() {
	activeIDs := w.router.ActiveSourceIDs()
	activeSet := make(map[string]struct{}, len(activeIDs))
	for _, id := range activeIDs {
		activeSet[id] = struct{}{}
	}

	var actions []livenessAction

	w.mu.Lock()

	now := time.Now()
	for _, id := range activeIDs {
		if _, ok := w.sources[id]; !ok {
			w.sources[id] = &sourceHealth{
				state:        StateHealthy,
				stateEntered: now,
			}
		}
	}

	for id := range w.sources {
		if _, ok := activeSet[id]; !ok {
			delete(w.sources, id)
		}
	}

	for _, id := range activeIDs {
		h := w.sources[id]
		if h == nil {
			continue
		}

		quietNow := w.callbacks.IsQuietHours != nil && w.callbacks.IsQuietHours(id)

		if h.wasQuietHours && !quietNow {
			w.router.ResetDispatchTime(id)
			h.state = StateHealthy
			h.retries = 0
			h.stateEntered = now
			w.log.Info("quiet hours ended, resetting liveness state",
				logger.String("source_id", id))
		}
		h.wasQuietHours = quietNow

		if quietNow {
			continue
		}

		actions = w.checkSource(id, now, actions)
	}

	w.mu.Unlock()

	w.executeActions(actions)
}

// checkSource drives the state machine for a single source. State transitions
// happen under the mutex; callbacks are collected into actions for deferred
// execution. The caller must hold w.mu.
//
//nolint:gocognit // state machine is inherently complex; splitting would obscure the flow
func (w *LivenessWatchdog) checkSource(sourceID string, now time.Time, actions []livenessAction) []livenessAction {
	h := w.sources[sourceID]
	if h == nil {
		return actions
	}

	lastFrame := w.router.LastDispatchTime(sourceID)

	// A source that has never dispatched is still initializing; skip it to
	// avoid false alarms during RTSP connection setup or device enumeration.
	if lastFrame.IsZero() {
		return actions
	}

	framesFlowing := now.Sub(lastFrame) < w.cfg.SilenceThreshold

	switch h.state {
	case StateHealthy:
		if framesFlowing {
			return actions
		}
		if !h.cooldownEnd.IsZero() && now.Before(h.cooldownEnd) {
			return actions
		}

		silenceDur := now.Sub(lastFrame)
		h.state = StateAlarmed
		h.stateEntered = now
		h.retries = 0

		w.log.Error("audio source silence detected",
			logger.String("source_id", sourceID),
			logger.Duration("silence", silenceDur),
		)
		actions = append(actions, livenessAction{
			sourceID: sourceID, kind: actionNotify,
			state: StateAlarmed, msg: "silence detected",
		})

	case StateAlarmed:
		if framesFlowing {
			actions = w.collectRecover(h, sourceID, now, actions)
			return actions
		}
		h.state = StateRecovering
		h.stateEntered = now
		actions = w.collectRestart(h, sourceID, now, actions)

	case StateRecovering:
		if framesFlowing {
			actions = w.collectRecover(h, sourceID, now, actions)
			return actions
		}
		if h.retries >= w.cfg.MaxRetries {
			h.state = StateEscalated
			h.stateEntered = now

			w.log.Error("audio source escalated after max retries",
				logger.String("source_id", sourceID),
				logger.Int("retries", h.retries),
			)
			_ = errors.Newf("audio source escalated: %s", sourceID).
				Component("audiocore.liveness").
				Category(errors.CategoryAudioSource).
				Context("source_id", sourceID).
				Context("retries_exhausted", h.retries).
				Build()

			actions = append(actions,
				livenessAction{sourceID: sourceID, kind: actionEscalate},
				livenessAction{sourceID: sourceID, kind: actionNotify, state: StateEscalated, msg: "retries exhausted"},
			)
			return actions
		}
		if now.Sub(h.lastRestart) < w.cfg.RetryBackoff {
			return actions
		}
		actions = w.collectRestart(h, sourceID, now, actions)

	case StateEscalated:
		if framesFlowing {
			actions = w.collectRecover(h, sourceID, now, actions)
			return actions
		}
		if now.Sub(h.stateEntered) >= w.cfg.EscalationTimeout {
			h.state = StateFailed
			h.stateEntered = now

			w.log.Error("audio source failed",
				logger.String("source_id", sourceID),
			)
			actions = append(actions, livenessAction{
				sourceID: sourceID, kind: actionNotify,
				state: StateFailed, msg: "escalation timeout elapsed",
			})
		}

	case StateFailed:
		if framesFlowing {
			actions = w.collectRecover(h, sourceID, now, actions)
		}
	}

	return actions
}

// collectRestart updates retry tracking and appends a restart action.
// The caller must hold w.mu.
func (w *LivenessWatchdog) collectRestart(h *sourceHealth, sourceID string, now time.Time, actions []livenessAction) []livenessAction {
	h.retries++
	h.lastRestart = now

	w.log.Info("attempting source restart",
		logger.String("source_id", sourceID),
		logger.Int("retry", h.retries),
		logger.Int("max_retries", w.cfg.MaxRetries),
	)

	return append(actions, livenessAction{
		sourceID: sourceID, kind: actionRestart,
	})
}

// collectRecover transitions a source back to HEALTHY and appends a notify action.
// The caller must hold w.mu.
func (w *LivenessWatchdog) collectRecover(h *sourceHealth, sourceID string, now time.Time, actions []livenessAction) []livenessAction {
	prevState := h.state
	h.state = StateHealthy
	h.stateEntered = now
	h.cooldownEnd = now.Add(w.cfg.CooldownAfterRecov)
	h.retries = 0

	w.log.Info("audio source recovered",
		logger.String("source_id", sourceID),
		logger.String("from_state", prevState.String()),
	)

	return append(actions, livenessAction{
		sourceID: sourceID, kind: actionNotify,
		state: StateHealthy, msg: "recovered",
	})
}

// executeActions runs collected callbacks outside the mutex.
func (w *LivenessWatchdog) executeActions(actions []livenessAction) {
	for i := range actions {
		a := &actions[i]
		switch a.kind {
		case actionRestart:
			if w.callbacks.RestartSource != nil {
				if err := w.callbacks.RestartSource(a.sourceID); err != nil {
					w.log.Error("source restart failed",
						logger.String("source_id", a.sourceID),
						logger.Error(err),
					)
				}
			}
		case actionEscalate:
			if w.callbacks.Escalate != nil {
				w.callbacks.Escalate(a.sourceID)
			}
		case actionNotify:
			if w.callbacks.Notify != nil {
				w.callbacks.Notify(a.sourceID, a.state, a.msg)
			}
		}
	}
}

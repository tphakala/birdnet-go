package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// saveHistoryTimeout is the context deadline for persisting alert history.
	saveHistoryTimeout = 3 * time.Second
	// cleanupTimeout is the context deadline for the periodic history deletion.
	cleanupTimeout = 5 * time.Second
	// cleanupInterval is how often the history cleanup goroutine runs.
	cleanupInterval = 1 * time.Hour
)

// ActionFunc is called when a rule fires. Receives the rule and triggering event.
type ActionFunc func(rule *entities.AlertRule, event *AlertEvent)

// Engine evaluates incoming alert events against configured rules.
type Engine struct {
	repo           repository.AlertRuleRepository
	metricTracker  *MetricTracker
	actionFunc     ActionFunc
	testActionFunc ActionFunc // Used by TestFireRule to tag notifications as test
	log            logger.Logger
	telemetry      *AlertingTelemetry // nil-safe engine health reporter

	// Cooldown tracking (in-memory, resets on restart).
	// Key is rule ID for event rules, or "ruleID|metricKey" for metric
	// rules so that per-instance metrics (e.g., disk paths) have
	// independent cooldowns.
	cooldowns   map[string]time.Time
	cooldownsMu sync.RWMutex

	// Escalation tracking: maps "ruleID|metricKey" → last fired step value.
	// Prevents re-fire until metric crosses the next higher step.
	escalations   map[string]float64
	escalationsMu sync.RWMutex

	// Cached rules (refreshed periodically)
	rules   []entities.AlertRule
	rulesMu sync.RWMutex

	// History cleanup
	cleanupStop chan struct{}
}

// NewEngine creates a new alerting rules engine.
func NewEngine(repo repository.AlertRuleRepository, actionFunc ActionFunc, log logger.Logger, at *AlertingTelemetry) *Engine {
	return &Engine{
		repo:          repo,
		metricTracker: NewMetricTracker(),
		actionFunc:    actionFunc,
		log:           log,
		telemetry:     at,
		cooldowns:     make(map[string]time.Time),
		escalations:   make(map[string]float64),
	}
}

// SetTestActionFunc sets the function called when a rule is test-fired.
// This should tag the resulting notification as a test so push providers skip it.
func (e *Engine) SetTestActionFunc(fn ActionFunc) {
	e.testActionFunc = fn
}

// RefreshRules reloads enabled rules from the database.
// Call this on startup and whenever rules are modified via API.
func (e *Engine) RefreshRules(ctx context.Context) error {
	rules, err := e.repo.GetEnabledRules(ctx)
	if err != nil {
		return err
	}
	e.rulesMu.Lock()
	e.rules = rules
	e.rulesMu.Unlock()
	return nil
}

// metricBufferKey derives the MetricTracker buffer key from the metric name
// and event properties. For per-instance metrics (e.g., disk usage per mount
// point), the key includes the "path" property to isolate ring buffers.
func metricBufferKey(metricName string, properties map[string]any) string {
	if path, ok := properties[PropertyPath].(string); ok && path != "" {
		return metricName + "|" + path
	}
	return metricName
}

// cooldownKey builds the cooldown state key. For metric rules, it includes
// the metric instance (path) so per-instance metrics have independent cooldowns.
// For event rules, it uses only the rule ID.
func cooldownKey(rule *entities.AlertRule, event *AlertEvent) string {
	if rule.TriggerType == TriggerTypeMetric {
		return fmt.Sprintf("%d|%s", rule.ID, metricBufferKey(rule.MetricName, event.Properties))
	}
	return fmt.Sprintf("%d", rule.ID)
}

// escalationKey builds the escalation state key from the rule ID and metric
// instance (path). Multiple mount points are tracked independently.
func escalationKey(ruleID uint, metricName string, properties map[string]any) string {
	return fmt.Sprintf("%d|%s", ruleID, metricBufferKey(metricName, properties))
}

// clearEscalationIfRecovered clears escalation state for rules whose metric
// has dropped below the base threshold (lowest EscalationStep). This must run
// for every metric event, even when ruleMatches returns false, because a
// metric below threshold causes ruleMatches to return false — which would
// leave stale escalation state that suppresses future alerts.
func (e *Engine) clearEscalationIfRecovered(rules []entities.AlertRule, event *AlertEvent) {
	if event.MetricName == "" {
		return
	}
	val, ok := event.Properties[PropertyValue]
	if !ok {
		return
	}
	floatVal, err := toFloat64(val)
	if err != nil {
		return
	}

	for i := range rules {
		rule := &rules[i]
		if rule.TriggerType != TriggerTypeMetric || rule.MetricName != event.MetricName {
			continue
		}
		if rule.ObjectType != event.ObjectType {
			continue
		}
		if len(rule.EscalationSteps) == 0 {
			continue
		}
		// Find the lowest step (base threshold) — steps may not be sorted.
		baseStep := slices.Min(rule.EscalationSteps)
		if floatVal < baseStep {
			key := escalationKey(rule.ID, event.MetricName, event.Properties)
			e.escalationsMu.Lock()
			delete(e.escalations, key)
			e.escalationsMu.Unlock()
		}
	}
}

// shouldSuppressEscalation checks if a metric rule should be suppressed
// because the metric hasn't crossed a new escalation step. Returns true
// if the rule should NOT fire. When it returns false (allow fire), it also
// returns a shallow copy of properties with PropertyThresholdStep set.
func (e *Engine) shouldSuppressEscalation(rule *entities.AlertRule, event *AlertEvent) (suppress bool, props map[string]any) {
	if len(rule.EscalationSteps) == 0 {
		return false, event.Properties
	}

	val, ok := event.Properties[PropertyValue]
	if !ok {
		return false, event.Properties
	}
	floatVal, err := toFloat64(val)
	if err != nil {
		return false, event.Properties
	}

	// Find the highest step the current value exceeds.
	// Uses explicit max tracking so the result is correct even if
	// EscalationSteps is not sorted ascending. A boolean flag avoids
	// issues with negative step values (e.g., temperature thresholds).
	var currentStep float64
	stepFound := false
	for _, step := range rule.EscalationSteps {
		if floatVal >= step && (!stepFound || step > currentStep) {
			currentStep = step
			stepFound = true
		}
	}
	if !stepFound {
		return true, nil
	}

	key := escalationKey(rule.ID, event.MetricName, event.Properties)

	// Single write lock for atomic check-and-update to avoid TOCTOU race
	// where two goroutines both pass the read check and both fire.
	e.escalationsMu.Lock()
	lastStep, exists := e.escalations[key]
	if exists && lastStep >= currentStep {
		e.escalationsMu.Unlock()
		return true, nil
	}
	e.escalations[key] = currentStep
	e.escalationsMu.Unlock()

	// Shallow-copy properties and add the threshold step (don't mutate shared map).
	propsCopy := make(map[string]any, len(event.Properties)+1)
	maps.Copy(propsCopy, event.Properties)
	propsCopy[PropertyThresholdStep] = currentStep

	return false, propsCopy
}

// HandleEvent evaluates an event against all enabled rules.
func (e *Engine) HandleEvent(event *AlertEvent) {
	// Record metric sample once before rule iteration to avoid duplicates
	// when multiple rules target the same metric.
	if event.MetricName != "" {
		if val, ok := event.Properties[PropertyValue]; ok {
			if floatVal, err := toFloat64(val); err == nil {
				trackerKey := metricBufferKey(event.MetricName, event.Properties)
				e.metricTracker.Record(trackerKey, floatVal, event.Timestamp)
			}
		}
	}

	e.rulesMu.RLock()
	rules := make([]entities.AlertRule, len(e.rules))
	copy(rules, e.rules)
	e.rulesMu.RUnlock()

	// Phase 1: Clear escalation state for metrics that have recovered.
	e.clearEscalationIfRecovered(rules, event)

	// Phase 2: Evaluate rules.
	for i := range rules {
		rule := &rules[i]
		if !e.ruleMatches(rule, event) {
			continue
		}

		cdKey := cooldownKey(rule, event)

		// Metric rules with escalation steps: check escalation BEFORE
		// acquiring cooldown so a suppressed step doesn't consume the
		// cooldown and block a later, legitimate escalated alert.
		// The escalation path has its own atomic protection via
		// shouldSuppressEscalation.
		if rule.TriggerType == TriggerTypeMetric && len(rule.EscalationSteps) > 0 {
			suppress, props := e.shouldSuppressEscalation(rule, event)
			if suppress {
				continue
			}
			if !e.tryAcquireCooldown(cdKey, rule.CooldownSec) {
				continue
			}
			augmentedEvent := &AlertEvent{
				ObjectType: event.ObjectType,
				EventName:  event.EventName,
				MetricName: event.MetricName,
				Properties: props,
				Timestamp:  event.Timestamp,
			}
			e.fireRule(rule, augmentedEvent)
			continue
		}

		if e.tryAcquireCooldown(cdKey, rule.CooldownSec) {
			e.fireRule(rule, event)
		}
	}
}

func (e *Engine) ruleMatches(rule *entities.AlertRule, event *AlertEvent) bool {
	// Match object type
	if rule.ObjectType != event.ObjectType {
		return false
	}

	// Match trigger
	switch rule.TriggerType {
	case TriggerTypeEvent:
		if rule.EventName != event.EventName {
			return false
		}
	case TriggerTypeMetric:
		if rule.MetricName != event.MetricName {
			return false
		}
	default:
		return false
	}

	// For metric triggers with duration conditions, check sustained threshold
	if rule.TriggerType == TriggerTypeMetric {
		return e.evaluateMetricConditions(rule, event)
	}

	// For event triggers, evaluate conditions directly
	return EvaluateConditions(rule.Conditions, event.Properties)
}

func (e *Engine) evaluateMetricConditions(rule *entities.AlertRule, event *AlertEvent) bool {
	// Evaluate each condition (metric sample already recorded in HandleEvent)
	trackerKey := metricBufferKey(rule.MetricName, event.Properties)
	for i := range rule.Conditions {
		cond := &rule.Conditions[i]
		if cond.DurationSec > 0 {
			// Check sustained threshold
			duration := time.Duration(cond.DurationSec) * time.Second
			if !e.metricTracker.IsSustained(trackerKey, cond.Operator, cond.Value, duration, event.Timestamp) {
				return false
			}
		} else if !evaluateCondition(cond, event.Properties) {
			// Instant check
			return false
		}
	}
	return true
}

// tryAcquireCooldown atomically checks whether key is in cooldown and, if not,
// records the current time so subsequent calls observe the cooldown. This avoids
// the TOCTOU race where a separate read-lock check + write-lock set would let
// two concurrent callers both pass the check before either writes.
func (e *Engine) tryAcquireCooldown(key string, cooldownSec int) (acquired bool) {
	if cooldownSec <= 0 {
		return true // no cooldown configured — always allow
	}
	cooldownDuration := time.Duration(cooldownSec) * time.Second

	e.cooldownsMu.Lock()
	defer e.cooldownsMu.Unlock()

	lastFired, exists := e.cooldowns[key]
	if exists && time.Since(lastFired) < cooldownDuration {
		return false // still in cooldown
	}
	e.cooldowns[key] = time.Now()
	return true
}

func (e *Engine) fireRule(rule *entities.AlertRule, event *AlertEvent) {
	e.fireRuleInternal(rule, event, e.actionFunc)
}

// TestFireRule fires a rule's actions directly, bypassing condition evaluation
// and cooldown checks. Used by the test endpoint. The resulting notification
// is marked as a test so that push providers (Telegram, Shoutrrr, etc.) do
// not forward it.
func (e *Engine) TestFireRule(rule *entities.AlertRule) {
	event := &AlertEvent{
		ObjectType: rule.ObjectType,
		EventName:  rule.EventName,
		MetricName: rule.MetricName,
		Properties: map[string]any{"test": true},
		Timestamp:  time.Now(),
	}
	actionFn := e.testActionFunc
	if actionFn == nil {
		actionFn = e.actionFunc
	}
	e.fireRuleInternal(rule, event, actionFn)
}

// fireRuleInternal contains the shared logic for firing a rule: persisting
// history and dispatching the provided action function.
func (e *Engine) fireRuleInternal(rule *entities.AlertRule, event *AlertEvent, actionFn ActionFunc) {
	// Record history
	eventJSON, err := json.Marshal(event.Properties)
	if err != nil {
		e.log.Error("failed to marshal event properties", logger.Error(err))
		eventJSON = []byte("{}")
	}
	actionsJSON, err := json.Marshal(rule.Actions)
	if err != nil {
		e.log.Error("failed to marshal rule actions", logger.Error(err))
		actionsJSON = []byte("[]")
	}
	history := &entities.AlertHistory{
		RuleID:    rule.ID,
		FiredAt:   time.Now(),
		EventData: string(eventJSON),
		Actions:   string(actionsJSON),
	}
	saveCtx, saveCancel := context.WithTimeout(context.Background(), saveHistoryTimeout)
	defer saveCancel()
	if err := e.repo.SaveHistory(saveCtx, history); err != nil {
		e.log.Error("failed to save alert history",
			logger.Uint64("rule_id", uint64(rule.ID)),
			logger.Error(err))
		e.telemetry.ReportDBWriteFailed("save_history", err.Error())
	}

	// Dispatch actions
	if actionFn != nil {
		actionFn(rule, event)
	}
}

// StartHistoryCleanup starts a background goroutine that periodically deletes
// alert history entries older than retentionDays. A value of 0 disables cleanup.
func (e *Engine) StartHistoryCleanup(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	// Stop any existing cleanup goroutine before starting a new one.
	e.stopCleanup()
	e.rulesMu.Lock()
	e.cleanupStop = make(chan struct{})
	stopCh := e.cleanupStop
	e.rulesMu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				e.log.Error("panic in alert history cleanup")
				e.telemetry.ReportPanic(r, debug.Stack())
			}
		}()
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cutoff := time.Now().AddDate(0, 0, -retentionDays)
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
				deleted, err := e.repo.DeleteHistoryBefore(cleanupCtx, cutoff)
				cleanupCancel()
				if err != nil {
					e.log.Error("alert history cleanup failed", logger.Error(err))
					e.telemetry.ReportDBWriteFailed("history_cleanup", err.Error())
				} else if deleted > 0 {
					e.log.Info("alert history cleanup completed",
						logger.Int64("deleted", deleted),
						logger.Int("retention_days", retentionDays))
				}
			case <-stopCh:
				return
			}
		}
	}()
}

// stopCleanup signals the cleanup goroutine to exit. Uses rulesMu to make
// the nil-check-then-close atomic, preventing double-close panics when
// Stop() and StartHistoryCleanup() race.
func (e *Engine) stopCleanup() {
	e.rulesMu.Lock()
	ch := e.cleanupStop
	e.cleanupStop = nil
	e.rulesMu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// Stop shuts down background goroutines (history cleanup).
func (e *Engine) Stop() {
	e.stopCleanup()
}

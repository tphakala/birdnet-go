package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
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
	// cleanupMaxRetries is how many times to retry the cleanup on transient DB lock errors.
	cleanupMaxRetries = 3
	// cleanupBaseDelay is the base delay for exponential backoff between cleanup retries.
	cleanupBaseDelay = 100 * time.Millisecond
	// unknownDetectionSpeciesKey is used when detection events lack species metadata.
	unknownDetectionSpeciesKey = "unknown"
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
	if speciesKey := detectionSpeciesKey(event); isSpeciesScopedDetectionRule(rule, event, speciesKey) {
		return fmt.Sprintf("%d|%s", rule.ID, speciesKey)
	}
	return fmt.Sprintf("%d", rule.ID)
}

// escalationKey builds the escalation state key from the rule ID and metric
// instance (path). Multiple mount points are tracked independently.
func escalationKey(ruleID uint, metricName string, properties map[string]any) string {
	return fmt.Sprintf("%d|%s", ruleID, metricBufferKey(metricName, properties))
}

func detectionSpeciesKey(event *AlertEvent) string {
	for _, propertyName := range []string{PropertyScientificName, PropertySpeciesName} {
		if value, ok := event.Properties[propertyName].(string); ok {
			key := strings.TrimSpace(strings.ToLower(value))
			if key != "" {
				return key
			}
		}
	}
	return unknownDetectionSpeciesKey
}

// isSpeciesScopedDetectionRule reports whether a detection rule's cooldown and
// escalation state should be tracked per species rather than per rule. The
// caller passes speciesKey from detectionSpeciesKey(event); species-less
// detections (unknownDetectionSpeciesKey) always use per-rule scoping. Novelty
// conditions reuse detectionMetadataProperties as the single source of truth
// for which properties scope a rule to a species.
func isSpeciesScopedDetectionRule(rule *entities.AlertRule, event *AlertEvent, speciesKey string) bool {
	if rule.TriggerType != TriggerTypeEvent || !isDetectionEvent(event.EventName) {
		return false
	}
	if speciesKey == unknownDetectionSpeciesKey {
		return false
	}
	if len(rule.EscalationSteps) > 0 {
		return true
	}
	for i := range rule.Conditions {
		if slices.Contains(detectionMetadataProperties, rule.Conditions[i].Property) {
			return true
		}
	}
	return false
}

func detectionEscalationKey(rule *entities.AlertRule, event *AlertEvent, speciesKey string) string {
	// Derive a stable per-episode component. NoveltyEpisodeStart is contractually
	// a string today, but format time.Time explicitly so a future type change
	// can't break episode grouping via fmt's verbose %v (monotonic clock, nanos).
	var episodeStart string
	switch value := event.Properties[PropertyNoveltyEpisodeStart].(type) {
	case time.Time:
		if !value.IsZero() {
			episodeStart = value.UTC().Format(time.RFC3339)
		}
	case string:
		episodeStart = strings.TrimSpace(value)
	case nil:
		// No episode metadata; fall back to the event day below.
	default:
		episodeStart = strings.TrimSpace(fmt.Sprintf("%v", value))
	}
	if episodeStart == "" {
		eventTime := event.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now()
		}
		episodeStart = eventTime.Format(time.DateOnly)
	}
	return fmt.Sprintf("%d|%s|%s", rule.ID, speciesKey, episodeStart)
}

func detectionEscalationKeyPrefix(ruleID uint, speciesKey string) string {
	return fmt.Sprintf("%d|%s|", ruleID, speciesKey)
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

// highestStepExceeded returns the highest escalation step that value meets or
// exceeds. found is false when value is below every step. Explicit max tracking
// keeps the result correct for unsorted steps and negative thresholds (e.g.
// temperature), where a plain running max with a zero sentinel would be wrong.
func highestStepExceeded(steps []float64, value float64) (step float64, found bool) {
	for _, s := range steps {
		if value >= s && (!found || s > step) {
			step = s
			found = true
		}
	}
	return step, found
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

	currentStep, stepFound := highestStepExceeded(rule.EscalationSteps, floatVal)
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

// shouldSuppressDetectionEscalation applies confidence laddering to detection
// event rules. It is keyed by species and novelty episode, so a higher
// confidence step can notify again without waiting for the rule cooldown.
func (e *Engine) shouldSuppressDetectionEscalation(rule *entities.AlertRule, event *AlertEvent, cdKey, speciesKey string) (suppress bool, props map[string]any) {
	if len(rule.EscalationSteps) == 0 {
		return false, event.Properties
	}

	confidence, ok := event.Properties[PropertyConfidence]
	if !ok {
		return true, nil
	}
	confidenceFloat, err := toFloat64(confidence)
	if err != nil {
		return true, nil
	}

	currentStep, stepFound := highestStepExceeded(rule.EscalationSteps, confidenceFloat)
	if !stepFound {
		return true, nil
	}

	key := detectionEscalationKey(rule, event, speciesKey)
	now := time.Now()

	e.cooldownsMu.Lock()
	e.escalationsMu.Lock()
	lastStep, exists := e.escalations[key]
	cooldownExpired := e.cooldownExpiredLocked(cdKey, rule.CooldownSec, now)
	if exists && lastStep >= currentStep && !cooldownExpired {
		e.escalationsMu.Unlock()
		e.cooldownsMu.Unlock()
		return true, nil
	}
	e.pruneDetectionEscalationsLocked(rule.ID, speciesKey, key)
	e.escalations[key] = currentStep
	e.escalationsMu.Unlock()
	if rule.CooldownSec > 0 {
		e.cooldowns[cdKey] = now
	}
	e.cooldownsMu.Unlock()

	propsCopy := make(map[string]any, len(event.Properties)+1)
	maps.Copy(propsCopy, event.Properties)
	propsCopy[PropertyThresholdStep] = currentStep

	return false, propsCopy
}

// cooldownExpiredLocked reports whether the cooldown for key has elapsed (or is
// disabled). Callers must hold e.cooldownsMu.
func (e *Engine) cooldownExpiredLocked(key string, cooldownSec int, now time.Time) bool {
	if cooldownSec <= 0 {
		return true
	}
	lastFired, exists := e.cooldowns[key]
	return !exists || now.Sub(lastFired) >= time.Duration(cooldownSec)*time.Second
}

// pruneDetectionEscalationsLocked removes escalation entries for older episodes
// of the same rule and species, keeping only currentKey. Callers must hold
// e.escalationsMu.
func (e *Engine) pruneDetectionEscalationsLocked(ruleID uint, speciesKey, currentKey string) {
	prefix := detectionEscalationKeyPrefix(ruleID, speciesKey)
	for key := range e.escalations {
		if key != currentKey && strings.HasPrefix(key, prefix) {
			delete(e.escalations, key)
		}
	}
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

	isDetection := isDetectionEvent(event.EventName)
	if isDetection {
		e.log.Debug("Alert engine received detection event",
			logger.String("component", "alerting.engine"),
			logger.String("event_name", event.EventName),
			logger.String("object_type", event.ObjectType),
			logger.Int("rules_count", len(rules)),
			logger.String("operation", "handle_detection_event"))
	}

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

		if rule.TriggerType == TriggerTypeEvent && isDetection && len(rule.EscalationSteps) > 0 {
			speciesKey := detectionSpeciesKey(event)
			if speciesKey != unknownDetectionSpeciesKey {
				suppress, props := e.shouldSuppressDetectionEscalation(rule, event, cdKey, speciesKey)
				if suppress {
					continue
				}
				augmentedEvent := &AlertEvent{
					ObjectType: event.ObjectType,
					EventName:  event.EventName,
					MetricName: event.MetricName,
					Properties: props,
					Timestamp:  event.Timestamp,
				}
				e.log.Debug("Detection rule fired at confidence escalation step",
					logger.String("component", "alerting.engine"),
					logger.String("event_name", event.EventName),
					logger.Uint64("rule_id", uint64(rule.ID)),
					logger.String("rule_name", rule.Name),
					logger.String("operation", "fire_detection_escalation_rule"))
				e.fireRule(rule, augmentedEvent)
				continue
			}
		}

		if e.tryAcquireCooldown(cdKey, rule.CooldownSec) {
			if isDetection {
				e.log.Debug("Detection rule fired",
					logger.String("component", "alerting.engine"),
					logger.String("event_name", event.EventName),
					logger.Uint64("rule_id", uint64(rule.ID)),
					logger.String("rule_name", rule.Name),
					logger.String("operation", "fire_detection_rule"))
			}
			e.fireRule(rule, event)
		} else if isDetection {
			e.log.Debug("Detection rule suppressed by cooldown",
				logger.String("component", "alerting.engine"),
				logger.String("event_name", event.EventName),
				logger.Uint64("rule_id", uint64(rule.ID)),
				logger.String("rule_name", rule.Name),
				logger.Int("cooldown_sec", rule.CooldownSec),
				logger.String("operation", "cooldown_suppressed"))
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
				deleted, err := e.deleteHistoryWithRetry(retentionDays, stopCh)
				if err != nil {
					if datastore.IsTransientDBError(err) {
						// Transient SQLite lock contention — the cleanup runs hourly,
						// so it will succeed on the next tick. Log as warning only;
						// do not report to Sentry.
						e.log.Warn("alert history cleanup skipped due to database lock, will retry next cycle",
							logger.Error(err))
					} else {
						e.log.Error("alert history cleanup failed", logger.Error(err))
						e.telemetry.ReportDBWriteFailed("history_cleanup", err.Error())
					}
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

// deleteHistoryWithRetry attempts the history deletion with exponential backoff
// on transient SQLite lock errors. Returns the number of deleted rows and any
// non-retryable (or exhausted) error.
func (e *Engine) deleteHistoryWithRetry(retentionDays int, stopCh <-chan struct{}) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	// Create a parent context that cancels when stopCh fires, so in-flight
	// DB calls are aborted promptly on shutdown instead of waiting for
	// their full timeout.
	parentCtx, parentCancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-stopCh:
			parentCancel()
		case <-parentCtx.Done():
		}
	}()
	defer parentCancel()

	var lastErr error
	for attempt := range cleanupMaxRetries {
		cleanupCtx, cleanupCancel := context.WithTimeout(parentCtx, cleanupTimeout)
		deleted, err := e.repo.DeleteHistoryBefore(cleanupCtx, cutoff)
		cleanupCancel()

		if err == nil {
			return deleted, nil
		}
		lastErr = err

		// Only retry on transient DB lock errors; bail immediately on other errors.
		if !datastore.IsTransientDBError(err) || attempt == cleanupMaxRetries-1 {
			return 0, err
		}

		backoff := cleanupBaseDelay * time.Duration(1<<uint(attempt)) //nolint:gosec // G115: attempt bounded by cleanupMaxRetries (3)
		e.log.Warn("alert history cleanup hit database lock, retrying",
			logger.Int("attempt", attempt+1),
			logger.Int("max_retries", cleanupMaxRetries),
			logger.Int64("backoff_ms", backoff.Milliseconds()))

		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-stopCh:
			timer.Stop()
			return 0, lastErr
		}
	}
	return 0, lastErr
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

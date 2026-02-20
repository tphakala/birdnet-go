package alerting

import (
	"context"
	"encoding/json"
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
	repo          repository.AlertRuleRepository
	metricTracker *MetricTracker
	actionFunc    ActionFunc
	log           logger.Logger

	// Cooldown tracking (in-memory, resets on restart)
	cooldowns   map[uint]time.Time // rule ID → last fired time
	cooldownsMu sync.RWMutex

	// Cached rules (refreshed periodically)
	rules   []entities.AlertRule
	rulesMu sync.RWMutex

	// History cleanup
	cleanupStop chan struct{}
}

// NewEngine creates a new alerting rules engine.
func NewEngine(repo repository.AlertRuleRepository, actionFunc ActionFunc, log logger.Logger) *Engine {
	return &Engine{
		repo:          repo,
		metricTracker: NewMetricTracker(),
		actionFunc:    actionFunc,
		log:           log,
		cooldowns:     make(map[uint]time.Time),
	}
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

// HandleEvent evaluates an event against all enabled rules.
func (e *Engine) HandleEvent(event *AlertEvent) {
	// Record metric sample once before rule iteration to avoid duplicates
	// when multiple rules target the same metric.
	if event.MetricName != "" {
		if val, ok := event.Properties[PropertyValue]; ok {
			if floatVal, err := toFloat64(val); err == nil {
				e.metricTracker.Record(event.MetricName, floatVal, event.Timestamp)
			}
		}
	}

	e.rulesMu.RLock()
	rules := make([]entities.AlertRule, len(e.rules))
	copy(rules, e.rules)
	e.rulesMu.RUnlock()

	for i := range rules {
		rule := &rules[i]
		if e.ruleMatches(rule, event) && !e.isInCooldown(rule.ID, rule.CooldownSec) {
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
	for i := range rule.Conditions {
		cond := &rule.Conditions[i]
		if cond.DurationSec > 0 {
			// Check sustained threshold
			duration := time.Duration(cond.DurationSec) * time.Second
			if !e.metricTracker.IsSustained(rule.MetricName, cond.Operator, cond.Value, duration, event.Timestamp) {
				return false
			}
		} else if !evaluateCondition(cond, event.Properties) {
			// Instant check
			return false
		}
	}
	return true
}

func (e *Engine) isInCooldown(ruleID uint, cooldownSec int) bool {
	if cooldownSec <= 0 {
		return false
	}
	e.cooldownsMu.RLock()
	lastFired, exists := e.cooldowns[ruleID]
	e.cooldownsMu.RUnlock()
	if !exists {
		return false
	}
	return time.Since(lastFired) < time.Duration(cooldownSec)*time.Second
}

func (e *Engine) fireRule(rule *entities.AlertRule, event *AlertEvent) {
	// Record cooldown
	e.cooldownsMu.Lock()
	e.cooldowns[rule.ID] = time.Now()
	e.cooldownsMu.Unlock()

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
	}

	// Dispatch actions
	if e.actionFunc != nil {
		e.actionFunc(rule, event)
	}
}

// TestFireRule fires a rule's actions directly, bypassing condition evaluation
// and cooldown checks. Used by the test endpoint.
func (e *Engine) TestFireRule(rule *entities.AlertRule) {
	event := &AlertEvent{
		ObjectType: rule.ObjectType,
		EventName:  rule.EventName,
		MetricName: rule.MetricName,
		Properties: map[string]any{"test": true},
		Timestamp:  time.Now(),
	}
	e.fireRule(rule, event)
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

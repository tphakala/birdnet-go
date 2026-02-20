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
	// Record the metric sample
	if val, ok := event.Properties[PropertyValue]; ok {
		if floatVal, err := toFloat64(val); err == nil {
			e.metricTracker.Record(rule.MetricName, floatVal, event.Timestamp)
		}
	}

	// Evaluate each condition
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
	eventJSON, _ := json.Marshal(event.Properties)
	actionsJSON, _ := json.Marshal(rule.Actions)
	history := &entities.AlertHistory{
		RuleID:    rule.ID,
		FiredAt:   time.Now(),
		EventData: string(eventJSON),
		Actions:   string(actionsJSON),
	}
	if err := e.repo.SaveHistory(context.Background(), history); err != nil {
		e.log.Error("failed to save alert history",
			logger.Uint64("rule_id", uint64(rule.ID)),
			logger.Error(err))
	}

	// Dispatch actions
	if e.actionFunc != nil {
		e.actionFunc(rule, event)
	}
}

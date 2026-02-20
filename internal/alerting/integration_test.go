package alerting

import (
	"context"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// integrationRepo is a full in-memory mock supporting seeding, creation, and history.
type integrationRepo struct {
	mu      sync.Mutex
	rules   []entities.AlertRule
	history []*entities.AlertHistory
	nextID  uint
}

func newIntegrationRepo() *integrationRepo {
	return &integrationRepo{nextID: 1}
}

func (r *integrationRepo) ListRules(_ context.Context, _ repository.AlertRuleFilter) ([]entities.AlertRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rules), nil
}

func (r *integrationRepo) GetRule(_ context.Context, id uint) (*entities.AlertRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rules {
		if r.rules[i].ID == id {
			return &r.rules[i], nil
		}
	}
	return nil, repository.ErrAlertRuleNotFound
}

func (r *integrationRepo) CreateRule(_ context.Context, rule *entities.AlertRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.ID = r.nextID
	r.nextID++
	for i := range rule.Conditions {
		rule.Conditions[i].RuleID = rule.ID
	}
	for i := range rule.Actions {
		rule.Actions[i].RuleID = rule.ID
	}
	r.rules = append(r.rules, *rule)
	return nil
}

func (r *integrationRepo) UpdateRule(_ context.Context, _ *entities.AlertRule) error { return nil }
func (r *integrationRepo) DeleteRule(_ context.Context, _ uint) error               { return nil }
func (r *integrationRepo) ToggleRule(_ context.Context, _ uint, _ bool) error        { return nil }
func (r *integrationRepo) DeleteBuiltInRules(_ context.Context) (int64, error)       { return 0, nil }

func (r *integrationRepo) GetEnabledRules(_ context.Context) ([]entities.AlertRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []entities.AlertRule
	for i := range r.rules {
		if r.rules[i].Enabled {
			out = append(out, r.rules[i])
		}
	}
	return out, nil
}

func (r *integrationRepo) SaveHistory(_ context.Context, h *entities.AlertHistory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = append(r.history, h)
	return nil
}

func (r *integrationRepo) ListHistory(_ context.Context, _ repository.AlertHistoryFilter) ([]entities.AlertHistory, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]entities.AlertHistory, len(r.history))
	for i, h := range r.history {
		out[i] = *h
	}
	return out, int64(len(r.history)), nil
}

func (r *integrationRepo) DeleteHistory(_ context.Context) (int64, error)                         { return 0, nil }
func (r *integrationRepo) DeleteHistoryBefore(_ context.Context, _ time.Time) (int64, error)      { return 0, nil }
func (r *integrationRepo) CountRulesByName(_ context.Context, _ string) (int64, error)            { return 0, nil }

func (r *integrationRepo) historyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.history)
}

// actionRecorder captures dispatched actions for verification.
type actionRecorder struct {
	mu       sync.Mutex
	calls    []actionCall
}

type actionCall struct {
	ruleName string
	targets  []string
}

func (a *actionRecorder) dispatch(rule *entities.AlertRule, _ *AlertEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	targets := make([]string, 0, len(rule.Actions))
	for _, act := range rule.Actions {
		targets = append(targets, act.Target)
	}
	a.calls = append(a.calls, actionCall{ruleName: rule.Name, targets: targets})
}

func (a *actionRecorder) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *actionRecorder) lastCall() actionCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[len(a.calls)-1]
}

func integrationLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil)
}

func TestIntegration_DetectionNewSpeciesRuleFires(t *testing.T) {
	repo := newIntegrationRepo()
	recorder := &actionRecorder{}
	log := integrationLogger()

	// Seed default rules
	err := seedDefaultRules(t.Context(), repo, log)
	require.NoError(t, err)

	// Create engine
	engine := NewEngine(repo, recorder.dispatch, log)
	err = engine.RefreshRules(t.Context())
	require.NoError(t, err)

	// Publish detection.new_species event
	event := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionNewSpecies,
		Properties: map[string]any{
			"species_name":    "Eurasian Blue Tit",
			"scientific_name": "Cyanistes caeruleus",
			"confidence":      0.92,
		},
		Timestamp: time.Now(),
	}
	engine.HandleEvent(event)

	// Verify the rule fired
	assert.Equal(t, 1, recorder.count(), "expected exactly one rule to fire")
	assert.Equal(t, "New species detected", recorder.lastCall().ruleName)
	assert.Contains(t, recorder.lastCall().targets, TargetBell)

	// Verify history entry created
	assert.Equal(t, 1, repo.historyCount(), "expected one history entry")
}

func TestIntegration_CooldownPreventsDuplicateFiring(t *testing.T) {
	repo := newIntegrationRepo()
	recorder := &actionRecorder{}
	log := integrationLogger()

	err := seedDefaultRules(t.Context(), repo, log)
	require.NoError(t, err)

	engine := NewEngine(repo, recorder.dispatch, log)
	err = engine.RefreshRules(t.Context())
	require.NoError(t, err)

	event := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionNewSpecies,
		Properties: map[string]any{"species_name": "Eurasian Blue Tit"},
		Timestamp:  time.Now(),
	}

	// First event fires
	engine.HandleEvent(event)
	assert.Equal(t, 1, recorder.count())

	// Second event within cooldown does NOT fire
	event.Timestamp = time.Now()
	engine.HandleEvent(event)
	assert.Equal(t, 1, recorder.count(), "second event within cooldown should not fire")
}

func TestIntegration_CustomRuleWithConditions(t *testing.T) {
	repo := newIntegrationRepo()
	recorder := &actionRecorder{}
	log := integrationLogger()

	// Create a custom rule with a condition
	customRule := &entities.AlertRule{
		Name:        "Rare bird alert",
		Enabled:     true,
		ObjectType:  ObjectTypeDetection,
		TriggerType: TriggerTypeEvent,
		EventName:   EventDetectionNewSpecies,
		CooldownSec: 0,
		Conditions: []entities.AlertCondition{
			{Property: "confidence", Operator: OperatorGreaterThan, Value: "0.95"},
		},
		Actions: []entities.AlertAction{
			{Target: TargetBell},
			{Target: "push"},
		},
	}
	err := repo.CreateRule(t.Context(), customRule)
	require.NoError(t, err)

	engine := NewEngine(repo, recorder.dispatch, log)
	err = engine.RefreshRules(t.Context())
	require.NoError(t, err)

	// Low confidence → does not match
	lowEvent := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionNewSpecies,
		Properties: map[string]any{"confidence": 0.8},
		Timestamp:  time.Now(),
	}
	engine.HandleEvent(lowEvent)
	assert.Equal(t, 0, recorder.count(), "low-confidence event should not trigger rule")

	// High confidence → matches
	highEvent := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionNewSpecies,
		Properties: map[string]any{"confidence": 0.98},
		Timestamp:  time.Now(),
	}
	engine.HandleEvent(highEvent)
	assert.Equal(t, 1, recorder.count(), "high-confidence event should trigger rule")
	assert.Equal(t, []string{TargetBell, "push"}, recorder.lastCall().targets)
}

func TestIntegration_MetricSustainedThresholdFires(t *testing.T) {
	repo := newIntegrationRepo()
	recorder := &actionRecorder{}
	log := integrationLogger()

	// Create a metric rule with duration
	metricRule := &entities.AlertRule{
		Name:        "High CPU test",
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricCPUUsage,
		CooldownSec: 300,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "80", DurationSec: 60},
		},
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	err := repo.CreateRule(t.Context(), metricRule)
	require.NoError(t, err)

	engine := NewEngine(repo, recorder.dispatch, log)
	err = engine.RefreshRules(t.Context())
	require.NoError(t, err)

	baseTime := time.Now().Add(-2 * time.Minute)

	// Send metric samples above threshold spanning > 60 seconds
	for i := range 13 {
		event := &AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricCPUUsage,
			Properties: map[string]any{PropertyValue: 85.0},
			Timestamp:  baseTime.Add(time.Duration(i*5) * time.Second),
		}
		engine.HandleEvent(event)
	}

	// After 65s of sustained > 80%, the rule should have fired
	assert.Equal(t, 1, recorder.count(), "sustained metric threshold should fire")
	assert.Equal(t, "High CPU test", recorder.lastCall().ruleName)
	assert.Equal(t, 1, repo.historyCount())
}

func TestIntegration_UnmatchedEventDoesNotFire(t *testing.T) {
	repo := newIntegrationRepo()
	recorder := &actionRecorder{}
	log := integrationLogger()

	err := seedDefaultRules(t.Context(), repo, log)
	require.NoError(t, err)

	engine := NewEngine(repo, recorder.dispatch, log)
	err = engine.RefreshRules(t.Context())
	require.NoError(t, err)

	// Publish an event that doesn't match any rule
	event := &AlertEvent{
		ObjectType: "nonexistent",
		EventName:  "unknown_event",
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	}
	engine.HandleEvent(event)

	assert.Equal(t, 0, recorder.count(), "unmatched event should not trigger any rule")
	assert.Equal(t, 0, repo.historyCount())
}

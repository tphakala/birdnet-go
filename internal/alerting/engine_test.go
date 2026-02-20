package alerting

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// mockAlertRuleRepo is a minimal in-memory mock of AlertRuleRepository.
type mockAlertRuleRepo struct {
	rules   []entities.AlertRule
	history []*entities.AlertHistory
	mu      sync.Mutex
}

func newMockRepo(rules ...entities.AlertRule) *mockAlertRuleRepo {
	return &mockAlertRuleRepo{rules: rules}
}

func (m *mockAlertRuleRepo) GetEnabledRules(_ context.Context) ([]entities.AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []entities.AlertRule
	for i := range m.rules {
		if m.rules[i].Enabled {
			out = append(out, m.rules[i])
		}
	}
	return out, nil
}

func (m *mockAlertRuleRepo) SaveHistory(_ context.Context, h *entities.AlertHistory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, h)
	return nil
}

// Unused methods — satisfy interface.
func (m *mockAlertRuleRepo) ListRules(_ context.Context, _ repository.AlertRuleFilter) ([]entities.AlertRule, error) {
	return []entities.AlertRule{}, nil
}
func (m *mockAlertRuleRepo) GetRule(_ context.Context, _ uint) (*entities.AlertRule, error) {
	return &entities.AlertRule{}, nil
}
func (m *mockAlertRuleRepo) CreateRule(_ context.Context, _ *entities.AlertRule) error { return nil }
func (m *mockAlertRuleRepo) UpdateRule(_ context.Context, _ *entities.AlertRule) error { return nil }
func (m *mockAlertRuleRepo) DeleteRule(_ context.Context, _ uint) error               { return nil }
func (m *mockAlertRuleRepo) ToggleRule(_ context.Context, _ uint, _ bool) error        { return nil }
func (m *mockAlertRuleRepo) DeleteBuiltInRules(_ context.Context) (int64, error)       { return 0, nil }
func (m *mockAlertRuleRepo) ListHistory(_ context.Context, _ repository.AlertHistoryFilter) ([]entities.AlertHistory, int64, error) {
	return nil, 0, nil
}
func (m *mockAlertRuleRepo) DeleteHistory(_ context.Context) (int64, error)            { return 0, nil }
func (m *mockAlertRuleRepo) DeleteHistoryBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockAlertRuleRepo) CountRulesByName(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func testLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil)
}

func TestEngine_EventMatchesRuleNoConditions(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{"stream_name": "test"},
		Timestamp:  time.Now(),
	})

	assert.True(t, fired, "rule with no conditions should fire on matching event")
}

func TestEngine_EventMatchesRuleWithPassingConditions(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeDetection,
		TriggerType: TriggerTypeEvent,
		EventName:   EventDetectionOccurred,
		Conditions: []entities.AlertCondition{
			{Property: PropertySpeciesName, Operator: OperatorContains, Value: "Owl"},
		},
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: map[string]any{PropertySpeciesName: "Great Horned Owl"},
		Timestamp:  time.Now(),
	})

	assert.True(t, fired)
}

func TestEngine_EventMatchesRuleWithFailingConditions(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeDetection,
		TriggerType: TriggerTypeEvent,
		EventName:   EventDetectionOccurred,
		Conditions: []entities.AlertCondition{
			{Property: PropertySpeciesName, Operator: OperatorContains, Value: "Owl"},
		},
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: map[string]any{PropertySpeciesName: "American Robin"},
		Timestamp:  time.Now(),
	})

	assert.False(t, fired, "rule should not fire when conditions fail")
}

func TestEngine_EventDoesNotMatchAnyRule(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	})

	assert.False(t, fired, "unmatched event should not fire any rule")
}

func TestEngine_DisabledRuleNotEvaluated(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     false,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	})

	assert.False(t, fired, "disabled rule should not fire")
}

func TestEngine_CooldownPreventsRefiring(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
		CooldownSec: 300, // 5 minutes
	}
	repo := newMockRepo(rule)

	var fireCount int
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fireCount++
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	}

	engine.HandleEvent(event)
	engine.HandleEvent(event)

	assert.Equal(t, 1, fireCount, "cooldown should prevent second fire")
}

func TestEngine_CooldownExpires(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
		CooldownSec: 1, // 1 second for test speed
	}
	repo := newMockRepo(rule)

	var fireCount int
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fireCount++
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	}

	engine.HandleEvent(event)

	// Manually expire cooldown by setting it in the past
	engine.cooldownsMu.Lock()
	engine.cooldowns[1] = time.Now().Add(-2 * time.Second)
	engine.cooldownsMu.Unlock()

	engine.HandleEvent(event)

	assert.Equal(t, 2, fireCount, "rule should fire again after cooldown expires")
}

func TestEngine_MultipleRulesMatchSameEvent(t *testing.T) {
	rule1 := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
	}
	rule2 := entities.AlertRule{
		ID:          2,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
	}
	repo := newMockRepo(rule1, rule2)

	var firedIDs []uint
	engine := NewEngine(repo, func(rule *entities.AlertRule, _ *AlertEvent) {
		firedIDs = append(firedIDs, rule.ID)
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	})

	assert.ElementsMatch(t, []uint{1, 2}, firedIDs, "both rules should fire independently")
}

func TestEngine_ActionDispatchRecordsHistory(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeDetection,
		TriggerType: TriggerTypeEvent,
		EventName:   EventDetectionOccurred,
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	repo := newMockRepo(rule)

	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		// no-op action
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: map[string]any{PropertySpeciesName: "Robin"},
		Timestamp:  time.Now(),
	})

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.history, 1)
	assert.Equal(t, uint(1), repo.history[0].RuleID)
	assert.NotEmpty(t, repo.history[0].EventData)
	assert.NotEmpty(t, repo.history[0].Actions)
}

func TestEngine_MetricRuleWithSustainedDuration(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricCPUUsage,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "90", DurationSec: 300},
		},
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	base := time.Now().Add(-10 * time.Minute)

	// Feed samples above threshold over 6 minutes
	for i := range 7 {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricCPUUsage,
			Properties: map[string]any{PropertyValue: 95.0},
			Timestamp:  base.Add(time.Duration(i) * time.Minute),
		})
	}

	assert.True(t, fired, "metric rule should fire after sustained threshold")
}

func TestEngine_NoCooldownMeansAlwaysFires(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
		CooldownSec: 0,
	}
	repo := newMockRepo(rule)

	var fireCount int
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fireCount++
	}, testLogger())

	require.NoError(t, engine.RefreshRules(t.Context()))

	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{},
		Timestamp:  time.Now(),
	}

	engine.HandleEvent(event)
	engine.HandleEvent(event)
	engine.HandleEvent(event)

	assert.Equal(t, 3, fireCount, "with 0 cooldown, rule should fire every time")
}

func TestEngine_MultipleRulesSameMetricNoDuplicateRecording(t *testing.T) {
	// Two rules targeting the same metric — metric sample should be recorded once per event.
	rule1 := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricCPUUsage,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "80", DurationSec: 300},
		},
	}
	rule2 := entities.AlertRule{
		ID:          2,
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricCPUUsage,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "90", DurationSec: 300},
		},
	}
	repo := newMockRepo(rule1, rule2)

	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {}, testLogger())
	require.NoError(t, engine.RefreshRules(t.Context()))

	now := time.Now()
	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricCPUUsage,
		Properties: map[string]any{PropertyValue: 95.0},
		Timestamp:  now,
	})

	// Verify tracker has exactly 1 sample, not 2
	engine.metricTracker.mu.RLock()
	samples := engine.metricTracker.buffers[MetricCPUUsage]
	engine.metricTracker.mu.RUnlock()
	assert.Len(t, samples, 1, "metric should be recorded once per event, not once per rule")
}

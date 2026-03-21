package alerting

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
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
func (m *mockAlertRuleRepo) DeleteRule(_ context.Context, _ uint) error                { return nil }
func (m *mockAlertRuleRepo) ToggleRule(_ context.Context, _ uint, _ bool) error        { return nil }
func (m *mockAlertRuleRepo) DeleteBuiltInRules(_ context.Context) (int64, error)       { return 0, nil }
func (m *mockAlertRuleRepo) ListHistory(_ context.Context, _ repository.AlertHistoryFilter) ([]entities.AlertHistory, int64, error) {
	return nil, 0, nil
}
func (m *mockAlertRuleRepo) DeleteHistory(_ context.Context) (int64, error) { return 0, nil }
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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	engine.cooldowns["1"] = time.Now().Add(-2 * time.Second)
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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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
	}, testLogger(), nil)

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

func TestEngine_DiskMetricPathIsolation(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricDiskUsage,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 0},
		},
	}
	repo := newMockRepo(rule)

	var fired bool
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fired = true
	}, testLogger(), nil)

	require.NoError(t, engine.RefreshRules(t.Context()))

	// Send event for "/" at 90% — should trigger the rule.
	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricDiskUsage,
		Properties: map[string]any{PropertyValue: 90.0, PropertyPath: "/"},
		Timestamp:  time.Now(),
	})
	assert.True(t, fired, "disk usage rule should fire for / at 90%%")

	// Reset and send event for "/data" at 15% — should NOT trigger.
	fired = false
	engine.HandleEvent(&AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricDiskUsage,
		Properties: map[string]any{PropertyValue: 15.0, PropertyPath: "/data"},
		Timestamp:  time.Now(),
	})
	assert.False(t, fired, "disk usage rule should not fire for /data at 15%%")
}

func TestEngine_DiskMetricPathIsolation_Sustained(t *testing.T) {
	// Uses DurationSec > 0 to exercise the per-path MetricTracker buffer isolation
	// in evaluateMetricConditions via metricBufferKey().
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricDiskUsage,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 60},
		},
	}
	repo := newMockRepo(rule)

	var firedPath string
	engine := NewEngine(repo, func(_ *entities.AlertRule, event *AlertEvent) {
		firedPath = event.Properties[PropertyPath].(string)
	}, testLogger(), nil)

	require.NoError(t, engine.RefreshRules(t.Context()))

	now := time.Now()

	// Send sustained events for "/" at 90% over 2 minutes.
	for i := range 5 {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: 90.0, PropertyPath: "/"},
			Timestamp:  now.Add(time.Duration(i) * 30 * time.Second),
		})
	}
	assert.Equal(t, "/", firedPath, "sustained disk usage rule should fire for /")

	// Interleave low "/data" events — should NOT fire because /data buffer is separate.
	firedPath = ""
	for i := range 5 {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: 15.0, PropertyPath: "/data"},
			Timestamp:  now.Add(time.Duration(i) * 30 * time.Second),
		})
	}
	assert.Empty(t, firedPath, "sustained disk usage rule should not fire for /data at 15%%")
}

func TestEngine_EscalationSteps_FiresAtEachStep(t *testing.T) {
	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeSystem,
		TriggerType:     TriggerTypeMetric,
		MetricName:      MetricDiskUsage,
		CooldownSec:     0,
		EscalationSteps: []float64{85, 90, 95, 99},
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 0, SortOrder: 0},
		},
	}

	var fired []float64
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(r *entities.AlertRule, e *AlertEvent) {
		fired = append(fired, e.Properties[PropertyValue].(float64))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(value float64) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: value, PropertyPath: "/"},
			Timestamp:  time.Now(),
		})
	}

	emit(86)
	assert.Equal(t, []float64{86}, fired, "first breach should fire")

	emit(87)
	assert.Equal(t, []float64{86}, fired, "same step should be suppressed")

	emit(91)
	assert.Equal(t, []float64{86, 91}, fired, "crossing 90 step should fire")

	emit(91)
	assert.Equal(t, []float64{86, 91}, fired, "same step should be suppressed")

	emit(96)
	assert.Equal(t, []float64{86, 91, 96}, fired, "crossing 95 step should fire")

	emit(99.5)
	assert.Equal(t, []float64{86, 91, 96, 99.5}, fired, "crossing 99 step should fire")

	emit(99.9)
	assert.Equal(t, []float64{86, 91, 96, 99.5}, fired, "top step should be suppressed")
}

func TestEngine_EscalationSteps_ResetOnRecovery(t *testing.T) {
	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeSystem,
		TriggerType:     TriggerTypeMetric,
		MetricName:      MetricDiskUsage,
		CooldownSec:     0,
		EscalationSteps: []float64{85, 90, 95, 99},
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 0, SortOrder: 0},
		},
	}

	var fireCount int
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fireCount++
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(value float64) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: value, PropertyPath: "/"},
			Timestamp:  time.Now(),
		})
	}

	emit(86)
	assert.Equal(t, 1, fireCount)

	emit(80) // drops below base threshold 85
	assert.Equal(t, 1, fireCount, "below threshold should not fire")

	emit(86) // re-breach after recovery
	assert.Equal(t, 2, fireCount, "re-breach after recovery should fire")
}

func TestEngine_EscalationWithCooldown_SuppressedStepDoesNotConsumeCooldown(t *testing.T) {
	// Verifies that a suppressed escalation step does not start a new cooldown.
	// Scenario: after cooldown expires, a same-step event is suppressed by
	// escalation. Without the fix, tryAcquireCooldown would consume the cooldown
	// before shouldSuppressEscalation runs, blocking a subsequent legitimate
	// escalated alert that arrives immediately after.
	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeSystem,
		TriggerType:     TriggerTypeMetric,
		MetricName:      MetricDiskUsage,
		CooldownSec:     300, // 5-minute cooldown
		EscalationSteps: []float64{85, 90, 95},
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 0},
		},
	}

	var fired []float64
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, e *AlertEvent) {
		fired = append(fired, e.Properties[PropertyValue].(float64))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(value float64) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: value, PropertyPath: "/"},
			Timestamp:  time.Now(),
		})
	}

	// Step 85 fires, cooldown starts.
	emit(86)
	assert.Equal(t, []float64{86}, fired)

	// Expire cooldown manually.
	engine.cooldownsMu.Lock()
	for k := range engine.cooldowns {
		engine.cooldowns[k] = time.Now().Add(-10 * time.Minute)
	}
	engine.cooldownsMu.Unlock()

	// Same step (87% → still step 85) — suppressed by escalation.
	// Must NOT start a new cooldown.
	emit(87)
	assert.Equal(t, []float64{86}, fired, "same step should be suppressed")

	// Higher step (91% → step 90) arrives immediately after.
	// Should fire because the suppressed event must not have consumed cooldown.
	emit(91)
	assert.Equal(t, []float64{86, 91}, fired, "escalated step should not be blocked by suppressed step's cooldown")
}

func TestEngine_EscalationSteps_MultiplePathsIndependent(t *testing.T) {
	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeSystem,
		TriggerType:     TriggerTypeMetric,
		MetricName:      MetricDiskUsage,
		CooldownSec:     0,
		EscalationSteps: []float64{85, 90, 95, 99},
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 0, SortOrder: 0},
		},
	}

	var fired []string
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, e *AlertEvent) {
		fired = append(fired, e.Properties[PropertyPath].(string))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(value float64, path string) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: value, PropertyPath: path},
			Timestamp:  time.Now(),
		})
	}

	emit(96, "/")
	assert.Equal(t, []string{"/"}, fired)

	emit(86, "/mnt/data")
	assert.Equal(t, []string{"/", "/mnt/data"}, fired)

	emit(96, "/")
	assert.Equal(t, []string{"/", "/mnt/data"}, fired, "same path+step should be suppressed")
}

func TestEngine_NoEscalationSteps_LegacyBehavior(t *testing.T) {
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeSystem,
		TriggerType: TriggerTypeMetric,
		MetricName:  MetricDiskUsage,
		CooldownSec: 0,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 0, SortOrder: 0},
		},
	}

	var fireCount int
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fireCount++
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(value float64) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: value, PropertyPath: "/"},
			Timestamp:  time.Now(),
		})
	}

	emit(86)
	emit(87)
	emit(88)
	assert.Equal(t, 3, fireCount, "without escalation steps, all events should fire")
}

func TestEngine_EscalationSteps_WithSustainedCondition(t *testing.T) {
	// Exercises the combined sustained-metric + escalation path (DurationSec > 0),
	// which is the production configuration for the "Low disk space" rule.
	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeSystem,
		TriggerType:     TriggerTypeMetric,
		MetricName:      MetricDiskUsage,
		CooldownSec:     0,
		EscalationSteps: []float64{85, 90, 95, 99},
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 60, SortOrder: 0},
		},
	}

	var fired []float64
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, e *AlertEvent) {
		fired = append(fired, e.Properties[PropertyValue].(float64))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	now := time.Now()
	emit := func(value float64, t time.Time) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeSystem,
			MetricName: MetricDiskUsage,
			Properties: map[string]any{PropertyValue: value, PropertyPath: "/"},
			Timestamp:  t,
		})
	}

	// Sustain 86% for >60 seconds — should fire once (step 85)
	for i := range 3 {
		emit(86, now.Add(time.Duration(i)*30*time.Second))
	}
	assert.Equal(t, []float64{86}, fired, "sustained breach should fire once")

	// Continue at 86% — suppressed (still step 85)
	emit(86, now.Add(120*time.Second))
	assert.Equal(t, []float64{86}, fired, "same step sustained should be suppressed")

	// Jump to 91% sustained — should fire (step 90)
	for i := range 3 {
		emit(91, now.Add(150*time.Second+time.Duration(i)*30*time.Second))
	}
	assert.Equal(t, []float64{86, 91}, fired, "higher step sustained should fire")
}

func TestEngine_CooldownAtomicUnderConcurrency(t *testing.T) {
	// Verifies that concurrent HandleEvent calls for the same event+rule
	// only fire once when a cooldown is set (no TOCTOU race).
	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeStream,
		TriggerType: TriggerTypeEvent,
		EventName:   EventStreamDisconnected,
		CooldownSec: 300,
	}
	repo := newMockRepo(rule)

	var fireCount atomic.Int64
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
		fireCount.Add(1)
	}, testLogger(), nil)

	require.NoError(t, engine.RefreshRules(t.Context()))

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			engine.HandleEvent(&AlertEvent{
				ObjectType: ObjectTypeStream,
				EventName:  EventStreamDisconnected,
				Properties: map[string]any{},
				Timestamp:  time.Now(),
			})
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), fireCount.Load(), "concurrent calls should fire exactly once with cooldown")
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

	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {}, testLogger(), nil)
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

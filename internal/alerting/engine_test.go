package alerting

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
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

func TestEngine_DetectionEscalationSteps_FiresAtHigherConfidence(t *testing.T) {
	const (
		bayBreastedWarblerSci = "Setophaga castanea"
		noveltyEpisodeStart   = "2026-05-23T12:00:00Z"
		detectionCooldownSec  = 24 * 60 * 60
		firstConfidenceStep   = 0.75
		secondConfidenceStep  = 0.85
		thirdConfidenceStep   = 0.90
		firstStepDetection    = 0.76
		sameStepDetection     = 0.80
		secondStepDetection   = 0.86
		thirdStepDetection    = 0.91
		noveltyEpisodeDays    = 12
	)

	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeDetection,
		TriggerType:     TriggerTypeEvent,
		EventName:       EventDetectionOccurred,
		CooldownSec:     detectionCooldownSec,
		EscalationSteps: []float64{firstConfidenceStep, secondConfidenceStep, thirdConfidenceStep},
		Conditions: []entities.AlertCondition{
			{Property: PropertyConfidence, Operator: OperatorGreaterOrEqual, Value: "0.75"},
			{Property: PropertyNoveltyEpisodeDays, Operator: OperatorGreaterOrEqual, Value: "7"},
		},
	}

	var fired []float64
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, event *AlertEvent) {
		fired = append(fired, event.Properties[PropertyThresholdStep].(float64))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(confidence float64) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionOccurred,
			Properties: map[string]any{
				PropertySpeciesName:         "Bay-breasted Warbler",
				PropertyScientificName:      bayBreastedWarblerSci,
				PropertyConfidence:          confidence,
				PropertyNoveltyEpisodeDays:  noveltyEpisodeDays,
				PropertyNoveltyEpisodeStart: noveltyEpisodeStart,
				PropertyDaysSinceLastSeen:   noveltyEpisodeDays,
			},
			Timestamp: time.Now(),
		})
	}

	emit(firstStepDetection)
	assert.Equal(t, []float64{firstConfidenceStep}, fired)

	emit(sameStepDetection)
	assert.Equal(t, []float64{firstConfidenceStep}, fired, "same confidence step should be suppressed")

	emit(secondStepDetection)
	assert.Equal(t, []float64{firstConfidenceStep, secondConfidenceStep}, fired)

	emit(thirdStepDetection)
	assert.Equal(t, []float64{firstConfidenceStep, secondConfidenceStep, thirdConfidenceStep}, fired)
}

func TestEngine_DetectionEscalationSteps_AllowsRepeatAfterCooldown(t *testing.T) {
	const (
		bayBreastedWarblerSci = "Setophaga castanea"
		noveltyEpisodeStart   = "2026-05-23T12:00:00Z"
		detectionCooldownSec  = 60
		firstConfidenceStep   = 0.75
		secondConfidenceStep  = 0.85
		firstStepDetection    = 0.80
		secondStepDetection   = 0.86
		noveltyEpisodeDays    = 12
	)

	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeDetection,
		TriggerType:     TriggerTypeEvent,
		EventName:       EventDetectionOccurred,
		CooldownSec:     detectionCooldownSec,
		EscalationSteps: []float64{firstConfidenceStep, secondConfidenceStep},
		Conditions: []entities.AlertCondition{
			{Property: PropertyConfidence, Operator: OperatorGreaterOrEqual, Value: "0.75"},
			{Property: PropertyNoveltyEpisodeDays, Operator: OperatorGreaterOrEqual, Value: "7"},
		},
	}

	var fired []float64
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, event *AlertEvent) {
		fired = append(fired, event.Properties[PropertyThresholdStep].(float64))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	newEvent := func(confidence float64) *AlertEvent {
		return &AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionOccurred,
			Properties: map[string]any{
				PropertySpeciesName:         "Bay-breasted Warbler",
				PropertyScientificName:      bayBreastedWarblerSci,
				PropertyConfidence:          confidence,
				PropertyNoveltyEpisodeDays:  noveltyEpisodeDays,
				PropertyNoveltyEpisodeStart: noveltyEpisodeStart,
			},
			Timestamp: time.Now(),
		}
	}

	secondStepEvent := newEvent(secondStepDetection)
	engine.HandleEvent(secondStepEvent)
	assert.Equal(t, []float64{secondConfidenceStep}, fired)

	engine.HandleEvent(newEvent(firstStepDetection))
	assert.Equal(t, []float64{secondConfidenceStep}, fired, "lower step should respect cooldown")

	cdKey := cooldownKey(&rule, secondStepEvent)
	engine.cooldownsMu.Lock()
	engine.cooldowns[cdKey] = time.Now().Add(-2 * time.Duration(detectionCooldownSec) * time.Second)
	engine.cooldownsMu.Unlock()

	engine.HandleEvent(newEvent(firstStepDetection))
	assert.Equal(t, []float64{secondConfidenceStep, firstConfidenceStep}, fired)

	engine.HandleEvent(newEvent(secondStepDetection))
	assert.Equal(t, []float64{secondConfidenceStep, firstConfidenceStep, secondConfidenceStep}, fired, "higher step should bypass active cooldown")
}

func TestEngine_DetectionEscalationSteps_SuppressesMissingConfidence(t *testing.T) {
	const (
		detectionCooldownSec = 24 * 60 * 60
		firstConfidenceStep  = 0.75
		secondConfidenceStep = 0.85
	)

	tests := []struct {
		name       string
		properties map[string]any
	}{
		{
			name: "missing confidence",
			properties: map[string]any{
				PropertySpeciesName: "Bay-breasted Warbler",
			},
		},
		{
			name: "invalid confidence",
			properties: map[string]any{
				PropertySpeciesName: "Bay-breasted Warbler",
				PropertyConfidence:  "not-a-number",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := entities.AlertRule{
				ID:              1,
				Enabled:         true,
				ObjectType:      ObjectTypeDetection,
				TriggerType:     TriggerTypeEvent,
				EventName:       EventDetectionOccurred,
				CooldownSec:     detectionCooldownSec,
				EscalationSteps: []float64{firstConfidenceStep, secondConfidenceStep},
			}

			var fireCount int
			repo := newMockRepo(rule)
			engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {
				fireCount++
			}, testLogger(), nil)
			require.NoError(t, engine.RefreshRules(t.Context()))

			engine.HandleEvent(&AlertEvent{
				ObjectType: ObjectTypeDetection,
				EventName:  EventDetectionOccurred,
				Properties: tt.properties,
				Timestamp:  time.Now(),
			})

			assert.Zero(t, fireCount, "confidence escalation should not fire without a usable confidence value")
		})
	}
}

func TestEngine_DetectionEscalationSteps_UnknownSpeciesUsesCooldown(t *testing.T) {
	const (
		detectionCooldownSec = 3600
		firstConfidenceStep  = 0.75
		secondConfidenceStep = 0.85
		detectionConfidence  = 0.90
	)

	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeDetection,
		TriggerType:     TriggerTypeEvent,
		EventName:       EventDetectionOccurred,
		CooldownSec:     detectionCooldownSec,
		EscalationSteps: []float64{firstConfidenceStep, secondConfidenceStep},
		Conditions: []entities.AlertCondition{
			{Property: PropertyConfidence, Operator: OperatorGreaterOrEqual, Value: "0.75"},
		},
	}

	var fired []bool
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, event *AlertEvent) {
		_, hasThresholdStep := event.Properties[PropertyThresholdStep]
		fired = append(fired, hasThresholdStep)
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	event := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: map[string]any{
			PropertyConfidence: detectionConfidence,
		},
		Timestamp: time.Now(),
	}

	engine.HandleEvent(event)
	engine.HandleEvent(event)

	assert.Equal(t, []bool{false}, fired, "species-less detections should use regular cooldown without threshold metadata")
}

func TestEngine_DetectionEscalationSteps_PrunesPreviousEpisode(t *testing.T) {
	const (
		bayBreastedWarblerSci = "Setophaga castanea"
		oldEpisodeStart       = "2026-05-23T12:00:00Z"
		newEpisodeStart       = "2026-06-02T12:00:00Z"
		confidenceStep        = 0.75
		detectionConfidence   = 0.80
		noveltyEpisodeDays    = 12
	)

	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeDetection,
		TriggerType:     TriggerTypeEvent,
		EventName:       EventDetectionOccurred,
		EscalationSteps: []float64{confidenceStep},
		Conditions: []entities.AlertCondition{
			{Property: PropertyConfidence, Operator: OperatorGreaterOrEqual, Value: "0.75"},
			{Property: PropertyNoveltyEpisodeDays, Operator: OperatorGreaterOrEqual, Value: "7"},
		},
	}

	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, _ *AlertEvent) {}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	buildEvent := func(episodeStart string) *AlertEvent {
		return &AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionOccurred,
			Properties: map[string]any{
				PropertySpeciesName:         "Bay-breasted Warbler",
				PropertyScientificName:      bayBreastedWarblerSci,
				PropertyConfidence:          detectionConfidence,
				PropertyNoveltyEpisodeDays:  noveltyEpisodeDays,
				PropertyNoveltyEpisodeStart: episodeStart,
			},
			Timestamp: time.Now(),
		}
	}

	oldEvent := buildEvent(oldEpisodeStart)
	newEpisodeEvent := buildEvent(newEpisodeStart)
	speciesKey := detectionSpeciesKey(oldEvent)
	oldKey := detectionEscalationKey(&rule, oldEvent, speciesKey)
	newKey := detectionEscalationKey(&rule, newEpisodeEvent, speciesKey)

	engine.HandleEvent(oldEvent)
	engine.HandleEvent(newEpisodeEvent)

	engine.escalationsMu.RLock()
	_, oldExists := engine.escalations[oldKey]
	_, newExists := engine.escalations[newKey]
	engine.escalationsMu.RUnlock()

	assert.False(t, oldExists, "old episode escalation key should be pruned")
	assert.True(t, newExists, "current episode escalation key should remain")
}

func TestEngine_DetectionNoveltyCooldownIsSpeciesScoped(t *testing.T) {
	const noveltyEpisodeStart = "2026-05-23T12:00:00Z"

	rule := entities.AlertRule{
		ID:          1,
		Enabled:     true,
		ObjectType:  ObjectTypeDetection,
		TriggerType: TriggerTypeEvent,
		EventName:   EventDetectionOccurred,
		CooldownSec: 3600,
		Conditions: []entities.AlertCondition{
			{Property: PropertyNoveltyEpisodeDays, Operator: OperatorGreaterOrEqual, Value: "7"},
		},
	}

	var fired []string
	repo := newMockRepo(rule)
	engine := NewEngine(repo, func(_ *entities.AlertRule, event *AlertEvent) {
		fired = append(fired, event.Properties[PropertyScientificName].(string))
	}, testLogger(), nil)
	require.NoError(t, engine.RefreshRules(t.Context()))

	emit := func(scientificName string) {
		engine.HandleEvent(&AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionOccurred,
			Properties: map[string]any{
				PropertySpeciesName:         scientificName,
				PropertyScientificName:      scientificName,
				PropertyConfidence:          0.8,
				PropertyNoveltyEpisodeDays:  9,
				PropertyNoveltyEpisodeStart: noveltyEpisodeStart,
			},
			Timestamp: time.Now(),
		})
	}

	emit("Setophaga castanea")
	emit("Setophaga castanea")
	emit("Bubo virginianus")

	assert.Equal(t, []string{"Setophaga castanea", "Bubo virginianus"}, fired)
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

// retryMockRepo wraps mockAlertRuleRepo and adds controllable DeleteHistoryBefore behavior.
type retryMockRepo struct {
	mockAlertRuleRepo
	deleteErrors []error      // errors to return on successive calls (nil = success)
	deleteCalls  atomic.Int64 // total calls to DeleteHistoryBefore
}

func (m *retryMockRepo) DeleteHistoryBefore(_ context.Context, _ time.Time) (int64, error) {
	call := int(m.deleteCalls.Add(1)) - 1
	if call < len(m.deleteErrors) && m.deleteErrors[call] != nil {
		return 0, m.deleteErrors[call]
	}
	return 5, nil // simulate 5 deleted rows on success
}

func TestDeleteHistoryWithRetry_SucceedsOnFirstAttempt(t *testing.T) {
	repo := &retryMockRepo{}
	engine := NewEngine(repo, nil, testLogger(), nil)
	stopCh := make(chan struct{})

	deleted, err := engine.deleteHistoryWithRetry(30, stopCh)

	require.NoError(t, err)
	assert.Equal(t, int64(5), deleted)
	assert.Equal(t, int64(1), repo.deleteCalls.Load())
}

func TestDeleteHistoryWithRetry_RetriesOnDBLock(t *testing.T) {
	repo := &retryMockRepo{
		deleteErrors: []error{
			fmt.Errorf("database is locked"),
			fmt.Errorf("database is locked"),
			nil, // succeeds on third attempt
		},
	}
	engine := NewEngine(repo, nil, testLogger(), nil)
	stopCh := make(chan struct{})

	deleted, err := engine.deleteHistoryWithRetry(30, stopCh)

	require.NoError(t, err)
	assert.Equal(t, int64(5), deleted)
	assert.Equal(t, int64(3), repo.deleteCalls.Load())
}

func TestDeleteHistoryWithRetry_ExhaustsRetriesOnPersistentLock(t *testing.T) {
	repo := &retryMockRepo{
		deleteErrors: []error{
			fmt.Errorf("database is locked"),
			fmt.Errorf("SQLITE_BUSY"),
			fmt.Errorf("database is locked"),
		},
	}
	engine := NewEngine(repo, nil, testLogger(), nil)
	stopCh := make(chan struct{})

	deleted, err := engine.deleteHistoryWithRetry(30, stopCh)

	require.Error(t, err)
	assert.Equal(t, int64(0), deleted)
	assert.True(t, datastore.IsTransientDBError(err), "error should be a DB lock error")
	assert.Equal(t, int64(3), repo.deleteCalls.Load(), "should attempt exactly cleanupMaxRetries times")
}

func TestDeleteHistoryWithRetry_NoRetryOnNonLockError(t *testing.T) {
	repo := &retryMockRepo{
		deleteErrors: []error{
			fmt.Errorf("disk I/O error"),
		},
	}
	engine := NewEngine(repo, nil, testLogger(), nil)
	stopCh := make(chan struct{})

	deleted, err := engine.deleteHistoryWithRetry(30, stopCh)

	require.Error(t, err)
	assert.Equal(t, int64(0), deleted)
	assert.Contains(t, err.Error(), "disk I/O error")
	assert.Equal(t, int64(1), repo.deleteCalls.Load(), "should not retry non-lock errors")
}

func TestDeleteHistoryWithRetry_AbortsOnStop(t *testing.T) {
	repo := &retryMockRepo{
		deleteErrors: []error{
			fmt.Errorf("database is locked"),
			fmt.Errorf("database is locked"),
			fmt.Errorf("database is locked"),
		},
	}
	engine := NewEngine(repo, nil, testLogger(), nil)
	stopCh := make(chan struct{})
	close(stopCh) // already stopped

	deleted, err := engine.deleteHistoryWithRetry(30, stopCh)

	require.Error(t, err)
	assert.Equal(t, int64(0), deleted)
	// stopCh is closed before retry, so only 1 DB call should happen
	assert.Equal(t, int64(1), repo.deleteCalls.Load(), "should abort after a single DB call when stop channel is closed")
}

func TestIsDBLockError(t *testing.T) {
	assert.True(t, datastore.IsTransientDBError(fmt.Errorf("database is locked")))
	assert.True(t, datastore.IsTransientDBError(fmt.Errorf("SQLITE_BUSY")))
	assert.True(t, datastore.IsTransientDBError(fmt.Errorf("some context: database is locked")))
	assert.False(t, datastore.IsTransientDBError(fmt.Errorf("disk I/O error")))
	assert.False(t, datastore.IsTransientDBError(fmt.Errorf("connection refused")))
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

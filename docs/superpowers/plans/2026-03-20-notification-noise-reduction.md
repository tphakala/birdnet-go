# Notification Noise Reduction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce notification noise from disk alert spam (stepped escalation) and securefs file-not-found errors (suppress expected 404s + error burst grouping).

**Architecture:** Three independent changes: (1) Add escalation step tracking to the alerting engine so metric rules fire only when crossing new threshold steps, (2) Skip EnhancedError wrapping for file-not-found in securefs serve methods, (3) Add an ErrorBurstTracker to the notification error hook that groups repeated errors from the same component+category.

**Tech Stack:** Go 1.26, testify, GORM (SQLite), i18n JSON files

**Spec:** `docs/superpowers/specs/2026-03-20-notification-noise-reduction-design.md`

---

### Task 1: Add EscalationSteps field to AlertRule entity

**Files:**
- Modify: `internal/datastore/v2/entities/alert_rule.go:7-24`

- [ ] **Step 1: Add the EscalationSteps field**

Add the field after line 19 (`CooldownSec`):

```go
EscalationSteps []float64 `gorm:"serializer:json;default:null" json:"escalation_steps,omitempty"`
```

GORM's `serializer:json` stores this as a JSON string in a TEXT column. Nil/empty means no escalation (legacy behavior). GORM auto-migrates this column on startup.

- [ ] **Step 2: Verify existing tests still pass**

Run: `go test -race -count=1 ./internal/alerting/...`
Expected: All existing tests PASS (new field is optional, zero value is nil).

- [ ] **Step 3: Commit**

```bash
git add internal/datastore/v2/entities/alert_rule.go
git commit -m "feat(alerting): add EscalationSteps field to AlertRule entity"
```

---

### Task 2: Add PropertyThresholdStep constant

**Files:**
- Modify: `internal/alerting/constants.go:62-74`

- [ ] **Step 1: Add the constant**

Add after `PropertyBroker` (line 73):

```go
PropertyThresholdStep = "threshold_step"
```

No test needed — it's a string constant.

- [ ] **Step 2: Commit**

```bash
git add internal/alerting/constants.go
git commit -m "feat(alerting): add PropertyThresholdStep constant"
```

---

### Task 3: Implement escalation logic in the engine

**Files:**
- Modify: `internal/alerting/engine.go:28-46` (Engine struct), `89-113` (HandleEvent)
- Test: `internal/alerting/engine_test.go`

- [ ] **Step 1: Write failing tests for escalation behavior**

Add to `internal/alerting/engine_test.go`. These tests cover:
- First breach at step[0] fires
- Same-step event is suppressed
- Higher step fires
- Drop below base clears state (next breach fires again)
- No escalation steps = legacy cooldown-only behavior
- Multiple paths tracked independently

```go
func TestEngine_EscalationSteps_FiresAtEachStep(t *testing.T) {
	rule := entities.AlertRule{
		ID:              1,
		Enabled:         true,
		ObjectType:      ObjectTypeSystem,
		TriggerType:     TriggerTypeMetric,
		MetricName:      MetricDiskUsage,
		CooldownSec:     0, // disable cooldown for test clarity
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

	// First breach at 86% — should fire (step 85)
	emit(86)
	assert.Equal(t, []float64{86}, fired, "first breach should fire")

	// Same level — should NOT fire (still in step 85)
	emit(87)
	assert.Equal(t, []float64{86}, fired, "same step should be suppressed")

	// Cross 90% step — should fire
	emit(91)
	assert.Equal(t, []float64{86, 91}, fired, "crossing 90 step should fire")

	// Still at 91% — should NOT fire
	emit(91)
	assert.Equal(t, []float64{86, 91}, fired, "same step should be suppressed")

	// Cross 95% — should fire
	emit(96)
	assert.Equal(t, []float64{86, 91, 96}, fired, "crossing 95 step should fire")

	// Cross 99% — should fire
	emit(99.5)
	assert.Equal(t, []float64{86, 91, 96, 99.5}, fired, "crossing 99 step should fire")

	// Still above 99% — should NOT fire
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

	// Fire at 86%
	emit(86)
	assert.Equal(t, 1, fireCount)

	// Drop below 85% — should clear escalation
	emit(80)
	assert.Equal(t, 1, fireCount, "below threshold should not fire")

	// Breach again at 86% — should fire again (state was cleared)
	emit(86)
	assert.Equal(t, 2, fireCount, "re-breach after recovery should fire")
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

	// / hits 96% (fires at step 95)
	emit(96, "/")
	assert.Equal(t, []string{"/"}, fired)

	// /mnt/data hits 86% — should fire independently (step 85)
	emit(86, "/mnt/data")
	assert.Equal(t, []string{"/", "/mnt/data"}, fired)

	// / at 96% again — suppressed (still step 95)
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
		// No EscalationSteps — legacy behavior
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

	// Without escalation steps, every matching event fires (cooldown=0)
	emit(86)
	emit(87)
	emit(88)
	assert.Equal(t, 3, fireCount, "without escalation steps, all events should fire")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 -run 'TestEngine_Escalation|TestEngine_NoEscalation' ./internal/alerting/...`
Expected: FAIL — escalation logic not yet implemented.

- [ ] **Step 3: Implement escalation state and logic in engine.go**

Add escalation map to Engine struct (after `cooldownsMu` at line 38):

```go
// Escalation tracking: maps "ruleID|metricKey" → last fired step value.
// Prevents re-fire until metric crosses the next higher step.
escalations   map[string]float64
escalationsMu sync.RWMutex
```

Initialize in `NewEngine` (after `cooldowns: make(...)` at line 56):

```go
escalations: make(map[string]float64),
```

Add helper function `escalationKey`:

```go
// escalationKey builds the escalation state key from the rule ID and metric
// instance (path). Multiple mount points are tracked independently.
func escalationKey(ruleID uint, metricName string, properties map[string]any) string {
	return fmt.Sprintf("%d|%s", ruleID, metricBufferKey(metricName, properties))
}
```

Add `import "fmt"` to the imports if not already present.

Add `clearEscalationIfRecovered` method:

```go
// clearEscalationIfRecovered clears escalation state for rules whose metric
// has dropped below the base threshold (EscalationSteps[0]). This must run
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
		if floatVal < rule.EscalationSteps[0] {
			key := escalationKey(rule.ID, event.MetricName, event.Properties)
			e.escalationsMu.Lock()
			delete(e.escalations, key)
			e.escalationsMu.Unlock()
		}
	}
}
```

Add `shouldSuppressEscalation` method:

```go
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
	currentStep := float64(-1)
	for _, step := range rule.EscalationSteps {
		if floatVal >= step {
			currentStep = step
		}
	}
	if currentStep < 0 {
		// Below all steps — shouldn't have matched, but be safe.
		return true, nil
	}

	key := escalationKey(rule.ID, event.MetricName, event.Properties)

	e.escalationsMu.RLock()
	lastStep, exists := e.escalations[key]
	e.escalationsMu.RUnlock()

	if exists && lastStep >= currentStep {
		return true, nil // Already fired at this step or higher
	}

	// Record the new step
	e.escalationsMu.Lock()
	e.escalations[key] = currentStep
	e.escalationsMu.Unlock()

	// Shallow-copy properties and add the threshold step (don't mutate shared map)
	propsCopy := make(map[string]any, len(event.Properties)+1)
	for k, v := range event.Properties {
		propsCopy[k] = v
	}
	propsCopy[PropertyThresholdStep] = currentStep

	return false, propsCopy
}
```

Modify `HandleEvent` to call `clearEscalationIfRecovered` and the escalation check:

```go
func (e *Engine) HandleEvent(event *AlertEvent) {
	// Record metric sample once before rule iteration.
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
		if e.ruleMatches(rule, event) && !e.isInCooldown(rule.ID, rule.CooldownSec) {
			// Check escalation suppression for metric rules with steps.
			if rule.TriggerType == TriggerTypeMetric && len(rule.EscalationSteps) > 0 {
				suppress, props := e.shouldSuppressEscalation(rule, event)
				if suppress {
					continue
				}
				// Fire with the augmented properties (includes PropertyThresholdStep).
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
			e.fireRule(rule, event)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race -count=1 -run 'TestEngine_Escalation|TestEngine_NoEscalation' ./internal/alerting/...`
Expected: All 4 new tests PASS.

- [ ] **Step 5: Run full alerting test suite**

Run: `go test -race -count=1 ./internal/alerting/...`
Expected: All tests PASS (new logic doesn't break existing behavior).

- [ ] **Step 6: Commit**

```bash
git add internal/alerting/engine.go internal/alerting/engine_test.go
git commit -m "feat(alerting): implement stepped escalation for metric rules

Metric rules with EscalationSteps only re-fire when the metric crosses
a new step. State is cleared when the metric drops below the base step,
so the next breach starts fresh. Multiple metric instances (e.g., disk
mount points) are tracked independently."
```

---

### Task 4: Update dispatcher to use threshold step in messages

**Files:**
- Modify: `internal/alerting/dispatcher.go:180-209` (metricMessage function)
- Test: `internal/alerting/dispatcher_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/alerting/dispatcher_test.go`:

```go
func TestMetricMessage_UsesThresholdStep(t *testing.T) {
	rule := &entities.AlertRule{
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", SortOrder: 0},
		},
	}
	event := &AlertEvent{
		MetricName: MetricDiskUsage,
		Properties: map[string]any{
			PropertyValue:         float64(91.2),
			PropertyThresholdStep: float64(90),
		},
	}

	key, params, fallback := metricMessage(rule, event)
	assert.Equal(t, MsgAlertMetricExceeded, key)
	assert.Equal(t, "90", params["threshold"])
	assert.Equal(t, "91.2", params["value"])
	assert.Equal(t, "Current value: 91.2% (threshold: 90%)", fallback)
}

func TestMetricMessage_NoThresholdStep_UsesCondition(t *testing.T) {
	rule := &entities.AlertRule{
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", SortOrder: 0},
		},
	}
	event := &AlertEvent{
		MetricName: MetricDiskUsage,
		Properties: map[string]any{
			PropertyValue: float64(87),
		},
	}

	key, params, fallback := metricMessage(rule, event)
	assert.Equal(t, MsgAlertMetricExceeded, key)
	assert.Equal(t, "85", params["threshold"])
	assert.Equal(t, "Current value: 87% (threshold: 85%)", fallback)
}
```

- [ ] **Step 2: Run tests to verify the first test fails**

Run: `go test -race -count=1 -run 'TestMetricMessage_UsesThresholdStep' ./internal/alerting/...`
Expected: FAIL — metricMessage doesn't read PropertyThresholdStep yet.

- [ ] **Step 3: Implement the change in metricMessage**

In `dispatcher.go`, modify `metricMessage` (around line 191-198). After determining the threshold from conditions, check for `PropertyThresholdStep` and prefer it:

```go
func metricMessage(rule *entities.AlertRule, event *AlertEvent) (key string, params map[string]any, fallback string) {
	val, ok := event.Properties[PropertyValue]
	if !ok {
		return "", nil, ""
	}
	floatVal, err := toFloat64(val)
	if err != nil {
		return "", nil, ""
	}
	formatted := formatMetricValue(floatVal)

	// Prefer the escalation step threshold if available; fall back to the
	// condition-level threshold for rules without escalation steps.
	threshold := ""
	if step, ok := event.Properties[PropertyThresholdStep]; ok {
		if stepFloat, err := toFloat64(step); err == nil {
			threshold = formatMetricValue(stepFloat)
		}
	}
	if threshold == "" {
		for i := range rule.Conditions {
			if rule.Conditions[i].Property == PropertyValue {
				threshold = rule.Conditions[i].Value
				break
			}
		}
	}
	if threshold == "" {
		return "", nil, ""
	}

	params = map[string]any{
		"value":     formatted,
		"threshold": threshold,
	}
	fallback = fmt.Sprintf("Current value: %s%% (threshold: %s%%)", formatted, threshold)
	return MsgAlertMetricExceeded, params, fallback
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race -count=1 -run 'TestMetricMessage' ./internal/alerting/...`
Expected: Both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/alerting/dispatcher.go internal/alerting/dispatcher_test.go
git commit -m "feat(alerting): show escalation step threshold in metric notifications

When a metric alert fires via escalation, the notification message now
shows the step threshold that was crossed (e.g. 'threshold: 90%')
instead of the base rule condition threshold."
```

---

### Task 5: Update default rules with escalation steps + migration

**Files:**
- Modify: `internal/alerting/defaults.go:107-124`
- Modify: `internal/alerting/init.go:127-156` (seedDefaultRules)
- Test: `internal/alerting/init_test.go`

- [ ] **Step 1: Write failing test for migration behavior**

Add to `internal/alerting/init_test.go`:

```go
func TestSeedDefaultRules_MigratesEscalationSteps(t *testing.T) {
	// Simulate existing installation: low disk rule exists but has no escalation steps.
	existingRule := entities.AlertRule{
		ID:      7,
		Name:    "Low disk space",
		NameKey: RuleKeyLowDiskName,
		BuiltIn: true,
		Enabled: true,
		// EscalationSteps is nil (pre-migration)
	}
	repo := &initMockRepo{rules: []entities.AlertRule{existingRule}}

	err := seedDefaultRules(t.Context(), repo, initTestLogger())
	require.NoError(t, err)

	// Verify the existing rule was updated with escalation steps.
	require.Len(t, repo.rules, len(DefaultRules()))
	for i := range repo.rules {
		if repo.rules[i].NameKey == RuleKeyLowDiskName {
			assert.Equal(t, []float64{85, 90, 95, 99}, repo.rules[i].EscalationSteps,
				"existing low disk rule should get escalation steps from migration")
			return
		}
	}
	t.Fatal("low disk rule not found after seeding")
}
```

Note: This test depends on `initMockRepo` supporting `UpdateRule`. Check the existing mock — if `UpdateRule` is a no-op, update it to actually apply the update to the in-memory slice.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race -count=1 -run 'TestSeedDefaultRules_MigratesEscalation' ./internal/alerting/...`
Expected: FAIL — migration logic not implemented yet.

- [ ] **Step 3: Add EscalationSteps to the low disk rule default**

In `internal/alerting/defaults.go`, add to the "Low disk space" rule (after `CooldownSec: 1800` at line 117):

```go
EscalationSteps: []float64{85, 90, 95, 99},
```

- [ ] **Step 4: Add migration logic to seedDefaultRules**

In `internal/alerting/init.go`, add migration logic after the existing seeding loop (after line 154, before the final return):

```go
// Migrate existing built-in rules: populate EscalationSteps on the low
// disk rule if it was seeded before escalation support was added.
defaults := DefaultRules()
defaultSteps := make(map[string][]float64)
for i := range defaults {
	if defaults[i].BuiltIn && len(defaults[i].EscalationSteps) > 0 {
		defaultSteps[defaults[i].NameKey] = defaults[i].EscalationSteps
	}
}
for i := range existing {
	rule := &existing[i]
	if !rule.BuiltIn || rule.NameKey == "" {
		continue
	}
	steps, ok := defaultSteps[rule.NameKey]
	if !ok || len(rule.EscalationSteps) > 0 {
		continue // no default steps or already has steps
	}
	rule.EscalationSteps = steps
	if err := repo.UpdateRule(ctx, rule); err != nil {
		log.Warn("failed to migrate escalation steps for built-in rule",
			logger.String("name", rule.Name),
			logger.Error(err))
		continue
	}
	log.Info("migrated escalation steps for built-in rule",
		logger.String("name", rule.Name))
}
```

Note: The `existing` slice is already loaded at the top of `seedDefaultRules`. The `defaults` variable name was already used above — rename appropriately or restructure to avoid shadowing.

- [ ] **Step 5: Update initMockRepo if needed**

Check if `initMockRepo.UpdateRule` is a no-op. If so, update it to apply changes to the in-memory rules slice so the migration test can verify the result.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -race -count=1 ./internal/alerting/...`
Expected: All tests PASS including the new migration test.

- [ ] **Step 7: Commit**

```bash
git add internal/alerting/defaults.go internal/alerting/init.go internal/alerting/init_test.go
git commit -m "feat(alerting): add escalation steps to low disk rule with migration

Existing installations get escalation steps [85, 90, 95, 99] applied
automatically on startup. New installations get them from the defaults."
```

---

### Task 6: Suppress securefs EnhancedError for file-not-found

**Files:**
- Modify: `internal/securefs/securefs.go:1-17` (imports), `646-649` (ServeFile), `670-672` (ServeRelativeFile)
- Test: `internal/securefs/securefs_test.go`

- [ ] **Step 1: Write failing tests**

Add tests that verify `ServeFile` and `ServeRelativeFile` return a plain error (not `*errors.EnhancedError`) for file-not-found, but still return an EnhancedError for other failures.

```go
func TestServeRelativeFile_FileNotFound_NoEnhancedError(t *testing.T) {
	dir := t.TempDir()
	sfs, err := New(dir)
	require.NoError(t, err)
	defer sfs.Close()

	// Create a mock echo context for serving
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/test.flac", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = sfs.ServeRelativeFile(ctx, "nonexistent.flac")
	require.Error(t, err)

	// Should be a plain error wrapping fs.ErrNotExist, NOT an EnhancedError
	var enhErr *errors.EnhancedError
	assert.False(t, errors.As(err, &enhErr), "file-not-found should not produce EnhancedError")
	assert.True(t, stdErrors.Is(err, fs.ErrNotExist), "should wrap fs.ErrNotExist")
}

func TestServeRelativeFile_PermissionDenied_ReturnsEnhancedError(t *testing.T) {
	// This test verifies that non-404 errors still produce EnhancedError.
	// Implementation depends on the platform's ability to create permission-denied scenarios.
	// Skip if not feasible in CI.
	t.Skip("platform-dependent — manual verification")
}
```

Note: The test needs imports for `echo`, `httptest`, `net/http`, `errors` (internal), and `stdErrors "errors"`. Check the existing test file for import patterns.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race -count=1 -run 'TestServeRelativeFile_FileNotFound' ./internal/securefs/...`
Expected: FAIL — currently wraps all errors with `.Build()`.

- [ ] **Step 3: Add standard errors import alias**

The securefs package imports `"github.com/tphakala/birdnet-go/internal/errors"`. Add the standard library alias. In `securefs.go` imports (line 1-17), add:

```go
stdErrors "errors"
```

- [ ] **Step 4: Modify ServeRelativeFile to skip .Build() for ErrNotExist**

At line 670-672, replace:

```go
return nil, validatedRelPath, errors.New(err).Component(componentSecurefs).Category(errors.CategoryFileIO).Context("operation", "serve_relative_file_open").Build()
```

With:

```go
if stdErrors.Is(err, fs.ErrNotExist) {
	return nil, validatedRelPath, fmt.Errorf("openat %s: %w", validatedRelPath, err)
}
return nil, validatedRelPath, errors.New(err).Component(componentSecurefs).Category(errors.CategoryFileIO).Context("operation", "serve_relative_file_open").Build()
```

- [ ] **Step 5: Apply the same change to ServeFile**

At line 647-649, replace:

```go
return nil, relPath, errors.New(err).Component(componentSecurefs).Category(errors.CategoryFileIO).Context("operation", "serve_file_open").Build()
```

With:

```go
if stdErrors.Is(err, fs.ErrNotExist) {
	return nil, relPath, fmt.Errorf("openat %s: %w", relPath, err)
}
return nil, relPath, errors.New(err).Component(componentSecurefs).Category(errors.CategoryFileIO).Context("operation", "serve_file_open").Build()
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -race -count=1 ./internal/securefs/...`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/securefs/securefs.go internal/securefs/securefs_test.go
git commit -m "fix(securefs): suppress notification hooks for expected file-not-found

File-not-found in ServeFile/ServeRelativeFile is expected during the
race window between detection DB commit and audio export completion.
Use plain error wrapping to avoid triggering telemetry hooks and
creating noise notifications. The API layer handles 404s gracefully
via handleAudio404WithWait."
```

---

### Task 7: Implement ErrorBurstTracker

**Files:**
- Create: `internal/notification/burst_tracker.go`
- Create: `internal/notification/burst_tracker_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/notification/burst_tracker_test.go`:

```go
package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBurstTracker_FirstErrorAllowed(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	action := bt.Record("securefs", "file-io", "file not found")
	assert.Equal(t, BurstActionAllow, action)
}

func TestBurstTracker_BelowThresholdAllowed(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "error 1")
	bt.Record("securefs", "file-io", "error 2")
	action := bt.Record("securefs", "file-io", "error 3")
	assert.Equal(t, BurstActionAllow, action, "at threshold count should still allow")
}

func TestBurstTracker_SummaryAtThresholdPlusOne(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "error 1")
	bt.Record("securefs", "file-io", "error 2")
	bt.Record("securefs", "file-io", "error 3")
	action := bt.Record("securefs", "file-io", "error 4")
	assert.Equal(t, BurstActionSummary, action)
}

func TestBurstTracker_SuppressAfterSummary(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	for i := range 4 {
		bt.Record("securefs", "file-io", "error")
		_ = i
	}
	action := bt.Record("securefs", "file-io", "error 5")
	assert.Equal(t, BurstActionSuppress, action)
}

func TestBurstTracker_WindowReset(t *testing.T) {
	bt := NewErrorBurstTracker(3, 100*time.Millisecond)
	for range 4 {
		bt.Record("securefs", "file-io", "error")
	}
	// Summary was sent. Wait for window to expire.
	time.Sleep(150 * time.Millisecond)

	// After window expires, next error should be allowed again.
	action := bt.Record("securefs", "file-io", "new error")
	assert.Equal(t, BurstActionAllow, action)
}

func TestBurstTracker_DifferentKeysIndependent(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	for range 4 {
		bt.Record("securefs", "file-io", "error")
	}
	// securefs:file-io is now in summary/suppress mode

	// Different component — should be allowed
	action := bt.Record("mqtt", "connection", "broker unreachable")
	assert.Equal(t, BurstActionAllow, action)
}

func TestBurstTracker_GetSummary(t *testing.T) {
	bt := NewErrorBurstTracker(3, 5*time.Minute)
	bt.Record("securefs", "file-io", "first error msg")
	bt.Record("securefs", "file-io", "second error")
	bt.Record("securefs", "file-io", "third error")
	bt.Record("securefs", "file-io", "fourth error")

	summary := bt.GetSummary("securefs", "file-io")
	assert.Equal(t, 4, summary.Count)
	assert.Equal(t, "first error msg", summary.SampleError)
	assert.Equal(t, "securefs", summary.Component)
	assert.Equal(t, "file-io", summary.Category)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 -run 'TestBurstTracker' ./internal/notification/...`
Expected: FAIL — BurstTracker doesn't exist yet.

- [ ] **Step 3: Implement ErrorBurstTracker**

Create `internal/notification/burst_tracker.go`:

```go
package notification

import (
	"sync"
	"time"
)

// BurstAction indicates what the caller should do with the current error.
type BurstAction int

const (
	// BurstActionAllow means create a normal individual notification.
	BurstActionAllow BurstAction = iota
	// BurstActionSummary means create a summary notification (threshold+1 reached).
	BurstActionSummary
	// BurstActionSuppress means don't create a notification (summary already sent).
	BurstActionSuppress
)

// BurstSummary holds information about a burst for summary notification rendering.
type BurstSummary struct {
	Component   string
	Category    string
	Count       int
	SampleError string
	WindowMin   int
}

// ErrorBurstTracker groups repeated errors from the same component+category
// within a sliding window. It prevents notification spam by collapsing bursts
// into a single summary notification.
type ErrorBurstTracker struct {
	mu        sync.Mutex
	buckets   map[string]*burstBucket
	threshold int
	window    time.Duration
}

type burstBucket struct {
	count     int
	firstSeen time.Time
	lastSeen  time.Time
	sample    string // first error message in the window
	notified  bool   // summary notification was sent
}

// NewErrorBurstTracker creates a tracker with the given burst threshold and
// window duration. Errors from the same component+category within the window
// are grouped. The first `threshold` errors pass through individually. At
// threshold+1, a summary notification is created. Subsequent errors in the
// window are suppressed.
func NewErrorBurstTracker(threshold int, window time.Duration) *ErrorBurstTracker {
	return &ErrorBurstTracker{
		buckets:   make(map[string]*burstBucket),
		threshold: threshold,
		window:    window,
	}
}

// Record records an error occurrence and returns the action the caller should
// take: allow (create individual notification), summary (create grouped
// notification), or suppress (skip notification).
func (t *ErrorBurstTracker) Record(component, category, errMsg string) BurstAction {
	key := component + ":" + category

	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	bucket, exists := t.buckets[key]

	// Expired or new bucket — reset.
	if !exists || now.Sub(bucket.firstSeen) > t.window {
		t.buckets[key] = &burstBucket{
			count:     1,
			firstSeen: now,
			lastSeen:  now,
			sample:    errMsg,
		}
		return BurstActionAllow
	}

	bucket.count++
	bucket.lastSeen = now

	if bucket.count <= t.threshold {
		return BurstActionAllow
	}

	if !bucket.notified {
		bucket.notified = true
		return BurstActionSummary
	}

	return BurstActionSuppress
}

// GetSummary returns burst information for rendering a summary notification.
// Returns nil if no bucket exists for the given component+category.
func (t *ErrorBurstTracker) GetSummary(component, category string) *BurstSummary {
	key := component + ":" + category

	t.mu.Lock()
	defer t.mu.Unlock()

	bucket, exists := t.buckets[key]
	if !exists {
		return nil
	}

	return &BurstSummary{
		Component:   component,
		Category:    category,
		Count:       bucket.count,
		SampleError: bucket.sample,
		WindowMin:   int(t.window.Minutes()),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race -count=1 -run 'TestBurstTracker' ./internal/notification/...`
Expected: All 7 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notification/burst_tracker.go internal/notification/burst_tracker_test.go
git commit -m "feat(notification): add ErrorBurstTracker for grouping repeated errors

Groups errors from the same component+category within a tumbling
window. First N errors pass through individually. At threshold+1,
a summary notification fires. Subsequent errors in the window are
suppressed."
```

---

### Task 8: Integrate ErrorBurstTracker into the error notification hook

**Files:**
- Modify: `internal/notification/error_integration.go`
- Modify: `internal/notification/message_keys.go`
- Add i18n keys: `frontend/static/messages/en.json` (and other locale files)

- [ ] **Step 1: Add i18n message key constants**

In `internal/notification/message_keys.go`, add after `MsgBufferOverloadMessage` (line 157):

```go
// Error burst grouping notifications
MsgErrorBurstTitle   = "notifications.content.error.burstTitle"
MsgErrorBurstMessage = "notifications.content.error.burstMessage"
```

- [ ] **Step 2: Add English i18n strings**

In `frontend/static/messages/en.json`, find the `notifications.content.error` section and add:

```json
"burstTitle": "Multiple {component} errors",
"burstMessage": "{count} errors in the last {window_minutes} minutes: {sample_error}"
```

- [ ] **Step 3: Add placeholder strings to other locale files**

For each locale file (`de.json`, `es.json`, `fi.json`, `fr.json`, `it.json`, `nl.json`, `pl.json`, `pt.json`, `sk.json`, `da.json`, `lv.json`, `sv.json`), add the same English strings as placeholders in the `notifications.content.error` section. Native speakers can translate later.

- [ ] **Step 4: Integrate burst tracker into errorNotificationHook**

Modify `internal/notification/error_integration.go`:

```go
package notification

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	burstThreshold = 3
	burstWindow    = 5 * time.Minute
)

// globalBurstTracker is the singleton burst tracker for the error hook.
var globalBurstTracker = NewErrorBurstTracker(burstThreshold, burstWindow)

// errorNotificationHook is called when errors are reported
func errorNotificationHook(ee any) {
	enhancedErr, ok := ee.(*errors.EnhancedError)
	if !ok {
		return
	}

	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	category := enhancedErr.GetCategory()
	priority := getNotificationPriority(category, enhancedErr.GetPriority())

	if priority == PriorityLow {
		return
	}

	component := enhancedErr.GetComponent()
	errMsg := enhancedErr.Error()

	// Check burst tracker before creating notification.
	action := globalBurstTracker.Record(component, category, errMsg)
	switch action {
	case BurstActionAllow:
		_, _ = service.CreateErrorNotification(enhancedErr)
	case BurstActionSummary:
		summary := globalBurstTracker.GetSummary(component, category)
		if summary != nil {
			createBurstSummaryNotification(service, summary, priority)
		}
	case BurstActionSuppress:
		// Don't create notification — summary already sent.
	}
}

// createBurstSummaryNotification creates a single summary notification for a burst of errors.
func createBurstSummaryNotification(service *Service, summary *BurstSummary, priority Priority) {
	title := fmt.Sprintf("Multiple %s errors", summary.Component)
	message := fmt.Sprintf("%d errors in the last %d minutes: %s",
		summary.Count, summary.WindowMin, summary.SampleError)

	notif := NewNotification(TypeError, priority, title, message).
		WithComponent(summary.Component).
		WithTitleKey(MsgErrorBurstTitle, map[string]any{
			"component": summary.Component,
		}).
		WithMessageKey(MsgErrorBurstMessage, map[string]any{
			"component":      summary.Component,
			"category":       summary.Category,
			"count":          summary.Count,
			"window_minutes": summary.WindowMin,
			"sample_error":   summary.SampleError,
		})

	_ = service.CreateWithMetadata(notif)
}

// getNotificationPriority determines the notification priority based on error category and explicit priority
func getNotificationPriority(category, explicitPriority string) Priority {
	// (keep existing implementation unchanged)
```

Keep the existing `getNotificationPriority` and `SetupErrorIntegration` functions unchanged.

- [ ] **Step 5: Run notification tests**

Run: `go test -race -count=1 ./internal/notification/...`
Expected: All tests PASS.

- [ ] **Step 6: Run linter**

Run: `golangci-lint run -v ./internal/notification/...`
Expected: No errors.

- [ ] **Step 7: Commit**

```bash
git add internal/notification/error_integration.go internal/notification/message_keys.go internal/notification/burst_tracker.go internal/notification/burst_tracker_test.go frontend/static/messages/*.json
git commit -m "feat(notification): integrate error burst grouping into notification hook

Repeated errors from the same component+category are now grouped.
First 3 errors pass through individually. At the 4th, a summary
notification replaces further individual notifications until the
5-minute window expires."
```

---

### Task 9: Final integration test and lint

**Files:**
- No new files — verification only

- [ ] **Step 1: Run full test suite for affected packages**

Run: `go test -race -count=1 ./internal/alerting/... ./internal/notification/... ./internal/securefs/...`
Expected: All tests PASS.

- [ ] **Step 2: Run linter on full project**

Run: `golangci-lint run -v`
Expected: No new errors introduced.

- [ ] **Step 3: Run frontend check**

Run: `cd frontend && npm run check:all`
Expected: No errors (i18n JSON files are valid).

- [ ] **Step 4: Commit any fixes if needed**

If linter or tests found issues, fix and commit.

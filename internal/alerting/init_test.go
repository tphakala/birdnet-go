package alerting

import (
	"bytes"
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// initMockRepo implements repository.AlertRuleRepository for initialization tests.
type initMockRepo struct {
	rules   []entities.AlertRule
	history []entities.AlertHistory
	// updated holds the id of each UpdateRule call that stored a rule.
	updated []uint
	// updateErrs makes UpdateRule fail for the listed rule ids.
	updateErrs map[uint]error
}

// ListRules returns a shallow copy, so assigning a field of a returned rule
// reaches the stored rules only through UpdateRule, as it does with a real
// repository. Slices inside a returned rule are shared with the stored rule.
func (m *initMockRepo) ListRules(_ context.Context, _ repository.AlertRuleFilter) ([]entities.AlertRule, error) {
	return slices.Clone(m.rules), nil
}

func (m *initMockRepo) GetRule(_ context.Context, id uint) (*entities.AlertRule, error) {
	for i := range m.rules {
		if m.rules[i].ID == id {
			return &m.rules[i], nil
		}
	}
	return &entities.AlertRule{}, repository.ErrAlertRuleNotFound
}

func (m *initMockRepo) CreateRule(_ context.Context, rule *entities.AlertRule) error {
	rule.ID = uint(len(m.rules)) + 1 //nolint:gosec // test mock, no overflow risk
	m.rules = append(m.rules, *rule)
	return nil
}

func (m *initMockRepo) UpdateRule(_ context.Context, rule *entities.AlertRule) error {
	if err, ok := m.updateErrs[rule.ID]; ok {
		return err
	}
	for i := range m.rules {
		if m.rules[i].ID == rule.ID {
			m.rules[i] = *rule
			m.updated = append(m.updated, rule.ID)
			return nil
		}
	}
	return nil
}
func (m *initMockRepo) DeleteRule(_ context.Context, _ uint) error         { return nil }
func (m *initMockRepo) ToggleRule(_ context.Context, _ uint, _ bool) error { return nil }

func (m *initMockRepo) GetEnabledRules(_ context.Context) ([]entities.AlertRule, error) {
	var enabled []entities.AlertRule
	for i := range m.rules {
		if m.rules[i].Enabled {
			enabled = append(enabled, m.rules[i])
		}
	}
	return enabled, nil
}

func (m *initMockRepo) DeleteBuiltInRules(_ context.Context) (int64, error) { return 0, nil }
func (m *initMockRepo) SaveHistory(_ context.Context, h *entities.AlertHistory) error {
	m.history = append(m.history, *h)
	return nil
}
func (m *initMockRepo) ListHistory(_ context.Context, _ repository.AlertHistoryFilter) ([]entities.AlertHistory, int64, error) {
	return m.history, int64(len(m.history)), nil
}
func (m *initMockRepo) DeleteHistory(_ context.Context) (int64, error) { return 0, nil }
func (m *initMockRepo) DeleteHistoryBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *initMockRepo) CountRulesByName(_ context.Context, _ string) (int64, error) { return 0, nil }

func initTestLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil)
}

func TestSeedDefaultRules_EmptyRepo(t *testing.T) {
	repo := &initMockRepo{}
	err := seedDefaultRules(t.Context(), repo, initTestLogger())
	require.NoError(t, err)

	expectedCount := len(DefaultRules())
	assert.Len(t, repo.rules, expectedCount)
}

func TestSeedDefaultRules_AlreadySeeded(t *testing.T) {
	// Pre-populate with all default rules so seeding finds them by name
	defaults := DefaultRules()
	existing := make([]entities.AlertRule, len(defaults))
	for i := range defaults {
		existing[i] = defaults[i]
		existing[i].ID = uint(i + 1) //nolint:gosec // test, no overflow risk
	}
	repo := &initMockRepo{rules: existing}
	err := seedDefaultRules(t.Context(), repo, initTestLogger())
	require.NoError(t, err)

	// Should not add any new rules since all defaults already exist
	assert.Len(t, repo.rules, len(defaults))
}

func TestInitialize_SeedsAndCreatesEngine(t *testing.T) {
	repo := &initMockRepo{}
	bus := NewAlertEventBus(nil)

	engine, err := Initialize(repo, bus, initTestLogger(), nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	// Defaults should have been seeded
	expectedCount := len(DefaultRules())
	assert.Len(t, repo.rules, expectedCount)
}

func TestInitialize_SeedsOnlyMissingDefaults(t *testing.T) {
	repo := &initMockRepo{
		rules: []entities.AlertRule{
			{ID: 1, Name: "Custom Rule", Enabled: true},
		},
	}
	bus := NewAlertEventBus(nil)

	engine, err := Initialize(repo, bus, initTestLogger(), nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	// Custom rule kept + all defaults seeded (none matched by name)
	assert.Len(t, repo.rules, 1+len(DefaultRules()))
}

func TestInitialize_SubscribesToEventBus(t *testing.T) {
	repo := &initMockRepo{}
	bus := NewAlertEventBus(nil)

	_, err := Initialize(repo, bus, initTestLogger(), nil)
	require.NoError(t, err)

	// The bus should have at least one handler registered
	bus.mu.RLock()
	handlerCount := len(bus.handlers)
	bus.mu.RUnlock()
	assert.Equal(t, 1, handlerCount)
}

// lowDiskDefaultSteps are the escalation steps of the shipped "Low disk space" rule.
var lowDiskDefaultSteps = []float64{85, 90, 95, 99}

// customEscalationSteps are user-chosen steps that differ from the defaults.
var customEscalationSteps = []float64{80, 90}

// lowDiskShortenedSustainSec is a sustained duration, in seconds, shorter than
// the shipped condition's.
const lowDiskShortenedSustainSec = 60

const (
	// lowDiskLoweredThreshold is a threshold below the shipped "> 85" condition.
	lowDiskLoweredThreshold = "80"
	// lowDiskCustomCooldownSec is a cooldown, in seconds, different from the shipped one.
	lowDiskCustomCooldownSec = 120
	// unmatchedNameKey is a name key that no default rule carries.
	unmatchedNameKey = "alerts.rules.unmatched.name"
	// lowDiskTestUsage is a disk usage percentage above the first escalation step.
	lowDiskTestUsage = 86.0
	// highUsageTestValue is a CPU or memory percentage just above the shipped
	// "> 90" condition.
	highUsageTestValue = 91.0
	// lowDiskSustainWindow is the sustained duration of the shipped condition.
	lowDiskSustainWindow = 300 * time.Second
	// lowDiskCooldownElapsed is longer than the shipped 1800 s cooldown.
	lowDiskCooldownElapsed = 31 * time.Minute
	// updateFailureMessage is the cause of a failed rule write in the tests.
	updateFailureMessage = "rule store unavailable"
	// migratedLogMessage is logged for each rule the migration wrote.
	migratedLogMessage = "migrated escalation steps for built-in rule"
	// failedLogMessage is logged for each rule the migration could not write.
	failedLogMessage = "failed to migrate escalation steps for built-in rule"
)

// legacyRow wraps change so the row also looks like one seeded before name keys
// existed: no NameKey and no DescriptionKey.
func legacyRow(change func(rule *entities.AlertRule)) func(rule *entities.AlertRule) {
	return func(r *entities.AlertRule) {
		r.NameKey = ""
		r.DescriptionKey = ""
		change(r)
	}
}

// seedRulesWithoutSteps returns a repo holding every default rule, with the
// rule that has the given name key stripped of its escalation steps (the
// pre-migration state) and mutate applied to it, together with that row's ID.
// The other rows keep their default steps, so the target is the only row the
// migration can write.
func seedRulesWithoutSteps(t *testing.T, nameKey string, mutate func(rule *entities.AlertRule)) (repo *initMockRepo, ruleID uint) {
	t.Helper()
	existing := DefaultRules()
	for i := range existing {
		existing[i].ID = uint(i + 1) //nolint:gosec // test, no overflow risk
		if existing[i].NameKey == nameKey {
			ruleID = existing[i].ID
			existing[i].EscalationSteps = nil
			mutate(&existing[i])
		}
	}
	require.NotZero(t, ruleID, "default rule %q not found", nameKey)
	return &initMockRepo{rules: existing}, ruleID
}

// seedRulesWithLowDisk is seedRulesWithoutSteps for the "Low disk space" row.
func seedRulesWithLowDisk(t *testing.T, mutate func(rule *entities.AlertRule)) (repo *initMockRepo, lowDiskID uint) {
	t.Helper()
	return seedRulesWithoutSteps(t, RuleKeyLowDiskName, mutate)
}

func TestSeedDefaultRules_EscalationStepMigration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(rule *entities.AlertRule)
		want   []float64
		// wantWritten is whether the low disk row is written back.
		wantWritten bool
	}{
		{
			name:        "keyed built-in without steps gets default steps",
			mutate:      func(_ *entities.AlertRule) {},
			want:        lowDiskDefaultSteps,
			wantWritten: true,
		},
		{
			// Rows seeded before name keys existed have an empty NameKey.
			name:        "legacy built-in without name key gets default steps",
			mutate:      legacyRow(func(_ *entities.AlertRule) {}),
			want:        lowDiskDefaultSteps,
			wantWritten: true,
		},
		{
			name:   "user-created rule sharing the built-in name is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.BuiltIn = false }),
		},
		{
			// A rule with steps fires only at or above its lowest step, so default
			// steps on a lowered threshold would suppress the samples below them.
			name: "keyed built-in with changed threshold is untouched",
			mutate: func(r *entities.AlertRule) {
				r.Conditions[0].Value = lowDiskLoweredThreshold
			},
		},
		{
			name: "built-in with an unknown name key is untouched",
			mutate: func(r *entities.AlertRule) {
				r.NameKey = unmatchedNameKey
			},
		},
		{
			name:   "legacy built-in with changed threshold is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.Conditions[0].Value = lowDiskLoweredThreshold }),
		},
		{
			name:   "legacy built-in with changed metric is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.MetricName = MetricCPUUsage }),
		},
		{
			name:   "legacy built-in with changed condition property is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.Conditions[0].Property = PropertyPath }),
		},
		{
			name:   "legacy built-in with changed operator is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.Conditions[0].Operator = OperatorLessThan }),
		},
		{
			name:   "legacy built-in with changed sustained duration is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.Conditions[0].DurationSec = lowDiskShortenedSustainSec }),
		},
		{
			name:   "legacy built-in without its condition is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.Conditions = nil }),
		},
		{
			name: "legacy built-in with an added condition is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) {
				r.Conditions = append(r.Conditions, entities.AlertCondition{
					Property: PropertyPath, Operator: OperatorIs, Value: "/",
				})
			}),
		},
		{
			name:   "legacy built-in with changed object type is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.ObjectType = ObjectTypeStream }),
		},
		{
			name:   "legacy built-in with changed trigger type is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.TriggerType = TriggerTypeEvent }),
		},
		{
			name:   "legacy built-in with an event name is untouched",
			mutate: legacyRow(func(r *entities.AlertRule) { r.EventName = EventStreamDisconnected }),
		},
		{
			name:   "existing steps are kept",
			mutate: legacyRow(func(r *entities.AlertRule) { r.EscalationSteps = slices.Clone(customEscalationSteps) }),
			want:   customEscalationSteps,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, id := seedRulesWithLowDisk(t, tt.mutate)
			ruleCount := len(repo.rules)

			require.NoError(t, seedDefaultRules(t.Context(), repo, initTestLogger()))

			// seedRulesWithLowDisk leaves every other row with its default steps,
			// so the low disk row is the only row that can be written back.
			var wantUpdated []uint
			if tt.wantWritten {
				wantUpdated = []uint{id}
			}

			assert.Len(t, repo.rules, ruleCount, "no rule should be added or removed")
			assert.Equal(t, wantUpdated, repo.updated)
			rule, err := repo.GetRule(t.Context(), id)
			require.NoError(t, err)
			assert.Equal(t, tt.want, rule.EscalationSteps)

			// A second startup finds the steps in place and changes nothing.
			require.NoError(t, seedDefaultRules(t.Context(), repo, initTestLogger()))
			assert.Len(t, repo.rules, ruleCount, "a second startup should not add or remove a rule")
			assert.Equal(t, wantUpdated, repo.updated, "a second startup should not write a rule")
		})
	}
}

func TestSeedDefaultRules_EscalationStepMigrationKeepsUserSettings(t *testing.T) {
	// Settings outside the trigger do not decide whether the default steps
	// apply, and the write that adds them leaves those settings as the user set
	// them.
	repo, id := seedRulesWithLowDisk(t, func(r *entities.AlertRule) {
		r.CooldownSec = lowDiskCustomCooldownSec
		r.Enabled = false
		r.Actions = []entities.AlertAction{{Target: TargetPush}}
	})

	require.NoError(t, seedDefaultRules(t.Context(), repo, initTestLogger()))

	rule, err := repo.GetRule(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, lowDiskDefaultSteps, rule.EscalationSteps)
	assert.Equal(t, lowDiskCustomCooldownSec, rule.CooldownSec)
	assert.False(t, rule.Enabled)
	require.Len(t, rule.Actions, 1)
	assert.Equal(t, TargetPush, rule.Actions[0].Target)
}

func TestSeedDefaultRules_FailedUpdateDoesNotStopMigration(t *testing.T) {
	// Two stored rows match the low disk default, one keyed and one legacy. The
	// write for the first fails; the second must still be migrated.
	repo, failedID := seedRulesWithLowDisk(t, func(_ *entities.AlertRule) {})
	second, _ := seedRulesWithLowDisk(t, legacyRow(func(_ *entities.AlertRule) {}))
	secondRule := second.rules[failedID-1]
	secondRule.ID = uint(len(repo.rules)) + 1 //nolint:gosec // test, no overflow risk
	repo.rules = append(repo.rules, secondRule)
	repo.updateErrs = map[uint]error{failedID: errors.NewStd(updateFailureMessage)}

	var logOutput bytes.Buffer
	log := logger.NewSlogLogger(&logOutput, logger.LogLevelInfo, nil)

	require.NoError(t, seedDefaultRules(t.Context(), repo, log))

	failed, err := repo.GetRule(t.Context(), failedID)
	require.NoError(t, err)
	assert.Empty(t, failed.EscalationSteps, "the row whose write failed is not stored with steps")
	migrated, err := repo.GetRule(t.Context(), secondRule.ID)
	require.NoError(t, err)
	assert.Equal(t, lowDiskDefaultSteps, migrated.EscalationSteps, "the next row is still migrated")
	assert.Equal(t, []uint{secondRule.ID}, repo.updated)
	var warning string
	for line := range strings.Lines(logOutput.String()) {
		if strings.Contains(line, failedLogMessage) {
			warning = line
		}
	}
	require.NotEmpty(t, warning, "the failed write is logged")
	assert.Contains(t, warning, failed.Name, "the warning names the rule")
	assert.Contains(t, warning, updateFailureMessage, "the warning carries the cause")
	assert.Equal(t, 1, strings.Count(logOutput.String(), migratedLogMessage), "only the written row is reported as migrated")
}

func TestSeedDefaultRules_MigratedRuleDoesNotRenotifySameStepAfterCooldown(t *testing.T) {
	// A stored system metric row without steps re-notified on every cooldown
	// expiry while usage stayed above the threshold. After migration the engine
	// must stay quiet at the same step, including for a sample just above the
	// rule's threshold.
	tests := []struct {
		name    string
		nameKey string
		mutate  func(rule *entities.AlertRule)
		metric  string
		usage   float64
		props   map[string]any
	}{
		{
			name:    "legacy low disk space",
			nameKey: RuleKeyLowDiskName,
			mutate:  legacyRow(func(_ *entities.AlertRule) {}),
			metric:  MetricDiskUsage,
			usage:   lowDiskTestUsage,
			props:   map[string]any{PropertyPath: "/"},
		},
		{
			name:    "keyed high CPU usage",
			nameKey: RuleKeyHighCPUName,
			mutate:  func(_ *entities.AlertRule) {},
			metric:  MetricCPUUsage,
			usage:   highUsageTestValue,
		},
		{
			name:    "keyed high memory usage",
			nameKey: RuleKeyHighMemoryName,
			mutate:  func(_ *entities.AlertRule) {},
			metric:  MetricMemoryUsage,
			usage:   highUsageTestValue,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, _ := seedRulesWithoutSteps(t, tt.nameKey, tt.mutate)
			require.NoError(t, seedDefaultRules(t.Context(), repo, initTestLogger()))

			var fired int
			engine := NewEngine(repo, func(rule *entities.AlertRule, _ *AlertEvent) {
				if rule.MetricName == tt.metric {
					fired++
				}
			}, initTestLogger(), nil)
			require.NoError(t, engine.RefreshRules(t.Context()))

			base := time.Now().Add(-2 * lowDiskSustainWindow)
			emit := func(offset time.Duration) {
				props := map[string]any{PropertyValue: tt.usage}
				maps.Copy(props, tt.props)
				engine.HandleEvent(&AlertEvent{
					ObjectType: ObjectTypeSystem,
					MetricName: tt.metric,
					Properties: props,
					Timestamp:  base.Add(offset),
				})
			}
			assertNoRenotifyAfterCooldown(t, engine, emit, &fired)
		})
	}
}

// assertNoRenotifyAfterCooldown feeds a sustained breach through emit, expects
// one notification, then expires every cooldown and expects the same step to
// stay quiet.
func assertNoRenotifyAfterCooldown(t *testing.T, engine *Engine, emit func(offset time.Duration), fired *int) {
	t.Helper()

	// Samples covering the sustained window, dense enough that the window
	// after the last emit below also starts within the grace period.
	emit(0)
	emit(lowDiskSustainWindow / 5)
	emit(lowDiskSustainWindow / 2)
	emit(lowDiskSustainWindow)
	require.Equal(t, 1, *fired, "sustained breach should notify once")

	// Expire the cooldown; the same step must not notify again.
	engine.cooldownsMu.Lock()
	for k := range engine.cooldowns {
		engine.cooldowns[k] = time.Now().Add(-lowDiskCooldownElapsed)
	}
	engine.cooldownsMu.Unlock()
	emit(lowDiskSustainWindow + lowDiskSustainWindow/10)
	assert.Equal(t, 1, *fired, "same step must not re-notify after the cooldown expires")
}

func TestEnrichFromEventProps_DetectionNotification(t *testing.T) {
	t.Parallel()
	props := map[string]any{
		PropertySpeciesName:        "Eurasian Blue Tit",
		PropertyScientificName:     "Cyanistes caeruleus",
		PropertyConfidence:         0.95,
		PropertyLocation:           "backyard",
		PropertyIsNewSpecies:       true,
		PropertyDaysSinceFirstSeen: 0,
		PropertyEventTimestamp:     time.Date(2026, 3, 21, 10, 30, 0, 0, time.UTC),
		PropertyEventMetadata: map[string]any{
			"note_id":    uint(42),
			"latitude":   60.1699,
			"longitude":  24.9384,
			"image_url":  "https://example.com/bird.jpg",
			"begin_time": time.Date(2026, 3, 21, 10, 30, 0, 0, time.UTC),
		},
	}

	notif := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, "Title", "Message")
	enriched := enrichFromEventProps(notif, notification.TypeDetection, props)

	// Verify top-level metadata
	assert.Equal(t, "Eurasian Blue Tit", enriched.Metadata["species"])
	assert.Equal(t, "Cyanistes caeruleus", enriched.Metadata["scientific_name"])
	assert.InDelta(t, 0.95, enriched.Metadata["confidence"], 1e-9)
	assert.Equal(t, "backyard", enriched.Metadata["location"])
	assert.Equal(t, true, enriched.Metadata["is_new_species"])
	assert.Equal(t, 0, enriched.Metadata["days_since_first_seen"])

	// Verify bg_* template data fields
	assert.Equal(t, "95", enriched.Metadata["bg_confidence_percent"])
	assert.InDelta(t, 60.1699, enriched.Metadata["bg_latitude"], 1e-9)
	assert.InDelta(t, 24.9384, enriched.Metadata["bg_longitude"], 1e-9)
	assert.Equal(t, "https://example.com/bird.jpg", enriched.Metadata["bg_image_url"])
	assert.Equal(t, "42", enriched.Metadata["bg_detection_id"])
	assert.Contains(t, enriched.Metadata["bg_detection_url"], "/ui/detections/42")
	assert.NotEmpty(t, enriched.Metadata["bg_detection_time"])
	assert.NotEmpty(t, enriched.Metadata["bg_detection_date"])

	// Verify note_id passthrough
	assert.Equal(t, uint(42), enriched.Metadata["note_id"])

	// Verify component
	assert.Equal(t, "detection", enriched.Component)
}

func TestEnrichFromEventProps_NonDetectionUnchanged(t *testing.T) {
	t.Parallel()
	props := map[string]any{
		PropertyStreamName: "backyard-cam",
		PropertyError:      "connection timeout",
	}

	notif := notification.NewNotification(notification.TypeWarning, notification.PriorityHigh, "Stream Down", "Error occurred")
	enriched := enrichFromEventProps(notif, notification.TypeWarning, props)

	// Non-detection notifications should not get detection metadata
	assert.Empty(t, enriched.Metadata, "non-detection notification should not have metadata added")
}

func TestEnrichFromEventProps_NilProps(t *testing.T) {
	t.Parallel()
	notif := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, "Title", "Message")
	enriched := enrichFromEventProps(notif, notification.TypeDetection, nil)

	// Nil props should return notification unchanged
	assert.Empty(t, enriched.Metadata)
}

func TestEnrichFromEventProps_FallbackImageURL(t *testing.T) {
	t.Parallel()
	props := map[string]any{
		PropertySpeciesName:    "Eurasian Blue Tit",
		PropertyScientificName: "Cyanistes caeruleus",
		PropertyConfidence:     0.9,
		// No event_metadata with image_url; should fall back to proxy URL
	}

	notif := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, "Title", "Message")
	enriched := enrichFromEventProps(notif, notification.TypeDetection, props)

	// Should use the proxy URL with encoded scientific name
	assert.Contains(t, enriched.Metadata["bg_image_url"], "/api/v2/media/species-image")
	assert.Contains(t, enriched.Metadata["bg_image_url"], "Cyanistes+caeruleus")
}

// validDetectionProps returns a props map suitable for renderDetectionTemplates tests.
func validDetectionProps() map[string]any {
	return map[string]any{
		PropertySpeciesName:    "Eurasian Blue Tit",
		PropertyScientificName: "Cyanistes caeruleus",
		PropertyConfidence:     0.95,
		PropertyEventTimestamp: time.Now(),
		PropertyLocation:       "backyard",
		PropertyIsNewSpecies:   true,
	}
}

func TestRenderDetectionTemplates_WithConfiguredTemplates(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.Notification.Templates.NewSpecies.Title = "New: {{.CommonName}}"
	settings.Notification.Templates.NewSpecies.Message = "{{.CommonName}} ({{.ScientificName}}) at {{.ConfidencePercent}}%"
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	title, message := renderDetectionTemplates(validDetectionProps())
	assert.Equal(t, "New: Eurasian Blue Tit", title)
	assert.Contains(t, message, "Eurasian Blue Tit")
	assert.Contains(t, message, "Cyanistes caeruleus")
	assert.Contains(t, message, "95%")
}

func TestRenderDetectionTemplates_DaysSinceLastSeen(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.Notification.Templates.NewSpecies.Message = "Returned after {{.DaysSinceLastSeen}} days"
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	props := validDetectionProps()
	props[PropertyDaysSinceLastSeen] = 20

	_, message := renderDetectionTemplates(props)
	assert.Equal(t, "Returned after 20 days", message)
}

func TestRenderDetectionTemplates_EmptyTemplates(t *testing.T) {
	settings := conftest.GetTestSettings()
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	title, message := renderDetectionTemplates(validDetectionProps())
	assert.Empty(t, title)
	assert.Empty(t, message)
}

func TestRenderDetectionTemplates_NilSettings(t *testing.T) {
	conftest.SetTestSettings(nil)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	title, message := renderDetectionTemplates(validDetectionProps())
	assert.Empty(t, title)
	assert.Empty(t, message)
}

func TestRenderDetectionTemplates_TitleOnlyTemplate(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.Notification.Templates.NewSpecies.Title = "Spotted: {{.CommonName}}"
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	title, message := renderDetectionTemplates(validDetectionProps())
	assert.Equal(t, "Spotted: Eurasian Blue Tit", title)
	assert.Empty(t, message)
}

func TestApplyDetectionTemplates_NonDetectionUnchanged(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.Notification.Templates.NewSpecies.Title = "Override: {{.CommonName}}"
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	title, message := applyDetectionTemplates(
		notification.TypeWarning, "Original Title", "Original Message", validDetectionProps(),
	)
	assert.Equal(t, "Original Title", title)
	assert.Equal(t, "Original Message", message)
}

func TestApplyDetectionTemplates_NonNewSpeciesUnchanged(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.Notification.Templates.NewSpecies.Title = "New: {{.CommonName}}"
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	props := validDetectionProps()
	props[PropertyIsNewSpecies] = false

	title, message := applyDetectionTemplates(
		notification.TypeDetection, "Original Title", "Original Message", props,
	)
	assert.Equal(t, "Original Title", title)
	assert.Equal(t, "Original Message", message)
}

func setupTestNotificationService(t *testing.T) *notification.Service {
	t.Helper()
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)
	return svc
}

func TestNotificationAdapter_DispatchTemplateBehavior(t *testing.T) {
	tests := []struct {
		name            string
		dispatch        func(a *notificationAdapter, props map[string]any) error
		expectedTitle   string
		expectedMessage string
	}{
		{
			name: "CreateAndBroadcast preserves custom template on new species",
			dispatch: func(a *notificationAdapter, props map[string]any) error {
				return a.CreateAndBroadcast(TargetPush, notification.TypeDetection, "Custom Title", "Custom Message", props)
			},
			expectedTitle:   "Custom Title",
			expectedMessage: "Custom Message",
		},
		{
			name: "CreateAndBroadcastTest preserves custom template on new species",
			dispatch: func(a *notificationAdapter, props map[string]any) error {
				return a.CreateAndBroadcastTest(TargetPush, notification.TypeDetection, "Custom Title", "Custom Message", props)
			},
			expectedTitle:   "Custom Title",
			expectedMessage: "Custom Message",
		},
		{
			name: "CreateAndBroadcastWithKeys applies global new species template",
			dispatch: func(a *notificationAdapter, props map[string]any) error {
				return a.CreateAndBroadcastWithKeys(
					TargetPush, notification.TypeDetection, "Fallback Title", "Fallback Message",
					"title.key", nil, "msg.key", nil, props,
				)
			},
			expectedTitle:   "Global: Eurasian Blue Tit",
			expectedMessage: "Global: Eurasian Blue Tit",
		},
		{
			name: "CreateAndBroadcastTestWithKeys passes fallback through unchanged",
			dispatch: func(a *notificationAdapter, props map[string]any) error {
				return a.CreateAndBroadcastTestWithKeys(
					TargetPush, notification.TypeDetection, "Fallback Title", "Fallback Message",
					"title.key", nil, "msg.key", nil, props,
				)
			},
			expectedTitle:   "Fallback Title",
			expectedMessage: "Fallback Message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := setupTestNotificationService(t)
			ch, _ := svc.Subscribe()
			defer svc.Unsubscribe(ch)

			settings := conftest.GetTestSettings()
			settings.Notification.Templates.NewSpecies.Title = "Global: {{.CommonName}}"
			settings.Notification.Templates.NewSpecies.Message = "Global: {{.CommonName}}"
			conftest.SetTestSettings(settings)
			t.Cleanup(func() { conftest.SetTestSettings(nil) })

			props := validDetectionProps()
			props[PropertyIsNewSpecies] = true

			adapter := &notificationAdapter{}
			err := tt.dispatch(adapter, props)
			require.NoError(t, err)

			select {
			case received := <-ch:
				assert.Equal(t, tt.expectedTitle, received.Title)
				assert.Equal(t, tt.expectedMessage, received.Message)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for notification")
			}
		})
	}
}

func TestSameThreshold(t *testing.T) {
	defaults := DefaultRules()
	i := slices.IndexFunc(defaults, func(rule entities.AlertRule) bool { return rule.NameKey == RuleKeyHighCPUName })
	require.GreaterOrEqual(t, i, 0, "the High CPU usage default exists")
	base := defaults[i]
	require.Len(t, base.Conditions, 1, "precondition: one threshold condition")

	// variant returns a copy of the base rule with change applied. The condition
	// slice is cloned so a change to it does not reach the base.
	variant := func(change func(rule *entities.AlertRule)) *entities.AlertRule {
		rule := base
		rule.Conditions = slices.Clone(base.Conditions)
		change(&rule)
		return &rule
	}

	tests := []struct {
		name   string
		change func(rule *entities.AlertRule)
		want   bool
	}{
		{"identical", func(*entities.AlertRule) {}, true},
		{"duration is not compared", func(r *entities.AlertRule) { r.Conditions[0].DurationSec++ }, true},
		{"condition ids and sort order are not compared", func(r *entities.AlertRule) {
			r.Conditions[0].ID++
			r.Conditions[0].SortOrder++
		}, true},
		{"cooldown, enabled and actions are not compared", func(r *entities.AlertRule) {
			r.CooldownSec++
			r.Enabled = !r.Enabled
			r.Actions = nil
		}, true},
		{"the same number written another way", func(r *entities.AlertRule) { r.Conditions[0].Value += ".0" }, true},
		{"object type", func(r *entities.AlertRule) { r.ObjectType += "x" }, false},
		{"trigger type", func(r *entities.AlertRule) { r.TriggerType += "x" }, false},
		{"metric name", func(r *entities.AlertRule) { r.MetricName += "x" }, false},
		{"event name", func(r *entities.AlertRule) { r.EventName += "x" }, false},
		{"condition property", func(r *entities.AlertRule) { r.Conditions[0].Property += "x" }, false},
		{"condition operator", func(r *entities.AlertRule) { r.Conditions[0].Operator += "x" }, false},
		{"condition value", func(r *entities.AlertRule) { r.Conditions[0].Value += "1" }, false},
		{"condition removed", func(r *entities.AlertRule) { r.Conditions = nil }, false},
		{"condition added", func(r *entities.AlertRule) { r.Conditions = append(r.Conditions, r.Conditions[0]) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SameThreshold(&base, variant(tt.change))

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSameThreshold_ConditionOrder(t *testing.T) {
	cond := func(value string, durationSec int) entities.AlertCondition {
		return entities.AlertCondition{Property: PropertyValue, Operator: OperatorGreaterThan, Value: value, DurationSec: durationSec}
	}
	rule := func(conditions ...entities.AlertCondition) *entities.AlertRule {
		return &entities.AlertRule{ObjectType: ObjectTypeSystem, TriggerType: TriggerTypeMetric, MetricName: MetricCPUUsage, Conditions: conditions}
	}

	tests := []struct {
		name          string
		a, b          *entities.AlertRule
		wantThreshold bool
		wantTrigger   bool
	}{
		{"same order", rule(cond("1", 5), cond("2", 6)), rule(cond("1", 5), cond("2", 6)), true, true},
		{"reordered", rule(cond("1", 5), cond("2", 6)), rule(cond("2", 6), cond("1", 5)), true, true},
		{"durations swapped between conditions", rule(cond("1", 5), cond("2", 6)), rule(cond("1", 6), cond("2", 5)), true, false},
		{"a duplicate does not match a distinct condition", rule(cond("1", 5), cond("1", 5)), rule(cond("1", 5), cond("2", 5)), false, false},
		{"one condition differs", rule(cond("1", 5), cond("2", 6)), rule(cond("1", 5), cond("3", 6)), false, false},
		{"different counts", rule(cond("1", 5)), rule(cond("1", 5), cond("1", 5)), false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantThreshold, SameThreshold(tt.a, tt.b), "SameThreshold")
			assert.Equal(t, tt.wantTrigger, sameTrigger(tt.a, tt.b), "sameTrigger")
		})
	}
}

func TestSameThreshold_NumbersEqualOnlyForNumericOperators(t *testing.T) {
	rule := func(operator, value string) *entities.AlertRule {
		return &entities.AlertRule{
			ObjectType: ObjectTypeSystem, TriggerType: TriggerTypeMetric, MetricName: MetricCPUUsage,
			Conditions: []entities.AlertCondition{{Property: PropertyValue, Operator: operator, Value: value}},
		}
	}

	for _, operator := range numericOperators {
		assert.True(t, SameThreshold(rule(operator, "90"), rule(operator, "90.0")), "%s compares numbers", operator)
	}
	assert.False(t, SameThreshold(rule(OperatorIs, "90"), rule(OperatorIs, "90.0")),
		"a string operator compares the text, so 90 and 90.0 differ")
}

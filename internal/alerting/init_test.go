package alerting

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// initMockRepo implements repository.AlertRuleRepository for initialization tests.
type initMockRepo struct {
	rules   []entities.AlertRule
	history []entities.AlertHistory
}

func (m *initMockRepo) ListRules(_ context.Context, _ repository.AlertRuleFilter) ([]entities.AlertRule, error) {
	return m.rules, nil
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
	for i := range m.rules {
		if m.rules[i].ID == rule.ID {
			m.rules[i] = *rule
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

func TestSeedDefaultRules_MigratesEscalationSteps(t *testing.T) {
	// Simulate existing installation: all default rules exist but the low disk
	// rule has no escalation steps (pre-migration state).
	defaults := DefaultRules()
	existing := make([]entities.AlertRule, len(defaults))
	copy(existing, defaults)
	for i := range existing {
		existing[i].ID = uint(i + 1)      //nolint:gosec // test, no overflow risk
		existing[i].EscalationSteps = nil // pre-migration: no steps
	}
	repo := &initMockRepo{rules: existing}

	err := seedDefaultRules(t.Context(), repo, initTestLogger())
	require.NoError(t, err)

	// Verify the low disk rule was updated with escalation steps.
	for i := range repo.rules {
		if repo.rules[i].NameKey == RuleKeyLowDiskName {
			assert.Equal(t, []float64{85, 90, 95, 99}, repo.rules[i].EscalationSteps,
				"existing low disk rule should get escalation steps from migration")
			return
		}
	}
	t.Fatal("low disk rule not found after seeding")
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
		// No event_metadata with image_url — should fall back to proxy URL
	}

	notif := notification.NewNotification(notification.TypeDetection, notification.PriorityHigh, "Title", "Message")
	enriched := enrichFromEventProps(notif, notification.TypeDetection, props)

	// Should use the proxy URL with encoded scientific name
	assert.Contains(t, enriched.Metadata["bg_image_url"], "/api/v2/media/species-image")
	assert.Contains(t, enriched.Metadata["bg_image_url"], "Cyanistes+caeruleus")
}

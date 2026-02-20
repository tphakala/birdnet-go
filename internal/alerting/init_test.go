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

func (m *initMockRepo) UpdateRule(_ context.Context, _ *entities.AlertRule) error { return nil }
func (m *initMockRepo) DeleteRule(_ context.Context, _ uint) error               { return nil }
func (m *initMockRepo) ToggleRule(_ context.Context, _ uint, _ bool) error        { return nil }

func (m *initMockRepo) GetEnabledRules(_ context.Context) ([]entities.AlertRule, error) {
	var enabled []entities.AlertRule
	for i := range m.rules {
		if m.rules[i].Enabled {
			enabled = append(enabled, m.rules[i])
		}
	}
	return enabled, nil
}

func (m *initMockRepo) DeleteBuiltInRules(_ context.Context) (int64, error)          { return 0, nil }
func (m *initMockRepo) SaveHistory(_ context.Context, h *entities.AlertHistory) error { m.history = append(m.history, *h); return nil }
func (m *initMockRepo) ListHistory(_ context.Context, _ repository.AlertHistoryFilter) ([]entities.AlertHistory, int64, error) {
	return m.history, int64(len(m.history)), nil
}
func (m *initMockRepo) DeleteHistory(_ context.Context) (int64, error)                         { return 0, nil }
func (m *initMockRepo) DeleteHistoryBefore(_ context.Context, _ time.Time) (int64, error)      { return 0, nil }
func (m *initMockRepo) CountRulesByName(_ context.Context, _ string) (int64, error)            { return 0, nil }

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
	bus := NewAlertEventBus()

	engine, err := Initialize(repo, bus, initTestLogger())
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
	bus := NewAlertEventBus()

	engine, err := Initialize(repo, bus, initTestLogger())
	require.NoError(t, err)
	require.NotNil(t, engine)

	// Custom rule kept + all defaults seeded (none matched by name)
	assert.Len(t, repo.rules, 1+len(DefaultRules()))
}

func TestInitialize_SubscribesToEventBus(t *testing.T) {
	repo := &initMockRepo{}
	bus := NewAlertEventBus()

	_, err := Initialize(repo, bus, initTestLogger())
	require.NoError(t, err)

	// The bus should have at least one handler registered
	bus.mu.RLock()
	handlerCount := len(bus.handlers)
	bus.mu.RUnlock()
	assert.Equal(t, 1, handlerCount)
}

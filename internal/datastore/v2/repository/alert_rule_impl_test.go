package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// setupAlertTestDB creates an in-memory SQLite database for alert rule tests.
// Uses shared-cache mode with a single connection to ensure all operations
// see the same in-memory database.
func setupAlertTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_foreign_keys=ON"), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err, "failed to open in-memory database")

	sqlDB, err := db.DB()
	require.NoError(t, err, "failed to get sql.DB")
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	err = db.AutoMigrate(
		&entities.AlertRule{},
		&entities.AlertCondition{},
		&entities.AlertAction{},
		&entities.AlertHistory{},
	)
	require.NoError(t, err, "failed to migrate alert tables")
	return db
}

// createTestRule creates a test alert rule with conditions and actions.
func createTestRule(t *testing.T, repo AlertRuleRepository, name, objectType, triggerType, eventName string) *entities.AlertRule {
	t.Helper()
	rule := &entities.AlertRule{
		Name:        name,
		Description: "test rule",
		Enabled:     true,
		BuiltIn:     false,
		ObjectType:  objectType,
		TriggerType: triggerType,
		EventName:   eventName,
		CooldownSec: 300,
		Conditions: []entities.AlertCondition{
			{Property: "species_name", Operator: "contains", Value: "Owl", SortOrder: 0},
		},
		Actions: []entities.AlertAction{
			{Target: "bell", SortOrder: 0},
		},
	}
	err := repo.CreateRule(t.Context(), rule)
	require.NoError(t, err)
	return rule
}

func TestAlertRuleRepository_CreateAndGet(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	rule := &entities.AlertRule{
		Name:        "Test Rule",
		Description: "A test rule",
		Enabled:     true,
		BuiltIn:     true,
		ObjectType:  "detection",
		TriggerType: "event",
		EventName:   "detection.new_species",
		CooldownSec: 60,
		Conditions: []entities.AlertCondition{
			{Property: "confidence", Operator: "greater_than", Value: "0.90", SortOrder: 0},
		},
		Actions: []entities.AlertAction{
			{Target: "bell", SortOrder: 0},
			{Target: "telegram-home", TemplateTitle: "New bird!", SortOrder: 1},
		},
	}

	err := repo.CreateRule(ctx, rule)
	require.NoError(t, err)
	assert.NotZero(t, rule.ID)

	got, err := repo.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, "Test Rule", got.Name)
	assert.Equal(t, "A test rule", got.Description)
	assert.True(t, got.Enabled)
	assert.True(t, got.BuiltIn)
	assert.Equal(t, "detection", got.ObjectType)
	assert.Equal(t, "event", got.TriggerType)
	assert.Equal(t, "detection.new_species", got.EventName)
	assert.Equal(t, 60, got.CooldownSec)
	assert.Len(t, got.Conditions, 1)
	assert.Equal(t, "confidence", got.Conditions[0].Property)
	assert.Equal(t, "greater_than", got.Conditions[0].Operator)
	assert.Equal(t, "0.90", got.Conditions[0].Value)
	assert.Len(t, got.Actions, 2)
	assert.Equal(t, "bell", got.Actions[0].Target)
	assert.Equal(t, "telegram-home", got.Actions[1].Target)
	assert.Equal(t, "New bird!", got.Actions[1].TemplateTitle)
}

func TestAlertRuleRepository_ListRules(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	// Create rules with different object types and states
	rule1 := &entities.AlertRule{Name: "Stream rule", Enabled: true, BuiltIn: true, ObjectType: "stream", TriggerType: "event", EventName: "stream.disconnected", CooldownSec: 300}
	rule2 := &entities.AlertRule{Name: "Detection rule", Enabled: true, BuiltIn: false, ObjectType: "detection", TriggerType: "event", EventName: "detection.new_species", CooldownSec: 60}
	rule3 := &entities.AlertRule{Name: "Disabled rule", Enabled: false, BuiltIn: true, ObjectType: "system", TriggerType: "metric", MetricName: "system.cpu_usage", CooldownSec: 900}

	for _, r := range []*entities.AlertRule{rule1, rule2, rule3} {
		require.NoError(t, repo.CreateRule(ctx, r))
	}

	t.Run("no filter returns all", func(t *testing.T) {
		rules, err := repo.ListRules(ctx, AlertRuleFilter{})
		require.NoError(t, err)
		assert.Len(t, rules, 3)
	})

	t.Run("filter by object type", func(t *testing.T) {
		rules, err := repo.ListRules(ctx, AlertRuleFilter{ObjectType: "stream"})
		require.NoError(t, err)
		assert.Len(t, rules, 1)
		assert.Equal(t, "Stream rule", rules[0].Name)
	})

	t.Run("filter by enabled", func(t *testing.T) {
		enabled := true
		rules, err := repo.ListRules(ctx, AlertRuleFilter{Enabled: &enabled})
		require.NoError(t, err)
		assert.Len(t, rules, 2)
	})

	t.Run("filter by built-in", func(t *testing.T) {
		builtIn := true
		rules, err := repo.ListRules(ctx, AlertRuleFilter{BuiltIn: &builtIn})
		require.NoError(t, err)
		assert.Len(t, rules, 2)
	})
}

func TestAlertRuleRepository_UpdateRule(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	rule := createTestRule(t, repo, "Original", "detection", "event", "detection.occurred")

	// Update name and replace conditions/actions
	rule.Name = "Updated"
	rule.Conditions = []entities.AlertCondition{
		{RuleID: rule.ID, Property: "confidence", Operator: "greater_than", Value: "0.95", SortOrder: 0},
		{RuleID: rule.ID, Property: "species_name", Operator: "is", Value: "Eagle", SortOrder: 1},
	}
	rule.Actions = []entities.AlertAction{
		{RuleID: rule.ID, Target: "discord", SortOrder: 0},
	}

	err := repo.UpdateRule(ctx, rule)
	require.NoError(t, err)

	got, err := repo.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.Name)
	assert.Len(t, got.Conditions, 2)
	assert.Equal(t, "confidence", got.Conditions[0].Property)
	assert.Len(t, got.Actions, 1)
	assert.Equal(t, "discord", got.Actions[0].Target)
}

func TestAlertRuleRepository_UpdateRule_WithExistingIDs(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	rule := createTestRule(t, repo, "IDTest", "detection", "event", "detection.occurred")

	// Simulate a GET→modify→PUT cycle where conditions have non-zero IDs
	got, err := repo.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Len(t, got.Conditions, 1)
	require.NotZero(t, got.Conditions[0].ID, "condition should have an ID after creation")

	// Modify the fetched rule (keeping existing condition IDs)
	got.Name = "Updated with IDs"
	got.Conditions = []entities.AlertCondition{
		{ID: got.Conditions[0].ID, RuleID: got.ID, Property: "confidence", Operator: "greater_than", Value: "0.80", SortOrder: 0},
	}
	got.Actions = []entities.AlertAction{
		{ID: got.Actions[0].ID, RuleID: got.ID, Target: "discord", SortOrder: 0},
	}

	err = repo.UpdateRule(ctx, got)
	require.NoError(t, err)

	updated, err := repo.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated with IDs", updated.Name)
	assert.Len(t, updated.Conditions, 1)
	assert.Equal(t, "confidence", updated.Conditions[0].Property)
	assert.Equal(t, "0.80", updated.Conditions[0].Value)
	assert.Len(t, updated.Actions, 1)
	assert.Equal(t, "discord", updated.Actions[0].Target)
}

func TestAlertRuleRepository_DeleteRule(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	rule := createTestRule(t, repo, "ToDelete", "stream", "event", "stream.error")

	err := repo.DeleteRule(ctx, rule.ID)
	require.NoError(t, err)

	_, err = repo.GetRule(ctx, rule.ID)
	require.ErrorIs(t, err, ErrAlertRuleNotFound)

	// Verify cascade deleted conditions and actions
	var condCount int64
	require.NoError(t, db.Model(&entities.AlertCondition{}).Where("rule_id = ?", rule.ID).Count(&condCount).Error)
	assert.Equal(t, int64(0), condCount)

	var actionCount int64
	require.NoError(t, db.Model(&entities.AlertAction{}).Where("rule_id = ?", rule.ID).Count(&actionCount).Error)
	assert.Equal(t, int64(0), actionCount)
}

func TestAlertRuleRepository_ToggleRule(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	rule := createTestRule(t, repo, "Toggle", "detection", "event", "detection.new_species")
	assert.True(t, rule.Enabled)

	err := repo.ToggleRule(ctx, rule.ID, false)
	require.NoError(t, err)

	got, err := repo.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)

	err = repo.ToggleRule(ctx, rule.ID, true)
	require.NoError(t, err)

	got, err = repo.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}

func TestAlertRuleRepository_History(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	rule := createTestRule(t, repo, "HistRule", "detection", "event", "detection.new_species")

	now := time.Now()
	for i := range 5 {
		h := &entities.AlertHistory{
			RuleID:    rule.ID,
			FiredAt:   now.Add(time.Duration(-i) * time.Hour),
			EventData: `{"species":"Robin"}`,
			Actions:   `[{"target":"bell"}]`,
		}
		require.NoError(t, repo.SaveHistory(ctx, h))
	}

	t.Run("list all history", func(t *testing.T) {
		items, total, err := repo.ListHistory(ctx, AlertHistoryFilter{})
		require.NoError(t, err)
		assert.Equal(t, int64(5), total)
		assert.Len(t, items, 5)
		// Should be ordered by fired_at DESC
		assert.True(t, items[0].FiredAt.After(items[1].FiredAt))
	})

	t.Run("list with pagination", func(t *testing.T) {
		items, total, err := repo.ListHistory(ctx, AlertHistoryFilter{Limit: 2, Offset: 1})
		require.NoError(t, err)
		assert.Equal(t, int64(5), total)
		assert.Len(t, items, 2)
	})

	t.Run("filter by rule ID", func(t *testing.T) {
		items, total, err := repo.ListHistory(ctx, AlertHistoryFilter{RuleID: rule.ID})
		require.NoError(t, err)
		assert.Equal(t, int64(5), total)
		assert.Len(t, items, 5)
	})

	t.Run("delete history before timestamp", func(t *testing.T) {
		cutoff := now.Add(-2 * time.Hour)
		deleted, err := repo.DeleteHistoryBefore(ctx, cutoff)
		require.NoError(t, err)
		assert.Equal(t, int64(2), deleted)

		_, remaining, err := repo.ListHistory(ctx, AlertHistoryFilter{})
		require.NoError(t, err)
		assert.Equal(t, int64(3), remaining)
	})

	t.Run("delete all history", func(t *testing.T) {
		deleted, err := repo.DeleteHistory(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(3), deleted)

		_, total, err := repo.ListHistory(ctx, AlertHistoryFilter{})
		require.NoError(t, err)
		assert.Equal(t, int64(0), total)
	})
}

func TestAlertRuleRepository_GetEnabledRules(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	// Create mix of enabled and disabled rules
	r1 := &entities.AlertRule{Name: "Enabled1", Enabled: true, ObjectType: "stream", TriggerType: "event", EventName: "stream.connected", CooldownSec: 60}
	r2 := &entities.AlertRule{Name: "Disabled1", Enabled: false, ObjectType: "stream", TriggerType: "event", EventName: "stream.disconnected", CooldownSec: 60}
	r3 := &entities.AlertRule{Name: "Enabled2", Enabled: true, ObjectType: "detection", TriggerType: "event", EventName: "detection.new_species", CooldownSec: 60}

	for _, r := range []*entities.AlertRule{r1, r2, r3} {
		require.NoError(t, repo.CreateRule(ctx, r))
	}

	rules, err := repo.GetEnabledRules(ctx)
	require.NoError(t, err)
	assert.Len(t, rules, 2)
	for _, r := range rules {
		assert.True(t, r.Enabled)
	}
}

func TestAlertRuleRepository_DeleteBuiltInRules(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	// Create mix of built-in and custom rules
	r1 := &entities.AlertRule{Name: "BuiltIn1", Enabled: true, BuiltIn: true, ObjectType: "stream", TriggerType: "event", EventName: "stream.disconnected", CooldownSec: 300}
	r2 := &entities.AlertRule{Name: "Custom1", Enabled: true, BuiltIn: false, ObjectType: "detection", TriggerType: "event", EventName: "detection.occurred", CooldownSec: 60}
	r3 := &entities.AlertRule{Name: "BuiltIn2", Enabled: true, BuiltIn: true, ObjectType: "system", TriggerType: "metric", MetricName: "system.cpu_usage", CooldownSec: 900}

	for _, r := range []*entities.AlertRule{r1, r2, r3} {
		require.NoError(t, repo.CreateRule(ctx, r))
	}

	deleted, err := repo.DeleteBuiltInRules(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	remaining, err := repo.ListRules(ctx, AlertRuleFilter{})
	require.NoError(t, err)
	assert.Len(t, remaining, 1)
	assert.Equal(t, "Custom1", remaining[0].Name)
}

func TestAlertRuleRepository_CountRulesByName(t *testing.T) {
	db := setupAlertTestDB(t)
	repo := NewAlertRuleRepository(db)
	ctx := t.Context()

	createTestRule(t, repo, "Duplicate Test", "detection", "event", "detection.new_species")
	createTestRule(t, repo, "Duplicate Test", "stream", "event", "stream.disconnected")
	createTestRule(t, repo, "Unique Rule", "system", "metric", "system.cpu_usage")

	count, err := repo.CountRulesByName(ctx, "Duplicate Test")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = repo.CountRulesByName(ctx, "Unique Rule")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = repo.CountRulesByName(ctx, "Nonexistent")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

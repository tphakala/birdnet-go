package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
)

// alertRuleRepository implements AlertRuleRepository.
type alertRuleRepository struct {
	db *gorm.DB
}

// NewAlertRuleRepository creates a new AlertRuleRepository.
func NewAlertRuleRepository(db *gorm.DB) AlertRuleRepository {
	return &alertRuleRepository{db: db}
}

// ListRules returns alert rules matching the given filter.
func (r *alertRuleRepository) ListRules(ctx context.Context, filter AlertRuleFilter) ([]entities.AlertRule, error) {
	var rules []entities.AlertRule
	query := r.db.WithContext(ctx).Preload("Conditions").Preload("Actions")

	if filter.ObjectType != "" {
		query = query.Where("object_type = ?", filter.ObjectType)
	}
	if filter.TriggerType != "" {
		query = query.Where("trigger_type = ?", filter.TriggerType)
	}
	if filter.Enabled != nil {
		query = query.Where("enabled = ?", *filter.Enabled)
	}
	if filter.BuiltIn != nil {
		query = query.Where("built_in = ?", *filter.BuiltIn)
	}

	if err := query.Order("id ASC").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("failed to list alert rules: %w", err)
	}
	return rules, nil
}

// GetRule returns a single alert rule by ID with its conditions and actions.
// Returns ErrAlertRuleNotFound if the rule does not exist.
func (r *alertRuleRepository) GetRule(ctx context.Context, id uint) (*entities.AlertRule, error) {
	var rule entities.AlertRule
	if err := r.db.WithContext(ctx).Preload("Conditions").Preload("Actions").First(&rule, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAlertRuleNotFound
		}
		return nil, fmt.Errorf("failed to get alert rule %d: %w", id, err)
	}
	return &rule, nil
}

// CreateRule creates a new alert rule with its conditions and actions.
func (r *alertRuleRepository) CreateRule(ctx context.Context, rule *entities.AlertRule) error {
	if err := r.db.WithContext(ctx).Create(rule).Error; err != nil {
		return fmt.Errorf("failed to create alert rule: %w", err)
	}
	return nil
}

// UpdateRule replaces an alert rule, deleting existing conditions and actions first.
func (r *alertRuleRepository) UpdateRule(ctx context.Context, rule *entities.AlertRule) error {
	if rule.ID == 0 {
		return fmt.Errorf("failed to update alert rule: missing rule ID")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("rule_id = ?", rule.ID).Delete(&entities.AlertCondition{}).Error; err != nil {
			return fmt.Errorf("failed to delete old conditions: %w", err)
		}
		if err := tx.Where("rule_id = ?", rule.ID).Delete(&entities.AlertAction{}).Error; err != nil {
			return fmt.Errorf("failed to delete old actions: %w", err)
		}
		// Zero out IDs so GORM inserts new rows instead of trying to update deleted ones
		for i := range rule.Conditions {
			rule.Conditions[i].ID = 0
		}
		for i := range rule.Actions {
			rule.Actions[i].ID = 0
		}
		if err := tx.Save(rule).Error; err != nil {
			return fmt.Errorf("failed to update alert rule: %w", err)
		}
		return nil
	})
}

// DeleteRule deletes an alert rule and its conditions/actions via cascade.
func (r *alertRuleRepository) DeleteRule(ctx context.Context, id uint) error {
	result := r.db.WithContext(ctx).Delete(&entities.AlertRule{}, id)
	if result.Error != nil {
		return fmt.Errorf("failed to delete alert rule %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrAlertRuleNotFound
	}
	return nil
}

// ToggleRule enables or disables an alert rule.
func (r *alertRuleRepository) ToggleRule(ctx context.Context, id uint, enabled bool) error {
	result := r.db.WithContext(ctx).Model(&entities.AlertRule{}).Where("id = ?", id).Update("enabled", enabled)
	if result.Error != nil {
		return fmt.Errorf("failed to toggle alert rule %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrAlertRuleNotFound
	}
	return nil
}

// GetEnabledRules returns all enabled alert rules with their conditions and actions.
func (r *alertRuleRepository) GetEnabledRules(ctx context.Context) ([]entities.AlertRule, error) {
	enabled := true
	return r.ListRules(ctx, AlertRuleFilter{Enabled: &enabled})
}

// DeleteBuiltInRules deletes all built-in alert rules.
func (r *alertRuleRepository) DeleteBuiltInRules(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Where("built_in = ?", true).Delete(&entities.AlertRule{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete built-in alert rules: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// SaveHistory saves an alert history entry.
func (r *alertRuleRepository) SaveHistory(ctx context.Context, history *entities.AlertHistory) error {
	if err := r.db.WithContext(ctx).Create(history).Error; err != nil {
		return fmt.Errorf("failed to save alert history: %w", err)
	}
	return nil
}

// ListHistory returns alert history entries matching the filter with pagination.
func (r *alertRuleRepository) ListHistory(ctx context.Context, filter AlertHistoryFilter) ([]entities.AlertHistory, int64, error) {
	var items []entities.AlertHistory
	var total int64

	countQuery := r.db.WithContext(ctx).Model(&entities.AlertHistory{})
	if filter.RuleID > 0 {
		countQuery = countQuery.Where("rule_id = ?", filter.RuleID)
	}
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count alert history: %w", err)
	}

	query := r.db.WithContext(ctx).Preload("Rule").Order("fired_at DESC")
	if filter.RuleID > 0 {
		query = query.Where("rule_id = ?", filter.RuleID)
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}
	if err := query.Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list alert history: %w", err)
	}
	return items, total, nil
}

// DeleteHistory deletes all alert history entries.
func (r *alertRuleRepository) DeleteHistory(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Where("1 = 1").Delete(&entities.AlertHistory{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete alert history: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// DeleteHistoryBefore deletes alert history entries older than the given time.
func (r *alertRuleRepository) DeleteHistoryBefore(ctx context.Context, before time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Where("fired_at < ?", before).Delete(&entities.AlertHistory{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete alert history before %v: %w", before, result.Error)
	}
	return result.RowsAffected, nil
}

// CountRulesByName returns the number of rules with the given name.
func (r *alertRuleRepository) CountRulesByName(ctx context.Context, name string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&entities.AlertRule{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count rules by name: %w", err)
	}
	return count, nil
}

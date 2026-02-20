package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// AlertRuleRepository handles alert rule CRUD and history operations.
type AlertRuleRepository interface {
	// Rule CRUD
	ListRules(ctx context.Context, filter AlertRuleFilter) ([]entities.AlertRule, error)
	GetRule(ctx context.Context, id uint) (*entities.AlertRule, error)
	CreateRule(ctx context.Context, rule *entities.AlertRule) error
	UpdateRule(ctx context.Context, rule *entities.AlertRule) error
	DeleteRule(ctx context.Context, id uint) error
	ToggleRule(ctx context.Context, id uint, enabled bool) error

	// Bulk operations
	GetEnabledRules(ctx context.Context) ([]entities.AlertRule, error)
	DeleteBuiltInRules(ctx context.Context) (int64, error)

	// History
	SaveHistory(ctx context.Context, history *entities.AlertHistory) error
	ListHistory(ctx context.Context, filter AlertHistoryFilter) ([]entities.AlertHistory, int64, error)
	DeleteHistory(ctx context.Context) (int64, error)
	DeleteHistoryBefore(ctx context.Context, before time.Time) (int64, error)

	// Import/Export
	CountRulesByName(ctx context.Context, name string) (int64, error)
}

// AlertRuleFilter controls rule listing queries.
type AlertRuleFilter struct {
	ObjectType  string
	TriggerType string
	Enabled     *bool
	BuiltIn     *bool
}

// AlertHistoryFilter controls history listing queries.
type AlertHistoryFilter struct {
	RuleID uint
	Limit  int
	Offset int
}

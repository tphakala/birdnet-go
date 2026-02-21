package entities

import "time"

// AlertRule defines a user-configurable alerting rule.
// Rules match events or metrics against conditions and dispatch actions.
type AlertRule struct {
	ID          uint             `gorm:"primaryKey" json:"id"`
	Name        string           `gorm:"size:255;not null" json:"name"`
	Description string           `gorm:"size:1000;default:''" json:"description"`
	Enabled     bool             `gorm:"not null;index" json:"enabled"`
	BuiltIn     bool             `gorm:"not null;default:false" json:"built_in"`
	ObjectType  string           `gorm:"size:50;not null;index" json:"object_type"`
	TriggerType string           `gorm:"size:10;not null" json:"trigger_type"`
	EventName   string           `gorm:"size:100;default:'';index" json:"event_name"`
	MetricName  string           `gorm:"size:100;default:''" json:"metric_name"`
	CooldownSec int              `gorm:"not null;default:300" json:"cooldown_sec"`
	CreatedAt   time.Time        `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time        `gorm:"autoUpdateTime" json:"updated_at"`
	Conditions  []AlertCondition `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE" json:"conditions"`
	Actions     []AlertAction    `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE" json:"actions"`
}

// TableName returns the table name for GORM.
func (AlertRule) TableName() string {
	return "alert_rules"
}

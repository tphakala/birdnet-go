package entities

import "time"

// AlertRule defines a user-configurable alerting rule.
// Rules match events or metrics against conditions and dispatch actions.
type AlertRule struct {
	ID          uint             `gorm:"primaryKey"`
	Name        string           `gorm:"size:255;not null"`
	Description string           `gorm:"size:1000;default:''"`
	Enabled     bool             `gorm:"not null;index"`
	BuiltIn     bool             `gorm:"not null;default:false"`
	ObjectType  string           `gorm:"size:50;not null;index"`
	TriggerType string           `gorm:"size:10;not null"`
	EventName   string           `gorm:"size:100;default:'';index"`
	MetricName  string           `gorm:"size:100;default:''"`
	CooldownSec int              `gorm:"not null;default:300"`
	CreatedAt   time.Time        `gorm:"autoCreateTime"`
	UpdatedAt   time.Time        `gorm:"autoUpdateTime"`
	Conditions  []AlertCondition `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE"`
	Actions     []AlertAction    `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE"`
}

// TableName returns the table name for GORM.
func (AlertRule) TableName() string {
	return "alert_rules"
}

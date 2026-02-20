package entities

import "time"

// AlertHistory records each time an alert rule fires.
type AlertHistory struct {
	ID        uint      `gorm:"primaryKey"`
	RuleID    uint      `gorm:"not null;index:idx_alert_history_rule_fired,priority:1"`
	FiredAt   time.Time `gorm:"not null;index:idx_alert_history_rule_fired,priority:2"`
	EventData string    `gorm:"type:text;default:''"`
	Actions   string    `gorm:"type:text;default:''"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	Rule      AlertRule `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE"`
}

// TableName returns the table name for GORM.
func (AlertHistory) TableName() string {
	return "alert_history"
}

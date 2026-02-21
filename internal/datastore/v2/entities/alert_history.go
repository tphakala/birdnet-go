package entities

import "time"

// AlertHistory records each time an alert rule fires.
type AlertHistory struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	RuleID    uint      `gorm:"not null;index:idx_alert_history_rule_fired,priority:1" json:"rule_id"`
	FiredAt   time.Time `gorm:"not null;index:idx_alert_history_rule_fired,priority:2" json:"fired_at"`
	EventData string    `gorm:"type:text;default:''" json:"event_data"`
	Actions   string    `gorm:"type:text;default:''" json:"actions"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	Rule      AlertRule `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE" json:"rule,omitzero"`
}

// TableName returns the table name for GORM.
func (AlertHistory) TableName() string {
	return "alert_history"
}

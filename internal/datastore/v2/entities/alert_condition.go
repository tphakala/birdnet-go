package entities

// AlertCondition defines a single condition within an alert rule.
// All conditions in a rule use AND logic.
type AlertCondition struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	RuleID      uint   `gorm:"not null;index" json:"rule_id"`
	Property    string `gorm:"size:100;not null" json:"property"`
	Operator    string `gorm:"size:20;not null" json:"operator"`
	Value       string `gorm:"size:500;not null" json:"value"`
	DurationSec int    `gorm:"default:0" json:"duration_sec"`
	SortOrder   int    `gorm:"default:0" json:"sort_order"`
}

// TableName returns the table name for GORM.
func (AlertCondition) TableName() string {
	return "alert_conditions"
}

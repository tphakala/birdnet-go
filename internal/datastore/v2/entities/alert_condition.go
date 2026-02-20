package entities

// AlertCondition defines a single condition within an alert rule.
// All conditions in a rule use AND logic.
type AlertCondition struct {
	ID          uint   `gorm:"primaryKey"`
	RuleID      uint   `gorm:"not null;index"`
	Property    string `gorm:"size:100;not null"`
	Operator    string `gorm:"size:20;not null"`
	Value       string `gorm:"size:500;not null"`
	DurationSec int    `gorm:"default:0"`
	SortOrder   int    `gorm:"default:0"`
}

// TableName returns the table name for GORM.
func (AlertCondition) TableName() string {
	return "alert_conditions"
}

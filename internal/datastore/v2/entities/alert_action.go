package entities

// AlertAction defines a notification target for an alert rule.
// Target is "bell" for web UI or a push provider name.
type AlertAction struct {
	ID              uint   `gorm:"primaryKey"`
	RuleID          uint   `gorm:"not null;index"`
	Target          string `gorm:"size:100;not null"`
	TemplateTitle   string `gorm:"size:500;default:''"`
	TemplateMessage string `gorm:"size:2000;default:''"`
	SortOrder       int    `gorm:"default:0"`
}

// TableName returns the table name for GORM.
func (AlertAction) TableName() string {
	return "alert_actions"
}

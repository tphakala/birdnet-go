package entities

// AlertAction defines a notification target for an alert rule.
// Target is "bell" for web UI or a push provider name.
type AlertAction struct {
	ID              uint   `gorm:"primaryKey" json:"id"`
	RuleID          uint   `gorm:"not null;index" json:"rule_id"`
	Target          string `gorm:"size:100;not null" json:"target"`
	TemplateTitle   string `gorm:"size:500;default:''" json:"template_title"`
	TemplateMessage string `gorm:"size:2000;default:''" json:"template_message"`
	SortOrder       int    `gorm:"default:0" json:"sort_order"`
}

// TableName returns the table name for GORM.
func (AlertAction) TableName() string {
	return "alert_actions"
}

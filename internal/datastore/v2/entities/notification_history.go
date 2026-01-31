package entities

import "time"

// NotificationHistory tracks notification suppression state.
// LabelID links to the species label for normalized storage.
type NotificationHistory struct {
	ID               uint      `gorm:"primaryKey"`
	LabelID          uint      `gorm:"uniqueIndex:idx_notification_label_type;not null"`
	NotificationType string    `gorm:"uniqueIndex:idx_notification_label_type;size:50;not null;default:new_species"`
	LastSent         time.Time `gorm:"index;not null"`
	ExpiresAt        time.Time `gorm:"index;not null"`
	CreatedAt        time.Time `gorm:"autoCreateTime"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime"`

	// Relationship
	Label *Label `gorm:"foreignKey:LabelID"`
}

// TableName returns the table name for GORM.
func (NotificationHistory) TableName() string {
	return "notification_histories"
}

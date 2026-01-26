package entities

import "time"

// NotificationHistory tracks notification suppression state.
// This mirrors the legacy notification_histories table structure.
type NotificationHistory struct {
	ID               uint      `gorm:"primaryKey"`
	ScientificName   string    `gorm:"uniqueIndex:idx_notification_species_type;size:200;not null"`
	NotificationType string    `gorm:"uniqueIndex:idx_notification_species_type;size:50;not null;default:new_species"`
	LastSent         time.Time `gorm:"index;not null"`
	ExpiresAt        time.Time `gorm:"index;not null"`
	CreatedAt        time.Time `gorm:"autoCreateTime"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime"`
}

// TableName returns the table name for GORM.
func (NotificationHistory) TableName() string {
	return "notification_histories"
}

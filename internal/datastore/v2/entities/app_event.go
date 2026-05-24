package entities

import "time"

// AppEvent records a significant application state change for diagnostics.
type AppEvent struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Timestamp time.Time `gorm:"not null;index:idx_app_events_timestamp"`
	Category  string    `gorm:"type:varchar(50);not null;index:idx_app_events_category"`
	EventType string    `gorm:"type:varchar(100);not null"`
	Message   string    `gorm:"type:text"`
	Metadata  string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

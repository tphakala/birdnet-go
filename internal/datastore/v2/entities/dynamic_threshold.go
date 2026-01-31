package entities

import "time"

// DynamicThreshold stores learned detection thresholds.
// LabelID links to the species label for normalized storage.
type DynamicThreshold struct {
	ID            uint      `gorm:"primaryKey"`
	LabelID       uint      `gorm:"uniqueIndex;not null"`
	Level         int       `gorm:"not null;default:0"`      // Adjustment level (0-3)
	CurrentValue  float64   `gorm:"not null"`                // Current threshold value
	BaseThreshold float64   `gorm:"not null"`                // Original base threshold for reference
	HighConfCount int       `gorm:"not null;default:0"`      // Count of high-confidence detections
	ValidHours    int       `gorm:"not null"`                // Hours until expiry
	ExpiresAt     time.Time `gorm:"index;not null"`          // When this threshold expires
	LastTriggered time.Time `gorm:"index;not null"`          // Last time threshold was triggered
	FirstCreated  time.Time `gorm:"not null"`                // When first created
	TriggerCount  int       `gorm:"not null;default:0"`      // Total number of times triggered
	CreatedAt     time.Time `gorm:"autoCreateTime"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime"`

	// Relationship
	Label *Label `gorm:"foreignKey:LabelID"`
}

// TableName returns the table name for GORM.
func (DynamicThreshold) TableName() string {
	return "dynamic_thresholds"
}

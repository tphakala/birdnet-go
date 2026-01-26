package entities

import "time"

// ThresholdEvent records threshold adjustment history.
// This mirrors the legacy threshold_events table structure.
type ThresholdEvent struct {
	ID            uint      `gorm:"primaryKey"`
	SpeciesName   string    `gorm:"index;size:200;not null"` // Common name (lowercase)
	PreviousLevel int       `gorm:"not null"`                // Level before change
	NewLevel      int       `gorm:"not null"`                // Level after change
	PreviousValue float64   `gorm:"not null"`                // Threshold value before change
	NewValue      float64   `gorm:"not null"`                // Threshold value after change
	ChangeReason  string    `gorm:"size:50;not null"`        // "high_confidence", "expiry", "manual_reset"
	Confidence    float64   `gorm:"default:0"`               // Detection confidence that triggered change
	CreatedAt     time.Time `gorm:"index;autoCreateTime"`
}

// TableName returns the table name for GORM.
func (ThresholdEvent) TableName() string {
	return "threshold_events"
}

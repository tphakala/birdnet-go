package entities

import "time"

// ThresholdEvent records threshold adjustment history.
//
// Keyed by SpeciesName (lowercase common name), model-independent (see #4195).
// ScientificName is display metadata only and may be empty; there is no
// model/label foreign key.
type ThresholdEvent struct {
	ID             uint      `gorm:"primaryKey"`
	SpeciesName    string    `gorm:"index;not null;size:200"` // Lowercase common name (identity key)
	ScientificName string    `gorm:"size:200"`                // Display metadata only; may be empty
	PreviousLevel  int       `gorm:"not null"`                // Level before change
	NewLevel       int       `gorm:"not null"`                // Level after change
	PreviousValue  float64   `gorm:"not null"`                // Threshold value before change
	NewValue       float64   `gorm:"not null"`                // Threshold value after change
	ChangeReason   string    `gorm:"size:50;not null"`        // "high_confidence", "expiry", "manual_reset"
	Confidence     float64   `gorm:"default:0"`               // Detection confidence that triggered change
	CreatedAt      time.Time `gorm:"index;autoCreateTime"`
}

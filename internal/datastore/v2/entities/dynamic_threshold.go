package entities

import "time"

// DynamicThreshold stores learned detection thresholds.
//
// Tracking is per species and model-independent (see #4173, #4195): the row is
// keyed by SpeciesName (lowercase common name), the same key the processor uses
// in memory. ScientificName is carried as display metadata only (thumbnails);
// it is not part of the identity and may be empty. There is deliberately no
// model/label foreign key: any model's high-confidence detection advances the
// same species-level state.
type DynamicThreshold struct {
	ID             uint      `gorm:"primaryKey"`
	SpeciesName    string    `gorm:"uniqueIndex;not null;size:200"` // Lowercase common name (identity key)
	ScientificName string    `gorm:"size:200"`                      // Display metadata only (thumbnails); may be empty
	Level          int       `gorm:"not null;default:0"`            // Adjustment level (0-3)
	CurrentValue   float64   `gorm:"not null"`                      // Current threshold value
	BaseThreshold  float64   `gorm:"not null"`                      // Original base threshold for reference
	HighConfCount  int       `gorm:"not null;default:0"`            // Count of high-confidence detections
	ValidHours     int       `gorm:"not null"`                      // Hours until expiry
	ExpiresAt      time.Time `gorm:"index;not null"`                // When this threshold expires
	LastTriggered  time.Time `gorm:"index;not null"`                // Last time threshold was triggered
	FirstCreated   time.Time `gorm:"not null"`                      // When first created
	TriggerCount   int       `gorm:"not null;default:0"`            // Total number of times triggered
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

package entities

// DetectionModelContribution records one AI model's contribution to a detection event.
// A single Detection may have contributions from multiple models (cross-model consensus).
type DetectionModelContribution struct {
	ID            uint    `gorm:"primaryKey"`
	DetectionID   uint    `gorm:"not null;index;uniqueIndex:idx_contrib_detection_model"`
	ModelID       uint    `gorm:"not null;uniqueIndex:idx_contrib_detection_model"`
	HitCount      int     `gorm:"not null"`
	MaxConfidence float64 `gorm:"not null"`

	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Model     *AIModel   `gorm:"foreignKey:ModelID"`
}

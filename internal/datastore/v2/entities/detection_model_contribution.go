package entities

// DetectionModelContribution records one AI model's contribution to a detection event.
// A single Detection may have contributions from multiple models (cross-model consensus).
//
// FirstHitAt, LastHitAt, MinConfidence and ConfidenceSum are nullable: NULL means "not
// recorded", which is the case for rows written before these columns existed or by an
// older binary. They are stored together or not at all. The mean confidence is
// ConfidenceSum / HitCount. ModelID carries its own index for per-model statistics;
// DetectionID is served by the leading column of the unique (detection_id, model_id)
// index and needs none.
type DetectionModelContribution struct {
	ID            uint    `gorm:"primaryKey"`
	DetectionID   uint    `gorm:"not null;uniqueIndex:idx_contrib_detection_model"`
	ModelID       uint    `gorm:"not null;index;uniqueIndex:idx_contrib_detection_model"`
	HitCount      int     `gorm:"not null"`
	MaxConfidence float64 `gorm:"not null"`

	// FirstHitAt and LastHitAt are the capture times of the end of the model's first
	// and last hit window, in Unix milliseconds (the convention of Detection.BeginTime).
	FirstHitAt *int64
	LastHitAt  *int64
	// MinConfidence is the lowest confidence among the model's hits.
	MinConfidence *float64
	// ConfidenceSum is the sum of the confidences of the model's hits.
	ConfidenceSum *float64

	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Model     *AIModel   `gorm:"foreignKey:ModelID"`
}

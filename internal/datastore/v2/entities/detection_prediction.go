package entities

// DetectionPrediction stores additional predictions for a detection.
// This replaces the legacy 'results' table.
type DetectionPrediction struct {
	ID          uint    `gorm:"primaryKey"`
	DetectionID uint    `gorm:"not null;index;uniqueIndex:idx_pred_detection_label"`
	LabelID     uint    `gorm:"not null;uniqueIndex:idx_pred_detection_label"`
	Confidence  float64 `gorm:"not null"`
	Rank        int     `gorm:"not null;default:1"` // 1 = second best, 2 = third best, etc. (rank 0 / primary is stored in Detection.LabelID)

	// Relationships
	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Label     *Label     `gorm:"foreignKey:LabelID"`
}

// TableName returns the table name for GORM.
func (DetectionPrediction) TableName() string {
	return "detection_predictions"
}

package entities

import "time"

// ModelLabel maps a model's raw label output to a normalized label.
// This handles different label formats per model (e.g., "Turdus merula_Common Blackbird" for BirdNET).
type ModelLabel struct {
	ModelID   uint      `gorm:"primaryKey;autoIncrement:false;uniqueIndex:idx_model_raw_label,priority:1"`
	LabelID   uint      `gorm:"primaryKey;autoIncrement:false"`
	RawLabel  string    `gorm:"type:varchar(300);not null;uniqueIndex:idx_model_raw_label,priority:2"`
	CreatedAt time.Time `gorm:"autoCreateTime"`

	// Relationships
	Model *AIModel `gorm:"foreignKey:ModelID;constraint:OnDelete:CASCADE"`
	Label *Label   `gorm:"foreignKey:LabelID;constraint:OnDelete:CASCADE"`
}

// TableName returns the table name for GORM.
func (ModelLabel) TableName() string {
	return "model_labels"
}

package entities

import "time"

// ModelType represents the type of species a model detects.
type ModelType string

const (
	ModelTypeBird  ModelType = "bird"
	ModelTypeBat   ModelType = "bat"
	ModelTypeMulti ModelType = "multi"
)

// AIModel represents an AI detection model (BirdNET, Perch, BatNET, etc.).
type AIModel struct {
	ID         uint      `gorm:"primaryKey"`
	Name       string    `gorm:"type:varchar(50);not null;uniqueIndex:idx_ai_models_name_version"`
	Version    string    `gorm:"type:varchar(20);not null;uniqueIndex:idx_ai_models_name_version"`
	ModelType  ModelType `gorm:"type:varchar(20);not null"`
	LabelCount int       `gorm:"default:0"` // Cached count of known labels
	CreatedAt  time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for GORM.
func (AIModel) TableName() string {
	return "ai_models"
}

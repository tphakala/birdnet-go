package entities

import "time"

// ModelType represents the type of species a model detects.
type ModelType string

const (
	ModelTypeBird  ModelType = "bird"
	ModelTypeBat   ModelType = "bat"
	ModelTypeMulti ModelType = "multi"
)

// TaxonomicClass returns the name of the taxonomic class the save path gives a
// species label of a model of this type: Chiroptera for bat models, none ("")
// for multi-taxa models and Aves for everything else (bird and unknown types).
func (t ModelType) TaxonomicClass() string {
	switch t {
	case ModelTypeBat:
		return TaxonomicClassChiroptera
	case ModelTypeMulti:
		return ""
	default:
		return TaxonomicClassAves
	}
}

// AIModel represents an AI detection model (BirdNET, Perch, BatNET, etc.).
type AIModel struct {
	ID             uint      `gorm:"primaryKey"`
	Name           string    `gorm:"type:varchar(50);not null;uniqueIndex:idx_model_identity"`
	Version        string    `gorm:"type:varchar(20);not null;uniqueIndex:idx_model_identity"`
	Variant        string    `gorm:"type:varchar(100);not null;uniqueIndex:idx_model_identity;default:default"`
	ModelType      ModelType `gorm:"type:varchar(20);not null"`
	ClassifierPath *string   `gorm:"type:varchar(500)"` // path to custom classifier file
	CreatedAt      time.Time `gorm:"autoCreateTime"`
}

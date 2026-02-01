package entities

import "time"

// Label represents a classification label (species or non-species).
// Labels are model-specific - the same species can have separate label entries
// for different models/variants to track which model's classification was used.
type Label struct {
	ID uint `gorm:"primaryKey"`
	// ScientificName stores the identifier for this label.
	// For species: the scientific name (e.g., "Turdus merula")
	// For non-species: the label identifier (e.g., "noise", "wind", "siren")
	ScientificName   string    `gorm:"size:200;not null;uniqueIndex:idx_label_identity"`
	ModelID          uint      `gorm:"not null;uniqueIndex:idx_label_identity;index"`
	LabelTypeID      uint      `gorm:"not null;index"`
	TaxonomicClassID *uint     `gorm:"index"` // nullable for non-species
	CreatedAt        time.Time `gorm:"autoCreateTime"`

	// Relationships
	Model          *AIModel         `gorm:"foreignKey:ModelID"`
	LabelType      *LabelType       `gorm:"foreignKey:LabelTypeID"`
	TaxonomicClass *TaxonomicClass  `gorm:"foreignKey:TaxonomicClassID"`
}

// TableName returns the table name for GORM.
func (Label) TableName() string {
	return "labels"
}

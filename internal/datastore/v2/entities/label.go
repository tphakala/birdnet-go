package entities

import "time"

// LabelType represents the classification category of a label.
type LabelType string

const (
	LabelTypeSpecies     LabelType = "species"
	LabelTypeNoise       LabelType = "noise"
	LabelTypeEnvironment LabelType = "environment"
	LabelTypeDevice      LabelType = "device"
	LabelTypeUnknown     LabelType = "unknown"
)

// Label represents a classification label (species or non-species).
// Non-species labels include noise, background sounds, environmental sounds, etc.
type Label struct {
	ID             uint      `gorm:"primaryKey"`
	ScientificName *string   `gorm:"uniqueIndex;size:200"` // NULL for non-species
	LabelType      LabelType `gorm:"type:varchar(20);not null;default:'species';index"`
	TaxonomicClass *string   `gorm:"size:50"` // 'Aves', 'Chiroptera', NULL for non-species
	CreatedAt      time.Time `gorm:"autoCreateTime"`
}

// TableName returns the table name for GORM.
func (Label) TableName() string {
	return "labels"
}

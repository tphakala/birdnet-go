package entities

// LabelType represents the classification category of a label.
// This is a lookup table for label categories, user-extensible since
// different classifiers may introduce new types.
type LabelType struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:30;uniqueIndex;not null"`
}

// TableName returns the table name for GORM.
func (LabelType) TableName() string {
	return "label_types"
}

// DefaultLabelTypes returns the default label type values to seed on initialization.
func DefaultLabelTypes() []LabelType {
	return []LabelType{
		{Name: "species"},
		{Name: "noise"},
		{Name: "environment"},
		{Name: "device"},
	}
}

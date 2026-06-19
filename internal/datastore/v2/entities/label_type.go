package entities

// Label-type name constants. These values match the string representation of
// the corresponding nonbird.Category constants so the seeded lookup table rows
// align with the nonbird classification buckets without a direct import.
const (
	LabelTypeSpecies     = "species"
	LabelTypeNoise       = "noise"
	LabelTypeEnvironment = "environment"
	LabelTypeDevice      = "device"
	LabelTypeHuman       = "human"
	LabelTypeAnimal      = "animal"
	LabelTypeMusic       = "music"
	LabelTypeMechanical  = "mechanical"
)

// LabelType represents the classification category of a label.
// This is a lookup table for label categories, user-extensible since
// different classifiers may introduce new types.
type LabelType struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:30;uniqueIndex;not null"`
}

// DefaultLabelTypes returns the default label type values to seed on initialization.
// Seeding is idempotent (FirstOrCreate), so new entries added here are picked up
// on the next startup without a migration.
func DefaultLabelTypes() []LabelType {
	return []LabelType{
		{Name: LabelTypeSpecies},
		{Name: LabelTypeNoise},
		{Name: LabelTypeEnvironment},
		{Name: LabelTypeDevice},
		{Name: LabelTypeHuman},
		{Name: LabelTypeAnimal},
		{Name: LabelTypeMusic},
		{Name: LabelTypeMechanical},
	}
}

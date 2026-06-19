package entities

// Label-type name constants. These values match the string representation of
// the corresponding nonbird.Category constants so the seeded lookup table rows
// align with the nonbird classification buckets without a direct import.
const (
	LabelTypeSpecies     = "species"     // a taxonomic species (bird, bat, or other taxon)
	LabelTypeNoise       = "noise"       // unstructured noise sound events
	LabelTypeEnvironment = "environment" // natural environmental sounds
	LabelTypeDevice      = "device"      // electronic devices and appliances
	LabelTypeHuman       = "human"       // human vocal and body sounds
	LabelTypeAnimal      = "animal"      // non-bird animal sounds
	LabelTypeMusic       = "music"       // musical instruments and performed music
	LabelTypeMechanical  = "mechanical"  // vehicles, tools, and other mechanical sources
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

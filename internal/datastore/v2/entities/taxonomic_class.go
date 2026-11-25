package entities

// TaxonomicClass represents a biological classification category.
// This is a lookup table for taxonomic classes.
type TaxonomicClass struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:50;uniqueIndex;not null"`
}

// Names of the seeded taxonomic classes.
const (
	// TaxonomicClassAves is the class of birds.
	TaxonomicClassAves = "Aves"
	// TaxonomicClassChiroptera is the class of bats.
	TaxonomicClassChiroptera = "Chiroptera"
)

// DefaultTaxonomicClasses returns the default taxonomic class values to seed on initialization.
func DefaultTaxonomicClasses() []TaxonomicClass {
	return []TaxonomicClass{
		{Name: TaxonomicClassAves},       // Birds
		{Name: TaxonomicClassChiroptera}, // Bats
	}
}

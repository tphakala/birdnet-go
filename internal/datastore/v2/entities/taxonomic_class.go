package entities

// TaxonomicClass represents a biological classification category.
// This is a lookup table for taxonomic classes.
type TaxonomicClass struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:50;uniqueIndex;not null"`
}

// DefaultTaxonomicClasses returns the default taxonomic class values to seed on initialization.
func DefaultTaxonomicClasses() []TaxonomicClass {
	return []TaxonomicClass{
		{Name: "Aves"},       // Birds
		{Name: "Chiroptera"}, // Bats
	}
}

package entities

import "time"

// SpeciesList represents a database-backed list of species.
type SpeciesList struct {
	ID          uint                `gorm:"primaryKey" json:"id"`
	Name        string              `gorm:"size:255;not null" json:"name"`
	Description string              `gorm:"size:1000;default:''" json:"description"`
	IsSystem    bool                `gorm:"default:false" json:"is_system"`
	CreatedAt   time.Time           `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time           `gorm:"autoUpdateTime" json:"updated_at"`
	Members     []SpeciesListMember `gorm:"foreignKey:ListID;constraint:OnDelete:CASCADE" json:"members,omitempty"`
}

// SpeciesListMember represents a species in a managed list.
// ScientificName is the canonical OpenFauna scientific name (lowercase), used as
// the stable identifier for species matching. The UI resolves localized common
// names from OpenFauna at display time; no common-name strings are stored here.
type SpeciesListMember struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ListID         uint      `gorm:"not null;index;uniqueIndex:idx_list_sci" json:"list_id"`
	ScientificName string    `gorm:"size:255;not null;uniqueIndex:idx_list_sci" json:"scientific_name"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}

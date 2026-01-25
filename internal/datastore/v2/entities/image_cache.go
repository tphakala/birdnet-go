package entities

import "time"

// ImageCache stores cached species images.
// This mirrors the legacy image_caches table structure.
type ImageCache struct {
	ID             uint      `gorm:"primaryKey"`
	ProviderName   string    `gorm:"uniqueIndex:idx_image_cache_provider_species;size:50;not null;default:wikimedia"`
	ScientificName string    `gorm:"uniqueIndex:idx_image_cache_provider_species;size:200;not null"`
	SourceProvider string    `gorm:"size:50;not null;default:wikimedia"`
	URL            string    `gorm:"size:500"`
	LicenseName    string    `gorm:"size:200"`
	LicenseURL     string    `gorm:"size:500"`
	AuthorName     string    `gorm:"size:200"`
	AuthorURL      string    `gorm:"size:500"`
	CachedAt       time.Time `gorm:"index"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

// TableName returns the table name for GORM.
func (ImageCache) TableName() string {
	return "image_caches"
}

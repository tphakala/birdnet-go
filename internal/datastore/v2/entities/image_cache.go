package entities

import "time"

// ImageCache stores cached species images.
// LabelID links to the species label for normalized storage.
type ImageCache struct {
	ID             uint      `gorm:"primaryKey"`
	ProviderName   string    `gorm:"uniqueIndex:idx_image_cache_provider_label;size:50;not null;default:wikimedia"`
	LabelID        uint      `gorm:"uniqueIndex:idx_image_cache_provider_label;not null"`
	SourceProvider string    `gorm:"size:50;not null;default:wikimedia"`
	URL            string    `gorm:"size:2048"`
	LicenseName    string    `gorm:"size:200"`
	LicenseURL     string    `gorm:"size:2048"`
	AuthorName     string    `gorm:"size:200"`
	AuthorURL      string    `gorm:"size:2048"`
	CachedAt       time.Time `gorm:"index"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`

	// Relationship
	Label *Label `gorm:"foreignKey:LabelID"`
}

// TableName returns the table name for GORM.
func (ImageCache) TableName() string {
	return "image_caches"
}

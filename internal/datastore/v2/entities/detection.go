package entities

import "time"

// Detection represents a normalized detection record.
// This replaces the legacy 'notes' table with a fully normalized structure.
type Detection struct {
	ID uint `gorm:"primaryKey"`

	// Foreign keys (normalized references)
	ModelID  uint  `gorm:"not null;index:idx_detection_model_label"`
	LabelID  uint  `gorm:"not null;index:idx_detection_model_label;index:idx_detection_label_date"`
	SourceID *uint `gorm:"index:idx_detection_source"`

	// Timestamps (consolidated - Unix timestamp is single source of truth)
	DetectedAt int64  `gorm:"not null;index;index:idx_detection_label_date;index:idx_detection_confidence"`
	BeginTime  *int64 // Milliseconds offset from source start
	EndTime    *int64 // Milliseconds offset from source start

	// Detection metadata
	Confidence  float64 `gorm:"not null;index:idx_detection_confidence"`
	Threshold   *float64
	Sensitivity *float64

	// Location (optional)
	Latitude  *float64
	Longitude *float64

	// Audio clip reference
	ClipName *string `gorm:"type:varchar(500)"`

	// Processing metadata
	ProcessingTimeMs *int64 // Milliseconds

	// Migration reference (preserves legacy ID for lookups)
	LegacyID *uint `gorm:"index"`

	CreatedAt time.Time `gorm:"autoCreateTime"`

	// Relationships (for preloading)
	// Note: Model and Label use FK constraints for referential integrity.
	// Source uses constraint:false because GORM's AutoMigrate doesn't properly
	// create ON DELETE SET NULL for SQLite. A trigger handles the SET NULL behavior.
	Model  *AIModel     `gorm:"foreignKey:ModelID"`
	Label  *Label       `gorm:"foreignKey:LabelID"`
	Source *AudioSource `gorm:"foreignKey:SourceID;references:ID;constraint:false"`

	// Reverse relationships for preloading (constraint:false to avoid duplicate FKs)
	Review *DetectionReview `gorm:"foreignKey:DetectionID;constraint:false"`
	Lock   *DetectionLock   `gorm:"foreignKey:DetectionID;constraint:false"`
}

// TableName returns the table name for GORM.
func (Detection) TableName() string {
	return "detections"
}

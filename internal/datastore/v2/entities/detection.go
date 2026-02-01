package entities

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
	Confidence float64 `gorm:"not null;index:idx_detection_confidence"`

	// Location (optional)
	Latitude  *float64
	Longitude *float64

	// Audio clip reference
	ClipName *string `gorm:"type:varchar(500)"`

	// Processing metadata
	ProcessingTimeMs *int64 // Milliseconds

	// Migration reference (preserves legacy ID for lookups and related data migration)
	LegacyID *uint `gorm:"index"`

	// Relationships (for preloading)
	// Note: Model and Label use FK constraints for referential integrity.
	// Source uses constraint:false because GORM's AutoMigrate doesn't properly
	// create ON DELETE SET NULL for SQLite. A trigger handles the SET NULL behavior.
	Model  *AIModel     `gorm:"foreignKey:ModelID"`
	Label  *Label       `gorm:"foreignKey:LabelID"`
	Source *AudioSource `gorm:"foreignKey:SourceID;references:ID;constraint:false"`

	// Reverse relationships for manual assignment (NOT GORM relationships).
	// These are populated by loadDetectionRelations, not by GORM preload.
	// We avoid GORM relationship tags here because they interfere with
	// the CASCADE constraints defined in DetectionReview/DetectionLock.
	Review *DetectionReview `gorm:"-"`
	Lock   *DetectionLock   `gorm:"-"`
}

// TableName returns the table name for GORM.
func (Detection) TableName() string {
	return "detections"
}

// model.go this code defines the data model for the application
package datastore

import "time"

// AudioSource is the runtime representation (not persisted)
type AudioSource struct {
	ID          string `json:"id"`
	SafeString  string `json:"safeString"`
	DisplayName string `json:"displayName"`
}

// AudioSourceRecord is persisted to DB; notes reference via FK
type AudioSourceRecord struct {
	ID        string    `gorm:"primaryKey;size:50"`
	Label     string    `gorm:"size:200"`
	Type      string    `gorm:"size:20"`
	CreatedAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

// Note represents a single observation data point
type Note struct {
	ID            uint   `gorm:"primaryKey"`
	SourceNode    string
	AudioSourceID string `gorm:"index:idx_notes_audio_source_id;size:50"`
	Date          string `gorm:"index:idx_notes_date;index:idx_notes_date_commonname_confidence;index:idx_notes_sciname_date;index:idx_notes_sciname_date_optimized,priority:2"`
	Time          string `gorm:"index:idx_notes_time"`
	//InputFile      string
	Source      AudioSource `gorm:"-"` // Runtime only, not stored in database
	BeginTime   time.Time
	EndTime     time.Time
	SpeciesCode string
	// ScientificName includes optimized index (scientific_name, date) for new species tracking performance
	ScientificName string  `gorm:"index:idx_notes_sciname;index:idx_notes_sciname_date;index:idx_notes_sciname_date_optimized,priority:1"`
	CommonName     string  `gorm:"index:idx_notes_comname;index:idx_notes_date_commonname_confidence"`
	Confidence     float64 `gorm:"index:idx_notes_date_commonname_confidence"`
	Latitude       float64
	Longitude      float64
	Threshold      float64
	Sensitivity    float64
	ClipName       string
	ProcessingTime time.Duration
	Occurrence     float64       `gorm:"-" json:"occurrence,omitempty"` // Runtime only, occurrence probability (0-1) based on location/time
	Results        []Results     `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"`
	Review         *NoteReview   `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"` // One-to-one relationship with cascade delete
	Comments       []NoteComment `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"` // One-to-many relationship with cascade delete
	Lock           *NoteLock     `gorm:"foreignKey:NoteID;constraint:OnDelete:CASCADE"` // One-to-one relationship with cascade delete

	// Virtual fields to maintain compatibility with templates
	Verified string `gorm:"-"` // This will be populated from Review.Verified
	Locked   bool   `gorm:"-"` // This will be populated from Lock presence
}

// Result represents the identification result with a species name and its confidence level, linked to a Note.
type Results struct {
	ID         uint `gorm:"primaryKey"`
	NoteID     uint `gorm:"index;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note
	Species    string
	Confidence float32
}

// Copy creates a deep copy of the Results struct
func (r Results) Copy() Results {
	return Results{
		ID:         r.ID,
		NoteID:     r.NoteID,
		Species:    r.Species,
		Confidence: r.Confidence,
	}
}

// NoteReview represents the review status of a Note
// GORM will automatically create table name as 'note_reviews'
type NoteReview struct {
	ID        uint      `gorm:"primaryKey"`
	NoteID    uint      `gorm:"uniqueIndex;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note
	Verified  string    `gorm:"type:varchar(20)"`                                                                                  // Values: "correct", "false_positive"
	CreatedAt time.Time `gorm:"index"`                                                                                             // When the review was created
	UpdatedAt time.Time // When the review was last updated
}

// NoteComment represents user comments on a detection
// GORM will automatically create table name as 'note_comments'
type NoteComment struct {
	ID        uint      `gorm:"primaryKey"`
	NoteID    uint      `gorm:"index;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note
	Entry     string    `gorm:"type:text"`                                                                                   // The actual comment text
	CreatedAt time.Time `gorm:"index"`                                                                                       // When the comment was created
	UpdatedAt time.Time // When the comment was last updated
}

// NoteLock represents the lock status of a Note
// GORM will automatically create table name as 'note_locks'
type NoteLock struct {
	ID       uint      `gorm:"primaryKey"`
	NoteID   uint      `gorm:"uniqueIndex;not null;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:NoteID;references:ID"` // Foreign key to associate with Note, with unique constraint
	LockedAt time.Time `gorm:"index;not null"`                                                                                    // When the note was locked
}

// DailyEvents represents the daily weather data that doesn't change throughout the day
type DailyEvents struct {
	ID       uint   `gorm:"primaryKey"`
	Date     string `gorm:"index:idx_dailyweather_date"`
	Sunrise  int64
	Sunset   int64
	Country  string
	CityName string
}

// HourlyWeather represents the hourly weather data that changes throughout the day
type HourlyWeather struct {
	ID            uint `gorm:"primaryKey"`
	DailyEventsID uint `gorm:"index"` // Foreign key to associate with DailyEvents
	Time          time.Time
	Temperature   float64
	FeelsLike     float64
	TempMin       float64
	TempMax       float64
	Pressure      int
	Humidity      int
	Visibility    int
	WindSpeed     float64
	WindDeg       int
	WindGust      float64
	Clouds        int
	WeatherMain   string
	WeatherDesc   string
	WeatherIcon   string
}

// ImageCache represents cached image metadata for species
type ImageCache struct {
	ID             uint      `gorm:"primaryKey"`
	ProviderName   string    `gorm:"index:idx_imagecache_provider_species,unique;not null;default:wikimedia"` // Name of the provider (e.g., "wikimedia", "flickr")
	ScientificName string    `gorm:"index:idx_imagecache_provider_species,unique;not null"`                   // Scientific name of the species
	SourceProvider string    `gorm:"not null;default:wikimedia"`                                              // The actual provider that supplied the image
	URL            string    // The URL of the image
	LicenseName    string    // The name of the license for the image
	LicenseURL     string    // The URL of the license details
	AuthorName     string    // The name of the image author
	AuthorURL      string    // The URL of the author's page or profile
	CachedAt       time.Time `gorm:"index"` // When the image was cached
}

// ImageCacheQuery encapsulates parameters for querying the image cache.
type ImageCacheQuery struct {
	ScientificName string
	ProviderName   string
}

// DetectionRecord represents a bird detection record for search results
type DetectionRecord struct {
	ID             string    `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	ScientificName string    `json:"scientificName,omitempty"`
	CommonName     string    `json:"commonName,omitempty"`
	Confidence     float64   `json:"confidence,omitempty"`
	Latitude       float64   `json:"latitude,omitempty"`
	Longitude      float64   `json:"longitude,omitempty"`
	Week           int       `json:"week,omitempty"`
	AudioFilePath  string    `json:"audioFilePath,omitempty"`
	Verified       string    `json:"verified,omitempty"`
	Locked         bool      `json:"locked,omitempty"`
	HasAudio       bool      `json:"hasAudio,omitempty"`
	Device         string    `json:"device,omitempty"`
	Source         string    `json:"source,omitempty"`
	TimeOfDay      string    `json:"timeOfDay,omitempty"`
}

// DynamicThreshold represents a persisted dynamic threshold for a species
// This allows thresholds to survive application restarts, preventing the issue where
// users experience a sudden drop in detections after restart when learned thresholds are lost.
type DynamicThreshold struct {
	ID             uint      `gorm:"primaryKey"`
	SpeciesName    string    `gorm:"uniqueIndex;not null;size:200"` // Common name (lowercase)
	ScientificName string    `gorm:"size:200"`                      // Scientific name for thumbnails
	Level          int       `gorm:"not null;default:0"`            // Adjustment level (0-3)
	CurrentValue   float64   `gorm:"not null"`                      // Current threshold value
	BaseThreshold  float64   `gorm:"not null"`                      // Original base threshold for reference
	HighConfCount  int       `gorm:"not null;default:0"`            // Count of high-confidence detections
	ValidHours     int       `gorm:"not null"`                      // Hours until expiry
	ExpiresAt      time.Time `gorm:"index;not null"`                // When this threshold expires
	LastTriggered  time.Time `gorm:"index;not null"`                // Last time threshold was triggered
	FirstCreated   time.Time `gorm:"not null"`                      // When first created
	UpdatedAt      time.Time `gorm:"not null"`                      // Last update time
	TriggerCount   int       `gorm:"not null;default:0"`            // Total number of times triggered (for statistics)
}

// ThresholdEvent records each change to a dynamic threshold for audit/history purposes.
// This enables the frontend to display a timeline of threshold adjustments per species.
type ThresholdEvent struct {
	ID            uint      `gorm:"primaryKey"`
	SpeciesName   string    `gorm:"index;not null;size:200"` // Common name (lowercase)
	PreviousLevel int       `gorm:"not null"`                // Level before change
	NewLevel      int       `gorm:"not null"`                // Level after change
	PreviousValue float64   `gorm:"not null"`                // Threshold value before change
	NewValue      float64   `gorm:"not null"`                // Threshold value after change
	ChangeReason  string    `gorm:"not null;size:50"`        // "high_confidence", "expiry", "manual_reset"
	Confidence    float64   `gorm:"default:0"`               // Detection confidence that triggered change (if applicable)
	CreatedAt     time.Time `gorm:"index;not null"`          // When the event occurred
}

// NotificationHistory tracks sent notifications to prevent duplicate notifications after restart
// Similar to DynamicThreshold, this ensures notification suppression state survives application restarts.
// Resolves BG-17: Species tracker loses state on restart - causes false "New Species" notifications
type NotificationHistory struct {
	ID               uint      `gorm:"primaryKey"`
	ScientificName   string    `gorm:"index:idx_notification_history_species_type,unique;not null;size:200"`                    // Scientific name of the species
	NotificationType string    `gorm:"index:idx_notification_history_species_type,unique;not null;size:50;default:new_species"` // Type: "new_species", "yearly", "seasonal"
	LastSent         time.Time `gorm:"index;not null"`                                                                          // When notification was last sent
	ExpiresAt        time.Time `gorm:"index;not null"`                                                                          // When this record expires (2x suppression window)
	CreatedAt        time.Time `gorm:"not null"`                                                                                // When first created
	UpdatedAt        time.Time `gorm:"not null"`                                                                                // Last update time
}

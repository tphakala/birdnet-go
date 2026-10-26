package entities

// PrivacyFilterDiscard is one pending detection the privacy filter discarded,
// with the settings in effect and the span of audio that would have been saved.
// Its Hits name the detectors whose counted speech hits reached that span.
// Rows are written by the analysis service in v2-only mode and pruned by age.
//
// DiscardedAt is the wall-clock time of the flush that decided, which can
// differ from the hour the hourly rollup (PrivacyFilterHourly) keys the same
// discard on: the rollup uses the stamp of the hit that caused it.
//
// Rows are deleted with their audio source. A row whose source could not be
// resolved has a NULL SourceID, survives source deletion and is removed by
// retention only.
type PrivacyFilterDiscard struct {
	ID uint `gorm:"primaryKey;autoIncrement"`
	// DiscardedAt is the wall-clock time of the flush that discarded the
	// detection, in Unix milliseconds. Retention prunes on it.
	DiscardedAt int64 `gorm:"not null;index"`
	// SourceID references audio_sources; NULL when the source could not be resolved.
	SourceID *uint `gorm:"index"`
	// ScientificName and CommonName name the discarded species.
	ScientificName string `gorm:"type:varchar(255);not null"`
	CommonName     string `gorm:"type:varchar(255);not null"`
	// Confidence is the confidence of the discarded detection.
	Confidence float64 `gorm:"not null"`
	// ModelID is the registry id of the model that made the discarded detection.
	ModelID string `gorm:"type:varchar(64);not null"`
	// ClipBegin and ClipEnd are the span the exporter would have saved, in Unix
	// milliseconds.
	ClipBegin int64 `gorm:"not null"`
	ClipEnd   int64 `gorm:"not null"`
	// LabelThresholdMilli is the label confidence threshold in effect at the
	// discard, in thousandths.
	LabelThresholdMilli int `gorm:"not null"`
	// VADEnabled reports whether the voice activity gate was on at the discard.
	VADEnabled bool `gorm:"column:vad_enabled;not null;default:false"`
	// VADThresholdMilli is the voice activity threshold in effect at the discard,
	// in thousandths.
	VADThresholdMilli int `gorm:"column:vad_threshold_milli;not null"`
	// HitCountsLowerBound is true when the in-memory hit list had dropped a hit
	// that reached the clip, so the hit counts of Hits are lower bounds.
	HitCountsLowerBound bool `gorm:"not null;default:false"`

	// Source is the audio source; it is never written through this association.
	Source *AudioSource `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE"`
	// Hits are the per-detector totals of the counted hits that reached the clip.
	Hits []PrivacyFilterDiscardHit `gorm:"foreignKey:DiscardID;constraint:OnDelete:CASCADE"`
}

// PrivacyFilterDiscardHit totals the counted speech hits one detector
// contributed to a discard.
type PrivacyFilterDiscardHit struct {
	ID uint `gorm:"primaryKey;autoIncrement"`
	// DiscardID references the PrivacyFilterDiscard the hits belong to.
	DiscardID uint `gorm:"not null;index"`
	// Detector is the classifier model id, or silero_vad for the voice activity
	// gate; empty when a hit was recorded without a model.
	Detector string `gorm:"type:varchar(64);not null"`
	// TriggerType is "label" or "vad". The column is not called "trigger" because
	// TRIGGER is a reserved word in MySQL.
	TriggerType string `gorm:"column:trigger_type;type:varchar(16);not null"`
	// HitCount is the number of counted hits from the detector (a lower bound when
	// the discard's HitCountsLowerBound is set).
	HitCount int `gorm:"not null"`
	// MinScore and MaxScore are the lowest and highest score among those hits.
	MinScore float64 `gorm:"not null"`
	MaxScore float64 `gorm:"not null"`
}

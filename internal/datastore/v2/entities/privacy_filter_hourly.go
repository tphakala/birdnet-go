package entities

// PrivacyFilterHourly is one hour of privacy filter activity for one audio
// source, detector model, trigger and label threshold. Rows are additive
// aggregates written by the analysis service; one UTC hour never has two rows
// for the same key.
//
// All counts are lower bounds: events dropped under load, events of sources
// that could not be resolved, failed writes and what was still unflushed at
// shutdown are not counted.
//
// Rows are deleted with their audio source: a rollup of a source that no longer
// exists has no meaning.
type PrivacyFilterHourly struct {
	ID uint `gorm:"primaryKey;autoIncrement"`
	// HourStart is the start of the UTC hour as Unix seconds. A hit is stored in
	// the hour of its own audio-end stamp, and a discard in the hour of the hit
	// that caused it, not the hour of the drop.
	HourStart int64 `gorm:"not null;uniqueIndex:idx_privacy_filter_hourly_key,priority:1"`
	// SourceID references audio_sources.
	SourceID uint `gorm:"not null;uniqueIndex:idx_privacy_filter_hourly_key,priority:2"`
	// ModelID is the detector model that scored the hit (registered ids, or
	// silero_vad for the VAD gate). Empty for unscanned saves, which have no hit.
	ModelID string `gorm:"type:varchar(64);not null;uniqueIndex:idx_privacy_filter_hourly_key,priority:3"`
	// TriggerType is "label" or "vad". For unscanned saves it names the detector
	// the coverage gate waited for. The column is not called "trigger" because
	// TRIGGER is a reserved word in MySQL.
	TriggerType string `gorm:"column:trigger_type;type:varchar(16);not null;uniqueIndex:idx_privacy_filter_hourly_key,priority:4"`
	// ThresholdMilli is the confidence threshold in effect when the hit was
	// scored, in thousandths. 0 for unscanned saves.
	ThresholdMilli int `gorm:"not null;uniqueIndex:idx_privacy_filter_hourly_key,priority:5"`

	// Hits counts speech hits that counted (would discard a reaching clip): label
	// hits above the threshold that counted at once, held hits later confirmed
	// (counted in the held hit's row), and VAD inferences at or above the VAD
	// threshold. Every label result above the threshold counts, so several human
	// labels in one chunk count separately. The VAD scores each (source,
	// speech-band model) stream, so with several models one stretch of speech
	// counts once per stream.
	Hits int64 `gorm:"not null;default:0"`
	// Held counts label hits held for confirmation. Nonzero only when the
	// effective base overlap is above 1.5 s, so 0 on default configs. A held hit
	// that never gets another hit, or is still unresolved at shutdown, is never
	// counted as confirmed or ignored (at most one per source at a time).
	Held int64 `gorm:"not null;default:0"`
	// Ignored counts held hits dropped because the next hit fell outside the
	// confirmation window, or arrived while label confirmation was off (effective
	// overlap at or below 1.5 s). A subset of Held.
	Ignored int64 `gorm:"not null;default:0"`
	// Confirmed counts held hits later confirmed by a second hit (label or VAD).
	// A subset of Held; Held >= Confirmed + Ignored.
	Confirmed int64 `gorm:"not null;default:0"`
	// Discards counts pending detections the privacy filter dropped, attributed
	// to the hit that caused the drop. One hit can discard many detections, so
	// discards and hits are not 1:1.
	Discards int64 `gorm:"not null;default:0"`
	// UnscannedSaves counts detections approved after the coverage wait expired,
	// before a speech detector had scanned the whole clip (fail open).
	UnscannedSaves int64 `gorm:"not null;default:0"`

	// MaxScore is the highest score over bucketed events (counted and held hits).
	MaxScore float64 `gorm:"not null;default:0"`
	// MarginLt50, MarginLt100, MarginLt200 and MarginGe200 bucket each counted or
	// held hit by how far its score is above the threshold: below 0.05, 0.05 to
	// 0.10, 0.10 to 0.20, and 0.20 or more. Per row,
	// sum(buckets) = Hits - Confirmed + Held. A confirmed hit is bucketed when it
	// is held, not again when it is confirmed.
	MarginLt50  int64 `gorm:"column:margin_lt_50;not null;default:0"`
	MarginLt100 int64 `gorm:"column:margin_lt_100;not null;default:0"`
	MarginLt200 int64 `gorm:"column:margin_lt_200;not null;default:0"`
	MarginGe200 int64 `gorm:"column:margin_ge_200;not null;default:0"`

	Source *AudioSource `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE"`
}

// Package repository provides V2 repository interfaces and implementations
// for the normalized database schema.
package repository

import "github.com/tphakala/birdnet-go/internal/errors"

// Sentinel errors for repository operations.
// These typed errors enable callers to distinguish between different
// failure modes without relying on string matching or GORM-specific errors.
var (
	// ErrDetectionNotFound indicates the requested detection does not exist.
	ErrDetectionNotFound = errors.NewStd("detection not found")

	// ErrLabelNotFound indicates the requested label does not exist.
	ErrLabelNotFound = errors.NewStd("label not found")

	// ErrLabelTypeNotFound indicates the requested label type does not exist.
	ErrLabelTypeNotFound = errors.NewStd("label type not found")

	// ErrTaxonomicClassNotFound indicates the requested taxonomic class does not exist.
	ErrTaxonomicClassNotFound = errors.NewStd("taxonomic class not found")

	// ErrModelNotFound indicates the requested AI model does not exist.
	ErrModelNotFound = errors.NewStd("model not found")

	// ErrAudioSourceNotFound indicates the requested audio source does not exist.
	ErrAudioSourceNotFound = errors.NewStd("audio source not found")

	// ErrPredictionNotFound indicates the requested prediction does not exist.
	ErrPredictionNotFound = errors.NewStd("prediction not found")

	// ErrReviewNotFound indicates no review exists for the detection.
	ErrReviewNotFound = errors.NewStd("review not found")

	// ErrCommentNotFound indicates the requested comment does not exist.
	ErrCommentNotFound = errors.NewStd("comment not found")

	// ErrLockNotFound indicates no lock exists for the detection.
	ErrLockNotFound = errors.NewStd("lock not found")

	// ErrDuplicateKey indicates a unique constraint violation.
	ErrDuplicateKey = errors.NewStd("duplicate key")

	// ErrDetectionLocked indicates the detection is locked and cannot be modified.
	ErrDetectionLocked = errors.NewStd("detection is locked")

	// ErrInvalidInput indicates invalid input parameters.
	ErrInvalidInput = errors.NewStd("invalid input")

	// ErrNoClipPath indicates the detection exists but has no associated clip path.
	ErrNoClipPath = errors.NewStd("detection has no clip path")

	// ErrDailyEventsNotFound indicates no daily events exist for the date.
	ErrDailyEventsNotFound = errors.NewStd("daily events not found")

	// ErrHourlyWeatherNotFound indicates no hourly weather data exists.
	ErrHourlyWeatherNotFound = errors.NewStd("hourly weather not found")

	// ErrImageCacheNotFound indicates the image cache entry was not found.
	ErrImageCacheNotFound = errors.NewStd("image cache not found")

	// ErrDynamicThresholdNotFound indicates no dynamic threshold exists for the species.
	ErrDynamicThresholdNotFound = errors.NewStd("dynamic threshold not found")

	// ErrThresholdEventNotFound indicates no threshold event exists.
	ErrThresholdEventNotFound = errors.NewStd("threshold event not found")

	// ErrNotificationHistoryNotFound indicates no notification history exists.
	ErrNotificationHistoryNotFound = errors.NewStd("notification history not found")

	// ErrAlertRuleNotFound indicates the requested alert rule does not exist.
	ErrAlertRuleNotFound = errors.NewStd("alert rule not found")
)

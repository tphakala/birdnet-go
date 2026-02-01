// Package repository provides V2 repository interfaces and implementations
// for the normalized database schema.
package repository

import "errors"

// Sentinel errors for repository operations.
// These typed errors enable callers to distinguish between different
// failure modes without relying on string matching or GORM-specific errors.
var (
	// ErrDetectionNotFound indicates the requested detection does not exist.
	ErrDetectionNotFound = errors.New("detection not found")

	// ErrLabelNotFound indicates the requested label does not exist.
	ErrLabelNotFound = errors.New("label not found")

	// ErrLabelTypeNotFound indicates the requested label type does not exist.
	ErrLabelTypeNotFound = errors.New("label type not found")

	// ErrTaxonomicClassNotFound indicates the requested taxonomic class does not exist.
	ErrTaxonomicClassNotFound = errors.New("taxonomic class not found")

	// ErrModelNotFound indicates the requested AI model does not exist.
	ErrModelNotFound = errors.New("model not found")

	// ErrAudioSourceNotFound indicates the requested audio source does not exist.
	ErrAudioSourceNotFound = errors.New("audio source not found")

	// ErrPredictionNotFound indicates the requested prediction does not exist.
	ErrPredictionNotFound = errors.New("prediction not found")

	// ErrReviewNotFound indicates no review exists for the detection.
	ErrReviewNotFound = errors.New("review not found")

	// ErrCommentNotFound indicates the requested comment does not exist.
	ErrCommentNotFound = errors.New("comment not found")

	// ErrLockNotFound indicates no lock exists for the detection.
	ErrLockNotFound = errors.New("lock not found")

	// ErrDuplicateKey indicates a unique constraint violation.
	ErrDuplicateKey = errors.New("duplicate key")

	// ErrDetectionLocked indicates the detection is locked and cannot be modified.
	ErrDetectionLocked = errors.New("detection is locked")

	// ErrInvalidInput indicates invalid input parameters.
	ErrInvalidInput = errors.New("invalid input")

	// ErrNoClipPath indicates the detection exists but has no associated clip path.
	ErrNoClipPath = errors.New("detection has no clip path")

	// ErrDailyEventsNotFound indicates no daily events exist for the date.
	ErrDailyEventsNotFound = errors.New("daily events not found")

	// ErrHourlyWeatherNotFound indicates no hourly weather data exists.
	ErrHourlyWeatherNotFound = errors.New("hourly weather not found")

	// ErrImageCacheNotFound indicates the image cache entry was not found.
	ErrImageCacheNotFound = errors.New("image cache not found")

	// ErrDynamicThresholdNotFound indicates no dynamic threshold exists for the species.
	ErrDynamicThresholdNotFound = errors.New("dynamic threshold not found")

	// ErrThresholdEventNotFound indicates no threshold event exists.
	ErrThresholdEventNotFound = errors.New("threshold event not found")

	// ErrNotificationHistoryNotFound indicates no notification history exists.
	ErrNotificationHistoryNotFound = errors.New("notification history not found")
)

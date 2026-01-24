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
)

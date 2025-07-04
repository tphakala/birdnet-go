package audiocore

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Component identifier for audiocore errors
const ComponentAudioCore = "audiocore"

// Error categories specific to audiocore
var (
	// ErrSourceNotFound is returned when an audio source is not found
	ErrSourceNotFound = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryNotFound).
		Context("resource", "audio_source").
		Build()

	// ErrSourceAlreadyExists is returned when trying to add a duplicate source
	ErrSourceAlreadyExists = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryConflict).
		Context("resource", "audio_source").
		Build()

	// ErrInvalidAudioFormat is returned when audio format is invalid
	ErrInvalidAudioFormat = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryValidation).
		Context("resource", "audio_format").
		Build()

	// ErrBufferTooSmall is returned when a buffer is too small for the operation
	ErrBufferTooSmall = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryResource).
		Context("resource", "buffer").
		Build()

	// ErrProcessorFailed is returned when an audio processor fails
	ErrProcessorFailed = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryProcessing).
		Context("operation", "audio_processing").
		Build()

	// ErrManagerNotStarted is returned when operations are attempted on a stopped manager
	ErrManagerNotStarted = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryState).
		Context("resource", "audio_manager").
		Build()

	// ErrSourceNotActive is returned when operations require an active source
	ErrSourceNotActive = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryState).
		Context("resource", "audio_source").
		Build()

	// ErrMaxSourcesReached is returned when the maximum number of sources is exceeded
	ErrMaxSourcesReached = errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryLimit).
		Context("resource", "audio_sources").
		Build()
)
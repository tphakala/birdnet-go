package myaudio

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Error sentinel values for common myaudio errors
var (
	// ErrSoundLevelProcessorNotRegistered is returned when attempting to process sound level data
	// for a source that hasn't been registered
	ErrSoundLevelProcessorNotRegistered = errors.Newf("no sound level processor registered").
		Component("myaudio").
		Category(errors.CategoryValidation).
		Context("operation", "process_sound_level_data").
		Build()
)
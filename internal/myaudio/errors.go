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

	// ErrBufferNotFound is returned when an analysis or capture buffer does not exist
	// for a given source ID. This typically occurs when a buffer has been removed
	// during stream disconnection while reader goroutines still reference it.
	ErrBufferNotFound = errors.Newf("buffer not found").
				Component("myaudio").
				Category(errors.CategoryBuffer).
				Build()
)

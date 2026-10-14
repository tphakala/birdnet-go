package transcode

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Sentinel errors. Errors returned by this package wrap exactly one of these (use
// errors.Is) together with the underlying decoder error when there is one. Callers
// map them to HTTP statuses; the sentinels themselves carry no request data.
var (
	// ErrUnsupportedSource marks a source this package does not decode: a codec
	// or container outside the supported set, more than two channels, or a sample
	// rate outside [MinSourceSampleRate, MaxSourceSampleRate].
	ErrUnsupportedSource = errors.NewStd("source audio format is not supported")

	// ErrHEAACSource marks an HE-AAC or HE-AACv2 source (AAC with SBR or PS),
	// which the native AAC decoder rejects. It wraps ErrUnsupportedSource.
	ErrHEAACSource = fmt.Errorf("%w: HE-AAC (SBR or PS) is not supported", ErrUnsupportedSource)

	// ErrUndecodableSource marks a source in a supported format that is corrupt,
	// truncated or empty.
	ErrUndecodableSource = errors.NewStd("source audio could not be decoded")

	// ErrAudioTooLong marks a source file or a decoded range that exceeds the
	// size bounds.
	ErrAudioTooLong = errors.NewStd("audio is too long for on-demand processing")

	// ErrEmptyRange marks a requested range that holds no audio, for example a
	// clip window that starts past the end of the source.
	ErrEmptyRange = errors.NewStd("requested range holds no audio")

	// ErrInvalidRequest marks a caller-supplied value outside what the package
	// accepts: a clip window with a non-finite or negative start or a non-positive
	// duration, an unknown denoise preset, format or time-expansion factor.
	ErrInvalidRequest = errors.NewStd("invalid transcode request")
)

const componentName = "audiocore/transcode"

// wrapSentinel returns an error that wraps sentinel and, when non-nil, cause.
func wrapSentinel(sentinel, cause error, operation, detail string, category errors.ErrorCategory) error {
	var b *errors.ErrorBuilder
	if cause != nil {
		b = errors.Newf("%w: %s: %w", sentinel, detail, cause)
	} else {
		b = errors.Newf("%w: %s", sentinel, detail)
	}
	return b.Component(componentName).
		Category(category).
		Context("operation", operation).
		Build()
}

// validationError builds a CategoryValidation error for a malformed request value.
func validationError(sentinel error, operation, format string, args ...any) error {
	return errors.Newf("%w: %s", sentinel, fmt.Sprintf(format, args...)).
		Component(componentName).
		Category(errors.CategoryValidation).
		Context("operation", operation).
		Build()
}

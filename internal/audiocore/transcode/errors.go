package transcode

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Sentinel errors. Problems with the source or the request wrap one of these (use
// errors.Is), together with the underlying decoder error when there is one;
// callers map them to HTTP statuses. Any other error (file access, an encoder or
// denoiser failure) carries none and is a server fault. ErrShorterThanDeclared
// is not a source fault and is not for status mapping: it tells the analysis
// caller to decode buffered.
var (
	// ErrUnsupportedSource marks a source this package does not decode: a file
	// whose extension is not a clip format, a non-regular file, or a sample rate
	// or channel count outside the bounds.
	ErrUnsupportedSource = errors.NewStd("source audio format is not supported")

	// ErrUndecodableSource marks a source that a decoder rejects: corrupt,
	// truncated, empty, or not what its extension says. A source known to be
	// shorter than its header declares also wraps ErrTruncatedSource.
	ErrUndecodableSource = errors.NewStd("source audio could not be decoded")

	// ErrTruncatedSource marks a WAV or FLAC source that delivers fewer frames than
	// its header declares: the file was cut short or is still being written. Such
	// an error also wraps ErrUndecodableSource, so callers that test only that
	// sentinel keep their answer. MP3, M4A and Opus carry no exact length and are
	// never reported this way.
	ErrTruncatedSource = errors.NewStd("source audio is shorter than its header declares")

	// ErrShorterThanDeclared is returned by MonoSource.Stream when a file-backed
	// source of a format without an exact length (MP3, M4A, Opus) delivers fewer
	// frames than its header declared. The span a caller took from the header is
	// then too long for the audio, so the caller should decode the file with
	// DecodeMono and use what it holds. It is a signal to redo the decode, not a
	// source fault: it is never wrapped together with ErrUndecodableSource.
	ErrShorterThanDeclared = errors.NewStd("source audio is shorter than its header declared")

	// ErrAudioTooLong marks a source file or a decoded range that exceeds the
	// size bounds.
	ErrAudioTooLong = errors.NewStd("audio is too long for on-demand processing")

	// ErrEmptyRange marks a requested range that holds no audio, for example a
	// clip window that starts past the end of the source.
	ErrEmptyRange = errors.NewStd("requested range holds no audio")

	// ErrBelowMinimumRate marks a source whose sample rate is below the minimum the
	// requested operation needs (audible bats needs ultrasonic capture rates).
	ErrBelowMinimumRate = errors.NewStd("source sample rate is below the minimum for this operation")

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

// truncatedError returns an error that wraps ErrTruncatedSource and
// ErrUndecodableSource and, when non-nil, cause.
func truncatedError(cause error, operation, detail string) error {
	var b *errors.ErrorBuilder
	if cause != nil {
		b = errors.Newf("%w: %w: %s: %w", ErrTruncatedSource, ErrUndecodableSource, detail, cause)
	} else {
		b = errors.Newf("%w: %w: %s", ErrTruncatedSource, ErrUndecodableSource, detail)
	}
	return b.Component(componentName).
		Category(errors.CategoryAudio).
		Context("operation", operation).
		Build()
}

// wrapError wraps err with the component, category and operation telemetry tags.
func wrapError(err error, category errors.ErrorCategory, operation string) error {
	return errors.New(err).
		Component(componentName).
		Category(category).
		Context("operation", operation).
		Build()
}

// validationError builds a CategoryValidation error for a malformed request value.
func validationError(sentinel error, operation, format string, args ...any) error {
	return wrapSentinel(sentinel, nil, operation, fmt.Sprintf(format, args...), errors.CategoryValidation)
}

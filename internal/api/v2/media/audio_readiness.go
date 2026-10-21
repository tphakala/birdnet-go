package media

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Readiness thresholds of the clip spectrogram path, run in Go with no binary.
const (
	// minReadyAudioBytes is the minimum size for a valid audio file (1KB).
	minReadyAudioBytes int64 = 1024

	// maxReadinessAttempts is the maximum number of readiness attempts.
	// Set to 5 to accommodate slow storage (SD cards, network drives) on
	// resource-constrained devices like Raspberry Pi / arm64 containers.
	maxReadinessAttempts = 5

	// readinessRetryDelay is the base delay between readiness attempts.
	// 500ms gives slow storage enough time between retries.
	readinessRetryDelay = 500 * time.Millisecond

	// fileStabilityCheckDuration is how long to wait to confirm file size is stable.
	// 200ms handles slow I/O on SD cards and network drives.
	fileStabilityCheckDuration = 200 * time.Millisecond

	// readinessBackoffMultiplier is the multiplier for exponential backoff.
	readinessBackoffMultiplier = 2
)

// Sentinel errors for the audio file states the readiness check reports.
var (
	errAudioFileNotReady   = errors.NewStd("audio file is not ready for processing")
	errAudioFileTooSmall   = errors.NewStd("audio file is too small to be valid")
	errAudioFileIncomplete = errors.NewStd("audio file is incomplete or still being written")
	errAudioFileEmpty      = errors.NewStd("audio file is empty")
)

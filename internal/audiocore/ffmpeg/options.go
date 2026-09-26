package ffmpeg

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// Options carries manager-level defaults that apply to every stream a Manager
// starts, as opposed to the per-stream settings on StreamConfig. The zero value
// reproduces the manager's historic behaviour, so the plain NewManager
// constructor (which passes a zero Options) is unaffected.
//
// SilenceTimeout exists so characterization tests can drive the silence
// watchdog in seconds instead of the production 90 s without mutating the
// package-level silenceTimeout constant. FFmpegPath, FFmpegParameters, and
// LogLevel are the manager-level FFmpeg settings the engine used to set on every
// StreamConfig; folding them here lets the engine hand StartStream a
// protocol-neutral audiocore.StreamSpec that carries none of them.
type Options struct {
	// SilenceTimeout overrides the per-stream silence watchdog timeout for every
	// stream started through this manager, unless a stream's own
	// StreamConfig.SilenceTimeout is already set. Zero keeps the production
	// default (silenceTimeout, 90 s).
	//
	// NOTE: the silence-restart error message embeds this value ("stream stopped
	// producing data for N seconds"), and internal/errors/telemetry_integration.go
	// suppresses that error from Sentry by matching the literal 90-second text. A
	// future phase that wires a non-90s value into a PRODUCTION path must update
	// that suppression signature to a prefix match or keep the two in sync.
	SilenceTimeout time.Duration

	// FFmpegPath is the absolute path to the FFmpeg binary applied to every stream
	// this manager starts.
	FFmpegPath string

	// FFmpegParameters are additional FFmpeg command-line parameters applied to
	// every stream this manager starts.
	FFmpegParameters []string

	// LogLevel is the FFmpeg log level (e.g. "error") applied to every stream this
	// manager starts.
	LogLevel string

	// Metrics receives stream health and data-rate samples for every stream this
	// manager starts. Nil-safe: the stream checks for nil before every call.
	Metrics audiocore.StreamMetrics
}

// withManagerDefaults returns cfg with manager-level defaults applied where the
// per-stream config leaves them unset. It never mutates the caller's config:
// when a default has to be applied it returns a shallow copy, otherwise it
// returns cfg unchanged.
func (m *Manager) withManagerDefaults(cfg *StreamConfig) *StreamConfig {
	// Treat any non-positive per-stream value as "unset", matching
	// effectiveSilenceTimeout, so a negative config value cannot both suppress
	// the manager option here and fall back to the default there.
	if cfg.SilenceTimeout <= 0 && m.opts.SilenceTimeout > 0 {
		c := *cfg
		c.SilenceTimeout = m.opts.SilenceTimeout
		return &c
	}
	return cfg
}

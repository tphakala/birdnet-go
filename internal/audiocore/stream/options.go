package stream

import (
	"time"

	"github.com/tphakala/go-audio-stream/supervisor"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// Native ingest defaults. The backoff schedule overrides the go-audio-stream
// library defaults (500 ms / 30 s / 2.0 / 0.2) so the ceiling matches FFmpeg's
// 2 min cap: a camera down for hours is polled every two minutes rather than
// every thirty seconds. The 1 s base is the native starting point (FFmpeg starts
// at 5 s); only the ceiling is held to parity. The cap is a politeness setting
// only; what bounds reconnects is the supervisor plus the liveness watchdog, not
// the cap value.
const (
	// DefaultReadIdle is the supervisor read-idle watchdog window: a session
	// with no delivered frame within this window ends with ErrReadTimeout and
	// reconnects. The engine tightens this to sit in front of the liveness
	// threshold; this is the base before that clamp.
	DefaultReadIdle = 20 * time.Second
	// defaultChunkBytes is the pooled flush unit for dispatched frames (42.7 ms
	// at 48 kHz mono). There is no time-based flush; a chunk emits when full or
	// when the session ends.
	defaultChunkBytes = 4096

	defaultBackoffBase   = 1 * time.Second
	defaultBackoffMax    = 2 * time.Minute
	defaultBackoffFactor = 2.0
	defaultBackoffJitter = 0.2
)

// Options carries manager-level defaults applied to every stream the native
// Manager starts, mirroring ffmpeg.Options. The zero value is valid: a nil or
// zero Options passed to NewManager is filled by applyDefaults. Producer-neutral
// per-stream settings live on audiocore.StreamSpec, not here.
type Options struct {
	// AllowInsecureAuth permits plaintext-credential auth on http and hls
	// sources. It has no effect on RTSP (challenge-response auth) and is unused
	// until Phase 3 adds those source types.
	AllowInsecureAuth bool

	// ReadIdle is the supervisor read-idle window: a session that delivers no
	// frame within it ends with a read timeout and reconnects. It defaults below
	// the liveness watchdog's silence threshold so the supervisor repairs a
	// transport stall in place before the watchdog would escalate. Zero uses
	// DefaultReadIdle.
	ReadIdle time.Duration

	// Backoff parameterizes the supervisor reconnect schedule. Any zero field is
	// filled with the native override defaults above (not the library defaults).
	Backoff supervisor.BackoffConfig

	// ChunkBytes is the pooled flush unit for dispatched AudioFrames. Zero uses
	// defaultChunkBytes.
	ChunkBytes int

	// InsecureTLS opts into skipping certificate verification for rtsps sources
	// (self-signed cameras). A nil pointer defaults to true for parity with the
	// FFmpeg path, which never verified; set a pointer to false to require
	// verification. It is a pointer because the parity default is true and a Go
	// bool cannot distinguish "unset" from "explicitly false".
	InsecureTLS *bool

	// Metrics receives stream health and data-rate samples. Nil-safe: the
	// manager checks for nil before every call.
	Metrics audiocore.StreamMetrics
}

// applyDefaults fills every unset field in place. NewManager applies it once, so
// the rest of the package reads resolved values.
func (o *Options) applyDefaults() {
	if o.ReadIdle <= 0 {
		o.ReadIdle = DefaultReadIdle
	}
	if o.ChunkBytes <= 0 {
		o.ChunkBytes = defaultChunkBytes
	} else if o.ChunkBytes%2 != 0 {
		// s16 samples are 2 bytes, so an odd chunk size would split a sample
		// across a chunk boundary and byte-misalign every subsequent chunk.
		o.ChunkBytes++
	}
	if o.Backoff.Base <= 0 {
		o.Backoff.Base = defaultBackoffBase
	}
	if o.Backoff.Max <= 0 {
		o.Backoff.Max = defaultBackoffMax
	}
	if o.Backoff.Factor <= 1.0 {
		o.Backoff.Factor = defaultBackoffFactor
	}
	if o.Backoff.Jitter <= 0 {
		o.Backoff.Jitter = defaultBackoffJitter
	}
	if o.InsecureTLS == nil {
		t := true
		o.InsecureTLS = &t
	}
}

// insecureTLS reports the effective TLS-verification-skipping setting, treating a
// nil InsecureTLS as the parity default of true.
func (o *Options) insecureTLS() bool {
	return o.InsecureTLS == nil || *o.InsecureTLS
}

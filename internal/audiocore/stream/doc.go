// Package stream is the pure-Go network stream ingest producer. It
// implements audiocore.StreamManager on top of github.com/tphakala/go-audio-stream,
// so the audio engine drives it through the same seam as the FFmpeg producer,
// selected at construction by the BIRDNET_STREAM_INGEST=native gate
// (conf.NativeStreamIngestEnabled). Phase 2 handles RTSP only; other source
// types hard-fail with ErrUnsupportedType.
//
// # Responsibilities
//
// The library owns transport, depacketization, reconnect, and backoff. This
// package owns policy: which track to set up, the audio-only fallback, decoding
// each depacketized frame (AAC via go-aac, Opus via go-opus, MP3 via go-mp3,
// PCM kinds passed through), downmixing to mono, resampling to the analysis
// sample rate, chunking, and dispatching audiocore.AudioFrames into the router.
// It also maps the supervisor's connection lifecycle onto the neutral
// audiocore.StreamHealth model, including a RecoveryState that a liveness
// watchdog can consult. The watchdog-side coordination that lets an ordinary
// supervised reconnect avoid a full source teardown is a planned follow-up; it
// is not wired yet.
//
// # Goroutine model
//
// Each source wraps one github.com/tphakala/go-audio-stream/supervisor.Supervisor,
// which runs a single supervising goroutine that connects, delivers, and
// reconnects. Frame delivery, decode, shape, and dispatch all run inline on the
// supervisor's reader goroutine inside the OnFrame callback; that callback must
// not block, and router.Dispatch never does. State transitions arrive on the
// same supervisor goroutine through OnState. The Manager owns a map of sourceID
// to *stream guarded by a mutex; Shutdown closes every supervisor and waits.
//
// # Buffer contract
//
// The audiostream.Frame handed to OnFrame owns its Data only for the duration of
// the call, so retained audio is copied. Each dispatched audiocore.AudioFrame
// owns its own pooled slice drawn from the buffer.Manager byte pool, carried by
// a FrameRef whose release returns the slice to the pool; a dispatched chunk is
// never a sub-slice of a shared buffer. The producer releases its own reference
// after onFrame returns, mirroring the FFmpeg producer.
package stream

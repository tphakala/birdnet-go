package stream

import (
	"context"
	"strings"
	"time"

	audiostream "github.com/tphakala/go-audio-stream"
	"github.com/tphakala/go-audio-stream/rtsp"
	"github.com/tphakala/go-audio-stream/supervisor"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// rtspDialTimeout bounds the dial and each request round-trip, matching the
// FFmpeg path's 10 s stimeout.
const rtspDialTimeout = 10 * time.Second

// rtspFactory returns the supervisor factory for an RTSP source. It dials,
// discovers and negotiates the audio track (and the video tracks in full-stream
// mode), resolves the decoder before SETUP begins routing frames, and plays,
// returning a delivering Client the supervisor drives.
func (s *stream) rtspFactory() supervisor.Factory {
	return func(ctx context.Context) (audiostream.Source, error) {
		// New session: clear the delivered flag before any frame can arrive, so
		// the audio-only fallback counts only sessions that truly produced no
		// audio. Doing it here (before SETUP begins routing) rather than on the
		// StateConnected transition avoids erasing a frame that arrived first.
		s.deliveredSession.Store(false)

		cfg := rtsp.Config{
			URL:           s.spec.URL,
			Timeout:       rtspDialTimeout,
			ReadIdle:      s.opts.ReadIdle,
			InsecureTLS:   s.opts.insecureTLS(),
			Transport:     mapTransport(s.spec.Transport),
			OnFrame:       s.onFrame,
			OnCodecUpdate: s.onCodecUpdate,
			Logger:        debugSlog(s.spec.Debug, s.log),
		}
		client, err := rtsp.Dial(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if negErr := s.negotiate(ctx, client); negErr != nil {
			// Close signals shutdown but does not drain the reader goroutine;
			// Wait does. SETUP may already have started routing frames into the
			// lock-free pipeline, so block until the reader has stopped before
			// returning, or onFrame could race the supervisor's flush on the
			// error transition.
			_ = client.Close()
			_ = client.Wait(ctx)
			return nil, negErr
		}
		return client, nil
	}
}

// negotiate discovers the tracks, resolves the decoder from the audio track's
// codec BEFORE the first SETUP (routing, and therefore OnFrame, begins at the
// first successful SETUP), sets the tracks up, and plays.
func (s *stream) negotiate(ctx context.Context, client *rtsp.Client) error {
	tracks, err := client.Describe(ctx)
	if err != nil {
		return err
	}
	audioTrack, ok := selectAudioTrack(tracks)
	if !ok {
		return ErrNoAudioTrack
	}
	// Build the decoder before SETUP so it is ready before any frame is routed.
	// A terminal ErrUnsupportedCodec here stops the supervisor via retryable; a
	// pending in-band config (MP4A-LATM cpresent=1) is not terminal, so proceed
	// and let OnCodecUpdate install the decoder after PLAY.
	if setErr := s.pl.setCodec(audioTrack.Codec, audioTrack.Format()); setErr != nil {
		if !errors.Is(setErr, errCodecConfigPending) {
			return setErr
		}
		s.log.Debug("audio codec config pending in-band resolution",
			logger.String("source_id", s.spec.SourceID))
	}
	if setupErr := client.Setup(ctx, audioTrack, rtsp.SetupOptions{}); setupErr != nil {
		return setupErr
	}
	if s.wantFullStream() {
		for _, vt := range videoTracks(tracks) {
			if vErr := client.Setup(ctx, vt, rtsp.SetupOptions{Discard: true}); vErr != nil {
				s.log.Warn("video SETUP failed in full-stream mode",
					logger.String("source_id", s.spec.SourceID),
					logger.Error(vErr))
			}
		}
	}
	return client.Play(ctx)
}

// onFrame decodes and dispatches one delivered frame. It runs on the reader
// goroutine and must not block. A per-frame decode error drops that frame; a
// codec that cannot be decoded at all was already rejected in negotiate.
func (s *stream) onFrame(f audiostream.Frame) { //nolint:gocritic // hugeParam: the by-value Frame signature is mandated by rtsp.Config.OnFrame
	if err := s.pl.process(f.Data); err != nil {
		s.log.Debug("native ingest dropped a frame",
			logger.String("source_id", s.spec.SourceID),
			logger.Error(err))
	}
}

// onCodecUpdate rebuilds the decoder when a track's codec configuration is
// resolved in-band (MP4A-LATM cpresent=1). It runs on the reader goroutine,
// before the first frame under the new config, so it is serialized with onFrame.
func (s *stream) onCodecUpdate(u audiostream.CodecUpdate) {
	format := audiostream.AudioFormat{Codec: u.Codec, Kind: audiostream.PayloadKindFor(u.Codec)}
	if err := s.pl.setCodec(u.Codec, format); err != nil {
		s.log.Warn("in-band codec update produced an unsupported decoder",
			logger.String("source_id", s.spec.SourceID),
			logger.Error(err))
	}
}

// wantFullStream reports whether the video tracks should be set up: either the
// canonical media mode is full-stream (which an unset mode resolves to, matching
// the FFmpeg path via conf.MediaMode.Canonical), or the audio-only fallback has
// latched.
func (s *stream) wantFullStream() bool {
	mode := conf.MediaMode(s.spec.MediaMode).Canonical()
	return mode == conf.MediaModeFullStream || s.fullStreamLatched.Load()
}

// selectAudioTrack picks the first supported audio track. If audio tracks exist
// but none is decodable it returns the first one so negotiate surfaces a clear
// terminal ErrUnsupportedCodec rather than a generic no-audio error.
func selectAudioTrack(tracks []rtsp.Track) (rtsp.Track, bool) {
	var firstAudio *rtsp.Track
	for i := range tracks {
		if tracks[i].Media != audiostream.MediaAudio {
			continue
		}
		if firstAudio == nil {
			firstAudio = &tracks[i]
		}
		if supportedCodec(tracks[i].Codec) {
			return tracks[i], true
		}
	}
	if firstAudio != nil {
		return *firstAudio, true
	}
	return rtsp.Track{}, false
}

// videoTracks returns the video tracks, set up with Discard in full-stream mode.
func videoTracks(tracks []rtsp.Track) []rtsp.Track {
	var out []rtsp.Track
	for i := range tracks {
		if tracks[i].Media == audiostream.MediaVideo {
			out = append(out, tracks[i])
		}
	}
	return out
}

// supportedCodec reports whether the native decode path can handle a codec.
// MP4A-LATM is included even when its AudioSpecificConfig is not yet known: an
// in-band (cpresent=1) config resolves after PLAY via OnCodecUpdate, so the
// track is selectable and the pipeline drops frames until the config arrives.
func supportedCodec(c audiostream.Codec) bool {
	switch c.(type) {
	case audiostream.CodecG711, audiostream.CodecG726, audiostream.CodecL16,
		audiostream.CodecOpus, audiostream.CodecMP3, audiostream.CodecAAC,
		audiostream.CodecMP4ALATM:
		return true
	default:
		return false
	}
}

// mapTransport maps the spec transport onto the library preference: udp prefers
// UDP with TCP fallback, everything else uses TCP interleaved.
func mapTransport(t string) rtsp.TransportPreference {
	if strings.EqualFold(strings.TrimSpace(t), "udp") {
		return rtsp.PreferUDPThenTCP
	}
	return rtsp.PreferTCP
}

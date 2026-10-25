package ffmpeg

import (
	"context"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// ffmpegTimeoutParam is the FFmpeg flag name for the connection timeout parameter.
const ffmpegTimeoutParam = "-timeout"

// ffmpegStimeoutMaxMajor is the first FFmpeg major version that removed -stimeout.
// FFmpeg < 5 needs -stimeout for RTSP; 5.x+ uses -timeout.
const ffmpegStimeoutMaxMajor = 5

// ffmpegRTSPTimeoutParam is the RTSP timeout flag for FFmpeg 5.x+ and unknown versions.
const ffmpegRTSPTimeoutParam = "-timeout"

// ffmpegLegacyRTSPTimeoutParam is the RTSP timeout flag required by FFmpeg 4.x.
const ffmpegLegacyRTSPTimeoutParam = "-stimeout"

// ffmpegAllowedMediaTypesFlag restricts the RTSP demuxer to SETUP only the
// listed media types during the RTSP handshake. BirdNET-Go consumes audio only,
// so requesting audio alone avoids opening a video client slot on cameras with a
// limited number of RTSP sessions (issue #3798). Shared by every RTSP-opening
// path: live capture (buildInputArgs), stream inspection (buildProbeArgs), and
// channel-energy analysis (buildAnalysisArgs), so they stay in lockstep.
const ffmpegAllowedMediaTypesFlag = "-allowed_media_types"

// ffmpegAllowedMediaTypesAudio is the value paired with ffmpegAllowedMediaTypesFlag
// to accept audio tracks only.
const ffmpegAllowedMediaTypesAudio = "audio"

// isRTSPURL reports whether a raw URL string is an RTSP or RTSPS stream. Used by
// the probe and channel-analysis paths, which take a bare URL rather than a
// StreamConfig, to decide whether RTSP-specific FFmpeg flags apply.
func isRTSPURL(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "rtsp://") || strings.HasPrefix(lower, "rtsps://")
}

// appendRTSPMediaArgs appends the RTSP transport flag and, when audioOnly is
// true, the -allowed_media_types audio restriction. audioOnly is false only when
// a camera has proven it cannot SETUP the audio track alone and the caller has
// fallen back to requesting the full stream (issue #3902); video is still
// dropped after decode via -vn, so the fallback only affects the RTSP handshake.
func appendRTSPMediaArgs(args []string, transport string, audioOnly bool) []string {
	// Final safety net: never emit an empty -rtsp_transport. A blank value makes
	// FFmpeg fail to parse the option ("Error setting option rtsp_transport to
	// value") and the stream never opens. Callers should resolve the transport
	// upstream, but a missing or misconfigured value falls back to the default.
	if transport == "" {
		transport = conf.DefaultTransport
	}
	args = append(args, "-rtsp_transport", transport)
	if audioOnly {
		args = append(args, ffmpegAllowedMediaTypesFlag, ffmpegAllowedMediaTypesAudio)
	}
	return args
}

// ffmpegMajorCache stores detected FFmpeg major versions by binary path.
var ffmpegMajorCache sync.Map

// ffmpegMajorGroup coalesces concurrent first-time version probes for the same
// binary path, so a burst of streams starting at once spawns a single
// `ffmpeg -version` process instead of one per stream.
var ffmpegMajorGroup singleflight.Group

// rtspTimeoutParamForMajor returns the RTSP connection-timeout flag for the
// given FFmpeg major version: -stimeout below 5 (FFmpeg 4.x), -timeout on 5.x+
// and unknown versions. This is the single source of truth shared by the live
// capture path (timeoutParamForSource) and the channel-energy analysis path
// (buildAnalysisArgs), so both stay in lockstep.
func rtspTimeoutParamForMajor(ffmpegMajor int) string {
	if ffmpegMajor > 0 && ffmpegMajor < ffmpegStimeoutMaxMajor {
		return ffmpegLegacyRTSPTimeoutParam
	}
	return ffmpegRTSPTimeoutParam
}

// timeoutParamForSource returns the correct FFmpeg timeout flag for the given source type.
func timeoutParamForSource(st audiocore.SourceType, ffmpegMajor int) string {
	if st == audiocore.SourceTypeRTSP {
		return rtspTimeoutParamForMajor(ffmpegMajor)
	}
	return ffmpegTimeoutParam
}

// ffmpegVersionProbeTimeout bounds the one-time `ffmpeg -version` probe so a
// hung or wrapped binary cannot stall stream startup. On timeout the probe
// yields major 0, which falls back to the safe -timeout default. A killed probe
// can take up to the 2 second wait delay of conf.GetFfmpegVersionFromContext
// longer to return, so the worst case is this timeout plus that delay.
const ffmpegVersionProbeTimeout = 5 * time.Second

// resolveFfmpegMajor returns the cached or detected FFmpeg major version for a binary path.
func resolveFfmpegMajor(ffmpegPath string) int {
	if ffmpegPath == "" {
		return 0
	}
	if cached, ok := ffmpegMajorCache.Load(ffmpegPath); ok {
		if major, ok := cached.(int); ok {
			return major
		}
	}

	// Coalesce concurrent cold-cache probes for the same path into one
	// `ffmpeg -version` execution.
	val, _, _ := ffmpegMajorGroup.Do(ffmpegPath, func() (any, error) {
		// Re-check the cache: another caller may have populated it while this
		// call was waiting for the singleflight slot.
		if cached, ok := ffmpegMajorCache.Load(ffmpegPath); ok {
			if major, ok := cached.(int); ok {
				return major, nil
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), ffmpegVersionProbeTimeout)
		defer cancel()

		_, major, _ := conf.GetFfmpegVersionFromContext(ctx, ffmpegPath)
		// Only cache a successful detection. Caching a failed probe (major 0)
		// would pin the safe -timeout fallback for the process lifetime, so a
		// transient first-probe failure on a real FFmpeg 4.x host would silently
		// suppress -stimeout until restart. Re-probing on the next start is cheap.
		if major > 0 {
			ffmpegMajorCache.Store(ffmpegPath, major)
		}
		return major, nil
	})

	if major, ok := val.(int); ok {
		return major
	}
	return 0
}

// stripTimeoutParams returns a copy of params with any -timeout/-stimeout key-value pairs removed.
func stripTimeoutParams(params []string) []string {
	out := make([]string, 0, len(params))
	skipNext := false
	for _, param := range params {
		if skipNext {
			skipNext = false
			continue
		}
		if param == ffmpegTimeoutParam || param == ffmpegRTSPTimeoutParam || param == ffmpegLegacyRTSPTimeoutParam {
			skipNext = true
			continue
		}
		out = append(out, param)
	}
	return out
}

// BuildFFmpegArgs constructs the complete FFmpeg argument list for a streaming source.
// It is a pure function suitable for unit testing. It produces the same output
// arguments as the runtime path (Stream.startProcess) by sharing buildOutputArgs;
// only the input-argument timeout warning logging differs between the two.
//
// RTSP-specific flags like -rtsp_transport are only added for RTSP sources.
// A default -timeout is added unless the caller supplies one via ffmpegParameters.
func BuildFFmpegArgs(cfg *StreamConfig, ffmpegParameters []string) []string {
	// Represent the initial request (no reactive fallback engaged yet). The audio
	// mode is decided by resolveAudioOnly so this mirror matches the runtime path
	// (buildFFmpegInputArgs) under every media mode.
	args := buildInputArgs(cfg, ffmpegParameters, resolveAudioOnly(cfg, false))
	return buildOutputArgs(args, cfg)
}

// resolveAudioOnly reports whether the stream should request audio-only RTSP
// media (-allowed_media_types audio). It is the single source of truth for that
// decision, shared by the runtime path (Stream.buildFFmpegInputArgs) and the
// unit-tested mirror (BuildFFmpegArgs), so the two cannot diverge.
//
// fallbackEngaged is consulted only in auto mode; audio-only and full-stream are
// deterministic and ignore it. An empty mode canonicalizes to the default
// (full-stream); any other unrecognized value (rejected at config validation)
// falls through to the default branch and is also treated as full-stream.
func resolveAudioOnly(cfg *StreamConfig, fallbackEngaged bool) bool {
	switch conf.MediaMode(cfg.MediaMode).Canonical() {
	case conf.MediaModeAudioOnly:
		return true
	case conf.MediaModeFullStream:
		return false
	case conf.MediaModeAuto:
		// Audio-only first, dropping the restriction once the reactive fallback
		// latches because the camera cannot deliver audio alone (issue #3902).
		return !fallbackEngaged
	default:
		return false
	}
}

// mediaModeAllowsFallback reports whether the reactive audio-only fallback may
// engage for this stream. Only auto mode allows it; audio-only fails visibly
// rather than falling back, and full-stream never requested audio-only at all.
func mediaModeAllowsFallback(cfg *StreamConfig) bool {
	return conf.MediaMode(cfg.MediaMode).Canonical() == conf.MediaModeAuto
}

// effectiveMediaMode returns the canonical media mode as a string, for logging.
func effectiveMediaMode(cfg *StreamConfig) string {
	return string(conf.MediaMode(cfg.MediaMode).Canonical())
}

// buildOutputArgs appends the post-input FFmpeg flags: the input URL, decode
// format, channel selection, conditional output resampling, and the stdout pipe.
// It is shared by BuildFFmpegArgs and Stream.startProcess so the unit-tested
// argument construction matches the runtime path exactly.
//
// -ac (channel count) is always emitted via appendChannelArgs so multi-channel
// sources are downmixed to mono. -ar (sample rate) is only emitted when the
// source rate is unknown or differs from the target, avoiding a needless
// resample when the source already matches.
func buildOutputArgs(args []string, cfg *StreamConfig) []string {
	sampleRate, numChannels, format := GetFFmpegFormat(cfg.SampleRate, cfg.Channels, cfg.BitDepth)

	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "error"
	}

	args = append(args,
		"-i", cfg.URL,
		"-loglevel", logLevel,
		"-vn",
		"-f", format,
	)
	args = appendChannelArgs(args, cfg.ChannelMode, cfg.SourceChannels, numChannels)
	if cfg.needsOutputResampling() {
		args = append(args, "-ar", sampleRate)
	}
	args = append(args, "-hide_banner", "pipe:1")

	return args
}

// channelModeLeft and channelModeRight are the ChannelMode values that make
// appendChannelArgs insert a pan filter and force mono output. They are shared
// with effectiveOutputChannels so the frame-size derivation and the argument
// builder cannot drift apart.
const (
	channelModeLeft  = "left"
	channelModeRight = "right"
)

// appendChannelArgs appends the appropriate FFmpeg channel selection flags.
// When channelMode is "left" or "right" and the source has >1 channel,
// it uses a pan filter to extract the selected channel. Falls back to
// simple -ac downmix when the source is mono or mode is "downmix"/empty.
func appendChannelArgs(args []string, channelMode string, sourceChannels int, numChannels string) []string {
	if sourceChannels > 1 {
		switch strings.ToLower(channelMode) {
		case channelModeLeft:
			return append(args, "-af", "pan=mono|c0=c0", "-ac", "1")
		case channelModeRight:
			return append(args, "-af", "pan=mono|c0=c1", "-ac", "1")
		}
	}
	return append(args, "-ac", numChannels)
}

// effectiveOutputChannels returns the channel count of the PCM stream FFmpeg
// actually emits for cfg. The left and right channel modes make
// appendChannelArgs pan a multi-channel source down to mono regardless of
// cfg.Channels, so readers of the output stream must use this, not the
// configured count. Every other configuration emits cfg.Channels unchanged.
func effectiveOutputChannels(cfg *StreamConfig) int {
	if cfg.SourceChannels > 1 {
		switch strings.ToLower(cfg.ChannelMode) {
		case channelModeLeft, channelModeRight:
			return 1
		}
	}
	return cfg.Channels
}

// buildInputArgs constructs the pre-input FFmpeg flags (transport, timeout, extra parameters).
// This mirrors the logic in Stream.buildFFmpegInputArgs but accepts explicit parameters.
// RTSP streams use the timeout flag supported by the configured FFmpeg major version.
// When audioOnly is false, the RTSP handshake is not restricted to audio tracks
// (the full-stream fallback for cameras that cannot SETUP audio alone, issue #3902).
func buildInputArgs(cfg *StreamConfig, ffmpegParameters []string, audioOnly bool) []string {
	args := make([]string, 0, 8+len(ffmpegParameters))
	ffmpegMajor := resolveFfmpegMajor(cfg.FFmpegPath)
	timeoutFlag := timeoutParamForSource(cfg.sourceType(), ffmpegMajor)

	if cfg.sourceType() == audiocore.SourceTypeRTSP {
		args = appendRTSPMediaArgs(args, cfg.Transport, audioOnly)
	}

	hasUserTimeout, userTimeoutValue := detectUserTimeout(ffmpegParameters)

	if !hasUserTimeout {
		args = append(args, timeoutFlag, strconv.FormatInt(defaultTimeoutMicroseconds, 10))
		if len(ffmpegParameters) > 0 {
			args = append(args, ffmpegParameters...)
		}
		return args
	}

	if err := validateTimeout(userTimeoutValue); err != nil {
		args = append(args, timeoutFlag, strconv.FormatInt(defaultTimeoutMicroseconds, 10))
	} else {
		args = append(args, timeoutFlag, userTimeoutValue)
	}
	args = append(args, stripTimeoutParams(ffmpegParameters)...)

	return args
}

// validateTimeout validates a user-provided timeout string value in microseconds.
// Returns an error if the value is not a valid integer or is below the minimum.
func validateTimeout(timeoutStr string) error {
	timeout, err := strconv.ParseInt(timeoutStr, 10, 64)
	if err != nil {
		return errors.Newf("invalid timeout format: %s (must be a number in microseconds)", timeoutStr).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_timeout").
			Context("timeout_value", timeoutStr).
			Build()
	}

	if timeout < minTimeoutMicroseconds {
		return errors.Newf("timeout too short: %d microseconds (minimum: %d microseconds = 1 second)", timeout, minTimeoutMicroseconds).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_timeout").
			Context("timeout_microseconds", timeout).
			Context("minimum_microseconds", minTimeoutMicroseconds).
			Build()
	}

	return nil
}

// CalculateBackoff computes the exponential backoff duration for a given restart count.
// It adds a random jitter of up to restartJitterPercentMax percent of the base backoff.
// The returned duration is always at least base and at most maxBackoff plus that jitter.
func CalculateBackoff(restartCount int, base, maxBackoff time.Duration) time.Duration {
	return applyBackoffJitter(computeBaseBackoff(restartCount, base, maxBackoff))
}

// computeBaseBackoff returns the capped exponential backoff for a restart count,
// without jitter. The exponent is clamped to [0, maxBackoffExponent] and the
// result is capped at maxBackoff.
func computeBaseBackoff(restartCount int, base, maxBackoff time.Duration) time.Duration {
	exponent := max(restartCount-1, 0)
	exponent = min(exponent, maxBackoffExponent)

	return min(base*time.Duration(1<<uint(exponent)), maxBackoff) //nolint:gosec // G115: exponent is capped by maxBackoffExponent
}

// applyBackoffJitter adds a random jitter of up to restartJitterPercentMax percent
// on top of backoff to prevent a thundering herd of simultaneous reconnects. A
// non-positive backoff (or jitter range) returns backoff unchanged.
func applyBackoffJitter(backoff time.Duration) time.Duration {
	if backoff <= 0 {
		return backoff
	}

	jitterRange := backoff * restartJitterPercentMax / 100
	if jitterRange <= 0 {
		return backoff
	}

	return backoff + time.Duration(rand.Int64N(jitterRange.Nanoseconds())) //nolint:gosec // G404: non-cryptographic jitter to spread out reconnects
}

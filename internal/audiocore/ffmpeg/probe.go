package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// MinBatSampleRate is the minimum source capture rate in Hz for bat detection
// models. Streams below this rate cannot carry ultrasonic content.
const MinBatSampleRate = 96000

// ErrNoAudioStreamsFound reports that ffprobe found no audio streams in the input.
var ErrNoAudioStreamsFound = errors.NewStd("no audio streams found in probe output")

// probeTimeout is the maximum time to wait for ffprobe stream probing.
// This is separate from FFprobeTimeout (3s) used for local file validation;
// live streams may need longer to connect and negotiate.
const probeTimeout = 10 * time.Second

// lossyCodecs lists codecs that destroy ultrasonic content, making them
// unsuitable for bat detection which relies on high-frequency audio.
var lossyCodecs = map[string]bool{
	"aac":    true,
	"mp3":    true,
	"opus":   true,
	"vorbis": true,
	"wmav2":  true,
	"ac3":    true,
	"eac3":   true,
}

// StreamInfo holds the audio properties discovered by probing a stream.
type StreamInfo struct {
	SampleRate int
	Channels   int
	Codec      string
	BitDepth   int
}

// ffprobeOutput represents the top-level JSON structure returned by ffprobe.
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
}

// ffprobeStream represents a single stream entry in ffprobe JSON output.
type ffprobeStream struct {
	CodecName     string `json:"codec_name"`
	CodecType     string `json:"codec_type"`
	SampleRate    string `json:"sample_rate"`
	Channels      int    `json:"channels"`
	BitsPerSample int    `json:"bits_per_sample"`
}

// ProbeStreamInfo probes a live audio stream via ffprobe and returns its
// audio properties. It applies a 10-second timeout and sanitizes the URL
// in error messages to avoid leaking credentials.
func ProbeStreamInfo(ctx context.Context, url string) (*StreamInfo, error) {
	ffprobeBinary, err := resolveFFprobeBinary()
	if err != nil {
		return nil, err
	}

	info, err := runProbe(ctx, ffprobeBinary, url, true)
	// Some cameras cannot SETUP the RTSP audio track alone, so the audio-only
	// restriction breaks the handshake (issue #3902). Retry once requesting the
	// full stream. Skip the retry when the parent context is done (shutdown/
	// caller cancel), so a cancel is not spent on a doomed second attempt; a
	// per-attempt probe timeout leaves ctx live, so a hung audio-only handshake
	// still falls back. ErrNoAudioStreamsFound means the handshake succeeded but
	// the input has no audio, which the fallback cannot fix, so skip it there too.
	if err != nil && ctx.Err() == nil && isRTSPURL(url) && !errors.Is(err, ErrNoAudioStreamsFound) {
		info, err = runProbe(ctx, ffprobeBinary, url, false)
	}
	return info, err
}

// runProbe executes a single ffprobe attempt. audioOnly controls whether the
// RTSP handshake is restricted to audio tracks via -allowed_media_types audio.
func runProbe(ctx context.Context, ffprobeBinary, url string, audioOnly bool) (*StreamInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := buildProbeArgs(url, audioOnly)

	cmd := exec.CommandContext(probeCtx, ffprobeBinary, args...) //nolint:gosec // G204: ffprobeBinary validated by config or exec.LookPath, URL from user config
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		safeURL := privacy.SanitizeStreamUrl(url)
		if probeCtx.Err() != nil {
			return nil, fmt.Errorf("stream probe timed out for %s: %w", safeURL, probeCtx.Err())
		}
		return nil, fmt.Errorf("stream probe failed for %s: %w (stderr: %s)", safeURL, err, privacy.SanitizeStreamUrls(stderr.String()))
	}

	info, err := parseProbeOutput(stdout.Bytes())
	if err != nil {
		safeURL := privacy.SanitizeStreamUrl(url)
		return nil, fmt.Errorf("failed to parse probe output for %s: %w", safeURL, err)
	}

	return info, nil
}

// IsLossyCodec reports whether the given codec destroys ultrasonic content,
// making it unsuitable for bat detection. The check is case-insensitive.
func IsLossyCodec(codec string) bool {
	return lossyCodecs[strings.ToLower(codec)]
}

// parseProbeOutput parses ffprobe JSON output and extracts audio stream info.
func parseProbeOutput(data []byte) (*StreamInfo, error) {
	var output ffprobeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("invalid ffprobe JSON: %w", err)
	}

	if len(output.Streams) == 0 {
		return nil, ErrNoAudioStreamsFound
	}

	stream := output.Streams[0]

	rateStr := stream.SampleRate
	if i := strings.Index(rateStr, "/"); i != -1 {
		rateStr = rateStr[:i]
	}
	sampleRate, err := strconv.Atoi(rateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid sample rate %q: %w", stream.SampleRate, err)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate %q: must be positive", stream.SampleRate)
	}

	return &StreamInfo{
		SampleRate: sampleRate,
		Channels:   stream.Channels,
		Codec:      stream.CodecName,
		BitDepth:   stream.BitsPerSample,
	}, nil
}

// buildProbeArgs constructs the ffprobe argument list for the given URL.
// For RTSP/RTSPS URLs, it prepends -rtsp_transport tcp to force TCP transport.
// When audioOnly is true it also restricts the handshake to audio tracks; the
// fallback path passes false for cameras that cannot SETUP audio alone (#3902).
func buildProbeArgs(url string, audioOnly bool) []string {
	args := make([]string, 0, 10)

	// For RTSP streams, force TCP transport before the input URL and restrict the
	// handshake to audio tracks so ffprobe never SETUPs the camera's video track
	// (issue #3798); -select_streams below only filters ffprobe's output.
	if isRTSPURL(url) {
		args = appendRTSPMediaArgs(args, "tcp", audioOnly)
	}

	args = append(args,
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-select_streams", "a:0",
		"-protocol_whitelist", "http,https,tcp,tls,crypto,rtsp,rtp,udp,rtmp",
		url,
	)

	return args
}

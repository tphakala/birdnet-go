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
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	ffprobeBinary, err := resolveFFprobeBinary()
	if err != nil {
		return nil, err
	}

	args := buildProbeArgs(url)

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
		return nil, errors.NewStd("no audio streams found in probe output")
	}

	stream := output.Streams[0]

	sampleRate, err := strconv.Atoi(stream.SampleRate)
	if err != nil {
		return nil, fmt.Errorf("invalid sample rate %q: %w", stream.SampleRate, err)
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
func buildProbeArgs(url string) []string {
	args := make([]string, 0, 10)

	// For RTSP streams, force TCP transport before the input URL.
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "rtsp://") || strings.HasPrefix(lower, "rtsps://") {
		args = append(args, "-rtsp_transport", "tcp")
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

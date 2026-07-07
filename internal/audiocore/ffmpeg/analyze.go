package ffmpeg

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	analysisCaptureDuration = 3
	analysisSampleRate      = 48000
	analysisChannels        = 2
	energyThresholdDb       = 6.0
	silenceFloorDbfs        = -96.0
	analysisTimeout         = 15 * time.Second
)

// ChannelEnergy holds the measured energy for a single audio channel.
type ChannelEnergy struct {
	Channel int     `json:"channel"`
	Label   string  `json:"label"`
	RmsDbfs float64 `json:"rmsDbfs"`
}

// ChannelAnalysis holds the result of a multi-channel energy analysis.
type ChannelAnalysis struct {
	Channels    int             `json:"channels"`
	Energy      []ChannelEnergy `json:"energy"`
	Recommended string          `json:"recommended"`
}

// AnalyzeChannelEnergy captures a short stereo sample from the given URL and
// computes per-channel RMS energy to recommend which channel to use.
// If ffmpegPath is empty, the binary is resolved via exec.LookPath.
func AnalyzeChannelEnergy(ctx context.Context, url, ffmpegPath string) (*ChannelAnalysis, error) {
	ffmpegBinary := ffmpegPath
	if ffmpegBinary == "" {
		var err error
		ffmpegBinary, err = resolveFFmpegBinary()
		if err != nil {
			return nil, err
		}
	}

	// Resolve the FFmpeg major version before starting the analysis timeout
	// window, so a cold-cache `ffmpeg -version` probe does not eat into the
	// time budget for the actual analysis run.
	ffmpegMajor := resolveFfmpegMajor(ffmpegBinary)

	analysisCtx, cancel := context.WithTimeout(ctx, analysisTimeout)
	defer cancel()

	args := buildAnalysisArgs(url, ffmpegMajor)

	cmd := exec.CommandContext(analysisCtx, ffmpegBinary, args...) //nolint:gosec // G204: ffmpegBinary validated by exec.LookPath, URL from user config
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		safeURL := privacy.SanitizeStreamUrl(url)
		if analysisCtx.Err() != nil {
			return nil, errors.Newf("channel analysis timed out for %s: %w", safeURL, analysisCtx.Err()).
				Component("ffmpeg").
				Category(errors.CategoryAudio).
				Build()
		}
		return nil, errors.Newf("channel analysis failed for %s: %w (stderr: %s)", safeURL, err, privacy.SanitizeStreamUrls(stderr.String())).
			Component("ffmpeg").
			Category(errors.CategoryAudio).
			Build()
	}

	pcmData := stdout.Bytes()
	minExpectedBytes := analysisSampleRate * analysisChannels * 2
	if len(pcmData) < minExpectedBytes {
		return nil, errors.Newf("channel analysis produced insufficient data (%d bytes, expected at least %d)", len(pcmData), minExpectedBytes).
			Component("ffmpeg").
			Category(errors.CategoryAudio).
			Build()
	}

	left, right := deinterleave(pcmData)

	leftDbfs := computeRmsDbfs(left)
	rightDbfs := computeRmsDbfs(right)

	return &ChannelAnalysis{
		Channels: analysisChannels,
		Energy: []ChannelEnergy{
			{Channel: 0, Label: "Left", RmsDbfs: math.Round(leftDbfs*10) / 10},
			{Channel: 1, Label: "Right", RmsDbfs: math.Round(rightDbfs*10) / 10},
		},
		Recommended: recommendChannel(leftDbfs, rightDbfs),
	}, nil
}

func buildAnalysisArgs(url string, ffmpegMajor int) []string {
	args := make([]string, 0, 16)

	lower := strings.ToLower(url)
	isRTSP := strings.HasPrefix(lower, "rtsp://") || strings.HasPrefix(lower, "rtsps://")
	// Default to -timeout; RTSP on FFmpeg 4.x needs -stimeout instead, since 4.x
	// treats -timeout on the RTSP demuxer as a listen timeout and fails to
	// connect. Mirror the live capture path's flag selection.
	timeoutFlag := ffmpegTimeoutParam
	if isRTSP {
		// Restrict the RTSP handshake to audio tracks; -vn below only drops video
		// after decode, so without this the camera's video track is still SETUP
		// (issue #3798).
		args = append(args, "-rtsp_transport", "tcp", ffmpegAllowedMediaTypesFlag, ffmpegAllowedMediaTypesAudio)
		timeoutFlag = rtspTimeoutParamForMajor(ffmpegMajor)
	}

	args = append(args,
		timeoutFlag, strconv.FormatInt(defaultTimeoutMicroseconds, 10),
		"-i", url,
		"-t", strconv.Itoa(analysisCaptureDuration),
		"-loglevel", "error",
		"-vn",
		"-f", "s16le",
		"-ar", strconv.Itoa(analysisSampleRate),
		"-ac", strconv.Itoa(analysisChannels),
		"-hide_banner",
		"pipe:1",
	)
	return args
}

func deinterleave(pcm []byte) (left, right []int16) {
	samplesPerChannel := len(pcm) / 4
	left = make([]int16, 0, samplesPerChannel)
	right = make([]int16, 0, samplesPerChannel)

	for i := 0; i+3 < len(pcm); i += 4 {
		left = append(left, int16(binary.LittleEndian.Uint16(pcm[i:i+2])))
		right = append(right, int16(binary.LittleEndian.Uint16(pcm[i+2:i+4])))
	}
	return left, right
}

func computeRmsDbfs(samples []int16) float64 {
	if len(samples) == 0 {
		return silenceFloorDbfs
	}

	var sumSquares float64
	for _, s := range samples {
		v := float64(s)
		sumSquares += v * v
	}

	rms := math.Sqrt(sumSquares / float64(len(samples)))
	if rms < 1.0 {
		return silenceFloorDbfs
	}

	return 20 * math.Log10(rms/32768.0)
}

func recommendChannel(leftDbfs, rightDbfs float64) string {
	const activeThresholdDb = -60.0
	if leftDbfs < activeThresholdDb && rightDbfs < activeThresholdDb {
		return "downmix"
	}

	diff := leftDbfs - rightDbfs
	if diff > energyThresholdDb {
		return "left"
	}
	if diff < -energyThresholdDb {
		return "right"
	}
	return "downmix"
}

func resolveFFmpegBinary() (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.Newf("ffmpeg not found in PATH: %w", err).
			Component("ffmpeg").
			Category(errors.CategorySystem).
			Build()
	}
	return path, nil
}

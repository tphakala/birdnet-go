//go:build normcompare

package normbench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
)

// This file holds the FFmpeg side of the comparison: the loudnorm statistics
// type and the analysis pass that production no longer contains. They exec the
// ffmpeg binary this harness already requires, so the package depends on no
// production FFmpeg code.

// loudnormMaxOffsetDB bounds the true-peak anchored offset of the gate fallback
// in loudnormFilterFromStats, the ceiling the removed export path used.
const loudnormMaxOffsetDB = 60.0

// loudnessStats holds the statistics FFmpeg's loudnorm filter prints with
// print_format=json.
type loudnessStats struct {
	InputI            string `json:"input_i"`
	InputTP           string `json:"input_tp"`
	InputLRA          string `json:"input_lra"`
	InputThresh       string `json:"input_thresh"`
	OutputI           string `json:"output_i"`
	OutputTP          string `json:"output_tp"`
	OutputLRA         string `json:"output_lra"`
	OutputThresh      string `json:"output_thresh"`
	NormalizationType string `json:"normalization_type"`
	TargetOffset      string `json:"target_offset"`
}

// isValid reports whether every field the second loudnorm pass consumes is a
// finite number. The removed export path gated its two-pass branch on exactly
// this check: FFmpeg reports "-inf" for input_i and input_thresh on a clip that
// gates to nothing, and any one unparseable field sent that path to its fallback.
func (s *loudnessStats) isValid() bool {
	for _, v := range []string{s.InputI, s.InputTP, s.InputLRA, s.InputThresh, s.TargetOffset} {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

// loudnessJSONPattern matches the JSON block loudnorm prints with print_format=json.
var loudnessJSONPattern = regexp.MustCompile(`(?s)\{[^}]*"input_i"\s*:[^}]*\}`)

// parseLoudnessJSON extracts loudnessStats from FFmpeg stderr output.
func parseLoudnessJSON(stderr string) (*loudnessStats, error) {
	match := loudnessJSONPattern.FindString(stderr)
	if match == "" {
		return nil, fmt.Errorf("no loudnorm JSON found in FFmpeg output")
	}
	var stats loudnessStats
	if err := json.Unmarshal([]byte(match), &stats); err != nil {
		return nil, fmt.Errorf("failed to parse loudnorm JSON: %w", err)
	}
	if stats.InputI == "" {
		return nil, fmt.Errorf("loudnorm analysis returned empty input_i")
	}
	return &stats, nil
}

// analyzeLoudness runs FFmpeg's loudnorm analysis pass over a file and returns
// the measured statistics.
func analyzeLoudness(ctx context.Context, ffmpegBin, path string) (*loudnessStats, error) {
	filter := fmt.Sprintf("loudnorm=I=%.1f:LRA=%.1f:TP=%.1f:print_format=json", targetLUFS, targetLRA, targetTruePeak)
	cmd := exec.CommandContext(ctx, ffmpegBin, //nolint:gosec // G204: test-controlled binary and path
		"-hide_banner", "-i", path, "-af", filter, "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("loudness analysis cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("loudness analysis failed: %w, stderr: %s", err, stderr.String())
	}
	return parseLoudnessJSON(stderr.String())
}

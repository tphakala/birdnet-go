package main

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreCalibration(t *testing.T) {
	t.Parallel()

	perch := &family{key: "perch-v2", requireScoreCalibration: true}
	v30 := &family{key: "birdnet-v3.0"}
	good := &manifestScoreCalibration{Method: scoreCalibrationMethod, RestLogit: new(9.75)}

	tests := []struct {
		name    string
		fam     *family
		sc      *manifestScoreCalibration
		want    float64
		wantErr string
	}{
		{name: "perch with a valid block", fam: perch, sc: good, want: 9.75},
		{name: "perch missing the block", fam: perch, wantErr: "missing score_calibration"},
		{name: "perch missing the value", fam: perch, sc: &manifestScoreCalibration{Method: scoreCalibrationMethod}, wantErr: "missing score_calibration"},
		{name: "perch unknown method", fam: perch, sc: &manifestScoreCalibration{Method: "other", RestLogit: new(9.75)}, wantErr: "unknown score_calibration method"},
		{name: "perch below the range", fam: perch, sc: &manifestScoreCalibration{Method: scoreCalibrationMethod, RestLogit: new(7.99)}, wantErr: "outside"},
		{name: "perch above the range", fam: perch, sc: &manifestScoreCalibration{Method: scoreCalibrationMethod, RestLogit: new(12.01)}, wantErr: "outside"},
		{name: "perch NaN", fam: perch, sc: &manifestScoreCalibration{Method: scoreCalibrationMethod, RestLogit: new(math.NaN())}, wantErr: "outside"},
		{name: "perch lower bound is accepted", fam: perch, sc: &manifestScoreCalibration{Method: scoreCalibrationMethod, RestLogit: new(scoreCalibrationRestLogitMin)}, want: scoreCalibrationRestLogitMin},
		{name: "v3.0 without the block", fam: v30},
		{name: "v3.0 with the block", fam: v30, sc: good, wantErr: "not allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, has, err := tt.fam.scoreCalibration(&manifestModel{Variant: "v", Region: "r", ScoreCalibration: tt.sc})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.fam.requireScoreCalibration, has)
			assert.InDelta(t, tt.want, got, 0)
		})
	}
}

// TestVariant_LabelsChecksumMustMatchSidecar checks the generator refuses a manifest
// whose labels_sha256 disagrees with the sidecar, so a calibration cannot be attached
// to labels it was not fitted on, and that the calibration reaches the rendered source.
func TestVariant_LabelsChecksumMustMatchSidecar(t *testing.T) {
	t.Parallel()

	const labelsRemote = "regional/x/labels.txt"
	fam := &family{
		key: "perch-v2", repo: "o/r", requireScoreCalibration: true,
		modelLocalName:  func(slug, token string) string { return slug + token },
		labelsLocalName: func(slug string) string { return slug + ".txt" },
	}
	table := &regionTable{Regions: map[string]struct {
		Classes *int `json:"classes"`
	}{"x": {Classes: new(3)}}}
	sidecar := map[string]labelChecksum{"o/r|" + labelsRemote: {SHA256: "aa", SizeBytes: 9, Lines: 3}}
	entry := func(labelsSHA string) *manifestModel {
		return &manifestModel{
			Path: "regional/x/m.onnx", Variant: "no-dft-fp32", Region: "x", Labels: labelsRemote, SHA256: "bb", SizeBytes: 1,
			Requirements:     &manifestRequirements{MinRAMMB: 1},
			LabelsSHA256:     labelsSHA,
			ScoreCalibration: &manifestScoreCalibration{Method: scoreCalibrationMethod, RestLogit: new(9.75)},
		}
	}

	_, err := fam.variant(entry("cc"), table, sidecar)
	require.ErrorContains(t, err, "labels_sha256")

	_, err = fam.variant(entry(""), table, sidecar)
	require.ErrorContains(t, err, "labels_sha256", "a calibrated entry must name the labels file it was fitted on")

	// A family without calibration keeps accepting an entry that has no labels_sha256.
	plain := *fam
	plain.requireScoreCalibration = false
	noCal := entry("")
	noCal.ScoreCalibration = nil
	_, err = plain.variant(noCal, table, sidecar)
	require.NoError(t, err)

	v, err := fam.variant(entry("aa"), table, sidecar)
	require.NoError(t, err)
	assert.Contains(t, render(&v), "ScoreCalibration: &ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: 9.75},")
}

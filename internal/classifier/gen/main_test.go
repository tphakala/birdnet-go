package main

import (
	"fmt"
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

	_, err := fam.variant(entry("cc"), table, sidecar, nil)
	require.ErrorContains(t, err, "labels_sha256")

	_, err = fam.variant(entry(""), table, sidecar, nil)
	require.ErrorContains(t, err, "labels_sha256", "a calibrated entry must name the labels file it was fitted on")

	// A family without calibration keeps accepting an entry that has no labels_sha256.
	plain := *fam
	plain.requireScoreCalibration = false
	noCal := entry("")
	noCal.ScoreCalibration = nil
	_, err = plain.variant(noCal, table, sidecar, nil)
	require.NoError(t, err)

	v, err := fam.variant(entry("aa"), table, sidecar, nil)
	require.NoError(t, err)
	assert.Contains(t, render(&v), "ScoreCalibration: &ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: 9.75},")
}

func TestCheckRegionCounts(t *testing.T) {
	t.Parallel()

	// uniform returns expectedRegions regions with n variants each, all live.
	uniform := func(n int) map[string]regionTally {
		m := make(map[string]regionTally, expectedRegions)
		for i := range expectedRegions {
			m[fmt.Sprintf("r%02d", i)] = regionTally{total: n, live: n}
		}
		return m
	}
	// uneven keeps the total of uniform(2) but gives one region three builds
	// and another one.
	uneven := uniform(2)
	uneven["r00"] = regionTally{total: 3, live: 3}
	uneven["r01"] = regionTally{total: 1, live: 1}
	// missing drops one region.
	missing := uniform(1)
	delete(missing, "r00")
	// oneWithdrawn withdraws one of the two builds of a region: still offerable.
	oneWithdrawn := uniform(2)
	oneWithdrawn["r00"] = regionTally{total: 2, live: 1}
	// allWithdrawn leaves a region with nothing a new install can use.
	allWithdrawn := uniform(2)
	allWithdrawn["r05"] = regionTally{total: 2, live: 0}

	tests := []struct {
		name      string
		perRegion int
		counts    map[string]regionTally
		wantErr   string
	}{
		{"one precision accepts one per region", 1, uniform(1), ""},
		{"one precision rejects two per region", 1, uniform(2), "has 2 variants, want 1"},
		{"two precisions accept two per region", 2, uniform(2), ""},
		{"two precisions reject one per region", 2, uniform(1), "has 1 variants, want 2"},
		{"uneven split with the right total is rejected", 2, uneven, "variants, want 2"},
		{"missing region is rejected", 1, missing, "distinct regions"},
		{"a withdrawn build next to a live one is accepted", 2, oneWithdrawn, ""},
		{"a region whose builds are all withdrawn is rejected", 2, allWithdrawn, "region r05 has no variant a new install can use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fam := &family{key: "x", variantsPerRegion: tt.perRegion}
			err := fam.checkRegionCounts(tt.counts)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestVariant_SupersededByResolvesToAVariantID checks a manifest superseded_by
// path becomes the variant id of a live entry in the same region, and that a
// dangling, cross-region or chained pointer is refused, so SupersededBy never
// names a variant a new install cannot use.
func TestVariant_SupersededByResolvesToAVariantID(t *testing.T) {
	t.Parallel()

	fam := &family{
		key: "perch-v2", repo: "o/r",
		modelLocalName:  func(slug, token string) string { return slug + token },
		labelsLocalName: func(slug string) string { return slug + ".txt" },
	}
	table := &regionTable{Regions: map[string]struct {
		Classes *int `json:"classes"`
	}{"x": {Classes: new(3)}, "y": {Classes: new(3)}}}
	sidecar := map[string]labelChecksum{
		"o/r|regional/x/labels.txt": {SHA256: "aa", SizeBytes: 9, Lines: 3},
		"o/r|regional/y/labels.txt": {SHA256: "aa", SizeBytes: 9, Lines: 3},
	}
	entry := func(variant, region string, supersededBy *string) *manifestModel {
		return &manifestModel{
			Path: "regional/" + region + "/" + variant + ".onnx", Variant: variant, Region: region,
			Labels: "regional/" + region + "/labels.txt", SHA256: "bb", SizeBytes: 1,
			Requirements: &manifestRequirements{MinRAMMB: 1},
			SupersededBy: supersededBy,
		}
	}
	live := entry("no-dft-fp32", "x", nil)
	otherRegion := entry("no-dft-fp32", "y", nil)
	chainedTo := entry("fp32", "x", new(live.Path))
	byPath := map[string]*manifestModel{live.Path: live, otherRegion.Path: otherRegion, chainedTo.Path: chainedTo}

	tests := []struct {
		name         string
		supersededBy *string
		wantID       string
		wantErr      string
	}{
		{name: "no pointer is a live variant"},
		{name: "same region live entry", supersededBy: new(live.Path), wantID: "no-dft-fp32@x"},
		{name: "unknown path", supersededBy: new("regional/x/missing.onnx"), wantErr: "not an entry of this family"},
		{name: "other region", supersededBy: new(otherRegion.Path), wantErr: "different region"},
		{name: "entry that is itself superseded", supersededBy: new(chainedTo.Path), wantErr: "itself superseded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := fam.variant(entry("int8-arm", "x", tt.supersededBy), table, sidecar, byPath)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.ErrorContains(t, err, "int8-arm@x", "the error must name the entry")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, v.supersededBy)
			assert.Equal(t, tt.wantID != "", v.legacy)
			if tt.wantID != "" {
				assert.Contains(t, render(&v), fmt.Sprintf("SupersededBy: %q,", tt.wantID))
			}
		})
	}
}

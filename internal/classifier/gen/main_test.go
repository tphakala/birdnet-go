package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
		modelLocalName:  func(slug, token, _ string, _ int) string { return slug + token },
		labelsLocalName: func(slug, _ string, _ int) string { return slug + ".txt" },
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

// TestCheckRegionCounts covers the live-token rule: the distinct regions equal
// the family's expectedRegions, and each region ships exactly one live build of
// every live token and of no other. Superseded builds are not counted.
func TestCheckRegionCounts(t *testing.T) {
	t.Parallel()

	const regions = 3
	entry := func(region, variant, release string, superseded bool) manifestModel {
		m := manifestModel{Region: region, Variant: variant, UpstreamVersion: release}
		if superseded {
			m.SupersededBy = new("successor")
		}
		return m
	}
	// tiles builds one live entry per token for each of the first n regions, plus
	// extra entries.
	tiles := func(n int, tokens []string, release string, extra ...manifestModel) []manifestModel {
		var out []manifestModel
		for i := range n {
			for _, token := range tokens {
				out = append(out, entry(fmt.Sprintf("r%d", i), token, release, false))
			}
		}
		return append(out, extra...)
	}
	single := []string{"fp32"}
	pair := []string{"fp16", "fp32"}

	tests := []struct {
		name    string
		live    []string
		release []string
		entries []manifestModel
		wantErr string
	}{
		{"one live build per token", single, nil, tiles(regions, single, ""), ""},
		{"two tokens, one live build each", pair, nil, tiles(regions, pair, ""), ""},
		{"superseded builds next to the live ones", single, nil, tiles(regions, single, "", entry("r0", "fp32", "", true), entry("r0", "int8-arm", "", true), entry("r1", "fp32", "", true)), ""},
		{"a tile without superseded builds next to tiles with two", single, nil, tiles(regions, single, "", entry("r0", "fp32", "", true), entry("r0", "fp32", "", true)), ""},
		{"a missing live token", pair, nil, append(tiles(regions-1, pair, ""), entry("r2", "fp32", "", false)), "region r2 has live builds [fp32]"},
		{"two live builds of one token", single, nil, tiles(regions, single, "", entry("r1", "fp32", "", false)), "region r1 has live builds [fp32 fp32]"},
		{"an unknown live token", single, nil, tiles(regions, single, "", entry("r1", "fp16", "", false)), "region r1 has live builds [fp16 fp32]"},
		{"a region whose builds are all superseded", single, nil, append(tiles(regions-1, single, ""), entry("r2", "fp32", "", true)), "region r2 has live builds []"},
		{"a missing region", single, nil, tiles(regions-1, single, ""), "2 distinct regions, want 3"},
		{"an extra region", single, nil, tiles(regions+1, single, ""), "4 distinct regions, want 3"},
		{"multi-release family accepts a live current release", single, []string{"1.0", "1.1"}, append(tiles(regions, single, "1.1"), entry("r0", "fp32", "1.0", true), entry("r1", "fp32", "1.0", true), entry("r2", "fp32", "1.0", true)), ""},
		{"a live build of an older release", single, []string{"1.0", "1.1"}, append(tiles(regions-1, single, "1.1"), entry("r2", "fp32", "1.0", false)), "region r2: live entry fp32@r2 is of release 1.0"},
		{"a region losing its older release", single, []string{"1.0", "1.1"}, append(tiles(regions, single, "1.1"), entry("r0", "fp32", "1.0", true), entry("r1", "fp32", "1.0", true)), "release 1.0 is carried by 2 regions, want 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fam := &family{key: "x", expectedRegions: regions, olderReleaseRegions: regions, liveVariants: tt.live, releases: tt.release}
			err := fam.checkRegionCounts(tt.entries)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestSupersededVariantID checks a manifest superseded_by path becomes the
// variant id of a live entry in the same region, and that a dangling,
// cross-region or chained pointer is refused, so SupersededBy never names a
// variant a new install cannot use. A target of a later release carries the
// release in its id.
func TestSupersededVariantID(t *testing.T) {
	t.Parallel()

	entry := func(variant, region, release string, supersededBy *string) *manifestModel {
		return &manifestModel{Path: "regional/" + region + "/" + release + "/" + variant + ".onnx", Variant: variant, Region: region, UpstreamVersion: release, SupersededBy: supersededBy}
	}
	live := entry("no-dft-fp32", "x", "1.0", nil)
	otherRegion := entry("no-dft-fp32", "y", "1.0", nil)
	chained := entry("fp32", "x", "1.0", new(live.Path))
	later := entry("fp32", "x", "1.1", nil)
	byPath := map[string]*manifestModel{live.Path: live, otherRegion.Path: otherRegion, chained.Path: chained, later.Path: later}

	oneRelease := &family{key: "x"}
	twoReleases := &family{key: "x", releases: []string{"1.0", "1.1"}}

	tests := []struct {
		name         string
		fam          *family
		supersededBy *string
		wantID       string
		wantErr      string
	}{
		{name: "no pointer is a live variant", fam: oneRelease},
		{name: "same region live entry", fam: oneRelease, supersededBy: new(live.Path), wantID: "no-dft-fp32@x"},
		{name: "unknown path", fam: oneRelease, supersededBy: new("regional/x/missing.onnx"), wantErr: "not an entry of this family"},
		{name: "other region", fam: oneRelease, supersededBy: new(otherRegion.Path), wantErr: "different region"},
		{name: "entry that is itself superseded", fam: oneRelease, supersededBy: new(chained.Path), wantErr: "itself superseded"},
		{name: "target of a later release carries the release", fam: twoReleases, supersededBy: new(later.Path), wantID: "fp32-1.1@x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.fam.supersededVariantID(entry("int8-arm", "x", "1.0", tt.supersededBy), byPath)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.ErrorContains(t, err, "int8-arm@x", "the error must name the entry")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, got)
		})
	}
}

// TestSupersededVariantID_RefusesOlderTarget checks a superseded entry never
// points at an earlier release or a lower build, which would offer a downgrade as
// the update, while the same build number (a withdrawn precision) and a newer
// build or release are accepted.
func TestSupersededVariantID_RefusesOlderTarget(t *testing.T) {
	t.Parallel()

	fam := &family{key: "x", releases: []string{"1.0", "1.1"}}
	mk := func(path, release string, build int, supersededBy *string) *manifestModel {
		return &manifestModel{Path: path, Variant: "fp32", Region: "a", UpstreamVersion: release, Build: &build, SupersededBy: supersededBy}
	}
	older := mk("1.0-b1", "1.0", 1, nil)
	lowBuild := mk("1.1-b1", "1.1", 1, nil)
	highBuild := mk("1.1-b2", "1.1", 2, nil)
	newer := mk("1.1-b3", "1.1", 3, nil)
	byPath := map[string]*manifestModel{}
	for _, m := range []*manifestModel{older, lowBuild, highBuild, newer} {
		byPath[m.Path] = m
	}

	tests := []struct {
		name    string
		from    *manifestModel
		target  *manifestModel
		wantErr string
	}{
		{"a lower build of the same release", highBuild, lowBuild, "older release or build"},
		{"an earlier release", lowBuild, older, "older release or build"},
		{"the same build number", highBuild, highBuild, ""},
		{"a higher build", lowBuild, newer, ""},
		{"a later release", older, lowBuild, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			from := *tt.from
			from.SupersededBy = new(tt.target.Path)
			_, err := fam.supersededVariantID(&from, byPath)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestIndexByPath checks two regional entries sharing a model path are refused
// instead of one silently shadowing the other.
func TestIndexByPath(t *testing.T) {
	t.Parallel()

	got, err := indexByPath([]manifestModel{{Path: "a"}, {Path: "b"}})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	_, err = indexByPath([]manifestModel{{Path: "a"}, {Path: "b"}, {Path: "a"}})
	require.ErrorContains(t, err, `duplicate model path "a"`)
}

// TestFamilyVariantID pins the id scheme: the first release of a family keeps
// the bare "<variant>@<region>" form, so an id never changes meaning, every
// later release carries "-<release>", and a build above 1 adds "-b<N>". Build 1
// must never carry a suffix: it would strand every existing install.
func TestFamilyVariantID(t *testing.T) {
	t.Parallel()

	e := func(release string, build int) *manifestModel {
		return &manifestModel{Variant: "fp32", Region: "nordic", UpstreamVersion: release, Build: &build}
	}
	multi := &family{releases: []string{"3.0", "3.0.1"}}
	assert.Equal(t, "fp32@nordic", multi.variantID(e("3.0", 1)))
	assert.Equal(t, "fp32-3.0.1@nordic", multi.variantID(e("3.0.1", 1)))
	assert.Equal(t, "fp32-3.0.1-b2@nordic", multi.variantID(e("3.0.1", 2)))
	assert.Equal(t, "fp32-3.0.1-b3@nordic", multi.variantID(e("3.0.1", 3)))
	single := &family{}
	assert.Equal(t, "fp32@nordic", single.variantID(e("anything", 1)), "a one-release family ignores upstream_version")
	assert.Equal(t, "fp32-b2@nordic", single.variantID(e("2", 2)))
	assert.Equal(t, "fp32@nordic", single.variantID(&manifestModel{Variant: "fp32", Region: "nordic"}), "a missing build is build 1")
}

// TestFamilyLocalNames pins the LocalName rule of every family: build 1 keeps the
// names that shipped before builds were tracked (so installs are recognised),
// later builds carry "_b<N>" before the extension for model and labels (so two
// builds never share a file).
func TestFamilyLocalNames(t *testing.T) {
	t.Parallel()

	byKey := map[string]*family{}
	for i := range families {
		byKey[families[i].key] = &families[i]
	}
	tests := []struct {
		family, token, release string
		build                  int
		wantModel, wantLabels  string
	}{
		{"perch-v2", "no-dft-fp32", "2", 1, "perch_v2_nordic_no_dft.onnx", "perch_v2_nordic_labels.txt"},
		{"perch-v2", "int8-arm", "2", 1, "perch_v2_nordic_int8_arm.onnx", "perch_v2_nordic_labels.txt"},
		{"perch-v2", "no-dft-fp32", "2", 2, "perch_v2_nordic_no_dft_b2.onnx", "perch_v2_nordic_labels_b2.txt"},
		{"birdnet-plus-v3.0", "fp32", "3.0", 1, "birdnet_plus_v3.0_nordic_fp32.onnx", "birdnet_plus_v3.0_nordic_labels.txt"},
		{"birdnet-plus-v3.0", "fp32", "3.0.1", 1, "birdnet_plus_v3.0.1_nordic_fp32.onnx", "birdnet_plus_v3.0.1_nordic_labels.txt"},
		{"birdnet-plus-v3.0", "fp32", "3.0.1", 3, "birdnet_plus_v3.0.1_nordic_fp32_b3.onnx", "birdnet_plus_v3.0.1_nordic_labels_b3.txt"},
		{"birdnet-v3.0", "fp16", "3.0", 1, "birdnet_v3.0_nordic_fp16.onnx", "birdnet_v3.0_nordic_labels.txt"},
		{"birdnet-v3.0", "fp32", "3.0", 2, "birdnet_v3.0_nordic_fp32_b2.onnx", "birdnet_v3.0_nordic_labels_b2.txt"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %s %s b%d", tt.family, tt.token, tt.release, tt.build), func(t *testing.T) {
			t.Parallel()
			fam := byKey[tt.family]
			require.NotNil(t, fam)
			assert.Equal(t, tt.wantModel, fam.modelLocalName("nordic", tt.token, tt.release, tt.build))
			assert.Equal(t, tt.wantLabels, fam.labelsLocalName("nordic", tt.release, tt.build))
		})
	}
}

// TestMergeFrozenReleases checks the frozen older release is chained to the
// current manifest's entry of the same region and variant, and that a frozen
// entry that cannot be chained or that collides is refused.
func TestMergeFrozenReleases(t *testing.T) {
	t.Parallel()

	fam := &family{key: "x", releases: []string{"1.0", "1.1"}}
	entry := func(path, region, variant, release string, supersededBy *string) manifestModel {
		return manifestModel{Path: path, Region: region, Variant: variant, UpstreamVersion: release, SupersededBy: supersededBy}
	}
	current := []manifestModel{
		entry("a/1.1/fp32.onnx", "a", "fp32", "1.1", nil),
		entry("b/1.1/fp32.onnx", "b", "fp32", "1.1", nil),
	}

	t.Run("each frozen entry points at the current entry of its region and variant", func(t *testing.T) {
		t.Parallel()
		got, err := fam.mergeFrozenReleases(current, []manifestModel{
			entry("b/1.0/fp32.onnx", "b", "fp32", "1.0", nil),
			entry("a/1.0/fp32.onnx", "a", "fp32", "1.0", nil),
		})
		require.NoError(t, err)
		require.Len(t, got, 4)
		assert.Nil(t, got[0].SupersededBy, "the current entries keep their own state")
		assert.Nil(t, got[1].SupersededBy)
		require.NotNil(t, got[2].SupersededBy)
		assert.Equal(t, "b/1.1/fp32.onnx", *got[2].SupersededBy)
		require.NotNil(t, got[3].SupersededBy)
		assert.Equal(t, "a/1.1/fp32.onnx", *got[3].SupersededBy)
	})

	t.Run("a frozen entry chains to the live build, not a superseded one", func(t *testing.T) {
		t.Parallel()
		withBuilds := []manifestModel{
			entry("a/1.1/fp32-b1.onnx", "a", "fp32", "1.1", new("a/1.1/fp32-b2.onnx")),
			entry("a/1.1/fp32-b2.onnx", "a", "fp32", "1.1", nil),
		}
		got, err := fam.mergeFrozenReleases(withBuilds, []manifestModel{entry("a/1.0/fp32.onnx", "a", "fp32", "1.0", nil)})
		require.NoError(t, err)
		require.Len(t, got, 3)
		require.NotNil(t, got[2].SupersededBy)
		assert.Equal(t, "a/1.1/fp32-b2.onnx", *got[2].SupersededBy)
	})

	t.Run("two live current-release entries for one region and variant are refused", func(t *testing.T) {
		t.Parallel()
		twoLive := []manifestModel{
			entry("a/1.1/fp32-b1.onnx", "a", "fp32", "1.1", nil),
			entry("a/1.1/fp32-b2.onnx", "a", "fp32", "1.1", nil),
		}
		_, err := fam.mergeFrozenReleases(twoLive, nil)
		require.ErrorContains(t, err, "two live current-release entries for fp32@a")
	})

	tests := []struct {
		name    string
		frozen  []manifestModel
		wantErr string
	}{
		{"no current entry of that region and variant", []manifestModel{entry("c/1.0/fp32.onnx", "c", "fp32", "1.0", nil)}, "no current-release entry"},
		{"variant missing from the current release", []manifestModel{entry("a/1.0/fp16.onnx", "a", "fp16", "1.0", nil)}, "no current-release entry"},
		{"frozen entry already chained", []manifestModel{entry("a/1.0/fp32.onnx", "a", "fp32", "1.0", new("a/1.1/fp32.onnx"))}, "already names a successor"},
		{"frozen entry of the current release", []manifestModel{entry("a/1.1/other.onnx", "a", "fp32", "1.1", nil)}, "current release"},
		{"frozen path collides with the current manifest", []manifestModel{entry("a/1.1/fp32.onnx", "a", "fp32", "1.0", nil)}, "already listed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := fam.mergeFrozenReleases(current, tt.frozen)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestCheckReleases covers the build-time refusals that keep an older release
// recognised: it must stay chained to a successor, the manifest must name the
// newest listed release as current, and a release string lands in a file name so
// it must be digits and dots only.
func TestCheckReleases(t *testing.T) {
	t.Parallel()

	fam := &family{key: "x", releases: []string{"1.0", "1.1"}}
	entry := func(release string, supersededBy *string) manifestModel {
		return manifestModel{Path: "p/" + release, Variant: "fp32", Region: "a", UpstreamVersion: release, SupersededBy: supersededBy}
	}
	chained := entry("1.0", new("p/1.1"))
	current := entry("1.1", nil)

	tests := []struct {
		name           string
		fam            *family
		regional       []manifestModel
		currentVersion string
		wantErr        string
	}{
		{name: "older release chained to the current one", fam: fam, regional: []manifestModel{chained, current}, currentVersion: "1.1"},
		{name: "a one-release family is not checked", fam: &family{key: "x"}, regional: []manifestModel{entry("", nil)}},
		{name: "older release without superseded_by", fam: fam, regional: []manifestModel{entry("1.0", nil), current}, currentVersion: "1.1", wantErr: "release 1.0 must name its successor"},
		{name: "current_version is not the newest listed release", fam: fam, regional: []manifestModel{chained, current}, currentVersion: "1.0", wantErr: `current_version "1.0"`},
		{name: "current_version missing", fam: fam, regional: []manifestModel{chained, current}, wantErr: "current_version"},
		{name: "release outside the list", fam: fam, regional: []manifestModel{entry("2.0", nil), current}, currentVersion: "1.1", wantErr: `release "2.0" is not one of`},
		{name: "release with a path character", fam: fam, regional: []manifestModel{entry("1.0/../x", new("p/1.1")), current}, currentVersion: "1.1", wantErr: "not a plain version"},
		{name: "release with a letter", fam: fam, regional: []manifestModel{entry("1.0rc1", new("p/1.1")), current}, currentVersion: "1.1", wantErr: "not a plain version"},
		{name: "release missing from the entry", fam: fam, regional: []manifestModel{entry("", nil), current}, currentVersion: "1.1", wantErr: "not a plain version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.fam.checkReleases(tt.regional, tt.currentVersion)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// familyByKey returns the generated family with the given key.
func familyByKey(t *testing.T, key string) *family {
	t.Helper()
	for i := range families {
		if families[i].key == key {
			return &families[i]
		}
	}
	require.FailNow(t, "family not found", key)
	return nil
}

// renderedTile is the part of one rendered variant the build tests assert on.
type renderedTile struct {
	id, slug     string
	legacy       bool
	supersededBy string
	src          string
}

var (
	renderedIDRe     = regexp.MustCompile(`ID: "([a-z0-9.-]+@([a-z0-9-]+))"`)
	renderedSuperRe  = regexp.MustCompile(`SupersededBy: "([^"]+)"`)
	renderedModelRe  = regexp.MustCompile(`LocalName: "([^"]+\.onnx)"`)
	renderedLabelsRe = regexp.MustCompile(`LocalName: "([^"]+\.txt)"`)
)

// groupByRegion splits the generator output (sorted by region) into per-region
// lists of rendered tiles, in output order.
func groupByRegion(t *testing.T, out []string) map[string][]renderedTile {
	t.Helper()
	groups := map[string][]renderedTile{}
	for _, src := range out {
		m := renderedIDRe.FindStringSubmatch(src)
		require.NotNil(t, m, src)
		tile := renderedTile{id: m[1], slug: m[2], legacy: strings.Contains(src, "Legacy: true,"), src: src}
		if sup := renderedSuperRe.FindStringSubmatch(src); sup != nil {
			tile.supersededBy = sup[1]
		}
		groups[tile.slug] = append(groups[tile.slug], tile)
	}
	return groups
}

// assertLiveFirstChain checks every region renders its single live build first
// and every other build as a Legacy variant naming that live build.
func assertLiveFirstChain(t *testing.T, groups map[string][]renderedTile) {
	t.Helper()
	for slug, tiles := range groups {
		live := tiles[0]
		assert.Falsef(t, live.legacy, "%s: the live build renders first", slug)
		assert.Empty(t, live.supersededBy)
		for _, old := range tiles[1:] {
			assert.Truef(t, old.legacy, "%s: %s is Legacy", slug, old.id)
			assert.Equalf(t, live.id, old.supersededBy, "%s: %s names the region's live build", slug, old.id)
		}
	}
}

// TestBuild_BirdNETPlusOlderReleaseFollowsCurrent runs the generator on the
// vendored BirdNET+ manifests: each region renders its live 3.0.1 build, then the
// superseded 3.0.1 build 1 and the frozen 3.0 build as Legacy variants that keep
// their pre-build ids and file names and name the live build as their successor.
// central-india has only its live build.
func TestBuild_BirdNETPlusOlderReleaseFollowsCurrent(t *testing.T) {
	t.Chdir("..") // the generator's manifest and sidecar paths are relative to the classifier package

	labels, err := loadLabelsSidecar()
	require.NoError(t, err)
	plus := familyByKey(t, "birdnet-plus-v3.0")
	out, err := plus.build(labels)
	require.NoError(t, err)

	groups := groupByRegion(t, out)
	require.Len(t, groups, plus.expectedRegions)
	assertLiveFirstChain(t, groups)

	for slug, tiles := range groups {
		wantLive := "fp32-3.0.1-b2@" + slug
		if slug == "central-india" {
			wantLive = "fp32-3.0.1-b3@central-india"
			require.Lenf(t, tiles, 1, "central-india has a single build")
		} else {
			require.Lenf(t, tiles, 3, "%s: live build, superseded 3.0.1 build 1, frozen 3.0", slug)
			// Sorted by release, then build: the frozen 3.0 tile, then 3.0.1 build 1.
			assert.Equal(t, "fp32@"+slug, tiles[1].id, "the 3.0 tile keeps the bare id")
			assert.Contains(t, tiles[1].src, `Version: "3.0",`)
			assert.Equal(t, "birdnet_plus_v3.0_"+slug+"_fp32.onnx", renderedModelRe.FindStringSubmatch(tiles[1].src)[1])
			assert.Equal(t, "fp32-3.0.1@"+slug, tiles[2].id, "3.0.1 build 1 keeps its pre-build id")
			assert.NotContains(t, tiles[2].src, "Build:")
			assert.Equal(t, "birdnet_plus_v3.0.1_"+slug+"_fp32.onnx", renderedModelRe.FindStringSubmatch(tiles[2].src)[1])
			assert.Equal(t, "birdnet_plus_v3.0.1_"+slug+"_labels.txt", renderedLabelsRe.FindStringSubmatch(tiles[2].src)[1])
		}
		assert.Equal(t, wantLive, tiles[0].id)
		assert.NotContains(t, tiles[0].src, "Version:", "the live build is of the current release")
		assert.Contains(t, tiles[0].src, "Build: ")
	}
}

// TestBuild_PerchWithdrawnVariantsFollowTheirReplacement runs the generator on
// the vendored Perch manifest: every region renders its live no-dft-fp32 build
// first, then its superseded build 1 variants (int8-arm and no-dft-fp32), all
// naming the live build. central-india has only its live build 1.
func TestBuild_PerchWithdrawnVariantsFollowTheirReplacement(t *testing.T) {
	t.Chdir("..") // the generator's manifest and sidecar paths are relative to the classifier package

	labels, err := loadLabelsSidecar()
	require.NoError(t, err)
	perch := familyByKey(t, "perch-v2")
	out, err := perch.build(labels)
	require.NoError(t, err)

	groups := groupByRegion(t, out)
	require.Len(t, groups, perch.expectedRegions)
	assertLiveFirstChain(t, groups)

	for slug, tiles := range groups {
		if slug == "central-india" {
			require.Len(t, tiles, 1)
			assert.Equal(t, "no-dft-fp32@central-india", tiles[0].id)
			assert.NotContains(t, tiles[0].src, "Build:")
			continue
		}
		require.Lenf(t, tiles, 3, "%s: live build 2 plus two superseded build 1 variants", slug)
		assert.Equal(t, "no-dft-fp32-b2@"+slug, tiles[0].id)
		assert.Equal(t, "perch_v2_"+slug+"_no_dft_b2.onnx", renderedModelRe.FindStringSubmatch(tiles[0].src)[1])
		assert.Equal(t, "perch_v2_"+slug+"_labels_b2.txt", renderedLabelsRe.FindStringSubmatch(tiles[0].src)[1])
		assert.Equal(t, "int8-arm@"+slug, tiles[1].id)
		assert.Equal(t, "no-dft-fp32@"+slug, tiles[2].id, "build 1 keeps the pre-build id")
		assert.Equal(t, "perch_v2_"+slug+"_no_dft.onnx", renderedModelRe.FindStringSubmatch(tiles[2].src)[1])
		assert.Equal(t, "perch_v2_"+slug+"_labels.txt", renderedLabelsRe.FindStringSubmatch(tiles[2].src)[1])
	}
}

// TestBuild_BirdNETPlusRefusesBadReleaseMetadata runs build on a copy of the
// vendored BirdNET+ manifest with its release metadata broken: a current_version
// that is not the newest listed release, an extra build of a new variant whose
// release string holds a path character, a second live fp32 build of the current
// release in one region, and an unknown live token. Each must be refused with the
// message of the rule that owns it, so the release check and the live-token check
// run inside build.
func TestBuild_BirdNETPlusRefusesBadReleaseMetadata(t *testing.T) {
	t.Chdir("..") // the generator's manifest and sidecar paths are relative to the classifier package

	labels, err := loadLabelsSidecar()
	require.NoError(t, err)
	plus := familyByKey(t, "birdnet-plus-v3.0")
	data, err := os.ReadFile(plus.manifestFile)
	require.NoError(t, err)

	// cloneLast copies the last manifest model (the live central-india build) as
	// a new entry under a new path in another region's tile.
	cloneLast := func(doc map[string]any, mutate func(extra map[string]any)) {
		models := doc["models"].([]any)
		extra := maps.Clone(models[len(models)-1].(map[string]any))
		extra["path"] = "regional/central-india/other.onnx"
		mutate(extra)
		doc["models"] = append(models, extra)
	}
	tests := []struct {
		name    string
		mutate  func(doc map[string]any)
		wantErr string
	}{
		{
			name: "current_version is not the newest listed release",
			mutate: func(doc map[string]any) {
				doc["family_meta"].(map[string]any)["current_version"] = "3.0"
			},
			wantErr: `current_version "3.0"`,
		},
		{
			name: "a build of a new variant with a path character in its release",
			mutate: func(doc map[string]any) {
				cloneLast(doc, func(extra map[string]any) {
					extra["variant"] = "fp16"
					extra["upstream_version"] = "3.0.1/../x"
				})
			},
			wantErr: "not a plain version",
		},
		{
			name: "a second live fp32 build of the current release in one region",
			mutate: func(doc map[string]any) {
				cloneLast(doc, func(extra map[string]any) { extra["build"] = float64(4) })
			},
			wantErr: "two live current-release entries for fp32@central-india",
		},
		{
			name: "an unknown live token",
			mutate: func(doc map[string]any) {
				cloneLast(doc, func(extra map[string]any) { extra["variant"] = "fp16" })
			},
			wantErr: "live builds [fp16 fp32]",
		},
		{
			name: "a regional entry without a build",
			mutate: func(doc map[string]any) {
				models := doc["models"].([]any)
				delete(models[len(models)-1].(map[string]any), "build")
			},
			wantErr: "missing build",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc map[string]any
			require.NoError(t, json.Unmarshal(data, &doc))
			tt.mutate(doc)
			mutated, err := json.Marshal(doc)
			require.NoError(t, err)
			file := filepath.Join(t.TempDir(), "BirdNET-Plus-V3.0.models.json")
			require.NoError(t, os.WriteFile(file, mutated, 0o600))

			fam := *plus
			fam.manifestFile = file
			_, err = fam.build(labels)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestVariant_SpeciesCountOfSupersededBuild checks a superseded entry takes its
// own manifest classes (the region table describes the live build) while a live
// entry must still agree with the table, and a superseded entry without classes
// is refused.
func TestVariant_SpeciesCountOfSupersededBuild(t *testing.T) {
	t.Parallel()

	const labelsRemote = "regional/x/labels.txt"
	fam := &family{
		key: "birdnet-v3.0", repo: "o/r",
		modelLocalName:  func(slug, token, _ string, _ int) string { return slug + token },
		labelsLocalName: func(slug, _ string, _ int) string { return slug + ".txt" },
	}
	table := &regionTable{Regions: map[string]struct {
		Classes *int `json:"classes"`
	}{"x": {Classes: new(5)}}}
	sidecar := map[string]labelChecksum{"o/r|" + labelsRemote: {SHA256: "aa", SizeBytes: 9, Lines: 3}}
	entry := func(classes *int, superseded bool) *manifestModel {
		m := &manifestModel{
			Path: "regional/x/m.onnx", Variant: "fp32", Region: "x", Labels: labelsRemote, SHA256: "bb", SizeBytes: 1,
			Requirements: &manifestRequirements{MinRAMMB: 1}, Classes: classes,
		}
		if superseded {
			m.SupersededBy = new("regional/x/new.onnx")
		}
		return m
	}

	v, err := fam.variant(entry(new(3), true), table, sidecar)
	require.NoError(t, err)
	assert.Equal(t, 3, v.speciesCount, "a superseded build keeps its own count")

	_, err = fam.variant(entry(new(3), false), table, sidecar)
	require.ErrorContains(t, err, "manifest classes 3 != region table classes 5")

	_, err = fam.variant(entry(nil, true), table, sidecar)
	require.ErrorContains(t, err, "variant fp32@x: superseded entry must list classes")
}

// TestBuild_LabelsLocalNameReuse checks the labels half of "different builds
// never share a LocalName": the precisions of one build may share a labels file,
// two builds whose labels would share a LocalName are refused.
func TestBuild_LabelsLocalNameReuse(t *testing.T) {
	t.Chdir("..") // the generator's manifest and sidecar paths are relative to the classifier package

	labels, err := loadLabelsSidecar()
	require.NoError(t, err)

	// The real BirdNET v3.0 manifest shares one labels file between fp16 and fp32.
	v3 := familyByKey(t, "birdnet-v3.0")
	_, err = v3.build(labels)
	require.NoError(t, err, "two precisions of one build share a labels file")

	// A family whose labels name ignores the build maps two builds to one name.
	perch := *familyByKey(t, "perch-v2")
	perch.labelsLocalName = func(slug, _ string, _ int) string { return "perch_v2_" + slug + "_labels.txt" }
	_, err = perch.build(labels)
	require.ErrorContains(t, err, "labels LocalName")
	require.ErrorContains(t, err, "shared with another release, build or labels file")
}

func TestBuildDocuments_Validation(t *testing.T) {
	t.Parallel()

	const (
		rev = "ba1c7529e785e9ec745dcbb3271853abfa3026d6"
		sha = "3ede50997d96045d7753576547d615c0bc6a33a5d87bffce9d3c5888dec9ed86"
	)
	hosted := func(kind string) manifestDocument {
		return manifestDocument{Kind: kind, Path: kind + ".md", Revision: rev, SHA256: sha}
	}
	lic := hosted("license")
	external := manifestDocument{Kind: "license", URL: "https://example.org/license"}
	mk := func(repo string, docs ...manifestDocument) namedManifest {
		return namedManifest{name: repo + ".json", m: &manifest{Repo: repo, FamilyMeta: manifestFamilyMeta{Documents: docs}}}
	}
	with := func(nm namedManifest, mutate func(*manifestDocument)) namedManifest {
		docs := slices.Clone(nm.m.FamilyMeta.Documents)
		mutate(&docs[len(docs)-1])
		return mk(nm.m.Repo, docs...)
	}
	release := func(docs *[]manifestDocument) namedManifest {
		nm := mk("o/a", lic)
		nm.m.FamilyMeta.Versions = map[string]manifestVersionDoc{"1.0": {Documents: docs}}
		return nm
	}

	tests := []struct {
		name    string
		in      []namedManifest
		wantErr string
	}{
		{name: "hosted license", in: []namedManifest{mk("o/a", lic)}},
		{name: "external license", in: []namedManifest{mk("o/a", external)}},
		{name: "unknown kind is kept", in: []namedManifest{mk("o/a", lic, hosted("future_kind"))}},
		{name: "release without documents key", in: []namedManifest{release(nil)}},
		{name: "release equal to family list", in: []namedManifest{release(&[]manifestDocument{lic})}},
		{name: "missing repo", in: []namedManifest{mk("", lic)}, wantErr: "missing repo"},
		{name: "no license", in: []namedManifest{mk("o/a", hosted("acceptable_use"))}, wantErr: "exactly one"},
		{name: "two licenses", in: []namedManifest{mk("o/a", lic, external)}, wantErr: "repeated"},
		{name: "repeated kind", in: []namedManifest{mk("o/a", lic, hosted("x"), hosted("x"))}, wantErr: "repeated"},
		{name: "hosted without sha256", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.SHA256 = "" })}, wantErr: "hosted document needs"},
		{name: "hosted without path", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "" })}, wantErr: "hosted document needs"},
		{name: "hosted with uppercase sha256", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.SHA256 = strings.ToUpper(d.SHA256) })}, wantErr: "hosted document needs"},
		{name: "hosted with short revision", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Revision = "abc" })}, wantErr: "hosted document needs"},
		{name: "external requiring agreement", in: []namedManifest{with(mk("o/a", external), func(d *manifestDocument) { d.RequiresAgreement = true })}, wantErr: "cannot require agreement"},
		{name: "external with http url", in: []namedManifest{with(mk("o/a", external), func(d *manifestDocument) { d.URL = "http://example.org" })}, wantErr: "not https"},
		{name: "external with a path", in: []namedManifest{with(mk("o/a", external), func(d *manifestDocument) { d.Path = "x.md" })}, wantErr: "must not set"},
		{name: "path with dot dot", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "a/../b.md" })}, wantErr: "inside the repo"},
		{name: "absolute path", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "/etc/passwd" })}, wantErr: "inside the repo"},
		{name: "external url without a host", in: []namedManifest{with(mk("o/a", external), func(d *manifestDocument) { d.URL = "https://" })}, wantErr: "not https"},
		{name: "external url with only a port", in: []namedManifest{with(mk("o/a", external), func(d *manifestDocument) { d.URL = "https://:443" })}, wantErr: "not https"},
		{name: "external url with a space", in: []namedManifest{with(mk("o/a", external), func(d *manifestDocument) { d.URL = "https://example.org/a b" })}, wantErr: "not https"},
		{name: "path naming the repo root", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "." })}, wantErr: "inside the repo"},
		{name: "path naming a directory", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "docs/" })}, wantErr: "inside the repo"},
		{name: "path with a dot segment", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "./LICENSE.md" })}, wantErr: "inside the repo"},
		{name: "path with a backslash", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = `..\\secret` })}, wantErr: "inside the repo"},
		{name: "whitespace path", in: []namedManifest{with(mk("o/a", lic), func(d *manifestDocument) { d.Path = "   " })}, wantErr: "inside the repo"},
		{name: "release list differs", in: []namedManifest{release(&[]manifestDocument{lic, hosted("acceptable_use")})}, wantErr: "per-release"},
		{name: "duplicate repo", in: []namedManifest{mk("o/a", lic), mk("o/a", lic)}, wantErr: "collides"},
		{name: "repos differing only in case", in: []namedManifest{mk("o/a", lic), mk("O/A/", lic)}, wantErr: "collides"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := buildDocuments(tc.in)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, got, len(tc.in))
		})
	}
}

func TestBuildDocuments_KeepsUnknownKind(t *testing.T) {
	t.Parallel()

	lic := manifestDocument{Kind: "license", URL: "https://example.org/license"}
	future := manifestDocument{Kind: "future_kind", URL: "https://example.org/future"}
	got, err := buildDocuments([]namedManifest{{name: "a.json", m: &manifest{Repo: "o/a", FamilyMeta: manifestFamilyMeta{Documents: []manifestDocument{lic, future}}}}})
	require.NoError(t, err)
	assert.Equal(t, []manifestDocument{lic, future}, got["o/a"])
}

// TestBuildRecords covers the release-notes records the generator emits: a
// record that carries only the maintainer-facing changes text is skipped (so a
// build without notes shows nothing), every emitted record must name a valid
// build of a release the family hosts, a date and non-empty notes, and the
// output is sorted by release and build whatever the manifest's key order.
func TestBuildRecords(t *testing.T) {
	t.Parallel()

	multi := &family{key: "multi", releases: []string{"1.0", "1.1"}}
	single := &family{key: "single"}
	regional := []manifestModel{{Path: "r/a", Variant: "fp32", Region: "a", UpstreamVersion: "2"}}
	rec := func(release string, build *int, reason string, notes ...string) manifestBuild {
		return manifestBuild{Build: build, UpstreamVersion: release, Date: "2026-11-21", Reason: reason, Notes: notes}
	}

	tests := []struct {
		name    string
		fam     *family
		builds  map[string]manifestBuild
		want    []buildRecord
		wantErr string
	}{
		{
			name: "a record with only changes is skipped",
			fam:  multi,
			builds: map[string]manifestBuild{
				"x-b1": {Build: new(1), UpstreamVersion: "1.1", Date: "2026-11-20"},
			},
		},
		{
			name: "records are sorted by release and build",
			fam:  multi,
			builds: map[string]manifestBuild{
				"x-b3":     rec("1.1", new(3), "third"),
				"x-b2":     rec("1.1", new(2), "", "second note"),
				"x-1.0-b2": rec("1.0", new(2), "older"),
			},
			want: []buildRecord{
				{version: "1.0", build: 2, date: "2026-11-21", reason: "older"},
				{version: "1.1", build: 2, date: "2026-11-21", notes: []string{"second note"}},
				{version: "1.1", build: 3, date: "2026-11-21", reason: "third"},
			},
		},
		{
			name:   "a one-release family takes the release of its regional entries",
			fam:    single,
			builds: map[string]manifestBuild{"y-b2": rec("2", new(2), "r", "n")},
			want:   []buildRecord{{version: "2", build: 2, date: "2026-11-21", reason: "r", notes: []string{"n"}}},
		},
		{name: "missing build", fam: multi, builds: map[string]manifestBuild{"k": rec("1.1", nil, "r")}, wantErr: "build"},
		{name: "build below 1", fam: multi, builds: map[string]manifestBuild{"k": rec("1.1", new(0), "r")}, wantErr: "build"},
		{name: "release the family does not host", fam: multi, builds: map[string]manifestBuild{"k": rec("2.0", new(2), "r")}, wantErr: `release "2.0"`},
		{name: "release no regional entry carries", fam: single, builds: map[string]manifestBuild{"k": rec("3", new(2), "r")}, wantErr: `release "3"`},
		{name: "missing date", fam: multi, builds: map[string]manifestBuild{"k": {Build: new(2), UpstreamVersion: "1.1", Reason: "r"}}, wantErr: "date"},
		{name: "malformed date", fam: multi, builds: map[string]manifestBuild{"k": {Build: new(2), UpstreamVersion: "1.1", Date: "21.11.2026", Reason: "r"}}, wantErr: "date"},
		{name: "empty note", fam: multi, builds: map[string]manifestBuild{"k": rec("1.1", new(2), "r", "ok", " ")}, wantErr: "empty note"},
		{
			name: "duplicate release and build",
			fam:  multi,
			builds: map[string]manifestBuild{
				"k1": rec("1.1", new(2), "r"),
				"k2": rec("1.1", new(2), "s"),
			},
			wantErr: "duplicate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.fam.buildRecords(&manifest{Builds: tt.builds, Models: regional})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

package main

import (
	"fmt"
	"math"
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

// TestSupersededVariantID checks a manifest superseded_by path becomes the
// variant id of a live entry in the same region, and that a dangling,
// cross-region or chained pointer is refused, so SupersededBy never names a
// variant a new install cannot use.
func TestSupersededVariantID(t *testing.T) {
	t.Parallel()

	entry := func(variant, region string, supersededBy *string) *manifestModel {
		return &manifestModel{Path: "regional/" + region + "/" + variant + ".onnx", Variant: variant, Region: region, SupersededBy: supersededBy}
	}
	live := entry("no-dft-fp32", "x", nil)
	otherRegion := entry("no-dft-fp32", "y", nil)
	chained := entry("fp32", "x", new(live.Path))
	byPath := map[string]*manifestModel{live.Path: live, otherRegion.Path: otherRegion, chained.Path: chained}

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
		{name: "entry that is itself superseded", supersededBy: new(chained.Path), wantErr: "itself superseded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := supersededVariantID(entry("int8-arm", "x", tt.supersededBy), byPath)
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

// TestBuild_PerchWithdrawnVariantsFollowTheirReplacement runs the generator on
// the vendored Perch manifest: every region's withdrawn int8-arm variant names
// that region's no-dft-fp32 variant and renders after it.
func TestBuild_PerchWithdrawnVariantsFollowTheirReplacement(t *testing.T) {
	t.Chdir("..") // the generator's manifest and sidecar paths are relative to the classifier package

	labels, err := loadLabelsSidecar()
	require.NoError(t, err)
	var perch *family
	for i := range families {
		if families[i].key == "perch-v2" {
			perch = &families[i]
		}
	}
	require.NotNil(t, perch)

	out, err := perch.build(labels)
	require.NoError(t, err)
	require.Len(t, out, expectedRegions*perch.variantsPerRegion)

	idRe := regexp.MustCompile(`ID: "([a-z0-9-]+)@([a-z0-9-]+)"`)
	// out is sorted by region: each region's live variant, then its withdrawn one.
	for i := 0; i < len(out); i += 2 {
		live := idRe.FindStringSubmatch(out[i])
		withdrawn := idRe.FindStringSubmatch(out[i+1])
		require.NotNil(t, live)
		require.NotNil(t, withdrawn)
		assert.Equal(t, "no-dft-fp32", live[1])
		assert.Equal(t, "int8-arm", withdrawn[1])
		assert.Equal(t, live[2], withdrawn[2], "a region's two variants are adjacent")
		assert.NotContains(t, out[i], "Legacy:")
		assert.Contains(t, out[i+1], "Legacy:")
		assert.Contains(t, out[i+1], fmt.Sprintf("SupersededBy: %q,", live[1]+"@"+live[2]))
	}
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

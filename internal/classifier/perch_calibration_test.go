package classifier

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// mauritiusTile is the smallest published tile; its labels file is a fixture.
	mauritiusTile = "mauritius"
	// customLabelCount is the size of the synthetic label sets used for custom models.
	customLabelCount = 40
	customRestLogit  = 9.5
	staleRestLogit   = 11.5
)

// mauritiusLabels reads the published mauritius labels file (the smallest tile) and its parsed labels.
func mauritiusLabels(t *testing.T) (data []byte, labels []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "perch", "mauritius_labels.txt")) //nolint:gosec // fixed testdata path
	require.NoError(t, err)
	labels, err = ParsePerchLabels(data)
	require.NoError(t, err)
	return data, labels
}

// syntheticLabelBytes returns the bytes of a labels file with n synthetic labels.
func syntheticLabelBytes(n int) (data []byte, labels []string) {
	labels = syntheticPerchLabels(n)
	return []byte(joinLines(labels)), labels
}

func joinLines(labels []string) string {
	var b bytes.Buffer
	for _, l := range labels {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// cloneEmbeddedPerch returns a deep-enough copy of the built-in catalog in which every
// Perch variant's calibration is replaced by mutate(variant calibration).
func cloneEmbeddedPerch(mutate func(v *CatalogVariant)) []CatalogEntry {
	out := slices.Clone(EmbeddedCatalog)
	for i := range out {
		if out[i].RegistryID != RegistryIDPerchV2 {
			continue
		}
		out[i].Variants = slices.Clone(out[i].Variants)
		for j := range out[i].Variants {
			mutate(&out[i].Variants[j])
		}
	}
	return out
}

// customPerchEntry is an active-catalog-only Perch entry whose single variant has
// the labels bytes of data and the given region and calibration.
func customPerchEntry(data []byte, region string, speciesCount int, cal *ScoreCalibration) CatalogEntry {
	return CatalogEntry{
		ID:         "custom-perch",
		Name:       "Custom Perch",
		RegistryID: RegistryIDPerchV2,
		Variants: []CatalogVariant{{
			ID:               "custom@" + region,
			Region:           region,
			SpeciesCount:     speciesCount,
			Default:          true,
			ScoreCalibration: cal,
			Files: []CatalogFile{
				{RemotePath: "custom/model.onnx", LocalName: "custom_model.onnx", Role: RoleModel, SHA256: "00", SizeBytes: 1},
				{RemotePath: "custom/labels.txt", LocalName: "custom_labels.txt", Role: RoleLabels, SHA256: labelsSHA256Hex(data), SizeBytes: int64(len(data))},
			},
		}},
	}
}

// TestPerchScoreCalibration covers the lookup NewPerch calls. It swaps the active
// catalog, so it must not run in parallel.
func TestPerchScoreCalibration(t *testing.T) {
	t.Cleanup(func() { setActiveCatalog(nil) })

	mauData, mauLabels := mauritiusLabels(t)
	want := embeddedScoreCalibration(t, "no-dft-fp32@"+mauritiusTile)
	require.InDelta(t, want.RestLogit, embeddedScoreCalibration(t, "int8-arm@"+mauritiusTile).RestLogit, 0, "both precisions carry the same value")

	crlf := bytes.ReplaceAll(mauData, []byte("\n"), []byte("\r\n"))
	unknownData, unknownLabels := syntheticLabelBytes(customLabelCount)
	fullData, fullLabels := syntheticLabelBytes(perchFullClassCount)

	tests := []struct {
		name      string
		catalog   []CatalogEntry // active catalog; nil keeps the built-in one
		data      []byte
		labels    []string
		modelPath string
		status    perchCalibrationStatus
		variantID string
		rest      float64
	}{
		{name: "gallery no_dft filename", data: mauData, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "gallery int8 filename", data: mauData, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_int8_arm.onnx",
			status: perchCalibrationApplied, variantID: "int8-arm@" + mauritiusTile, rest: want.RestLogit},
		{name: "HuggingFace filename", data: mauData, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft_fp32.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "custom model path still applies", data: mauData, labels: mauLabels, modelPath: "/somewhere/else.onnx",
			status: perchCalibrationApplied, variantID: "int8-arm@" + mauritiusTile, rest: want.RestLogit},
		{name: "CRLF labels use the canonical hash", data: crlf, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "full-size label set with unknown bytes", data: fullData, labels: fullLabels, modelPath: "/m/custom.onnx",
			status: perchCalibrationFullModel},
		{name: "unknown slice labels", data: unknownData, labels: unknownLabels, modelPath: "/m/custom.onnx",
			status: perchCalibrationUnknownLabels},
		{name: "stale catalog without the field gets the built-in value",
			catalog: cloneEmbeddedPerch(func(v *CatalogVariant) { v.ScoreCalibration = nil }),
			data:    mauData, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "stale catalog with an old value gets the built-in value",
			catalog: cloneEmbeddedPerch(func(v *CatalogVariant) {
				if v.ScoreCalibration != nil {
					v.ScoreCalibration = &ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: staleRestLogit}
				}
			}),
			data: mauData, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "active-only custom entry applies",
			catalog: []CatalogEntry{customPerchEntry(unknownData, "custom", customLabelCount,
				&ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: customRestLogit})},
			data: unknownData, labels: unknownLabels, modelPath: "/m/custom_model.onnx",
			status: perchCalibrationApplied, variantID: "custom@custom", rest: customRestLogit},
		{name: "active-only global entry is the full model",
			catalog: []CatalogEntry{customPerchEntry(unknownData, "", customLabelCount, nil)},
			data:    unknownData, labels: unknownLabels, modelPath: "/m/custom_model.onnx",
			status: perchCalibrationFullModel},
		{name: "active custom entry cannot claim a built-in tile",
			catalog: []CatalogEntry{customPerchEntry(mauData, "", len(mauLabels), nil)},
			data:    mauData, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "active entry hashing resaved CRLF labels cannot override the built-in tile",
			catalog: []CatalogEntry{customPerchEntry(crlf, "custom", len(mauLabels),
				&ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: staleRestLogit})},
			data: crlf, labels: mauLabels, modelPath: "/m/perch_v2_mauritius_no_dft.onnx",
			status: perchCalibrationApplied, variantID: "no-dft-fp32@" + mauritiusTile, rest: want.RestLogit},
		{name: "out-of-range value is skipped",
			catalog: []CatalogEntry{customPerchEntry(unknownData, "custom", customLabelCount,
				&ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: scoreCalibrationRestLogitMax + 1})},
			data: unknownData, labels: unknownLabels, modelPath: "/m/custom_model.onnx",
			status: perchCalibrationUnknownLabels},
		{name: "non-finite value is skipped",
			catalog: []CatalogEntry{customPerchEntry(unknownData, "custom", customLabelCount,
				&ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: math.NaN()})},
			data: unknownData, labels: unknownLabels, modelPath: "/m/custom_model.onnx",
			status: perchCalibrationUnknownLabels},
		{name: "unknown method is skipped",
			catalog: []CatalogEntry{customPerchEntry(unknownData, "custom", customLabelCount,
				&ScoreCalibration{Method: "other", RestLogit: customRestLogit})},
			data: unknownData, labels: unknownLabels, modelPath: "/m/custom_model.onnx",
			status: perchCalibrationUnknownLabels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setActiveCatalog(tt.catalog)
			got := perchScoreCalibration(tt.data, tt.labels, tt.modelPath)
			assert.Equal(t, tt.status, got.status)
			assert.Equal(t, labelsSHA256Hex(tt.data), got.labelsSHA256)
			if tt.status == perchCalibrationApplied {
				assert.Equal(t, tt.variantID, got.variantID)
				assert.InDelta(t, tt.rest, got.calibration.RestLogit, 0)
				assert.Equal(t, ScoreCalibrationSoftmaxRestLogit, got.calibration.Method)
			}
		})
	}
}

// TestPerchScoreCalibration_LabelsFixtureMatchesCatalog guards the fixture itself:
// the stored labels must be byte-identical to the published file the catalog hashes,
// and the canonical form of its parsed labels must reproduce those bytes.
func TestPerchScoreCalibration_LabelsFixtureMatchesCatalog(t *testing.T) {
	data, labels := mauritiusLabels(t)
	var catalogHash string
	for i := range EmbeddedCatalog {
		if EmbeddedCatalog[i].RegistryID != RegistryIDPerchV2 {
			continue
		}
		for j := range EmbeddedCatalog[i].Variants {
			v := &EmbeddedCatalog[i].Variants[j]
			if v.ID != "no-dft-fp32@"+mauritiusTile {
				continue
			}
			for _, f := range v.Files {
				if f.Role == RoleLabels {
					catalogHash = f.SHA256
				}
			}
		}
	}
	require.NotEmpty(t, catalogHash)
	assert.Equal(t, catalogHash, labelsSHA256Hex(data))
	assert.Equal(t, catalogHash, labelsSHA256Hex([]byte(joinLines(labels))), "canonical form must equal the published bytes")
}

// TestLogPerchCalibration checks what a build logs per lookup status: the applied
// calibration, a warning for unknown slice labels, and nothing for the full model.
// It swaps the global logger, so it must not run in parallel.
func TestLogPerchCalibration(t *testing.T) {
	tests := []struct {
		name     string
		lookup   perchCalibrationLookup
		contains []string
		silent   bool
	}{
		{name: "applied", lookup: perchCalibrationLookup{
			status: perchCalibrationApplied, variantID: "no-dft-fp32@nordic",
			calibration: ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: testRestLogit},
		}, contains: []string{"score calibration applied", "no-dft-fp32@nordic", "rest_logit=9.75", ScoreCalibrationSoftmaxRestLogit}},
		{name: "unknown labels", lookup: perchCalibrationLookup{status: perchCalibrationUnknownLabels, labelsSHA256: "abc123"},
			contains: []string{"match no catalog Perch model", "level=WARN", "labels_sha256=abc123", "species=40"}},
		{name: "full model", lookup: perchCalibrationLookup{status: perchCalibrationFullModel}, silent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := newCaptureLogger(t)
			logPerchCalibration(GetLogger(), tt.lookup, customLabelCount)
			if tt.silent {
				assert.Empty(t, buf.String())
				return
			}
			for _, want := range tt.contains {
				assert.Contains(t, buf.String(), want)
			}
		})
	}
}

// TestResolvePerchCalibration covers the step NewPerch runs after parsing the
// labels: the lookup result must end up as the calibrated flag and rest logit the
// instance stores, with one log line per build. It swaps the global logger, so it
// must not run in parallel.
func TestResolvePerchCalibration(t *testing.T) {
	mauData, mauLabels := mauritiusLabels(t)
	want := embeddedScoreCalibration(t, "no-dft-fp32@"+mauritiusTile)
	unknownData, unknownLabels := syntheticLabelBytes(customLabelCount)
	fullData, fullLabels := syntheticLabelBytes(perchFullClassCount)

	tests := []struct {
		name       string
		data       []byte
		labels     []string
		calibrated bool
		rest       float64
		logged     string
	}{
		{name: "catalog slice", data: mauData, labels: mauLabels, calibrated: true, rest: want.RestLogit, logged: "score calibration applied"},
		{name: "unknown slice", data: unknownData, labels: unknownLabels, logged: "match no catalog Perch model"},
		{name: "full model", data: fullData, labels: fullLabels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := newCaptureLogger(t)
			calibrated, rest := resolvePerchCalibration(tt.data, tt.labels, "/m/perch_v2_mauritius_no_dft.onnx")
			assert.Equal(t, tt.calibrated, calibrated)
			assert.InDelta(t, tt.rest, rest, 0)
			if tt.logged == "" {
				assert.Empty(t, buf.String())
				return
			}
			assert.Equal(t, 1, strings.Count(buf.String(), tt.logged), "exactly one log line per build")
		})
	}
}

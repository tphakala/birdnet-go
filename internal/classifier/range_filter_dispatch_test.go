package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestResolveRangeFilterBackend verifies the dispatch decision that routes a
// range-filter config to the TFLite, strict-ONNX, or mapped-geomodel backend.
//
// The critical case is the orphaned geomodel config (GitHub #3320): BirdNET v2.4
// with rangefilter.model="" but modelpath/labelspath still pointing at the 12,012
// species geomodel. It MUST route through the name-matching mapped path (which
// scores by scientific name and honors passunmappedspecies) instead of the strict
// ONNX path, where the geomodel's 12,012 outputs vs the classifier's ~6,559 labels
// produce a fatal LabelCountError and a silent fail-open.
func TestResolveRangeFilterBackend(t *testing.T) {
	t.Parallel()

	const sharedModelPathPrefix = "/data/model/shared/"

	var (
		geomodelONNX   = sharedModelPathPrefix + conf.GeomodelONNXLocalName
		geomodelLabels = sharedModelPathPrefix + conf.GeomodelLabelsLocalName
		customONNX     = "/data/model/custom_rangefilter.onnx"
	)

	tests := []struct {
		name string
		rf   conf.RangeFilterSettings
		want rangeFilterBackend
	}{
		{
			name: "orphaned geomodel on v2.4 routes to mapped path",
			rf: conf.RangeFilterSettings{
				Model:      "",
				ModelPath:  geomodelONNX,
				LabelsPath: geomodelLabels,
			},
			want: rangeFilterBackendMappedGeomodel,
		},
		{
			name: "explicit v3 geomodel routes to mapped path",
			rf: conf.RangeFilterSettings{
				Model:      "v3",
				ModelPath:  geomodelONNX,
				LabelsPath: geomodelLabels,
			},
			want: rangeFilterBackendMappedGeomodel,
		},
		{
			name: "v3 with empty paths still routes to mapped path (init reports missing paths)",
			rf: conf.RangeFilterSettings{
				Model:      "v3",
				ModelPath:  "",
				LabelsPath: "",
			},
			want: rangeFilterBackendMappedGeomodel,
		},
		{
			name: "custom onnx without labels file uses strict ONNX path",
			rf: conf.RangeFilterSettings{
				Model:      "",
				ModelPath:  customONNX,
				LabelsPath: "",
			},
			want: rangeFilterBackendONNXStrict,
		},
		{
			name: "default empty config uses embedded TFLite",
			rf: conf.RangeFilterSettings{
				Model:      "",
				ModelPath:  "",
				LabelsPath: "",
			},
			want: rangeFilterBackendTFLite,
		},
		{
			name: "legacy model uses embedded TFLite",
			rf: conf.RangeFilterSettings{
				Model:     "legacy",
				ModelPath: "",
			},
			want: rangeFilterBackendTFLite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rf := tt.rf
			got := resolveRangeFilterBackend(&rf)
			assert.Equal(t, tt.want, got, "backend for %+v", tt.rf)
		})
	}
}

// TestHasNativeRangeFilter verifies which classifiers can fall back to an embedded
// TFLite range filter when an ONNX geomodel cannot be loaded (ORT missing, file
// missing, corrupt). Only BirdNET v2.4 ships the embedded MData range filter; Perch
// v2 and BirdNET v3.0 rely solely on the ONNX geomodel, so a load failure for them
// must surface as unhealthy instead of silently falling back.
func TestHasNativeRangeFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		{name: "BirdNET v2.4 has embedded TFLite range filter", modelID: "BirdNET_V2.4", want: true},
		{name: "Perch v2 has no native range filter", modelID: RegistryIDPerchV2, want: false},
		{name: "BirdNET v3.0 has no native range filter", modelID: RegistryIDBirdNETV3, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info, ok := ModelRegistry[tt.modelID]
			require.True(t, ok, "model %s must exist in registry", tt.modelID)
			bn := &BirdNET{ModelInfo: info}
			assert.Equal(t, tt.want, bn.hasNativeRangeFilter())
		})
	}

	// A custom or future TFLite-backed classifier is not BirdNET v2.4, so it must not
	// fall back to the v2.4-specific embedded MData range filter; it should surface as
	// unhealthy instead of silently filtering against mismatched labels.
	t.Run("custom non-v2.4 TFLite model has no native range filter", func(t *testing.T) {
		t.Parallel()
		bn := &BirdNET{ModelInfo: ModelInfo{ID: "Custom_TFLite", Backend: BackendTFLite}}
		assert.False(t, bn.hasNativeRangeFilter())
	})
}

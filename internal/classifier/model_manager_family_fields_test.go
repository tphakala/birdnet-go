package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFamilyFields pins the single family-to-settings-field mapping. Every pointer
// the accessor returns must be the exact address of the settings field it names (or
// nil when the family has no such field), so a caller reading or writing through the
// set touches exactly the right field. Table-driven so a new family mapping is a
// single row and cannot silently omit an assertion. Unlike the pre-consolidation
// accessor, the primary now exposes its Labels pointer like every other family; the
// "a variant swap writes the model field only" rule lives in applyConfigForVariantSwap,
// pinned separately.
func TestFamilyFields(t *testing.T) {
	t.Parallel()

	s := &conf.Settings{}
	s.BirdNET.ModelPath = "primary.tflite"
	s.BirdNET.LabelPath = "user-labels.txt"
	s.BirdNET.Threshold = 0.3
	s.BirdNET.Locale = "en-uk"
	s.Perch.ModelPath = "perch.onnx"
	s.Perch.LabelPath = "perch-labels.txt"
	s.Perch.Threshold = 0.5
	s.Perch.OverrideThreshold = true
	s.Perch.Locale = "fi"
	s.BirdNETV3.ModelPath = "v3.onnx"
	s.BirdNETV3.LabelPath = "v3-labels.txt"
	s.BirdNETV3.Threshold = 0.6
	s.BirdNETV3.Locale = "de"
	s.BSG.ModelPath = "bsg.onnx"
	s.BSG.LabelPath = "bsg-labels.txt"
	s.BSG.Locale = "sv"
	s.Bat.ClassifierModel = "bat.onnx"
	s.Bat.LabelPath = "bat-labels.txt"
	s.Bat.EmbeddingModel = "bat-emb.onnx"
	s.Bat.Threshold = 0.7
	s.Bat.Locale = "en-uk"

	// The want* fields are the exact field addresses the accessor must return; nil
	// means the family has no such field and it must never be written.
	tests := []struct {
		name          string
		registryID    string
		wantModel     *string
		wantLabels    *string
		wantEmbed     *string
		wantThreshold *float64
		wantOverride  *bool
		wantLocale    *string
		wantOK        bool
	}{
		{
			name:          "primary maps every field including Labels (LabelPath)",
			registryID:    permanentRegistryID,
			wantModel:     &s.BirdNET.ModelPath,
			wantLabels:    &s.BirdNET.LabelPath,
			wantThreshold: &s.BirdNET.Threshold,
			wantLocale:    &s.BirdNET.Locale,
			wantOK:        true,
		},
		{
			name:          "perch has model, labels, threshold, override, locale; no embeddings",
			registryID:    RegistryIDPerchV2,
			wantModel:     &s.Perch.ModelPath,
			wantLabels:    &s.Perch.LabelPath,
			wantThreshold: &s.Perch.Threshold,
			wantOverride:  &s.Perch.OverrideThreshold,
			wantLocale:    &s.Perch.Locale,
			wantOK:        true,
		},
		{
			name:          "birdnet v3 has model, labels, threshold, override, locale; no embeddings",
			registryID:    RegistryIDBirdNETV3,
			wantModel:     &s.BirdNETV3.ModelPath,
			wantLabels:    &s.BirdNETV3.LabelPath,
			wantThreshold: &s.BirdNETV3.Threshold,
			wantOverride:  &s.BirdNETV3.OverrideThreshold,
			wantLocale:    &s.BirdNETV3.Locale,
			wantOK:        true,
		},
		{
			name:       "bsg has model, labels, locale; no threshold, override or embeddings",
			registryID: RegistryIDBSG,
			wantModel:  &s.BSG.ModelPath,
			wantLabels: &s.BSG.LabelPath,
			wantLocale: &s.BSG.Locale,
			wantOK:     true,
		},
		{
			name:          "bat carries model, labels, embeddings, threshold, locale; no override",
			registryID:    RegistryIDBat,
			wantModel:     &s.Bat.ClassifierModel,
			wantLabels:    &s.Bat.LabelPath,
			wantEmbed:     &s.Bat.EmbeddingModel,
			wantThreshold: &s.Bat.Threshold,
			wantLocale:    &s.Bat.Locale,
			wantOK:        true,
		},
		{
			name:       "unknown registry yields no fields",
			registryID: "NoSuchModel",
			wantOK:     false,
		},
		{
			name:       "empty registry ID yields no fields",
			registryID: "",
			wantOK:     false,
		},
		{
			name:       "Custom sentinel is not a registry key",
			registryID: modelIDCustom,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// s is only read (familyFields returns pointers into it, never mutates),
			// so sharing it across parallel subtests is safe.
			fs, ok := familyFields(s, tt.registryID)
			assert.Equal(t, tt.wantOK, ok)
			assertSamePtrOrNil(t, tt.wantModel, fs.Model, "Model")
			assertSamePtrOrNil(t, tt.wantLabels, fs.Labels, "Labels")
			assertSamePtrOrNil(t, tt.wantEmbed, fs.Embeddings, "Embeddings")
			assertSamePtrOrNil(t, tt.wantThreshold, fs.Threshold, "Threshold")
			assertSamePtrOrNil(t, tt.wantOverride, fs.OverrideThreshold, "OverrideThreshold")
			assertSamePtrOrNil(t, tt.wantLocale, fs.Locale, "Locale")
		})
	}
}

// TestFamilyFields_NilSettings pins the nil-receiver contract: the zero set and
// ok=false, with every pointer nil so no caller can dereference one.
func TestFamilyFields_NilSettings(t *testing.T) {
	t.Parallel()
	fs, ok := familyFields(nil, RegistryIDPerchV2)
	assert.False(t, ok)
	assert.Nil(t, fs.Model)
	assert.Nil(t, fs.Labels)
	assert.Nil(t, fs.Embeddings)
	assert.Nil(t, fs.Threshold)
	assert.Nil(t, fs.OverrideThreshold)
	assert.Nil(t, fs.Locale)
}

// TestFamilyFields_PrimaryLabelsIsLabelPath makes the design change explicit: the
// primary's Labels pointer is &s.BirdNET.LabelPath, not nil. The "model only" rule
// for a variant swap now lives in applyConfigForVariantSwap, and planPathCorrection
// still never rewrites the primary's label path because resolvePrimaryModelPath only
// ever resolves a model path (pinned by TestResolvePrimaryModelPath_ResolvesModelOnly).
func TestFamilyFields_PrimaryLabelsIsLabelPath(t *testing.T) {
	t.Parallel()
	s := &conf.Settings{}
	fs, ok := familyFields(s, permanentRegistryID)
	assert.True(t, ok)
	assert.Same(t, &s.BirdNET.LabelPath, fs.Labels, "the primary's Labels pointer must be &BirdNET.LabelPath")
}

// assertSamePtrOrNil asserts got is exactly the expected field address, or nil when
// the family has no such field.
func assertSamePtrOrNil[T any](t *testing.T, want, got *T, field string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got, "%s pointer must be nil for a family without that field", field)
		return
	}
	assert.Same(t, want, got, "%s pointer must be the settings field's own address", field)
}

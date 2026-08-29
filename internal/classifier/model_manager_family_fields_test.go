package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFamilyPathFields pins the single family-to-settings-field mapping, including
// the primary's model-only contract (nil labels/embeddings so planPathCorrection
// can never rewrite BirdNET.LabelPath) and BSG, which an earlier copy of this
// switch omitted. Table-driven so a new family mapping is a single row and cannot
// silently omit an assertion.
func TestFamilyPathFields(t *testing.T) {
	t.Parallel()

	s := &conf.Settings{}
	s.BirdNET.ModelPath = "primary.tflite"
	s.BirdNET.LabelPath = "user-labels.txt"
	s.Perch.ModelPath = "perch.onnx"
	s.Perch.LabelPath = "perch-labels.txt"
	s.BirdNETV3.ModelPath = "v3.onnx"
	s.BirdNETV3.LabelPath = "v3-labels.txt"
	s.BSG.ModelPath = "bsg.onnx"
	s.BSG.LabelPath = "bsg-labels.txt"
	s.Bat.ClassifierModel = "bat.onnx"
	s.Bat.LabelPath = "bat-labels.txt"
	s.Bat.EmbeddingModel = "bat-emb.onnx"

	// wantModel/wantLabels/wantEmbeddings are the exact field addresses the accessor
	// must return (nil = the family has no such field and it must never be written).
	tests := []struct {
		name           string
		registryID     string
		wantModel      *string
		wantLabels     *string
		wantEmbeddings *string
		wantOK         bool
	}{
		{
			name:       "primary is model-only (nil labels protects the embedded label set)",
			registryID: permanentRegistryID,
			wantModel:  &s.BirdNET.ModelPath,
			wantOK:     true,
		},
		{
			name:       "perch has model+labels, no embeddings",
			registryID: RegistryIDPerchV2,
			wantModel:  &s.Perch.ModelPath,
			wantLabels: &s.Perch.LabelPath,
			wantOK:     true,
		},
		{
			name:       "birdnet v3 has model+labels, no embeddings",
			registryID: RegistryIDBirdNETV3,
			wantModel:  &s.BirdNETV3.ModelPath,
			wantLabels: &s.BirdNETV3.LabelPath,
			wantOK:     true,
		},
		{
			name:       "bsg is mapped (the field an earlier switch omitted)",
			registryID: RegistryIDBSG,
			wantModel:  &s.BSG.ModelPath,
			wantLabels: &s.BSG.LabelPath,
			wantOK:     true,
		},
		{
			name:           "bat carries all three",
			registryID:     RegistryIDBat,
			wantModel:      &s.Bat.ClassifierModel,
			wantLabels:     &s.Bat.LabelPath,
			wantEmbeddings: &s.Bat.EmbeddingModel,
			wantOK:         true,
		},
		{
			name:       "unknown registry yields no fields",
			registryID: "NoSuchModel",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// s is only read (familyPathFields returns pointers into it, never
			// mutates), so sharing it across parallel subtests is safe.
			model, labels, embeddings, ok := familyPathFields(s, tt.registryID)
			assert.Equal(t, tt.wantOK, ok)
			assertSameOrNil(t, tt.wantModel, model, "model")
			assertSameOrNil(t, tt.wantLabels, labels, "labels")
			assertSameOrNil(t, tt.wantEmbeddings, embeddings, "embeddings")
		})
	}

	t.Run("nil settings is not ok", func(t *testing.T) {
		t.Parallel()
		model, labels, embeddings, ok := familyPathFields(nil, RegistryIDPerchV2)
		assert.False(t, ok)
		assert.Nil(t, model)
		assert.Nil(t, labels)
		assert.Nil(t, embeddings)
	})
}

// assertSameOrNil asserts got is exactly the expected field address, or nil when
// the family has no such field.
func assertSameOrNil(t *testing.T, want, got *string, field string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got, "%s pointer must be nil for a family without that field", field)
		return
	}
	assert.Same(t, want, got, "%s pointer must be the settings field's own address", field)
}

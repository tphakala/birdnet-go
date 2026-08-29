package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestFamilyPathFields pins the single family-to-settings-field mapping, including
// the primary's model-only contract (nil labels/embeddings so planPathCorrection
// can never rewrite BirdNET.LabelPath) and BSG, which an earlier copy of this
// switch omitted.
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

	t.Run("primary is model-only", func(t *testing.T) {
		t.Parallel()
		model, labels, emb, ok := familyPathFields(s, permanentRegistryID)
		assert.True(t, ok)
		assert.Same(t, &s.BirdNET.ModelPath, model)
		assert.Nil(t, labels, "the primary's label set is embedded; LabelPath must never be rewritten")
		assert.Nil(t, emb)
	})

	t.Run("perch has model+labels, no embeddings", func(t *testing.T) {
		t.Parallel()
		model, labels, emb, ok := familyPathFields(s, RegistryIDPerchV2)
		assert.True(t, ok)
		assert.Same(t, &s.Perch.ModelPath, model)
		assert.Same(t, &s.Perch.LabelPath, labels)
		assert.Nil(t, emb)
	})

	t.Run("bsg is mapped (the field an earlier switch omitted)", func(t *testing.T) {
		t.Parallel()
		model, labels, emb, ok := familyPathFields(s, RegistryIDBSG)
		assert.True(t, ok)
		assert.Same(t, &s.BSG.ModelPath, model)
		assert.Same(t, &s.BSG.LabelPath, labels)
		assert.Nil(t, emb)
	})

	t.Run("bat carries all three", func(t *testing.T) {
		t.Parallel()
		model, labels, emb, ok := familyPathFields(s, RegistryIDBat)
		assert.True(t, ok)
		assert.Same(t, &s.Bat.ClassifierModel, model)
		assert.Same(t, &s.Bat.LabelPath, labels)
		assert.Same(t, &s.Bat.EmbeddingModel, emb)
	})

	t.Run("unknown registry and nil settings are not ok", func(t *testing.T) {
		t.Parallel()
		_, _, _, ok := familyPathFields(s, "NoSuchModel")
		assert.False(t, ok)
		_, _, _, okNil := familyPathFields(nil, RegistryIDPerchV2)
		assert.False(t, okNil)
	})
}

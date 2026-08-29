package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestUsesONNXBackend verifies backend dispatch: the ONNX path is taken either
// when the configured model path is an .onnx file (explicit selection) or when
// the resolved ModelInfo declares the ONNX backend (the arm64 INT8 default,
// where the model path is empty).
func TestUsesONNXBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		modelPath string
		backend   string
		want      bool
	}{
		{"explicit onnx model path", "/models/x.onnx", BackendTFLite, true},
		{"onnx backend with empty model path", "", BackendONNX, true},
		{"tflite backend with empty model path", "", BackendTFLite, false},
		{"tflite backend with tflite model path", "/models/x.tflite", BackendTFLite, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bn := birdNETWithModelPath(tt.modelPath, &ModelInfo{Backend: tt.backend})
			assert.Equal(t, tt.want, bn.usesONNXBackend())
		})
	}
}

// TestUsesONNXBackend_PathNormalization verifies isONNXModel's env-var expansion
// and case-folding are exercised through usesONNXBackend (not parallel: t.Setenv).
func TestUsesONNXBackend_PathNormalization(t *testing.T) {
	t.Run("uppercase extension", func(t *testing.T) {
		bn := birdNETWithModelPath("/models/X.ONNX", &ModelInfo{Backend: BackendTFLite})
		assert.True(t, bn.usesONNXBackend())
	})
	t.Run("env var expansion", func(t *testing.T) {
		t.Setenv("TEST_MODELS_DIR", "/models")
		bn := birdNETWithModelPath("$TEST_MODELS_DIR/x.onnx", &ModelInfo{Backend: BackendTFLite})
		assert.True(t, bn.usesONNXBackend())
	})
}

// TestONNXModelPath verifies the ONNX model file resolves from the explicit
// config model path when set, and otherwise from ModelInfo.CustomPath (set by
// the arm64 default resolver). This keeps the default from having to mutate
// settings.BirdNET.ModelPath, which would make it look like a user override.
func TestONNXModelPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modelPath  string
		customPath string
		want       string
	}{
		{"explicit model path wins", "/models/x.onnx", "/models/y.onnx", "/models/x.onnx"},
		{"falls back to custom path", "", "/models/y.onnx", "/models/y.onnx"},
		{"both empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bn := birdNETWithModelPath(tt.modelPath, &ModelInfo{CustomPath: tt.customPath})
			assert.Equal(t, tt.want, bn.onnxModelPath())
		})
	}
}

// birdNETWithModelPath builds a BirdNET the way NewBirdNET does for a caller with
// no path resolver: the configured setting AND the resolved primary path both
// carry modelPath.
//
// Setting only Settings.BirdNET.ModelPath is not enough. Every load-path consumer
// reads configuredModelPath(), which returns the RESOLVED path, so a literal that
// leaves primaryPath at its zero value describes the "configured file was missing
// and we recovered to the built-in baseline" state rather than the "user
// configured this file" state these tests mean.
func birdNETWithModelPath(modelPath string, info *ModelInfo) *BirdNET {
	bn := &BirdNET{Settings: &conf.Settings{}, ModelInfo: *info}
	bn.Settings.BirdNET.ModelPath = modelPath
	bn.primaryPath = pathResolution{resolved: modelFileSet{model: modelPath}}
	return bn
}

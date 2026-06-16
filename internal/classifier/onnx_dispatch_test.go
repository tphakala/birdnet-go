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
			bn := &BirdNET{Settings: &conf.Settings{}, ModelInfo: ModelInfo{Backend: tt.backend}}
			bn.Settings.BirdNET.ModelPath = tt.modelPath
			assert.Equal(t, tt.want, bn.usesONNXBackend())
		})
	}
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
			bn := &BirdNET{Settings: &conf.Settings{}, ModelInfo: ModelInfo{CustomPath: tt.customPath}}
			bn.Settings.BirdNET.ModelPath = tt.modelPath
			assert.Equal(t, tt.want, bn.onnxModelPath())
		})
	}
}

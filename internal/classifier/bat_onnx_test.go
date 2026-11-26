package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference"
	ort "github.com/tphakala/birdnet-go/internal/inference/onnx"
)

func TestBatEmbeddingClassifierOptions(t *testing.T) {
	t.Parallel()

	configuredLabels := []string{"Turdus merula_Common Blackbird"}

	cases := []struct {
		name           string
		cfg            *BatModelConfig
		expectedLabels []string
		expectedThread int
	}{
		{
			name:           "nil config falls back to placeholder",
			cfg:            nil,
			expectedLabels: []string{batEmbeddingPlaceholderLabel},
		},
		{
			name:           "nil embedding labels falls back to placeholder",
			cfg:            &BatModelConfig{Threads: 2},
			expectedLabels: []string{batEmbeddingPlaceholderLabel},
			expectedThread: 2,
		},
		{
			name:           "empty embedding labels falls back to placeholder",
			cfg:            &BatModelConfig{EmbeddingLabels: []string{}, Threads: 4},
			expectedLabels: []string{batEmbeddingPlaceholderLabel},
			expectedThread: 4,
		},
		{
			name:           "configured embedding labels preserved",
			cfg:            &BatModelConfig{EmbeddingLabels: configuredLabels, Threads: 3},
			expectedLabels: configuredLabels,
			expectedThread: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := batEmbeddingClassifierOptions(tc.cfg)
			assert.Equal(t, tc.expectedLabels, opts.Labels)
			assert.Equal(t, tc.expectedThread, opts.Threads)
			assert.True(t, opts.SkipLabelValidation)
		})
	}
}

func TestBatEmbeddingClassifierOptions_ClearsONNXLabelGuard(t *testing.T) {
	t.Parallel()

	_, err := inference.NewONNXClassifier("", inference.ONNXClassifierOptions{
		SkipLabelValidation: true,
	})
	require.ErrorContains(t, err, "ONNX classifier requires labels")

	_, err = inference.NewONNXClassifier("", batEmbeddingClassifierOptions(&BatModelConfig{Threads: 2}))
	require.Error(t, err)
	require.ErrorIs(t, err, ort.ErrModelPathRequired)
}

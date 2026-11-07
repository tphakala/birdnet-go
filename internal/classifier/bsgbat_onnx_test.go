package classifier

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
)

func TestParseBSGBatLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		in             string
		wantSpecies    []string
		wantBackground int
		wantErr        bool
	}{
		{
			name:           "species then background",
			in:             "Myotis daubentonii\nNyctalus noctula\nBackground\n",
			wantSpecies:    []string{"Myotis daubentonii", "Nyctalus noctula"},
			wantBackground: 2,
		},
		{
			name:           "blank lines and carriage returns are ignored",
			in:             "Myotis daubentonii\r\n\r\nBackground\r\n",
			wantSpecies:    []string{"Myotis daubentonii"},
			wantBackground: 1,
		},
		{
			name:           "background position is kept wherever it sits",
			in:             "Background\nMyotis daubentonii\n",
			wantSpecies:    []string{"Myotis daubentonii"},
			wantBackground: 0,
		},
		{name: "no background class is rejected", in: "Myotis daubentonii\n", wantErr: true},
		{name: "no species is rejected", in: "Background\n", wantErr: true},
		{name: "a second background class is rejected", in: "Myotis daubentonii\nBackground\nBackground\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			species, background, err := parseBSGBatLabels([]byte(tt.in))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSpecies, species)
			assert.Equal(t, tt.wantBackground, background)
		})
	}
}

// fakeTensorModel returns fixed raw outputs so Predict can run without a model file.
type fakeTensorModel struct {
	shape   []int64
	outputs [][]float32
	inputs  [][]float32
	closed  bool
}

func (f *fakeTensorModel) Run(input []float32) ([][]float32, error) {
	f.inputs = append(f.inputs, slices.Clone(input))
	return f.outputs, nil
}
func (f *fakeTensorModel) InputShape() []int64 {
	if f.shape != nil {
		return f.shape
	}
	return []int64{1, 1, bsgbat.NumFrames, bsgbat.NumMels}
}
func (f *fakeTensorModel) OutputSizes() []int {
	sizes := make([]int, len(f.outputs))
	for i, o := range f.outputs {
		sizes[i] = len(o)
	}
	return sizes
}
func (f *fakeTensorModel) Close() { f.closed = true }

// newFakeBSGBat builds an instance over a fake model whose labels are two species
// and Background, with Background in the middle to prove its logit is skipped.
func newFakeBSGBat(t *testing.T, logits []float32) (*BSGBat, *fakeTensorModel) {
	t.Helper()
	frontEnd, err := bsgbat.NewFrontEnd()
	require.NoError(t, err)
	model := &fakeTensorModel{outputs: [][]float32{logits}}
	info := ModelRegistry[RegistryIDBSGBat]
	return &BSGBat{
		model:           model,
		frontEnd:        frontEnd,
		labels:          []string{"Myotis daubentonii", "Nyctalus noctula"},
		backgroundIndex: 1,
		info:            info,
		modelPath:       "/models/bsgbat.onnx",
	}, model
}

func TestBSGBat_Predict(t *testing.T) {
	t.Parallel()

	// A noisy-looking window; the fake model ignores it, but silence would make the
	// front end return all zeros and hide a wiring mistake.
	window := make([]float32, bsgbat.ClipSamples)
	for i := range window {
		window[i] = float32(math.Sin(float64(i) * 0.37))
	}

	t.Run("drops background, applies sigmoid and no threshold", func(t *testing.T) {
		t.Parallel()
		// Background (index 1) has the largest logit and must not surface. The
		// second species has a logit far below any threshold and is still returned,
		// because the processor, not Predict, applies the threshold.
		inst, model := newFakeBSGBat(t, []float32{1, 9, -6})
		results, err := inst.Predict(t.Context(), [][]float32{window})
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Equal(t, "Myotis daubentonii", results[0].Species)
		assert.InDelta(t, 1/(1+math.Exp(-1)), float64(results[0].Confidence), 1e-6)
		assert.Equal(t, "Nyctalus noctula", results[1].Species)
		assert.InDelta(t, 1/(1+math.Exp(6)), float64(results[1].Confidence), 1e-6)
		for _, r := range results {
			assert.NotEqual(t, bsgbat.BackgroundLabel, r.Species)
		}
		require.Len(t, model.inputs, 1)
		assert.Len(t, model.inputs[0], bsgbat.InputSize, "the model receives one 512 x 128 spectrogram")
	})

	t.Run("a window of the wrong length is an error", func(t *testing.T) {
		t.Parallel()
		inst, model := newFakeBSGBat(t, []float32{0, 0, 0})
		_, err := inst.Predict(t.Context(), [][]float32{window[:bsgbat.ClipSamples-1]})
		require.Error(t, err)
		assert.Empty(t, model.inputs, "the model must not run on a malformed window")
	})

	t.Run("a non-finite logit fails the window", func(t *testing.T) {
		t.Parallel()
		inst, _ := newFakeBSGBat(t, []float32{0, float32(math.NaN()), 0})
		_, err := inst.Predict(t.Context(), [][]float32{window})
		require.ErrorIs(t, err, ErrNonFiniteScore, "a NaN in the Background column fails the window too")
	})

	t.Run("an output of the wrong width is an error", func(t *testing.T) {
		t.Parallel()
		inst, _ := newFakeBSGBat(t, []float32{0, 0})
		_, err := inst.Predict(t.Context(), [][]float32{window})
		require.Error(t, err)
	})

	t.Run("empty input is rejected", func(t *testing.T) {
		t.Parallel()
		inst, _ := newFakeBSGBat(t, []float32{0, 0, 0})
		_, err := inst.Predict(t.Context(), nil)
		require.Error(t, err)
	})

	t.Run("closed instance reports not initialized", func(t *testing.T) {
		t.Parallel()
		inst, model := newFakeBSGBat(t, []float32{0, 0, 0})
		require.NoError(t, inst.Close())
		assert.True(t, model.closed)
		_, err := inst.Predict(t.Context(), [][]float32{window})
		require.Error(t, err)
		require.NoError(t, inst.Close(), "Close is idempotent")
	})
}

func TestBSGBat_Identity(t *testing.T) {
	t.Parallel()

	inst, _ := newFakeBSGBat(t, []float32{0, 0, 0})
	assert.Equal(t, RegistryIDBSGBat, inst.ModelID())
	assert.Equal(t, "BSG-BAT", inst.ModelName())
	assert.Equal(t, DetectionVersionBSGBat, inst.ModelVersion())
	assert.Equal(t, 2, inst.NumSpecies())
	assert.Equal(t, "/models/bsgbat.onnx", inst.ResolvedModelPath())
	assert.Equal(t, ModelRegistry[RegistryIDBSGBat].Spec, inst.Spec())

	device, backend, precision := inst.RuntimeInfo()
	assert.Equal(t, deviceCPU, device)
	assert.Equal(t, BackendONNX, backend)
	assert.Equal(t, string(QuantizationFP32), precision)

	labels := inst.Labels()
	labels[0] = "mutated"
	assert.Equal(t, "Myotis daubentonii", inst.Labels()[0], "Labels returns a copy")
}

func TestCheckBSGBatShapes(t *testing.T) {
	t.Parallel()

	good := &fakeTensorModel{outputs: [][]float32{make([]float32, 22)}}
	require.NoError(t, checkBSGBatShapes(good, 22))
	require.Error(t, checkBSGBatShapes(good, 21), "output width must match the label count")
	require.Error(t, checkBSGBatShapes(&fakeTensorModel{}, 22), "a model without outputs is rejected")

	for _, shape := range [][]int64{
		{1, 1, bsgbat.NumMels, bsgbat.NumFrames},
		{1, bsgbat.NumFrames * bsgbat.NumMels},
	} {
		transposed := &fakeTensorModel{shape: shape, outputs: [][]float32{make([]float32, 22)}}
		assert.Error(t, checkBSGBatShapes(transposed, 22), "input shape %v has the right element count but the wrong layout", shape)
	}
}

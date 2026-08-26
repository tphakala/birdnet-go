//go:build !noembed

package vad

import _ "embed" // Embedding the Silero VAD model directly into the binary.

// embeddedModel is the Silero VAD ONNX sequence model: derived from upstream
// snakers4/silero-vad via the official examples/onnx_sequence export at 16 kHz
// (opset 16, ~1.25 MB, MIT license unchanged). One Run scores a whole [n, 576]
// hop sequence with full internal LSTM recurrence, bit-exact to running the hops
// one at a time through the upstream recurrent frame model. It ships in the
// binary and container images like the BirdNET TFLite model, so the privacy VAD
// works out of the box when the feature is enabled and an ONNX Runtime library
// is present. Excluded from -tags noembed builds.
//
//go:embed data/silero_vad.onnx
var embeddedModel []byte

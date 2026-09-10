package vad

import _ "embed" // Embedding the Silero VAD model directly into the binary.

// embeddedModel is the Silero VAD ONNX sequence model: derived from upstream
// snakers4/silero-vad via the official examples/onnx_sequence export at 16 kHz
// (opset 16, ~1.25 MB, MIT license unchanged). One Run scores a whole [n, 576]
// hop sequence with full internal LSTM recurrence, bit-exact to running the hops
// one at a time through the upstream recurrent frame model. The model is tiny and
// MIT-licensed, so it is embedded unconditionally: unlike the large BirdNET
// classifier models (which -tags noembed strips so container images can ship them
// as separate, deduplicated layers), this file has no build constraint. The
// privacy VAD therefore works out of the box in every build, Docker included, when
// the feature is enabled and an ONNX Runtime library is present.
//
//go:embed data/silero_vad.onnx
var embeddedModel []byte

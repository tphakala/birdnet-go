//go:build !noembed

package vad

import _ "embed" // Embedding the Silero VAD model directly into the binary.

// embeddedModel is the Silero VAD ONNX model (upstream v6.2.1, MIT licensed,
// unmodified; byte-identical to the official snakers4/silero-vad export, ~2.3 MB).
// It ships in the binary and container images like the BirdNET TFLite model, so
// the privacy VAD works out of the box when the feature is enabled and an ONNX
// Runtime library is present. Excluded from -tags noembed builds.
//
//go:embed data/silero_vad.onnx
var embeddedModel []byte

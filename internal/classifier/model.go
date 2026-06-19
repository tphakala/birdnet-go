// model.go defines the interfaces for multi-model classifier support.
package classifier

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Device name strings reported by ModelInstance.Device(). CPU-bound backends
// (TFLite, ONNX Runtime CPU EP) report deviceCPU; OpenVINO-backed instances
// report the concrete OpenVINO device (inference.OVDeviceCPU/OVDeviceGPU).
// deviceUnknown is returned by the Orchestrator when a model is not loaded.
const (
	deviceCPU     = "CPU"
	deviceUnknown = "Unknown"
)

// ModelSpec describes a model's fixed audio requirements.
// Overlap is NOT included - it comes from user configuration
// (the false positive filter has multiple levels with specific overlap values).
type ModelSpec struct {
	SampleRate            int           // Hz: 48000 (BirdNET v2.4), 32000 (v3.0, Perch)
	ClipLength            time.Duration // 3s (BirdNET v2.4), 5s (v3.0, Perch)
	RawSampleRate         int           // Hz: when non-zero, the model expects raw audio at this rate (e.g. 256000 for bat detection)
	MinRawSampleRate      int           // Hz: minimum source capture rate for this model (0 = no constraint)
	RecommendedSampleRate int           // Hz: recommended source capture rate (0 = no constraint)
}

// ClipSizeBytes returns the analysis window size in bytes for this model.
// Uses SampleRate (not EffectiveSampleRate) because the model's inference
// layer determines the window size regardless of the source capture rate.
func (s ModelSpec) ClipSizeBytes() int {
	return s.SampleRate * int(s.ClipLength.Seconds()) * conf.NumChannels * conf.BytesPerSample
}

// BufferDimensions returns the analysis buffer dimensions for this model
// with 50% overlap: (clipBytes, overlapBytes, readSize).
func (s ModelSpec) BufferDimensions() (clipBytes, overlapBytes, readSize int) {
	clipBytes = s.ClipSizeBytes()
	overlapBytes = clipBytes / 2
	readSize = clipBytes - overlapBytes
	return
}

// BufferInterval returns how often a new analysis window is produced,
// derived from 50% overlap: ClipLength / 2. If inference exceeds this
// interval the pipeline falls behind real-time input.
func (s ModelSpec) BufferInterval() time.Duration {
	return s.ClipLength / 2
}

// EffectiveSampleRate returns the sample rate the model expects to receive
// audio at. When RawSampleRate is set, the model needs raw audio at that
// rate (e.g. 256kHz bat audio fed without resampling). Otherwise, the
// standard SampleRate is used.
func (s ModelSpec) EffectiveSampleRate() int {
	if s.RawSampleRate > 0 {
		return s.RawSampleRate
	}
	return s.SampleRate
}

// ModelInstance represents a loaded model that can run inference.
// Implementations are NOT goroutine-safe; the Orchestrator serializes access.
type ModelInstance interface {
	// Predict runs inference on the given audio samples.
	// Each inner slice is one clip of float32 PCM at the model's native sample rate.
	Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error)

	// Spec returns the model's fixed audio requirements.
	Spec() ModelSpec

	// ModelID returns the unique identifier for this model (e.g. "BirdNET_V2.4").
	ModelID() string

	// ModelName returns the human-readable model name.
	ModelName() string

	// ModelVersion returns the model version string.
	ModelVersion() string

	// NumSpecies returns the number of species the model can classify.
	NumSpecies() int

	// Labels returns the full list of species labels.
	Labels() []string

	// Device returns the compute device (execution provider) the model's
	// inference actually runs on, resolved from the backend selected at load
	// time. Returns deviceCPU ("CPU") or, for OpenVINO-backed instances, the
	// concrete OpenVINO device (inference.OVDeviceCPU/OVDeviceGPU). It is never
	// inferred from the backend string: the value reflects the real chosen path.
	Device() string

	// Close releases resources held by the model.
	Close() error
}

// NameResolver resolves scientific names to common names.
// Implementations form a chain: BirdNET labels (in-memory) → database/external (future).
type NameResolver interface {
	Resolve(scientificName, locale string) string
}

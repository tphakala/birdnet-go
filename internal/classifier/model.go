// model.go defines the interfaces for multi-model classifier support.
package classifier

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// ModelSpec describes a model's fixed audio requirements.
// Overlap is NOT included — it comes from user configuration
// (the false positive filter has multiple levels with specific overlap values).
type ModelSpec struct {
	SampleRate int           // Hz: 48000 (BirdNET v2.4), 32000 (v3.0, Perch)
	ClipLength time.Duration // 3s (BirdNET v2.4), 5s (v3.0, Perch)
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

	// Close releases resources held by the model.
	Close() error
}

// NameResolver resolves scientific names to common names.
// Implementations form a chain: BirdNET labels (in-memory) → database/external (future).
type NameResolver interface {
	Resolve(scientificName, locale string) string
}

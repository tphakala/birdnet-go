// Package inference provides ML runtime abstractions for species classification
// and geographic range filtering. Implementations handle the details of specific
// inference engines (TFLite, ONNX, etc.) while exposing a unified interface.
package inference

// Classifier abstracts the ML runtime for species classification.
// Implementations are NOT goroutine-safe; callers must synchronize access.
type Classifier interface {
	// Predict runs classification on audio samples, returning raw logits (pre-activation).
	// The input must contain exactly the number of samples expected by the model.
	// Returns one logit per species in label order.
	Predict(samples []float32) ([]float32, error)

	// NumSpecies returns the number of species in the model output.
	NumSpecies() int

	// Close releases all runtime resources.
	Close()
}

// RangeFilter abstracts the ML runtime for geographic range filtering.
// Implementations are NOT goroutine-safe; callers must synchronize access.
type RangeFilter interface {
	// Predict returns species occurrence scores for a geographic location and week.
	// Latitude: [-90, 90], Longitude: [-180, 180], Week: BirdNET week number.
	// Returns one score per species in label order.
	Predict(latitude, longitude, week float32) ([]float32, error)

	// NumSpecies returns the number of species in the model output.
	NumSpecies() int

	// Close releases all runtime resources.
	Close()
}

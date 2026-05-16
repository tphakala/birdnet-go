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

// EmbeddingExtractor extends Classifier with the ability to also return
// embedding vectors from models that produce them (e.g., patched BirdNET v2.4).
type EmbeddingExtractor interface {
	Classifier
	// PredictWithEmbeddings runs classification and returns both raw logits
	// and the embedding vector. Returns nil embeddings if the model does not
	// produce embeddings.
	PredictWithEmbeddings(samples []float32) (logits []float32, embeddings []float32, err error)
}

// CustomClassifier runs secondary classification on embedding vectors.
// Used for custom classification heads (e.g., bat species from BirdNET embeddings).
type CustomClassifier interface {
	// PredictEmbedding runs inference on an embedding vector and returns
	// sigmoid-applied scores for each class.
	PredictEmbedding(embeddings []float32) ([]float32, error)

	// NumClasses returns the number of output classes.
	NumClasses() int

	// Labels returns the classification labels.
	Labels() []string

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

// BatchRangeFilter extends RangeFilter with batch inference support.
// Implementations are NOT goroutine-safe; callers must synchronize access.
type BatchRangeFilter interface {
	RangeFilter
	// PredictBatch runs inference on multiple location/week inputs in a single batch.
	// inputs is a flat slice of [lat, lon, week] triples: len(inputs) must equal batchSize * 3.
	// Returns a flat slice of [batchSize * numSpecies] scores in row-major order.
	PredictBatch(inputs []float32, batchSize int) ([]float32, error)
}

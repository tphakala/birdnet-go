//go:build onnx

package onnx

// ModelType identifies which bird classification model is being used.
type ModelType int

const (
	BirdNETv24 ModelType = iota
	BirdNETv30
	PerchV2
)

func (m ModelType) String() string {
	switch m {
	case BirdNETv24:
		return "BirdNET v2.4"
	case BirdNETv30:
		return "BirdNET v3.0"
	case PerchV2:
		return "Perch v2"
	default:
		return "Unknown"
	}
}

func (m ModelType) SampleRate() int {
	switch m {
	case BirdNETv24:
		return 48000
	case BirdNETv30, PerchV2:
		return 32000
	default:
		return 0
	}
}

func (m ModelType) Duration() float64 {
	switch m {
	case BirdNETv24:
		return 3.0
	case BirdNETv30, PerchV2:
		return 5.0
	default:
		return 0
	}
}

func (m ModelType) SampleCount() int {
	switch m {
	case BirdNETv24:
		return 144000
	case BirdNETv30, PerchV2:
		return 160000
	default:
		return 0
	}
}

// ModelConfig holds the derived parameters for a loaded model.
type ModelConfig struct {
	Type           ModelType
	SampleRate     int
	Duration       float64
	SampleCount    int
	NumOutputs     int
	EmbeddingSize  int     // 0 if model doesn't produce embeddings
	EmbeddingIndex int     // which output tensor contains embeddings, -1 if none
	LogitsIndex    int     // which output tensor contains logits
	InputShape     []int64 // actual shape from ONNX model
}

// Prediction represents a single species prediction with confidence score.
type Prediction struct {
	Species    string
	Confidence float32
	Index      int
}

// Result holds the full output of a classification inference.
type Result struct {
	ModelType   ModelType
	Predictions []Prediction // top-K, sorted descending by confidence
	Embeddings  []float32    // nil if model doesn't produce embeddings
	RawScores   []float32    // all scores after activation (sigmoid/softmax)
}

// LocationScore represents a species' occurrence probability at a given location and date.
type LocationScore struct {
	Species string
	Score   float32
	Index   int
}

package queue

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Results represents the data structure for storing analysis results
type Results struct {
	StartTime   time.Time           // Time when the analysis started
	PCMdata     []byte              // Raw PCM audio data
	Results     []datastore.Results // Slice of analysis results
	ElapsedTime time.Duration       // Time taken for analysis
	ClipName    string              // Name of the audio clip
	Source      string              // Source of the audio data, RSTP URL or audio card name
}

// Copy creates a deep copy of the Results struct
// This is needed because the data in the struct is a pointer to the original data
// and if we don't make a deep copy, the original data will be overwritten when the
// struct is reused for another detection.
func (r Results) Copy() Results { //nolint:gocritic // This is a copy function, avoid warning about heavy parameters
	// Create a new Results struct with simple field copies
	newCopy := Results{
		StartTime:   r.StartTime,
		ElapsedTime: r.ElapsedTime,
		ClipName:    r.ClipName,
		Source:      r.Source,
	}

	// Deep copy PCMdata
	if r.PCMdata != nil {
		newCopy.PCMdata = make([]byte, len(r.PCMdata))
		newCopy.PCMdata = append(newCopy.PCMdata, r.PCMdata...)
	}

	// Deep copy Results slice
	if r.Results != nil {
		newCopy.Results = make([]datastore.Results, len(r.Results))
		for i, result := range r.Results {
			newCopy.Results[i] = result.Copy()
		}
	}

	return newCopy
}

var (
	ResultsQueue chan Results // Channel for main results queue
	RetryQueue   chan Results // Channel for retry queue
)

// Init initializes the ResultsQueue and RetryQueue with specified sizes
func Init(mainSize, retrySize int) {
	ResultsQueue = make(chan Results, mainSize)
	RetryQueue = make(chan Results, retrySize)
}

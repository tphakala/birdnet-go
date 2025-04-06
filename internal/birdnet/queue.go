package birdnet

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Results represents the data structure for storing BirdNET inference results
type Results struct {
	StartTime   time.Time           // Time when the analysis started
	PCMdata     []byte              // Raw PCM audio data
	Results     []datastore.Results // Slice of analysis results
	ElapsedTime time.Duration       // Time taken for analysis
	ClipName    string              // Name of the audio clip
	Source      string              // Source of the audio data, RSTP URL or audio card name
}

// Default buffer size for the results queue
const DefaultQueueSize = 100

// ResultsQueue is a channel for sending analysis results
var ResultsQueue = make(chan Results, DefaultQueueSize)

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
		copy(newCopy.PCMdata, r.PCMdata)
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

// ResizeQueue resizes the results queue to the specified size
func ResizeQueue(size int) {
	// Create a new channel with the specified size
	newQueue := make(chan Results, size)

	// Close the old queue to prevent new writes
	close(ResultsQueue)

	// Drain the old queue into the new one
	for result := range ResultsQueue {
		newQueue <- result
	}

	// Replace the old queue with the new one
	ResultsQueue = newQueue
}

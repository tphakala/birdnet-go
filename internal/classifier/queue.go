package classifier

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Results represents the data structure for storing BirdNET inference results
type Results struct {
	StartTime       time.Time             // Time when the analysis started (back-dated for clip export)
	AudioCapturedAt time.Time             // Wall-clock time when the 3s audio chunk was ready for analysis
	PCMdata         []byte                // Raw PCM audio data
	Results         []datastore.Results   // Slice of analysis results
	ElapsedTime     time.Duration         // Time taken for analysis
	ClipName        string                // Name of the audio clip
	Source          datastore.AudioSource // Audio source with ID, SafeString, and DisplayName
	ModelID         string                // identifies which model produced these results
	// Audio describes the timing of PCMdata on the producer's audio timeline.
	Audio AudioTiming
}

// AudioTiming describes where one analysis window sits on its source's audio
// timeline. The zero value means the producer does not know: speech hits are then
// stamped at processing time and no coverage is claimed for the window.
type AudioTiming struct {
	// EndAt is the capture time of the last sample of the window, corrected for
	// the analysis backlog (the ring is FIFO, so a lagging monitor reads old audio).
	EndAt time.Time
	// Gap reports that the window is not contiguous with the previous window of
	// the same (source, model): the producer lost or spliced audio at or inside
	// its fresh part, this is the first window of a buffer or stream, or the
	// previous window was never analyzed (skipped, failed, or its result dropped
	// by a full results queue).
	Gap bool
	// FreshBytes is the producer's count of bytes at the end of the window that
	// are new audio, not repeated from the previous window: one step of the
	// analysis buffer, or the whole window when its overlap head is zero filled.
	// A Gap caused by lost audio still reports one step; consumers treat any gap
	// window as wholly fresh. Zero when unknown.
	FreshBytes int
	// ContiguousBytes is how many bytes at the end of the window are genuine
	// audio adjacent in time, with no loss, splice or zero fill; a window after a
	// loss still has a contiguous tail. Zero when the producer reports no
	// positions, in which case the whole window is assumed genuine.
	ContiguousBytes int
	// EndPos is the producer's stream position just after the window's last byte
	// (audio refused before it still counts toward it, so a loss leaves a hole),
	// and Generation identifies the stretch of stream it belongs to (unique per
	// buffer instance and per buffer Reset, never shared by a rebuilt buffer). EndPos minus
	// ContiguousBytes is where the contiguous tail starts. Two windows of the same
	// Generation are adjacent when the later tail starts at or before the earlier
	// EndPos, whatever their timestamps say. Generation zero means no positions.
	EndPos     int64
	Generation uint64
}

// Default buffer size for the results queue
const DefaultQueueSize = 100

// ResultsQueue is a channel for sending analysis results.
// OWNERSHIP: Once a Results struct is sent to this queue, the sender must not
// modify it. The receiver takes full ownership of the data. This allows us to
// avoid unnecessary deep copies of the PCM audio data.
var ResultsQueue = make(chan Results, DefaultQueueSize)

// Copy creates a deep copy of the Results struct.
// NOTE: This method is kept for compatibility but is no longer used in the main
// audio processing pipeline. The ownership model has been updated so that data
// ownership transfers through the ResultsQueue without requiring copies.
func (r Results) Copy() Results { //nolint:gocritic // This is a copy function, avoid warning about heavy parameters
	newCopy := r

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

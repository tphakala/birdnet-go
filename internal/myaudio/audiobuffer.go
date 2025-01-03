// this file defines ring buffer which is used for capturing audio clips
package myaudio

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// SegmentInfo contains audio segment data and sample tracking information
type SegmentInfo struct {
	Data             []byte
	StartSampleIdx   uint64
	EndSampleIdx     uint64
	HasDiscontinuity bool
	DiscontinuityAt  []int
}

// AudioBuffer represents a circular buffer for storing PCM audio data, with timestamp tracking.
type AudioBuffer struct {
	data           []byte
	sampleIndices  []uint64
	lastSampleIdx  uint64
	writeIndex     int
	sampleRate     int
	bytesPerSample int
	bufferSize     int
	bufferDuration time.Duration
	startTime      time.Time
	initialized    bool
	lock           sync.Mutex
}

// map to store audio buffers for each audio source
var audioBuffers map[string]*AudioBuffer

func InitAudioBuffers(durationSeconds int, sampleRate, bytesPerSample int, sources []string) {
	audioBuffers = make(map[string]*AudioBuffer)
	for _, source := range sources {
		audioBuffers[source] = NewAudioBuffer(durationSeconds, sampleRate, bytesPerSample)
	}
}

// NewAudioBuffer initializes a new AudioBuffer with timestamp tracking
func NewAudioBuffer(durationSeconds int, sampleRate, bytesPerSample int) *AudioBuffer {
	bufferSize := durationSeconds * sampleRate * bytesPerSample
	alignedBufferSize := ((bufferSize + 2047) / 2048) * 2048
	numSamples := alignedBufferSize / bytesPerSample

	ab := &AudioBuffer{
		data:           make([]byte, alignedBufferSize),
		sampleIndices:  make([]uint64, numSamples),
		lastSampleIdx:  0,
		sampleRate:     sampleRate,
		bytesPerSample: bytesPerSample,
		bufferSize:     alignedBufferSize,

		bufferDuration: time.Second * time.Duration(durationSeconds),
		initialized:    false,
	}
	return ab
}

// Write adds PCM audio data to the buffer for a given source.
func WriteToCaptureBuffer(source string, data []byte) {
	ab, exists := audioBuffers[source]
	if !exists {
		log.Printf("No audio buffer found for source: %s", source)
		return
	}

	ab.Write(data)
}

// ReadSegment extracts a segment of audio data from the buffer for a given source.
func ReadSegmentFromCaptureBuffer(source string, requestedStartTime time.Time, duration int) (*SegmentInfo, error) {
	ab, exists := audioBuffers[source]
	if !exists {
		return nil, fmt.Errorf("No audio buffer found for source: %s", source)
	}

	return ab.ReadSegment(requestedStartTime, duration)
}

// Write adds PCM audio data to the buffer, ensuring thread safety and accurate timekeeping.
func (ab *AudioBuffer) Write(data []byte) {
	// Lock the buffer to prevent concurrent writes or reads from interfering with the update process.
	ab.lock.Lock()
	defer ab.lock.Unlock()

	if !ab.initialized {
		// Initialize the buffer's start time based on the current time.
		ab.startTime = time.Now()
		ab.initialized = true
	}

	// Store previous state for debugging
	prevWriteIndex := ab.writeIndex
	prevLastSampleIdx := ab.lastSampleIdx

	samplesWritten := 0

	// Write data and sample indices
	for i := 0; i < len(data); i += ab.bytesPerSample {
		dataIdx := ab.writeIndex + i
		if dataIdx >= ab.bufferSize {
			dataIdx -= ab.bufferSize
		}

		// Copy audio data
		for j := 0; j < ab.bytesPerSample && i+j < len(data); j++ {
			ab.data[dataIdx+j] = data[i+j]
		}

		// Update sample index
		sampleIdx := (ab.writeIndex + i) / ab.bytesPerSample
		if sampleIdx >= len(ab.sampleIndices) {
			sampleIdx -= len(ab.sampleIndices)
		}
		ab.sampleIndices[sampleIdx] = ab.lastSampleIdx + uint64(samplesWritten)
		samplesWritten++
	}

	ab.writeIndex = (ab.writeIndex + len(data)) % ab.bufferSize
	ab.lastSampleIdx += uint64(samplesWritten)

	if ab.writeIndex <= prevWriteIndex {
		// Buffer wrapped - log more details
		if conf.Setting().Realtime.Audio.Export.Debug {
			log.Printf("Buffer wrap details: prevWriteIndex=%d, newWriteIndex=%d, "+
				"prevLastSampleIdx=%d, newLastSampleIdx=%d, samplesWritten=%d",
				prevWriteIndex, ab.writeIndex,
				prevLastSampleIdx, ab.lastSampleIdx,
				samplesWritten)
		}
		// Adjust startTime with a small safety margin
		ab.startTime = time.Now().Add(-ab.bufferDuration + 100*time.Millisecond)
	}

	if conf.Setting().Realtime.Audio.Export.Debug {
		if ab.writeIndex <= prevWriteIndex {
			log.Printf("Buffer wrapped during write: writeIndex moved from %d to %d, lastSampleIdx: %d",
				prevWriteIndex, ab.writeIndex, ab.lastSampleIdx)
		}
	}
}

// ReadSegment extracts a segment of audio data based on precise start and end times, handling wraparounds.
// It waits until the current time is past the requested end time.
func (ab *AudioBuffer) ReadSegment(requestedStartTime time.Time, duration int) (*SegmentInfo, error) {
	requestedEndTime := requestedStartTime.Add(time.Duration(duration) * time.Second)

	for {
		ab.lock.Lock()

		startOffset := requestedStartTime.Sub(ab.startTime)
		endOffset := requestedEndTime.Sub(ab.startTime)

		startIndex := int(startOffset.Seconds()) * ab.sampleRate * ab.bytesPerSample
		endIndex := int(endOffset.Seconds()) * ab.sampleRate * ab.bytesPerSample

		startIndex = startIndex % ab.bufferSize
		endIndex = endIndex % ab.bufferSize

		if startOffset < 0 {
			if ab.writeIndex == 0 || ab.writeIndex+int(startOffset.Seconds())*ab.sampleRate*ab.bytesPerSample > ab.bufferSize {
				ab.lock.Unlock()
				return nil, errors.New("requested start time is outside the buffer's current timeframe")
			}
			startIndex = (ab.bufferSize + startIndex) % ab.bufferSize
		}

		if endOffset < 0 || endOffset <= startOffset {
			ab.lock.Unlock()
			return nil, errors.New("requested times are outside the buffer's current timeframe")
		}

		// Wait until the current time is past the requested end time
		if time.Now().After(requestedEndTime) {
			var segment []byte

			// Set the sample indices based on the buffer positions
			startSampleIdx := ab.sampleIndices[startIndex/ab.bytesPerSample]
			endSampleIdx := ab.sampleIndices[(endIndex-ab.bytesPerSample)/ab.bytesPerSample]

			discontinuities := make([]int, 0)

			if startIndex < endIndex {
				// Non-wrapped case - check discontinuities in one continuous block
				for i := startIndex/ab.bytesPerSample + 1; i < endIndex/ab.bytesPerSample; i++ {
					if ab.sampleIndices[i] != ab.sampleIndices[i-1]+1 {
						pos := (i * ab.bytesPerSample) - startIndex
						if pos >= 0 {
							discontinuities = append(discontinuities, pos)
							if conf.Setting().Realtime.Audio.Export.Debug {
								log.Printf("Discontinuity at sample %d: expected %d, got %d (gap=%d)",
									i, ab.sampleIndices[i-1]+1, ab.sampleIndices[i],
									ab.sampleIndices[i]-ab.sampleIndices[i-1]-1)
							}
						}
					}
				}
			} else {
				// Wrapped case - check each section separately
				// First part: startIndex to buffer end
				for i := startIndex/ab.bytesPerSample + 1; i < ab.bufferSize/ab.bytesPerSample; i++ {
					if ab.sampleIndices[i] != ab.sampleIndices[i-1]+1 {
						pos := (i * ab.bytesPerSample) - startIndex
						if pos >= 0 {
							discontinuities = append(discontinuities, pos)
							if conf.Setting().Realtime.Audio.Export.Debug {
								log.Printf("First part discontinuity at sample %d: expected %d, got %d (gap=%d)",
									i, ab.sampleIndices[i-1]+1, ab.sampleIndices[i],
									ab.sampleIndices[i]-ab.sampleIndices[i-1]-1)
							}
						}
					}
				}

				// Second part: start to endIndex
				// Skip the first sample after wrap as it's expected to be discontinuous
				for i := 1; i < endIndex/ab.bytesPerSample; i++ {
					if ab.sampleIndices[i] != ab.sampleIndices[i-1]+1 {
						pos := ((i * ab.bytesPerSample) + (ab.bufferSize - startIndex))
						if pos >= 0 {
							discontinuities = append(discontinuities, pos)
							if conf.Setting().Realtime.Audio.Export.Debug {
								log.Printf("Second part discontinuity at sample %d: expected %d, got %d (gap=%d)",
									i, ab.sampleIndices[i-1]+1, ab.sampleIndices[i],
									ab.sampleIndices[i]-ab.sampleIndices[i-1]-1)
							}
						}
					}
				}

				// Only check wrap point if there's a gap larger than expected
				lastIdx := (ab.bufferSize / ab.bytesPerSample) - 1
				firstWrappedIdx := 0
				if lastIdx >= 0 && firstWrappedIdx < endIndex/ab.bytesPerSample {
					expectedNext := ab.sampleIndices[lastIdx] + 1
					actual := ab.sampleIndices[firstWrappedIdx]
					// Allow for normal wrap progression
					if actual != expectedNext && (actual < ab.sampleIndices[lastIdx]) {
						discontinuities = append(discontinuities, ab.bufferSize-startIndex)
						if conf.Setting().Realtime.Audio.Export.Debug {
							log.Printf("Wrap point gap detected: expected %d, got %d (possible dropout)",
								expectedNext, actual)
						}
					}
				}
			}

			if conf.Setting().Realtime.Audio.Export.Debug {
				if len(discontinuities) > 0 {
					log.Printf("Found %d discontinuities in samples %d to %d:",
						len(discontinuities), startSampleIdx, endSampleIdx)
					for i, pos := range discontinuities {
						sampleIdx := (startIndex + pos) / ab.bytesPerSample
						before := ab.sampleIndices[sampleIdx-1]
						after := ab.sampleIndices[sampleIdx]

						// Use signed math to avoid wrap-around
						gap := int64(after) - int64(before) - 1

						// If gap < 0, treat it as a "wrapped" or "backward" jump
						if gap < 0 {
							log.Printf("  Discontinuity %d: position=%d, before=%d, after=%d (wrapped)",
								i+1, pos, before, after)
						} else {
							log.Printf("  Discontinuity %d: position=%d, before=%d, after=%d, gap=%d samples",
								i+1, pos, before, after, gap)
						}
					}
				} else {
					log.Printf("Sample sequence continuous from %d to %d",
						startSampleIdx, endSampleIdx)
				}
			}

			if conf.Setting().Realtime.Audio.Export.Debug {
				log.Printf("Reading audio segment: startTime=%v, endTime=%v, bufferStartTime=%v",
					requestedStartTime, requestedEndTime, ab.startTime)
			}

			ab.lock.Unlock()
			return &SegmentInfo{
				Data:             segment,
				StartSampleIdx:   startSampleIdx,
				EndSampleIdx:     endSampleIdx,
				HasDiscontinuity: len(discontinuities) > 0,
				DiscontinuityAt:  discontinuities,
			}, nil
		}

		ab.lock.Unlock()
		time.Sleep(1 * time.Second) // Sleep briefly to avoid busy waiting
	}
}

// Helper function for absolute duration
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

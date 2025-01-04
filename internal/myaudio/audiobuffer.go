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

	// Store the current write index to determine if we've wrapped around the buffer.
	prevWriteIndex := ab.writeIndex
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
		// If old data has been overwritten, adjust startTime to maintain accurate timekeeping.
		ab.startTime = time.Now().Add(-ab.bufferDuration)
		if conf.Setting().Realtime.Audio.Export.Debug {
			log.Printf("Buffer wrapped during write, adjusting start time to %v", ab.startTime)
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
			var startSampleIdx, endSampleIdx uint64
			discontinuities := make([]int, 0)

			if startIndex < endIndex {
				if conf.Setting().Realtime.Audio.Export.Debug {
					log.Printf("Reading segment from %d to %d", startIndex, endIndex)
				}
				segmentSize := endIndex - startIndex
				segment = make([]byte, segmentSize)
				copy(segment, ab.data[startIndex:endIndex])

				// Track sample indices
				startSampleIdx = ab.sampleIndices[startIndex/ab.bytesPerSample]
				endSampleIdx = ab.sampleIndices[(endIndex/ab.bytesPerSample)-1] + 1

				// Check for discontinuities
				for i := startIndex/ab.bytesPerSample + 1; i < endIndex/ab.bytesPerSample; i++ {
					if ab.sampleIndices[i] != ab.sampleIndices[i-1]+1 {
						discontinuities = append(discontinuities, (i*ab.bytesPerSample)-startIndex)
					}
				}
			} else {
				if conf.Setting().Realtime.Audio.Export.Debug {
					log.Printf("Buffer wrapped during read, reading segment from %d to %d", startIndex, endIndex)
				}
				segmentSize := (ab.bufferSize - startIndex) + endIndex
				segment = make([]byte, segmentSize)
				firstPartSize := ab.bufferSize - startIndex
				copy(segment[:firstPartSize], ab.data[startIndex:])
				copy(segment[firstPartSize:], ab.data[:endIndex])

				// Track sample indices for wrapped buffer
				startSampleIdx = ab.sampleIndices[startIndex/ab.bytesPerSample]
				endSampleIdx = ab.sampleIndices[(endIndex/ab.bytesPerSample)-1] + 1

				// Check for discontinuities including wrap point
				for i := startIndex / ab.bytesPerSample; i < ab.bufferSize/ab.bytesPerSample; i++ {
					if i > startIndex/ab.bytesPerSample && i > 0 {
						if ab.sampleIndices[i] != ab.sampleIndices[i-1]+1 {
							discontinuities = append(discontinuities, (i*ab.bytesPerSample)-startIndex)
						}
					}
				}
				for i := 1; i < endIndex/ab.bytesPerSample; i++ {
					if ab.sampleIndices[i] != ab.sampleIndices[i-1]+1 {
						discontinuities = append(discontinuities, ((i*ab.bytesPerSample)+firstPartSize)-startIndex)
					}
				}
			}

			if conf.Setting().Realtime.Audio.Export.Debug {
				if len(discontinuities) > 0 {
					log.Printf("Found %d discontinuities in samples %d to %d at positions: %v",
						len(discontinuities), startSampleIdx, endSampleIdx, discontinuities)
				} else {
					log.Printf("Sample sequence continuous from %d to %d",
						startSampleIdx, endSampleIdx)
				}
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

// buffers.go
package myaudio

import (
	"sync"
	"time"

	"github.com/smallnest/ringbuffer"

	"github.com/tphakala/birdnet-go/internal/birdnet"
)

const (
	chunkSize    = 288000 // 3 seconds of 16-bit PCM data at 48 kHz
	pollInterval = time.Millisecond * 100
)

// A variable to set the overlap. Can range from 0 to 2 seconds, represented in bytes.
// For example, for 1.5-second overlap: overlapSize = 144000
var overlapSize int = 144000 // Set as required
var readSize int = chunkSize - overlapSize

var prevData []byte

// ringBuffer is used to store real time detection audio data
var ringBuffer *ringbuffer.RingBuffer

// InitRingBuffer initializes the ring buffer with a given capacity.
func InitRingBuffer(capacity int) {
	ringBuffer = ringbuffer.New(capacity)
}

// writeToBuffer writes audio data into the ring buffer.
func WriteToBuffer(data []byte) {
	_, err := ringBuffer.Write(data)
	if err != nil {
		// yolo, try again
	}
}

// readFromBuffer reads a sliding chunk of audio data from the ring buffer.
func readFromBuffer() []byte {
	bytesWritten := ringBuffer.Length() - ringBuffer.Free()
	if bytesWritten < readSize {
		return nil
	}

	data := make([]byte, readSize)
	_, err := ringBuffer.Read(data)
	if err != nil {
		return nil
	}

	// Join with previous data to ensure we're processing chunkSize bytes
	fullData := append(prevData, data...)
	if len(fullData) > chunkSize {
		// Update prevData for the next iteration
		prevData = fullData[readSize:]
		fullData = fullData[:chunkSize]
	} else {
		// If there isn't enough data even after appending, update prevData and return nil
		prevData = fullData
		return nil
	}

	return fullData
}

// BufferMonitor monitors the buffer and processes audio data when enough data is present.
func BufferMonitor(wg *sync.WaitGroup, bn *birdnet.BirdNET, quitChan chan struct{}) {
	defer wg.Done()

	for {
		select {
		case <-quitChan:
			// Make sure to complete any ongoing processData before exiting.
			// This could involve some flag or condition to check if processData is running.
			return

		default:
			data := readFromBuffer()
			// if buffer has 3 seconds of data, process it
			if len(data) == chunkSize {
				ProcessData(data, bn)
			} else {
				time.Sleep(pollInterval)

				/*
					today := time.Now().Truncate(24 * time.Hour)
									if today.After(ctx.SpeciesListUpdated) {
						// update location based species list once a day
						p.IncludedSpeciesList = bn.GetProbableSpecies()
						p.SpeciesListUpdated = today
					}
				*/
			}
		}
	}
}

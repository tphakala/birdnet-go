package myaudio

import (
	"time"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/go-birdnet/pkg/config"
)

const (
	chunkSize    = 288000 // 3 seconds of 16-bit PCM data at 48 kHz
	pollInterval = time.Millisecond * 1000
)

// ringBuffer is used to store real time detection audio data
var ringBuffer *ringbuffer.RingBuffer

// InitRingBuffer initializes the ring buffer with a given capacity.
func InitRingBuffer(capacity int) {
	ringBuffer = ringbuffer.New(capacity)
}

// writeToBuffer writes audio data into the ring buffer.
func writeToBuffer(data []byte) {
	_, err := ringBuffer.Write(data)
	if err != nil {
		// handle or log the error
	}
}

// readFromBuffer reads a chunk of audio data from the ring buffer.
func readFromBuffer() []byte {
	bytesWritten := ringBuffer.Length() - ringBuffer.Free()
	if bytesWritten < chunkSize {
		//fmt.Println("Not enough data in buffer")
		return nil
	}

	data := make([]byte, chunkSize)
	_, err := ringBuffer.Read(data)
	if err != nil {
		return nil
	}
	return data
}

// BufferMonitor monitors the buffer and processes audio data when enough data is present.
func BufferMonitor(cfg *config.Settings) {
	for {
		select {
		case <-QuitChannel:
			return
		default:
			data := readFromBuffer()
			//fmt.Println("data length: ", len(data))
			// if buffer has 3 seconds of data, process it
			if len(data) == chunkSize {
				processData(data, cfg)
			} else {
				time.Sleep(pollInterval)
			}
		}
	}
}

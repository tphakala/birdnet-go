package myaudio

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/smallnest/ringbuffer"
	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/output"
)

const (
	chunkSize    = 288000 // 3 seconds of 16-bit PCM data at 48 kHz
	pollInterval = time.Millisecond * 100
)

var (
	ringBuffer  *ringbuffer.RingBuffer
	QuitChannel = make(chan struct{})
)

func writeToBuffer(data []byte) {
	ringBuffer.Write(data)
}

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

func BufferMonitor(debug *bool, capturePath *string, logPath *string, threshold *float64) {
	for {
		select {
		case <-QuitChannel:
			return
		default:
			data := readFromBuffer()
			fmt.Println("data length: ", len(data))
			// if buffer has 3 seconds of data, process it
			if len(data) == chunkSize {
				processData(data, debug, capturePath, logPath, threshold)
			} else {
				time.Sleep(pollInterval)
			}
		}
	}
}

func processData(data []byte, debug *bool, capturePath *string, logPath *string, threshold *float64) {
	// get time stamp to calculate processing time

	ts := time.Now()

	// temporary assignments
	var bitDepth int = 16
	var sensitivity float64 = 1.25

	sampleData, err := ConvertToFloat32(data, bitDepth)
	if err != nil {
		log.Fatalf("Error converting to float32: %v", err)
	}
	results, err := birdnet.Predict(sampleData, sensitivity)
	if err != nil {
		log.Fatalf("Error predicting: %v", err)
	}

	te := time.Now()
	if *debug {
		fmt.Printf("processing time %v\n", te.Sub(ts))
	}

	var threshold32 float32 = float32(*threshold)

	if results[0].Confidence > threshold32 {
		commonName := birdnet.ExtractCommonName(results[0].Species)
		logMessage := fmt.Sprintf("%s", commonName) // TODO: make log message user configurable
		output.WriteToLogfile(*logPath, logMessage)

		fileName := fmt.Sprintf("%s/%s_%s.wav", *capturePath, strconv.FormatInt(time.Now().Unix(), 10), commonName)

		// Save PCM data to WAV
		if err := savePCMDataToWAV(fileName, data); err != nil {
			fmt.Println("Error:", err)
		}
		fmt.Println("data length: ", len(data))
		//fmt.Println("Species:", birdnet.ExtractCommonName(results[0].Species), "Confidence:", results[0].Confidence)
	}
}

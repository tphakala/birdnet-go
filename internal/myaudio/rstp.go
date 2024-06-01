package myaudio

import (
	"context"
	"log"
	"os/exec"
	"sync"
	"time"
)

func captureAudioRTSP(url string, transport string, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioBuffer *AudioBuffer) {
	defer wg.Done()

	// Context to control the lifetime of the FFmpeg command
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start FFmpeg with the configured settings
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", transport, // RTSP transport protocol (tcp/udp)
		"-i", url, // RTSP url
		"-loglevel", "error", // Suppress FFmpeg log output
		"-vn",         // No video
		"-f", "s16le", // 16-bit signed little-endian PCM
		"-ar", "48000", // Sample rate
		"-ac", "1", // Single channel (mono)
		"pipe:1", // Output raw audio data to standard out
	)

	// Capture FFmpeg's stdout for processing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Error creating ffmpeg pipe: %v", err)
	}

	// Attempt to start the FFmpeg process
	log.Println("Starting ffmpeg with command: ", cmd.String())
	if err := cmd.Start(); err != nil {
		log.Printf("Error starting FFmpeg: %v", err)
		return
	}

	// Ensure cmd.Wait() is called to clean up the process table entry on FFmpeg exit
	cmdDone := make(chan error)
	go func() {
		cmdDone <- cmd.Wait()
	}()

	// Channel to signal the reading goroutine to exit
	stopReadingChan := make(chan struct{})
	shuttingDown := false

	// Start a goroutine to read from FFmpeg's stdout and write to the ring buffer
	go func() {
		defer cancel()

		// Buffer to hold the audio data read from FFmpeg's stdout.
		buf := make([]byte, 144000)
		for {
			select {
			case <-stopReadingChan:
				log.Println("Stop reading signal received, exiting goroutine.")
				return
			default:
				n, err := stdout.Read(buf)
				if err != nil {
					if !shuttingDown {
						log.Printf("Error reading from ffmpeg: %v", err)
						time.Sleep(3 * time.Second) // wait before restarting
						log.Println("Restarting FFmpeg.")
						restartChan <- struct{}{}
					}
					return
				}
				// Write to ring buffer when audio data is received
				WriteToBuffer(buf[:n])
				audioBuffer.Write(buf[:n])
			}
		}
	}()

	// Stop here and wait for a quit signal or context cancellation (ffmpeg exit)
	for {
		select {
		case <-quitChan:
			log.Println("Quit signal received, stopping FFmpeg.")
			shuttingDown = true
			close(stopReadingChan)
			cancel()
			if err := cmd.Process.Kill(); err != nil {
				log.Printf("Error killing FFmpeg process: %v", err)
			} else {
				log.Println("FFmpeg process killed.")
			}
			<-cmdDone // Wait for the process to actually exit
			return
		case err := <-cmdDone:
			if err != nil {
				log.Printf("FFmpeg wait error: %v", err)
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

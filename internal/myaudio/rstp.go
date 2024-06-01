package myaudio

import (
	"context"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func captureAudioRTSP(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioBuffer *AudioBuffer) {
	defer wg.Done()

	// Context to control the lifetime of the FFmpeg command
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Determine the RTSP transport protocol based on settings
	rtspTransport := "udp"
	if settings.Realtime.RTSP.Transport != "" {
		rtspTransport = settings.Realtime.RTSP.Transport
	}

	// Start FFmpeg with the configured settings
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", rtspTransport, // RTSP transport protocol (tcp/udp)
		"-i", settings.Realtime.RTSP.Url, // RTSP url
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
	defer func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("FFmpeg wait error: %v", err)
		}
	}()

	// Start a goroutine to read from FFmpeg's stdout and write to the ring buffer
	go func() {
		// Ensure the FFmpeg process is terminated when this goroutine exits.
		defer cancel()

		// Buffer to hold the audio data read from FFmpeg's stdout.
		buf := make([]byte, 65536)
		for {
			n, err := stdout.Read(buf)
			// On read error, log the error, signal a restart, and exit the goroutine.
			if err != nil {
				log.Printf("Error reading from ffmpeg: %v", err)
				cancel()
				time.Sleep(3 * time.Second) // wait before restarting
				restartChan <- struct{}{}
				return
			}
			// Write to ring buffer when audio data is received
			WriteToBuffer(buf[:n])
			audioBuffer.Write(buf[:n])
		}
	}()

	// Stop here and wait for a quit signal or context cancellation (ffmpeg exit)
	for {
		select {
		case <-quitChan:
			log.Println("Quit signal received, stopping FFmpeg.")
			cancel()
			return
		case <-ctx.Done():
			// Context was cancelled, clean up and exit goroutine
			return
		}
	}
}

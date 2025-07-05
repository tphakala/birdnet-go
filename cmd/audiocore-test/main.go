// This is a test program for audiocore soundcard source implementation
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/adapter"
	"github.com/tphakala/birdnet-go/internal/audiocore/sources"
	"github.com/tphakala/birdnet-go/internal/audiocore/sources/malgo"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

func main() {
	// List available devices
	fmt.Println("Available audio devices:")
	devices, err := malgo.EnumerateDevices()
	if err != nil {
		log.Printf("Failed to enumerate devices: %v", err)
	} else {
		for _, device := range devices {
			fmt.Printf("  %d: %s (ID: %s)\n", device.Index, device.Name, device.ID)
		}
	}

	// Get default device
	defaultDevice, err := malgo.GetDefaultDevice()
	if err != nil {
		log.Printf("Failed to get default device: %v", err)
	} else {
		fmt.Printf("\nDefault device: %s\n", defaultDevice.Name)
	}

	// Initialize buffer pool
	poolConfig := audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
		EnableMetrics:     true,
	}
	bufferPool := audiocore.NewBufferPool(poolConfig)

	// Create source configuration
	sourceConfig := audiocore.SourceConfig{
		ID:   "main-mic",
		Name: "Main Microphone",
		Type: "malgo",
		Device: func() string {
			if defaultDevice != nil {
				return defaultDevice.Name
			}
			return "default"
		}(),
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		BufferSize: 512 * 2, // 512 frames * 2 bytes per sample
		Gain:       1.0,
		ExtraConfig: map[string]any{
			"buffer_frames": uint32(512),
		},
	}

	// Create source via factory
	source, err := sources.CreateSource(&sourceConfig, bufferPool)
	if err != nil {
		log.Fatalf("Failed to create source: %v", err)
	}

	// Initialize myaudio buffers
	if err := myaudio.AllocateAnalysisBuffer(144384, "malgo"); err != nil {
		log.Fatalf("Failed to allocate analysis buffer: %v", err)
	}
	if err := myaudio.AllocateCaptureBuffer(60, 48000, 2, "malgo"); err != nil {
		log.Fatalf("Failed to allocate capture buffer: %v", err)
	}

	// Create buffer bridge
	bridge := adapter.NewBufferBridge(source, "malgo")

	// Start capture
	ctx := context.Background()
	if err := bridge.Start(ctx); err != nil {
		log.Fatalf("Failed to start bridge: %v", err)
	}

	fmt.Println("\nAudio capture started. Press Ctrl+C to stop...")

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Monitor for a few seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	frameCount := 0
	for {
		select {
		case <-sigChan:
			fmt.Println("\nShutting down...")
			if err := bridge.Stop(); err != nil {
				log.Printf("Error stopping bridge: %v", err)
			}
			return
		case <-ticker.C:
			// Print some stats
			fmt.Printf("Running for %d seconds, source active: %v\n",
				(frameCount+1)*5, source.IsActive())
			frameCount++
			if frameCount >= 3 { // Stop after 15 seconds
				fmt.Println("Test completed successfully!")
				if err := bridge.Stop(); err != nil {
					log.Printf("Error stopping bridge: %v", err)
				}
				return
			}
		}
	}
}

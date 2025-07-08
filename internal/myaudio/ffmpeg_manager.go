package myaudio

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// FFmpegManager manages all FFmpeg streams
type FFmpegManager struct {
	streams   map[string]*FFmpegStream
	streamsMu sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewFFmpegManager creates a new FFmpeg manager
func NewFFmpegManager() *FFmpegManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &FFmpegManager{
		streams: make(map[string]*FFmpegStream),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// StartStream starts a new FFmpeg stream for the given URL
func (m *FFmpegManager) StartStream(url, transport string, audioChan chan UnifiedAudioData) error {
	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()

	// Check if stream already exists
	if _, exists := m.streams[url]; exists {
		return errors.New(fmt.Errorf("stream already exists for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Build()
	}

	// Create new stream
	stream := NewFFmpegStream(url, transport, audioChan)
	m.streams[url] = stream

	// Start stream in goroutine
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		stream.Run(m.ctx)
	}()

	log.Printf("✅ Started FFmpeg stream for %s", privacy.SanitizeRTSPUrl(url))
	return nil
}

// StopStream stops a specific stream
func (m *FFmpegManager) StopStream(url string) error {
	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()

	stream, exists := m.streams[url]
	if !exists {
		return errors.New(fmt.Errorf("no stream found for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Build()
	}

	stream.Stop()
	delete(m.streams, url)
	
	log.Printf("🛑 Stopped FFmpeg stream for %s", privacy.SanitizeRTSPUrl(url))
	return nil
}

// RestartStream restarts a specific stream
func (m *FFmpegManager) RestartStream(url string) error {
	m.streamsMu.RLock()
	stream, exists := m.streams[url]
	m.streamsMu.RUnlock()

	if !exists {
		return errors.New(fmt.Errorf("no stream found for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Build()
	}

	stream.Restart()
	log.Printf("🔄 Restarted FFmpeg stream for %s", privacy.SanitizeRTSPUrl(url))
	return nil
}

// GetActiveStreams returns a list of active stream URLs
func (m *FFmpegManager) GetActiveStreams() []string {
	m.streamsMu.RLock()
	defer m.streamsMu.RUnlock()

	urls := make([]string, 0, len(m.streams))
	for url := range m.streams {
		urls = append(urls, url)
	}
	return urls
}

// HealthCheck performs a health check on all streams
func (m *FFmpegManager) HealthCheck() map[string]StreamHealth {
	m.streamsMu.RLock()
	defer m.streamsMu.RUnlock()

	health := make(map[string]StreamHealth)
	for url, stream := range m.streams {
		health[url] = stream.GetHealth()
	}
	return health
}

// SyncWithConfig synchronizes running streams with configuration
func (m *FFmpegManager) SyncWithConfig(audioChan chan UnifiedAudioData) error {
	settings := conf.Setting()
	configuredURLs := make(map[string]string) // url -> transport

	// Build map of configured URLs
	for _, url := range settings.Realtime.RTSP.URLs {
		configuredURLs[url] = settings.Realtime.RTSP.Transport
	}

	// Stop streams that are no longer configured
	m.streamsMu.RLock()
	toStop := []string{}
	for url := range m.streams {
		if _, configured := configuredURLs[url]; !configured {
			toStop = append(toStop, url)
		}
	}
	m.streamsMu.RUnlock()

	for _, url := range toStop {
		if err := m.StopStream(url); err != nil {
			log.Printf("⚠️ Error stopping unconfigured stream %s: %v", url, err)
		}
	}

	// Start streams that are configured but not running
	for url, transport := range configuredURLs {
		m.streamsMu.RLock()
		_, running := m.streams[url]
		m.streamsMu.RUnlock()

		if !running {
			if err := m.StartStream(url, transport, audioChan); err != nil {
				log.Printf("⚠️ Error starting configured stream %s: %v", url, err)
			}
		}
	}

	return nil
}

// StartMonitoring starts periodic monitoring of streams
func (m *FFmpegManager) StartMonitoring(interval time.Duration) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.checkStreamHealth()
			}
		}
	}()
}

// checkStreamHealth checks health of all streams
func (m *FFmpegManager) checkStreamHealth() {
	health := m.HealthCheck()
	
	for url, h := range health {
		if !h.IsHealthy {
			log.Printf("⚠️ Unhealthy stream detected: %s (last data: %v ago)", 
				privacy.SanitizeRTSPUrl(url), time.Since(h.LastDataReceived))
			
			// Restart unhealthy streams
			if err := m.RestartStream(url); err != nil {
				log.Printf("❌ Failed to restart unhealthy stream %s: %v", url, err)
			}
		}
	}
}

// Shutdown gracefully shuts down all streams
func (m *FFmpegManager) Shutdown() {
	log.Printf("🛑 Shutting down FFmpeg manager...")
	
	// Cancel context to signal shutdown
	m.cancel()
	
	// Stop all streams
	m.streamsMu.Lock()
	for url := range m.streams {
		if stream := m.streams[url]; stream != nil {
			stream.Stop()
		}
	}
	m.streams = make(map[string]*FFmpegStream)
	m.streamsMu.Unlock()
	
	// Wait for all goroutines to finish
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	
	// Wait with timeout
	select {
	case <-done:
		log.Printf("✅ FFmpeg manager shutdown complete")
	case <-time.After(30 * time.Second):
		log.Printf("⚠️ FFmpeg manager shutdown timeout")
	}
}
package analysis

import (
	"log"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// SoundLevelManager manages the lifecycle of sound level monitoring components
type SoundLevelManager struct {
	mutex            sync.Mutex
	isRunning        bool
	doneChan         chan struct{}
	wg               sync.WaitGroup
	soundLevelChan   chan myaudio.SoundLevelData
	proc             *processor.Processor
	httpServer       *httpcontroller.Server
	metrics          *observability.Metrics
}

// NewSoundLevelManager creates a new sound level manager
func NewSoundLevelManager(soundLevelChan chan myaudio.SoundLevelData, proc *processor.Processor, httpServer *httpcontroller.Server, metrics *observability.Metrics) *SoundLevelManager {
	return &SoundLevelManager{
		soundLevelChan: soundLevelChan,
		proc:           proc,
		httpServer:     httpServer,
		metrics:        metrics,
	}
}

// Start starts sound level monitoring if enabled in settings
func (m *SoundLevelManager) Start() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.isRunning {
		log.Println("Sound level monitoring is already running")
		return nil
	}

	settings := conf.Setting()
	if !settings.Realtime.Audio.SoundLevel.Enabled {
		log.Println("🔇 Sound level monitoring is disabled")
		return nil
	}

	// Update debug log levels
	updateSoundLevelDebugSettings()

	// Register sound level processors for all active sources
	if err := registerSoundLevelProcessorsForActiveSources(settings); err != nil {
		log.Printf("❌ Failed to register sound level processors: %v", err)
		return err
	}

	// Create done channel for this session
	m.doneChan = make(chan struct{})

	// Start publishers
	startSoundLevelPublishers(&m.wg, m.doneChan, m.proc, m.soundLevelChan, m.httpServer)

	m.isRunning = true
	log.Println("🔊 Sound level monitoring started")
	return nil
}

// Stop stops all sound level monitoring components
func (m *SoundLevelManager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.isRunning {
		log.Println("Sound level monitoring is not running")
		return
	}

	log.Println("🔌 Stopping sound level monitoring...")

	// Signal all goroutines to stop
	if m.doneChan != nil {
		close(m.doneChan)
	}

	// Wait for all goroutines to finish with timeout to prevent hanging
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished cleanly
		log.Println("🔇 All sound level monitoring goroutines stopped cleanly")
	case <-time.After(30 * time.Second):
		// Timeout occurred - force shutdown
		log.Println("⚠️ Warning: Sound level monitoring shutdown timed out after 30s, forcing cleanup")
		// Continue with cleanup anyway - don't hang the system
	}

	// Unregister all sound level processors
	settings := conf.Setting()
	unregisterAllSoundLevelProcessors(settings)

	m.isRunning = false
	m.doneChan = nil
	log.Println("🔇 Sound level monitoring stopped")
}

// Restart stops and starts sound level monitoring with current settings
func (m *SoundLevelManager) Restart() error {
	log.Println("🔄 Restarting sound level monitoring...")
	m.Stop()
	return m.Start()
}

// IsRunning returns whether sound level monitoring is currently active
func (m *SoundLevelManager) IsRunning() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.isRunning
}

// updateSoundLevelDebugSettings updates the debug log levels for sound level components
func updateSoundLevelDebugSettings() {
	settings := conf.Setting()
	
	// Update the analysis sound level logger
	if settings.Realtime.Audio.SoundLevel.Debug {
		getSoundLevelServiceLevelVar().Set(slog.LevelDebug)
	} else {
		getSoundLevelServiceLevelVar().Set(slog.LevelInfo)
	}
	
	// Update the myaudio sound level logger
	myaudio.UpdateSoundLevelDebugSetting(settings.Realtime.Audio.SoundLevel.Debug)
}
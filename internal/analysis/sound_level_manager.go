package analysis

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// SoundLevelManager manages the lifecycle of sound level monitoring components
type SoundLevelManager struct {
	mutex          sync.Mutex
	isRunning      bool
	doneChan       chan struct{}
	wg             sync.WaitGroup
	soundLevelChan chan myaudio.SoundLevelData
	proc           *processor.Processor
	apiController  *apiv2.Controller
	metrics        *observability.Metrics
}

// NewSoundLevelManager creates a new sound level manager
func NewSoundLevelManager(soundLevelChan chan myaudio.SoundLevelData, proc *processor.Processor, apiController *apiv2.Controller, metrics *observability.Metrics) *SoundLevelManager {
	return &SoundLevelManager{
		soundLevelChan: soundLevelChan,
		proc:           proc,
		apiController:  apiController,
		metrics:        metrics,
	}
}

// Start starts sound level monitoring if enabled in settings
func (m *SoundLevelManager) Start() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log := GetLogger()
	if m.isRunning {
		log.Debug("sound level monitoring is already running")
		return nil
	}

	settings := conf.Setting()
	if !settings.Realtime.Audio.SoundLevel.Enabled {
		log.Debug("sound level monitoring is disabled")
		return nil
	}

	// Update debug log levels
	updateSoundLevelDebugSettings()

	// Register sound level processors for all active sources
	if err := registerSoundLevelProcessorsForActiveSources(settings); err != nil {
		log.Error("failed to register sound level processors",
			logger.Error(err))
		return err
	}

	// Create done channel for this session
	m.doneChan = make(chan struct{})

	// Start publishers
	startSoundLevelPublishers(&m.wg, m.doneChan, m.proc, m.soundLevelChan, m.apiController)

	m.isRunning = true
	log.Info("sound level monitoring started")
	return nil
}

// Stop stops all sound level monitoring components
func (m *SoundLevelManager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	log := GetLogger()
	if !m.isRunning {
		log.Debug("sound level monitoring is not running")
		return
	}

	log.Info("stopping sound level monitoring")

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
		log.Debug("all sound level monitoring goroutines stopped cleanly")
	case <-time.After(30 * time.Second):
		// Timeout occurred - force shutdown
		log.Warn("sound level monitoring shutdown timed out, forcing cleanup",
			logger.Duration("timeout", 30*time.Second))
		// Continue with cleanup anyway - don't hang the system
	}

	// Unregister all sound level processors
	settings := conf.Setting()
	unregisterAllSoundLevelProcessors(settings)

	// Note: With the centralized logger, file handle cleanup is managed by the central logger
	// No explicit close is needed here

	m.isRunning = false
	m.doneChan = nil
	log.Info("sound level monitoring stopped")
}

// Restart stops and starts sound level monitoring with current settings
func (m *SoundLevelManager) Restart() error {
	GetLogger().Info("restarting sound level monitoring")
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

	// Note: With the centralized logger, log levels are managed via configuration.
	// Debug checks happen at call sites using conf.Setting().Realtime.Audio.SoundLevel.Debug

	// Update the myaudio sound level logger
	myaudio.UpdateSoundLevelDebugSetting(settings.Realtime.Audio.SoundLevel.Debug)
}

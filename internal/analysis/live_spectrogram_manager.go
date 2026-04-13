package analysis

import (
	"fmt"
	"sync"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

type liveSpectrogramSourceState struct {
	refCount int
	consumer *SpectrogramConsumer
	cleanup  func()
}

// LiveSpectrogramManager attaches spectrogram FFT consumers only while there
// is at least one active SSE viewer for a given source.
type LiveSpectrogramManager struct {
	settings *conf.Settings
	engine   *engine.AudioEngine
	outChan  chan apiv2.LiveSpectrogramBatch

	mu         sync.Mutex
	sources    map[string]*liveSpectrogramSourceState
	forwarders sync.WaitGroup
}

func NewLiveSpectrogramManager(settings *conf.Settings, audioEngine *engine.AudioEngine, outChan chan apiv2.LiveSpectrogramBatch) *LiveSpectrogramManager {
	return &LiveSpectrogramManager{
		settings: settings,
		engine:   audioEngine,
		outChan:  outChan,
		sources:  make(map[string]*liveSpectrogramSourceState),
	}
}

func (m *LiveSpectrogramManager) Acquire(sourceID string) error {
	if m == nil || sourceID == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if state := m.sources[sourceID]; state != nil {
		state.refCount++
		return nil
	}

	if m.engine == nil || m.outChan == nil {
		return fmt.Errorf("live spectrogram manager is not initialized")
	}

	cfg := m.settings.WebServer.LiveStream.Spectrogram
	if !cfg.Enabled {
		return fmt.Errorf("live spectrogram streaming is disabled")
	}

	targetSampleRate := m.settings.WebServer.LiveStream.EffectiveSampleRate()

	consumerID := "spectrogram_" + sourceID
	consumer, consumerOut, err := NewSpectrogramConsumer(
		consumerID,
		targetSampleRate,
		conf.BitDepth,
		1,
		cfg.FFTSize,
		cfg.HopSize,
		cfg.Window,
		cfg.BatchIntervalMs,
	)
	if err != nil {
		return fmt.Errorf("failed to create live spectrogram consumer: %w", err)
	}

	// Look up per-source gain and EQ so the spectrogram reflects exactly
	// what the user hears via HLS on the Live Audio page. The HLS
	// consumer uses the same lookups (see audio_hls.go), so the visual
	// waterfall stays in lockstep with the audible stream even after
	// runtime gain/EQ changes.
	gainDB, _ := m.engine.Registry().GetGain(sourceID)
	liveSettings := conf.Setting()
	audioSettings := &liveSettings.Realtime.Audio
	srcCfg := audioSettings.FindSourceByID(sourceID)
	eqChain := equalizer.BuildFilterChainForSource(srcCfg, audioSettings.Equalizer, targetSampleRate)

	if err := m.engine.Router().AddRoute(sourceID, consumer, targetSampleRate, gainDB, eqChain); err != nil {
		return fmt.Errorf("failed to add live spectrogram route: %w", err)
	}

	m.forwarders.Go(func() {
		var drops int64
		for batch := range consumerOut {
			select {
			case m.outChan <- batch:
			default:
				drops++
				if drops == 1 || drops%spectrogramDropLogInterval == 0 {
					GetLogger().Warn("live spectrogram batch dropped at manager forwarder",
						logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
						logger.String("consumer_id", consumerID),
						logger.Int64("total_drops", drops))
				}
			}
		}
	})

	m.sources[sourceID] = &liveSpectrogramSourceState{
		refCount: 1,
		consumer: consumer,
		cleanup: func() {
			m.engine.Router().RemoveRoute(sourceID, consumerID)
			_ = consumer.Close()
		},
	}

	GetLogger().Debug("Registered live spectrogram route",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("consumer_id", consumerID))

	return nil
}

func (m *LiveSpectrogramManager) Release(sourceID string) {
	if m == nil || sourceID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.sources[sourceID]
	if state == nil {
		return
	}

	state.refCount--
	if state.refCount > 0 {
		return
	}

	state.cleanup()
	delete(m.sources, sourceID)

	GetLogger().Debug("Removed live spectrogram route",
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.String("consumer_id", "spectrogram_"+sourceID))
}

// Close tears down every active source and waits for the per-source
// forwarding goroutines to exit. Callers must not close m.outChan until Close
// has returned — otherwise the forwarders may send on a closed channel.
func (m *LiveSpectrogramManager) Close() {
	if m == nil {
		return
	}

	m.mu.Lock()
	for sourceID, state := range m.sources {
		state.cleanup()
		delete(m.sources, sourceID)
	}
	m.mu.Unlock()

	m.forwarders.Wait()
}

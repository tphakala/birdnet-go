package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestGetAudioSourcesForDiscovery_NilRegistry(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	sources := p.getAudioSourcesForDiscovery()
	assert.Empty(t, sources, "nil registry should return empty slice, not default fallback")
}

func TestGetAudioSourcesForDiscovery_EmptyRegistry(t *testing.T) {
	t.Parallel()
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	p := &Processor{}
	p.registry = reg
	sources := p.getAudioSourcesForDiscovery()
	assert.Empty(t, sources, "empty registry should return empty slice, not default fallback")
}

func TestGetAudioSourcesForDiscovery_WithSources(t *testing.T) {
	t.Parallel()
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	_, err := reg.Register(&audiocore.SourceConfig{
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.168.1.10/stream",
		DisplayName:      "Backyard",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	})
	require.NoError(t, err)

	p := &Processor{}
	p.registry = reg
	sources := p.getAudioSourcesForDiscovery()
	assert.Len(t, sources, 1)
	assert.Equal(t, "Backyard", sources[0].DisplayName)
	assert.Contains(t, sources[0].ID, "rtsp_")
}

func TestGetAudioSourcesForDiscovery_NeverReturnsDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		registry *audiocore.SourceRegistry
	}{
		{"nil registry", nil},
		{"empty registry", audiocore.NewSourceRegistry(audiocore.GetLogger())},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{}
			p.registry = tt.registry
			sources := p.getAudioSourcesForDiscovery()
			for _, src := range sources {
				assert.NotEqual(t, "default", src.ID, "must not return hardcoded 'default' source")
			}
		})
	}
}

func TestPublishDiscoveryIfReady_NilSettings(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	p.publishDiscoveryIfReady()
}

func TestPublishDiscoveryIfReady_MQTTDisabled(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = false
	p := &Processor{Settings: settings}
	p.publishDiscoveryIfReady()
}

func TestScheduleDiscoveryPublish_Debounce(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	p.scheduleDiscoveryPublish()
	p.scheduleDiscoveryPublish()
	// Stop timer to prevent background goroutine from running
	p.discoveryDebounceMu.Lock()
	if p.discoveryDebounce != nil {
		p.discoveryDebounce.Stop()
	}
	p.discoveryDebounceMu.Unlock()
}

func TestScheduleDiscoveryPublish_ResetsTimer(t *testing.T) {
	t.Parallel()
	p := &Processor{}

	p.scheduleDiscoveryPublish()
	p.discoveryDebounceMu.Lock()
	firstTimer := p.discoveryDebounce
	p.discoveryDebounceMu.Unlock()
	assert.NotNil(t, firstTimer)

	p.scheduleDiscoveryPublish()
	p.discoveryDebounceMu.Lock()
	secondTimer := p.discoveryDebounce
	secondTimer.Stop()
	p.discoveryDebounceMu.Unlock()

	assert.NotNil(t, secondTimer, "second call should create a new timer")
}

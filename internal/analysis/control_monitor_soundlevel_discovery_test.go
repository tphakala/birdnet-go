// control_monitor_soundlevel_discovery_test.go: toggling sound level monitoring
// refreshes Home Assistant discovery so the Sound Level sensor is added or
// removed without an MQTT reconnect (GitHub #4349 follow-up).
package analysis

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// discoveryRefreshWait bounds the wait for the debounced discovery publish
// (debounce is 3s in the processor; generous so a starved CI runner under
// -race does not flake).
const discoveryRefreshWait = 15 * time.Second

func TestHandleReconfigureSoundLevel_RefreshesDiscovery(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Main.Name = "node"
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = "birdnet"
	settings.Realtime.MQTT.HomeAssistant.Enabled = true
	settings.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
	// Sound level stays disabled so SoundLevelManager.Restart starts nothing; the
	// refresh must still publish (it then removes the Sound Level sensor).
	conftest.SetTestSettings(settings)

	var mu sync.Mutex
	var topics []string
	proc := createMockProcessor(func(_ context.Context, topic, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		topics = append(topics, topic)
		return nil
	})
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	_, err := reg.Register(&audiocore.SourceConfig{
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://192.0.2.1/stream",
		DisplayName:      "Backyard",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	})
	require.NoError(t, err)
	proc.SetRegistry(reg)

	cm := &ControlMonitor{
		soundLevelChan: make(chan soundlevel.SoundLevelData, 1),
		proc:           proc,
	}
	t.Cleanup(cm.Stop)

	cm.handleReconfigureSoundLevel()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, topic := range topics {
			if strings.HasSuffix(topic, "_sound_level/config") {
				return true
			}
		}
		return false
	}, discoveryRefreshWait, 50*time.Millisecond, "sound level reconfigure must trigger a discovery refresh")
}

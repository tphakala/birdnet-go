package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	enginepkg "github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

const (
	nameTestDisplayName = "Backyard feeder"
	nameTestInterval    = 1
	nameTestWait        = 10 * time.Second
	nameTestFrameChunks = 10
)

// TestRegisterSoundLevelConsumers_NameIsSourceDisplayName pins that published
// sound level data carries the source display name in Name and the source ID in
// Source, not the ID in both.
func TestRegisterSoundLevelConsumers_NameIsSourceDisplayName(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.SoundLevel.Enabled = true
	settings.Realtime.Audio.SoundLevel.Interval = nameTestInterval
	conftest.SetTestSettings(settings)

	engine := enginepkg.New(t.Context(), &enginepkg.Config{}, nil)
	src, err := engine.Registry().Register(&audiocore.SourceConfig{
		DisplayName:      nameTestDisplayName,
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: "rtsp://127.0.0.1/name-test",
		SampleRate:       conf.SampleRate,
		BitDepth:         conf.BitDepth,
		Channels:         1,
	})
	require.NoError(t, err)

	p := &AudioPipelineService{
		engine:         engine,
		soundLevelChan: make(chan soundlevel.SoundLevelData, 1),
		done:           make(chan struct{}),
	}
	p.registerSoundLevelConsumers([]string{src.ID}, "unit_test_name")
	t.Cleanup(func() {
		p.removeAllSoundLevelConsumers("unit_test_name_cleanup")
		close(p.done)
		p.wg.Wait()
	})
	require.Contains(t, p.soundLevelConsumers, src.ID)

	// One second of silence, split into frames the way the router delivers them.
	frameBytes := conf.SampleRate * 2 / nameTestFrameChunks
	for range nameTestFrameChunks {
		engine.Router().Dispatch(audiocore.AudioFrame{
			SourceID:   src.ID,
			Data:       make([]byte, frameBytes),
			SampleRate: conf.SampleRate,
			BitDepth:   conf.BitDepth,
			Channels:   1,
			Timestamp:  time.Now(),
		})
	}

	select {
	case got := <-p.soundLevelChan:
		assert.Equal(t, src.ID, got.Source)
		assert.Equal(t, nameTestDisplayName, got.Name)
	case <-time.After(nameTestWait):
		require.Fail(t, "no sound level data published")
	}
}

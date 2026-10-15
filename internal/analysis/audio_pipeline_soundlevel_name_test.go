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
	nameTestRenamed     = "Renamed feeder"
)

// newNameTestPipeline registers one RTSP source and a sound level consumer for
// it, and returns the pipeline, engine and source. The cleanup stops them.
func newNameTestPipeline(t *testing.T) (*AudioPipelineService, *enginepkg.AudioEngine, *audiocore.AudioSource) {
	t.Helper()
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
	t.Cleanup(func() {
		p.removeAllSoundLevelConsumers("unit_test_name_cleanup")
		close(p.done)
		p.wg.Wait()
	})
	return p, engine, src
}

// publishOneSecond dispatches one second of silence for the source, split into
// frames the way the router delivers them, and returns the published interval.
func publishOneSecond(t *testing.T, p *AudioPipelineService, engine *enginepkg.AudioEngine, sourceID string) soundlevel.SoundLevelData {
	t.Helper()
	frameBytes := conf.SampleRate * 2 / nameTestFrameChunks
	for range nameTestFrameChunks {
		engine.Router().Dispatch(audiocore.AudioFrame{
			SourceID:   sourceID,
			Data:       make([]byte, frameBytes),
			SampleRate: conf.SampleRate,
			BitDepth:   conf.BitDepth,
			Channels:   1,
			Timestamp:  time.Now(),
		})
	}
	select {
	case got := <-p.soundLevelChan:
		return got
	case <-time.After(nameTestWait):
		require.Fail(t, "no sound level data published")
		return soundlevel.SoundLevelData{}
	}
}

// TestRegisterSoundLevelConsumers_NameIsSourceDisplayName pins that published
// sound level data carries the source display name in Name and the source ID in
// Source, not the ID in both.
func TestRegisterSoundLevelConsumers_NameIsSourceDisplayName(t *testing.T) {
	p, engine, src := newNameTestPipeline(t)
	p.registerSoundLevelConsumers([]string{src.ID}, "unit_test_name")
	require.Contains(t, p.soundLevelConsumers, src.ID)

	got := publishOneSecond(t, p, engine, src.ID)
	assert.Equal(t, src.ID, got.Source)
	assert.Equal(t, nameTestDisplayName, got.Name)
}

// TestRegisterSoundLevelConsumers_NameFollowsRename pins that a rename after the
// consumer was built shows up in the next interval, without a rebuild.
func TestRegisterSoundLevelConsumers_NameFollowsRename(t *testing.T) {
	p, engine, src := newNameTestPipeline(t)
	p.registerSoundLevelConsumers([]string{src.ID}, "unit_test_rename")
	require.True(t, engine.Registry().UpdateDisplayName(src.ID, nameTestRenamed))

	got := publishOneSecond(t, p, engine, src.ID)
	assert.Equal(t, nameTestRenamed, got.Name)
}

// TestRegisterSoundLevelConsumers_EmptyDisplayNameKeepsNameValid pins that a
// source whose display name is cleared publishes its ID as Name, which the
// publishers validate as non-empty, whether the name is cleared before the
// consumer is built or after.
func TestRegisterSoundLevelConsumers_EmptyDisplayNameKeepsNameValid(t *testing.T) {
	t.Run("cleared before registration", func(t *testing.T) {
		p, engine, src := newNameTestPipeline(t)
		require.True(t, engine.Registry().UpdateDisplayName(src.ID, ""))
		p.registerSoundLevelConsumers([]string{src.ID}, "unit_test_empty_before")

		got := publishOneSecond(t, p, engine, src.ID)
		assert.Equal(t, src.ID, got.Name)
	})

	t.Run("cleared after registration", func(t *testing.T) {
		p, engine, src := newNameTestPipeline(t)
		p.registerSoundLevelConsumers([]string{src.ID}, "unit_test_empty_after")
		require.True(t, engine.Registry().UpdateDisplayName(src.ID, ""))

		got := publishOneSecond(t, p, engine, src.ID)
		assert.Equal(t, src.ID, got.Name)
	})
}

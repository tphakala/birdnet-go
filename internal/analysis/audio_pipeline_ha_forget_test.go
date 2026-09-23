// audio_pipeline_ha_forget_test.go: reconfigureChangedSources queues Home
// Assistant entity removal for streams deleted or renamed in settings, and not
// for streams that are only disabled (GitHub #4349 follow-up).
package analysis

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	enginepkg "github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// fakeHAForgetter records the HA cleanup requests made by the pipeline.
type fakeHAForgetter struct {
	mu      sync.Mutex
	deleted []datastore.AudioSource
	renamed []datastore.AudioSource
}

func (f *fakeHAForgetter) ForgetHomeAssistantSource(source datastore.AudioSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, source)
}

func (f *fakeHAForgetter) ForgetHomeAssistantEntityName(previous datastore.AudioSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renamed = append(f.renamed, previous)
}

// runReconfigureWithStream registers one running RTSP source named runningName
// on connection, applies streams as the configured streams, runs
// reconfigureChangedSources, and returns the recorded HA cleanup calls and the
// running source's ID.
func runReconfigureWithStream(t *testing.T, connection, runningName string, streams []conf.StreamConfig) (*fakeHAForgetter, *enginepkg.AudioEngine, string) {
	t.Helper()
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = streams
	conftest.SetTestSettings(settings)

	engine := enginepkg.New(t.Context(), &enginepkg.Config{}, nil)
	src, err := engine.Registry().Register(&audiocore.SourceConfig{
		DisplayName:      runningName,
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: connection,
		SampleRate:       conf.SampleRate,
		BitDepth:         conf.BitDepth,
		Channels:         1,
		// Match what the configured stream resolves to, so a kept stream takes
		// the in-place path rather than a full restart this harness cannot run.
		Transport: settings.Realtime.RTSP.ResolveTransport(""),
	})
	require.NoError(t, err)

	forgetter := &fakeHAForgetter{}
	p := &AudioPipelineService{engine: engine, haEntityForgetter: forgetter}
	p.reconfigureChangedSources(make(chan audiocore.AudioLevelData))
	return forgetter, engine, src.ID
}

func TestReconfigureChangedSources_HAEntityCleanup(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.

	t.Run("deleted stream queues removal", func(t *testing.T) {
		forgetter, _, id := runReconfigureWithStream(t, "rtsp://cam-deleted.invalid/stream", "Garden", nil)

		assert.Equal(t, []datastore.AudioSource{{ID: id, DisplayName: "Garden"}}, forgetter.deleted)
		assert.Empty(t, forgetter.renamed)
	})

	t.Run("disabled stream keeps its entities", func(t *testing.T) {
		const connection = "rtsp://cam-disabled.invalid/stream"
		forgetter, engine, _ := runReconfigureWithStream(t, connection, "Garden", []conf.StreamConfig{{
			Name: "Garden", URL: connection, Enabled: false, Type: conf.StreamTypeRTSP, Models: []string{"birdnet"},
		}})

		_, running := engine.Registry().GetByConnection(connection)
		require.False(t, running, "a disabled stream is still removed from the running registry")
		assert.Empty(t, forgetter.deleted, "a disabled stream must keep its HA entities")
	})

	t.Run("renamed stream queues removal of the old name", func(t *testing.T) {
		const connection = "rtsp://cam-renamed.invalid/stream"
		forgetter, engine, id := runReconfigureWithStream(t, connection, "Garden", []conf.StreamConfig{{
			Name: "Yard", URL: connection, Enabled: true, Type: conf.StreamTypeRTSP, Models: []string{"birdnet"},
		}})

		assert.Equal(t, []datastore.AudioSource{{ID: id, DisplayName: "Garden"}}, forgetter.renamed)
		assert.Empty(t, forgetter.deleted)
		src, ok := engine.Registry().Get(id)
		require.True(t, ok)
		assert.Equal(t, "Yard", src.DisplayName)
	})
}

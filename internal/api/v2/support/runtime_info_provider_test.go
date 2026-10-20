package support

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/inference"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
	supportpkg "github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// fakeRuntimeSource is a runtimeSource with fixed answers.
type fakeRuntimeSource struct {
	models  []classifier.ModelRuntimeSnapshot
	summary classifier.RangeFilterRuntimeSummary
	dir     string
}

func (f *fakeRuntimeSource) ModelRuntimeSnapshots() []classifier.ModelRuntimeSnapshot {
	return f.models
}
func (f *fakeRuntimeSource) RangeFilterRuntimeSummary() classifier.RangeFilterRuntimeSummary {
	return f.summary
}
func (f *fakeRuntimeSource) ModelsDirSnapshot() string { return f.dir }

// testRuntimeProvider builds a provider whose every read is a seam.
func testRuntimeProvider(s *conf.Settings, src runtimeSource, env string) *runtimeInfoProvider {
	return &runtimeInfoProvider{
		ortSnap: func() inference.ORTRuntime { return inference.ORTRuntime{} },
		ovSnap: func() inference.OpenVINORuntime {
			return inference.OpenVINORuntime{ProbeStatus: inference.OVProbeStatusNotRun}
		},
		orch:     func() runtimeSource { return src },
		settings: func() *conf.Settings { return s },
		env:      func() string { return env },
		mountTable: func() ([]diagnostics.MountTableEntry, error) {
			return nil, nil
		},
		resolve: func(p string) (string, error) { return p, nil },
	}
}

// TestRuntimeInfoProvider_ConfigFacts pins the facts taken from settings and the
// disk when no orchestrator can be reached.
func TestRuntimeInfoProvider_ConfigFacts(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "geomodel.onnx")
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0o600))

	s := &conf.Settings{}
	s.BirdNET.Threads = 3
	s.BirdNET.RangeFilter.Species = []string{"a", "b", "c"}
	s.BirdNET.RangeFilter.Model = conf.RangeFilterModelV3
	s.BirdNET.RangeFilter.ModelPath = existing
	s.BirdNET.RangeFilter.LabelsPath = filepath.Join(dir, "missing_labels.txt")
	s.Models.Directory = dir
	s.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "front yard mic", Models: []string{"birdnet"}},
		{Name: "second", Models: nil},
		{Name: "third", Models: []string{"no_such_model"}},
	}
	s.Realtime.RTSP.Streams = []conf.StreamConfig{
		{Name: "cam", URL: "rtsp://user:pw@host/stream", Enabled: false, Models: []string{"birdnet"}},
	}

	info := testRuntimeProvider(s, nil, "Native").CollectRuntimeInfo(t.Context())

	assert.True(t, info.Available)
	assert.False(t, info.OrchestratorAvailable)
	assert.Empty(t, info.Models)
	assert.Equal(t, 3, info.BirdNETThreadsSetting)
	assert.Equal(t, inference.ORTRequiredVersion(), info.ONNXRuntime.RequiredVersion)

	require.NotNil(t, info.RangeFilter)
	assert.False(t, info.RangeFilter.Available)
	assert.Equal(t, 3, info.RangeFilter.IncludedSpeciesCount)
	assert.Equal(t, conf.RangeFilterModelV3, info.RangeFilter.Model)
	assert.True(t, info.RangeFilter.ModelPathConfigured)
	assert.True(t, info.RangeFilter.ModelFileExists)
	assert.True(t, info.RangeFilter.LabelsPathConfigured)
	assert.False(t, info.RangeFilter.LabelsFileExists)

	require.Len(t, info.Sources, 4)
	assert.Equal(t, []string{"birdnet"}, info.Sources[0].Models)
	assert.Equal(t, []string{classifier.RegistryIDBirdNETV24}, info.Sources[0].RegistryIDs)
	assert.True(t, info.Sources[0].Enabled)
	assert.False(t, info.Sources[0].UsesDefault)
	assert.True(t, info.Sources[1].UsesDefault)
	assert.Empty(t, info.Sources[2].RegistryIDs)
	assert.Empty(t, info.Sources[2].Models, "an unknown model ID is config text and must not ship")
	assert.False(t, info.Sources[2].UsesDefault)
	unknown, err := json.Marshal(info.Sources[2])
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"audio_source","index":2,"enabled":true,"unknown_models":1,"uses_default":false}`, string(unknown))
	assert.Equal(t, 0, info.Sources[3].Index)
	assert.Equal(t, "stream", info.Sources[3].Kind)
	assert.False(t, info.Sources[3].Enabled)

	require.NotNil(t, info.ModelsDirectory)
	assert.Equal(t, "configured", info.ModelsDirectory.Source)
	assert.True(t, info.ModelsDirectory.Exists)
	assert.Equal(t, "not_applicable", info.ModelsDirectory.Persistence)
}

// TestRuntimeInfoProvider_OrchestratorAndContainer pins the mapping of the
// orchestrator snapshots and the persistence verdict inside a container.
func TestRuntimeInfoProvider_OrchestratorAndContainer(t *testing.T) {
	dir := t.TempDir()
	loaded := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	src := &fakeRuntimeSource{
		models: []classifier.ModelRuntimeSnapshot{{
			RegistryID: classifier.RegistryIDPerchV2, Backend: classifier.BackendONNX, Device: "CPU",
			ModelPath: "/models/perch.onnx", NumSpecies: 10, ConfiguredThreads: 2, Generation: 4, LoadedAt: loaded,
		}},
		summary: classifier.RangeFilterRuntimeSummary{Available: true, Active: true, Backend: "geomodel_v3", MappedSpecies: 9, Participants: []string{classifier.RegistryIDPerchV2}},
		dir:     dir,
	}
	// Settings name a directory that does not exist; the orchestrator's wins.
	s := &conf.Settings{}
	s.Models.Directory = filepath.Join(dir, "not-the-directory-in-use")
	p := testRuntimeProvider(s, src, sysinfo.EnvDocker)
	p.mountTable = func() ([]diagnostics.MountTableEntry, error) {
		return []diagnostics.MountTableEntry{{Destination: "/", FSType: "overlay"}}, nil
	}

	p.ovSnap = func() inference.OpenVINORuntime {
		return inference.OpenVINORuntime{LibraryLoaded: true, ProbeStatus: inference.OVProbeStatusCached}
	}

	info := p.CollectRuntimeInfo(t.Context())

	assert.Equal(t, ov.DefaultLibraryName, info.OpenVINO.LibraryPath, "a default-soname load has an empty path and must be named")
	assert.True(t, info.OrchestratorAvailable)
	require.Len(t, info.Models, 1)
	assert.Equal(t, supportpkg.ModelRuntimeInfo{
		RegistryID: classifier.RegistryIDPerchV2, Backend: classifier.BackendONNX, Device: "CPU",
		ModelPath: "/models/perch.onnx", LabelCount: 10, ConfiguredThreads: 2, Generation: 4, LoadedAt: loaded,
	}, info.Models[0])
	require.NotNil(t, info.RangeFilter)
	assert.True(t, info.RangeFilter.Available)
	assert.True(t, info.RangeFilter.Active)
	assert.Equal(t, 9, info.RangeFilter.MappedSpecies)
	assert.Equal(t, "configured", info.ModelsDirectory.Source)
	assert.True(t, info.ModelsDirectory.Exists, "the directory the orchestrator uses must win over the configured one")
	assert.Equal(t, string(diagnostics.PersistenceContainerLayer), info.ModelsDirectory.Persistence)
	assert.Equal(t, "overlay", info.ModelsDirectory.FSType)
}

// TestRuntimeInfoProvider_NoSettings pins that missing settings drop only the
// settings-derived sections, not the runtime state.
func TestRuntimeInfoProvider_NoSettings(t *testing.T) {
	info := testRuntimeProvider(nil, nil, "Native").CollectRuntimeInfo(t.Context())

	assert.True(t, info.Available)
	assert.NotEmpty(t, info.Reason)
	assert.NotNil(t, info.ONNXRuntime)
	assert.NotNil(t, info.OpenVINO)
	assert.Nil(t, info.RangeFilter)
	assert.Nil(t, info.ModelsDirectory)
	assert.Empty(t, info.Sources)
}

// TestNewRuntimeInfoProviderWithoutCore verifies the production seams are wired
// and a provider with no core reports no orchestrator instead of failing.
func TestNewRuntimeInfoProviderWithoutCore(t *testing.T) {
	p := newRuntimeInfoProvider(nil)
	require.NotNil(t, p.ortSnap)
	require.NotNil(t, p.ovSnap)
	require.NotNil(t, p.env)
	require.NotNil(t, p.mountTable)
	require.NotNil(t, p.resolve)

	assert.Nil(t, p.orch())

	info := p.CollectRuntimeInfo(t.Context())
	require.NotNil(t, info)
	assert.True(t, info.Available)
	assert.False(t, info.OrchestratorAvailable)
}

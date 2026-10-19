package support

import (
	"context"
	"os"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/inference"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
	supportpkg "github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// runtimeSettingsUnavailableReason is reported when no settings snapshot has
// been published yet, so the settings-derived sections are missing.
const runtimeSettingsUnavailableReason = "settings not loaded; settings-derived sections are missing"

// runtimeSource is the part of the orchestrator the runtime info reads. All three
// methods are lock-free snapshots that never wait on inference or a model load.
type runtimeSource interface {
	ModelRuntimeSnapshots() []classifier.ModelRuntimeSnapshot
	RangeFilterRuntimeSummary() classifier.RangeFilterRuntimeSummary
	ModelsDirSnapshot() string
}

// runtimeInfoProvider implements supportpkg.RuntimeInfoProvider from the live
// process. The constructor fills every seam with the real implementation; tests
// replace them.
//
// It reads only snapshots that the inference and classifier packages publish
// when the runtime initializes and when models load, reload and unload. It must
// never call the orchestrator accessors that take a model lock
// (GetModelRuntimeInfo, RangeFilterStatus, ModelInfos): they wait behind
// inference, and the dump has a deadline.
type runtimeInfoProvider struct {
	core *apicore.Core

	ortSnap    func() inference.ORTRuntime
	ovSnap     func() inference.OpenVINORuntime
	orch       func() runtimeSource
	settings   func() *conf.Settings
	env        func() string
	mountTable func() ([]diagnostics.MountTableEntry, error)
	resolve    func(string) (string, error)
}

// newRuntimeInfoProvider returns the production provider for core.
func newRuntimeInfoProvider(core *apicore.Core) *runtimeInfoProvider {
	p := &runtimeInfoProvider{
		core:       core,
		ortSnap:    inference.ORTRuntimeSnapshot,
		ovSnap:     inference.OpenVINORuntimeSnapshot,
		env:        currentEnvironment,
		mountTable: diagnostics.ReadOwnMountTable,
		resolve:    diagnostics.ResolveRealPath,
	}
	p.orch = p.liveSource
	p.settings = p.liveSettings
	return p
}

// currentEnvironment returns the detected runtime environment type.
func currentEnvironment() string {
	env, _ := sysinfo.GetEnvironment()
	return env
}

// CollectRuntimeInfo implements supportpkg.RuntimeInfoProvider. Settings are read
// on every call, so a hot-reloaded change shows in the next dump.
func (p *runtimeInfoProvider) CollectRuntimeInfo(_ context.Context) *supportpkg.RuntimeInfo {
	info := &supportpkg.RuntimeInfo{Available: true}

	info.ONNXRuntime = p.onnxRuntime()
	info.OpenVINO = p.openVINO()

	src := p.orch()
	info.OrchestratorAvailable = src != nil

	var summary classifier.RangeFilterRuntimeSummary
	if src != nil {
		info.Models = modelRuntimeInfos(src.ModelRuntimeSnapshots())
		summary = src.RangeFilterRuntimeSummary()
	}

	s := p.settings()
	if s == nil {
		info.Reason = runtimeSettingsUnavailableReason
		return info
	}
	info.BirdNETThreadsSetting = s.BirdNET.Threads
	info.RangeFilter = rangeFilterInfo(s, summary)
	info.Sources = sourceModelInfos(s)
	info.ModelsDirectory = p.modelsDirectory(s, src)
	return info
}

// onnxRuntime reports the ONNX Runtime snapshot with the raw library path left
// for the collector to reduce.
func (p *runtimeInfoProvider) onnxRuntime() *supportpkg.ONNXRuntimeInfo {
	ort := p.ortSnap()
	return &supportpkg.ONNXRuntimeInfo{
		Initialized:       ort.Initialized,
		Version:           ort.Version,
		RequiredVersion:   inference.ORTRequiredVersion(),
		LibraryPath:       ort.LibraryPath,
		LibraryConfigured: ort.LibraryConfigured,
	}
}

// openVINO reports the OpenVINO snapshot. A load that used the default soname
// has an empty path, which is reported as that soname.
func (p *runtimeInfoProvider) openVINO() *supportpkg.OpenVINOInfo {
	rt := p.ovSnap()
	path := rt.LibraryPath
	if rt.LibraryLoaded && path == "" {
		path = ov.DefaultLibraryName
	}
	return &supportpkg.OpenVINOInfo{
		CompiledIn:  rt.CompiledIn,
		Initialized: rt.Initialized,
		Active:      rt.Active,
		LibraryPath: path,
		Probe: supportpkg.OpenVINOProbeInfo{
			Status:  rt.ProbeStatus,
			Devices: rt.ProbeDevices,
			Failed:  rt.ProbeFailed,
		},
	}
}

// liveSource returns the orchestrator snapshots, or nil when it cannot be reached.
func (p *runtimeInfoProvider) liveSource() runtimeSource {
	if p.core == nil {
		return nil
	}
	o, err := p.core.GetBirdNETInstance()
	if err != nil {
		return nil
	}
	return o
}

// liveSettings returns the latest settings snapshot, or nil before any is published.
func (p *runtimeInfoProvider) liveSettings() *conf.Settings {
	if p.core == nil {
		return conf.GetSettings()
	}
	return p.core.CurrentSettings()
}

// modelRuntimeInfos maps the orchestrator snapshots, keeping the raw model path
// for the collector to reduce to a basename.
func modelRuntimeInfos(snaps []classifier.ModelRuntimeSnapshot) []supportpkg.ModelRuntimeInfo {
	out := make([]supportpkg.ModelRuntimeInfo, 0, len(snaps))
	for i := range snaps {
		out = append(out, supportpkg.ModelRuntimeInfo{
			RegistryID:        snaps[i].RegistryID,
			Backend:           snaps[i].Backend,
			Device:            snaps[i].Device,
			Precision:         snaps[i].Precision,
			LabelCount:        snaps[i].NumSpecies,
			ConfiguredThreads: snaps[i].ConfiguredThreads,
			Generation:        snaps[i].Generation,
			LoadedAt:          snaps[i].LoadedAt,
			ModelPath:         snaps[i].ModelPath,
		})
	}
	return out
}

// rangeFilterInfo combines the orchestrator summary with the configured range
// filter. Paths are reported as set and existing, never as the path.
func rangeFilterInfo(s *conf.Settings, summary classifier.RangeFilterRuntimeSummary) *supportpkg.RangeFilterRuntimeInfo {
	rf := &s.BirdNET.RangeFilter
	return &supportpkg.RangeFilterRuntimeInfo{
		Available:            summary.Available,
		Active:               summary.Active,
		FellBack:             summary.FellBack,
		Backend:              summary.Backend,
		MappedSpecies:        summary.MappedSpecies,
		Participants:         summary.Participants,
		IncludedSpeciesCount: len(rf.Species),
		Model:                rf.Model,
		ModelPathConfigured:  rf.ModelPath != "",
		ModelFileExists:      fileExists(rf.ModelPath),
		LabelsPathConfigured: rf.LabelsPath != "",
		LabelsFileExists:     fileExists(rf.LabelsPath),
		StockGeomodelPresent: summary.StockGeomodelPresent,
	}
}

// fileExists reports whether path names an existing file or directory. An empty
// path does not exist, because os.Stat rejects it.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sourceModelInfos lists the model assignment of every configured audio source
// and stream by index. Names, devices and URLs are never copied.
func sourceModelInfos(s *conf.Settings) []supportpkg.SourceModelInfo {
	audio := s.Realtime.Audio.Sources
	streams := s.Realtime.RTSP.Streams
	out := make([]supportpkg.SourceModelInfo, 0, len(audio)+len(streams))
	for i := range audio {
		out = append(out, sourceModelInfo(supportpkg.SourceKindAudio, i, true, audio[i].Models))
	}
	for i := range streams {
		out = append(out, sourceModelInfo(supportpkg.SourceKindStream, i, streams[i].IsEnabled(), streams[i].Models))
	}
	return out
}

// sourceModelInfo resolves one source's config model IDs to registry IDs. Only IDs
// that resolve are copied; the rest are counted, since they are free text.
func sourceModelInfo(kind string, index int, enabled bool, models []string) supportpkg.SourceModelInfo {
	info := supportpkg.SourceModelInfo{
		Kind:        kind,
		Index:       index,
		Enabled:     enabled,
		UsesDefault: len(models) == 0,
	}
	for _, id := range models {
		registryID, ok := classifier.ResolveConfigModelID(id)
		if !ok {
			info.UnknownModels++
			continue
		}
		info.Models = append(info.Models, id)
		info.RegistryIDs = append(info.RegistryIDs, registryID)
	}
	return info
}

// modelsDirectory describes the models directory in use. The directory is not
// named, but the container-side mount point can equal it. The directory the
// orchestrator actually uses wins over the one settings resolve to, because the
// orchestrator takes it once at startup.
func (p *runtimeInfoProvider) modelsDirectory(s *conf.Settings, src runtimeSource) *supportpkg.ModelsDirectoryInfo {
	dir := ""
	if src != nil {
		dir = src.ModelsDirSnapshot()
	}
	if dir == "" {
		dir, _ = s.ResolveModelsDir()
	}
	info := &supportpkg.ModelsDirectoryInfo{Source: supportpkg.ModelsDirSourceDefault}
	if s.Models.Directory != "" {
		info.Source = supportpkg.ModelsDirSourceConfigured
	}
	if dir == "" {
		info.Source = supportpkg.ModelsDirSourceUnavailable
		return info
	}
	info.Exists = fileExists(dir)

	if !diagnostics.StoragePersistenceAppliesTo(p.env()) {
		info.Persistence = supportpkg.PersistenceNotApplicable
		return info
	}

	table, err := p.mountTable()
	if err != nil {
		info.Persistence = string(diagnostics.PersistenceUnknown)
		return info
	}
	verdicts := diagnostics.EvaluateStoragePersistence(table, []diagnostics.StoragePath{{Label: diagnostics.StorageLabelModels, Path: dir}}, p.resolve)
	if len(verdicts) > 0 {
		info.Persistence = string(verdicts[0].Class)
		info.MountPoint = verdicts[0].MountPoint
		info.FSType = verdicts[0].FSType
	}
	return info
}

// internal/api/v2/system/inference_status.go
package system

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// sourceTypeSoundCard is the source type label for local ALSA/sound card captures.
const sourceTypeSoundCard = "soundcard"

// eventInferenceTopologyChanged is the SSE event name emitted over the metrics
// stream whenever the inference topology (loaded models or audio source
// attachment) changes. It is the single source of truth for the event name;
// the frontend listens for this exact string and re-fetches the
// /api/v2/system/inference snapshot on receipt.
const eventInferenceTopologyChanged = "system.inference_topology_changed"

// InferenceStatusResponse is the top-level payload for GET /api/v2/system/inference.
type InferenceStatusResponse struct {
	Hardware HardwareInfo           `json:"hardware"`
	Backends BackendsInfo           `json:"backends"`
	Models   []InferenceModelStatus `json:"models"`
	Audio    AudioMetricsInfo       `json:"audio"`
	// VAD is the privacy-filter Silero VAD speech-gate status. Present only when
	// the privacy filter is enabled (nil hides the dashboard panel entirely).
	VAD                  *VADStatusInfo `json:"vad,omitempty"`
	RuntimeBaselineBytes int64          `json:"runtimeBaselineBytes,omitempty"`
	SnapshotAtUnix       int64          `json:"snapshotAtUnix"`
}

// VADStatusInfo reports the privacy-filter Silero VAD speech gate for the
// inference dashboard. Stats are lifetime totals that survive session reloads.
type VADStatusInfo struct {
	// Enabled is the configured VAD gate toggle (realtime.privacyfilter.vad.enabled).
	Enabled bool `json:"enabled"`
	// Available reports whether a model source resolves (an embedded model is
	// present, or a modelpath override is set). When false the gate is inert even
	// if Enabled is true (e.g. a noembed build with no modelpath).
	Available bool `json:"available"`
	// Loaded is true when a session is currently held (loaded and scoring). It is
	// set on a successful load and cleared on unload or an inference error.
	Loaded bool `json:"loaded"`
	// Threshold is the configured speech-probability gate threshold.
	Threshold float64 `json:"threshold"`
	// ModelSource is "embedded", "path" or "" (unloaded); never the on-disk path.
	ModelSource string `json:"modelSource,omitempty"`
	// Strategy is the active windowing strategy ("sequence"),
	// empty when unloaded.
	Strategy string `json:"strategy,omitempty"`
	// SampleRate is the native sample rate of the loaded Silero VAD model (16 kHz),
	// 0 when unloaded.
	SampleRate int `json:"sampleRate,omitempty"`
	// Stats holds lifetime inference counters for the gate.
	Stats VADStatsInfo `json:"stats"`
	// LastSpeechAtUnix is the Unix timestamp (seconds) of the most recent speech
	// hit, 0 when none since start.
	LastSpeechAtUnix int64 `json:"lastSpeechAtUnix,omitempty"`
	// LastSpeechProbability is the VAD speech probability [0,1] of the most recent
	// speech hit (pairs with LastSpeechAtUnix); 0 when none since start.
	LastSpeechProbability float64 `json:"lastSpeechProbability,omitempty"`
	// RecentHits is the newest-first history of recent speech hits (up to 10),
	// always present (empty when none) so the frontend renders a stable feed.
	RecentHits []VADHitInfo `json:"recentHits"`
}

// VADStatsInfo holds lifetime inference statistics for the VAD speech gate.
type VADStatsInfo struct {
	// Invocations is the total number of VAD inference calls performed.
	Invocations int64 `json:"invocations"`
	// AvgMs is the lifetime average inference time in milliseconds.
	AvgMs float64 `json:"avgMs"`
	// MaxMs is the lifetime peak inference time in milliseconds.
	MaxMs float64 `json:"maxMs"`
	// SpeechHits is the total number of chunks scored at or above the threshold.
	SpeechHits int64 `json:"speechHits"`
}

// VADHitInfo is one recent VAD speech hit in the dashboard history feed.
type VADHitInfo struct {
	// AtUnix is the Unix timestamp (seconds) of the speech hit.
	AtUnix int64 `json:"atUnix"`
	// Probability is the VAD speech probability [0,1] that tripped the gate.
	Probability float64 `json:"probability"`
	// Source is the display name of the audio source; may be empty.
	Source string `json:"source,omitempty"`
}

// HardwareInfo describes the host CPU/environment reported at snapshot time.
// The first four fields predate the hardware profile and keep their names,
// types and sources; everything below them is additive and omitted when the
// probe found nothing to report.
type HardwareInfo struct {
	Arch        string `json:"arch"`
	CPUModel    string `json:"cpuModel"`
	Environment string `json:"environment"`
	FP16        bool   `json:"fp16"`
	// Board identifies the single-board computer this runs on, absent on hosts
	// with no device tree (every PC).
	Board *BoardInfo `json:"board,omitempty"`
	// Accelerators lists the GPUs present on the host, whether or not this build
	// can use them, so the UI can explain an unusable one instead of hiding it.
	Accelerators []AcceleratorInfo `json:"accelerators,omitempty"`
	// TotalRamBytes is the effective memory ceiling, host RAM clamped by any
	// cgroup limit.
	TotalRAMBytes int64 `json:"totalRamBytes,omitempty"`
	// PhysicalCores is the physical core count.
	PhysicalCores int `json:"physicalCores,omitempty"`
	// Capabilities are the capability tokens this host matches, in the same
	// vocabulary the published model manifests use.
	Capabilities []string `json:"capabilities,omitempty"`
}

// BoardInfo identifies the host board as named by its device tree.
type BoardInfo struct {
	// Kind is the board family ("raspberry-pi", "generic").
	Kind string `json:"kind"`
	// Model is the device-tree model string.
	Model string `json:"model,omitempty"`
	// SoC is the system-on-chip identifier, e.g. "bcm2712".
	SoC string `json:"soc,omitempty"`
	// Tier is the performance band ("pi5", "pi4", "pi3"), empty for boards the
	// model catalog does not distinguish.
	Tier string `json:"tier,omitempty"`
}

// AcceleratorInfo describes one GPU the host exposes.
type AcceleratorInfo struct {
	// Kind is "igpu" or "dgpu".
	Kind string `json:"kind"`
	// Vendor is "intel", "amd" or "nvidia".
	Vendor string `json:"vendor"`
	// Name is a display name pairing the vendor with the PCI IDs. It is not
	// unique: two identical cards produce the same name, so no client may key
	// off it.
	Name string `json:"name,omitempty"`
	// Accessible reports whether the server can reach the device's DRM render
	// node. It is not a prediction that inference will run here; which device a
	// model actually runs on is reported per model in the models list.
	Accessible bool `json:"accessible"`
	// Reasons lists every reason code explaining why this GPU is not an
	// inference target, most fundamental first. The frontend renders them
	// through the i18n catalog.
	Reasons []string `json:"reasons,omitempty"`
}

// BackendsInfo groups availability status for all supported inference backends.
type BackendsInfo struct {
	TFLite   BackendStatus         `json:"tflite"`
	ONNX     BackendStatus         `json:"onnx"`
	OpenVINO OpenVINOBackendStatus `json:"openvino"`
}

// BackendStatus reports whether an inference backend is available and initialized.
type BackendStatus struct {
	Available   bool   `json:"available"`
	Initialized bool   `json:"initialized,omitempty"`
	Version     string `json:"version,omitempty"`
}

// OpenVINOBackendStatus extends BackendStatus with OpenVINO-specific device info.
type OpenVINOBackendStatus struct {
	Supported bool     `json:"supported"`
	Active    bool     `json:"active"`
	Devices   []string `json:"devices,omitempty"`
}

// InferenceModelStatus describes one loaded model and its runtime statistics.
type InferenceModelStatus struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Backend          string `json:"backend"`
	DetectionName    string `json:"detectionName,omitempty"`
	DetectionVersion string `json:"detectionVersion,omitempty"`
	Quantization     string `json:"quantization,omitempty"`
	IsStock          bool   `json:"isStock"`
	// Device is the compute device (execution provider) this model's inference
	// runs on, resolved from the live runtime binding ("CPU", "GPU", or "Unknown"
	// when the model is not loaded). Never inferred from the backend string.
	Device string `json:"device"`
	// Paused is true when the model is currently prevented from running inference
	// by a schedule (e.g. the bat model outside its nighttime window).
	Paused bool `json:"paused"`
	// ScheduleLabel is the human-readable reason the model is paused (e.g.
	// "Night schedule"). Empty when the model is active.
	ScheduleLabel string             `json:"scheduleLabel,omitempty"`
	Spec          ModelSpecInfo      `json:"spec"`
	NumSpecies    int                `json:"numSpecies"`
	Stats         ModelStats         `json:"stats"`
	Memory        ModelMemoryInfo    `json:"memory"`
	Sources       []ModelSourceInfo  `json:"sources"`
	MetricKeys    ModelMetricKeys    `json:"metricKeys"`
	LastDetection *LastDetectionInfo `json:"lastDetection,omitempty"`
	// RecentDetections is the newest-first feed of up to 20 recent above-threshold
	// detections for this model (the "Last heard" table), throttled per species so
	// a continuously singing bird does not flood it. Empty when none.
	RecentDetections []LastDetectionInfo `json:"recentDetections"`
}

// ModelSpecInfo carries the audio input requirements of a model.
type ModelSpecInfo struct {
	SampleRate    int     `json:"sampleRate"`
	ClipLengthSec float64 `json:"clipLengthSec"`
}

// ModelStats holds invocation counts and latency for a single model.
// AvgMs is the lifetime average and MaxMs is the lifetime peak (both since
// startup), so MaxMs is always >= AvgMs; RTF is nil when there have been no
// invocations. MaxMs comes from the never-reset PeekSnapshot.InvokeMaxUsLifetime,
// not the collector's reset-on-read windowed max.
//
// RTF is the lifetime cumulative-average real-time factor: (avgMs / 1000) / clipSec.
// This differs from the per-model ring-buffer series at MetricKeys.RTF, which is
// an interval-windowed average computed by the collector on each tick.
//
// ErrorRate is the fraction of calls that resulted in an error:
// InvokeErrors / (InvokeCount + InvokeErrors). Nil when total is zero.
// LoadFailures is the cumulative count of model-load failures from the orchestrator.
type ModelStats struct {
	Invocations  int64    `json:"invocations"`
	AvgMs        float64  `json:"avgMs"`
	MaxMs        float64  `json:"maxMs"`
	RTF          *float64 `json:"rtf,omitempty"`
	ErrorRate    *float64 `json:"errorRate,omitempty"`
	LoadFailures *int64   `json:"loadFailures,omitempty"`
}

// ModelMemoryInfo reports the estimated RSS contribution of a loaded model.
// ApproxRssBytes is nil when the platform does not support measurement.
type ModelMemoryInfo struct {
	ApproxRssBytes *int64 `json:"approxRssBytes,omitempty"`
	Approximate    bool   `json:"approximate"`
}

// ModelSourceInfo describes one audio source attached to a model.
// Fallback is true when the source is attached to the primary model by default
// rather than by an explicit config selection.
type ModelSourceInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Fallback bool   `json:"fallback,omitempty"`
	// NotRunning marks a source that the configuration assigns to this model but
	// whose audio does not actually reach it, because the audio router has no
	// analysis buffer for the pair. That happens when the model failed to load or
	// its buffer allocation failed, and it used to be invisible: the status
	// reported the model as attached purely because the config said so, so the UI
	// showed a model running while it analyzed nothing (GitHub #4201, #4204).
	// Omitted when the source really is attached.
	NotRunning bool `json:"notRunning,omitempty"`
}

// ModelMetricKeys carries the Prometheus-style metric key names for a model's
// latency, real-time-factor, throughput, and error-rate time series, so clients
// can look them up without hardcoding.
type ModelMetricKeys struct {
	AvgMs      string `json:"avgMs"`
	RTF        string `json:"rtf"`
	Throughput string `json:"throughput"`
	ErrorRate  string `json:"errorRate"`
}

// AudioMetricKeys holds the time-series metric key names for audio pipeline metrics.
type AudioMetricKeys struct {
	// QueueDepth is the metric key for the aggregate analysis queue depth.
	QueueDepth string `json:"queueDepth"`
}

// AudioMetricsInfo holds a point-in-time snapshot of audio pipeline metrics.
type AudioMetricsInfo struct {
	// QueueDepth is the sum across all active sources of each source's maximum
	// route inbox occupancy at snapshot time (per-source max, then summed). This
	// matches the aggregate series produced by the observability collector, which
	// records the same sum into MetricKeyAudioQueueDepthAggregate each tick.
	QueueDepth int `json:"queueDepth"`
	// DroppedChunksTotal is the cumulative count of dropped audio chunks.
	DroppedChunksTotal int64 `json:"droppedChunksTotal"`
	// QueueCapacity is the aggregate inbox capacity represented by QueueDepth:
	// RouteInboxCapacity per active source, summed, so it stays on the same
	// scale as the per-source-summed QueueDepth (depth never exceeds capacity).
	QueueCapacity int `json:"queueCapacity"`
	// MetricKeys holds the metric key names for audio pipeline time series.
	MetricKeys AudioMetricKeys `json:"metricKeys"`
}

// LastDetectionInfo holds information about the most recently detected species for a model.
type LastDetectionInfo struct {
	// Species is the common name of the detected species.
	Species string `json:"species"`
	// ScientificName is the scientific name of the detected species.
	ScientificName string `json:"scientificName"`
	// Confidence is the detection confidence in the range [0, 1].
	Confidence float64 `json:"confidence"`
	// AtUnix is the Unix timestamp (seconds) of when the detection occurred.
	AtUnix int64 `json:"atUnix"`
	// InRange reports whether the species passes the range filter. True when in
	// range or the range filter is inactive (e.g. no location configured). When
	// the range filter is active it is false for out-of-range birds and for
	// non-avian and human classes, which are shown for diagnostics but not saved.
	InRange bool `json:"inRange"`
}

// deviceCPU and deviceGPU are the OpenVINO device name strings used when
// probing which compute devices are available for inference. deviceUnknown is
// the per-model device fallback when the orchestrator cannot resolve a live
// binding (model not loaded).
const (
	deviceCPU     = "CPU"
	deviceGPU     = "GPU"
	deviceUnknown = "Unknown"
)

// buildModelStatus assembles an InferenceModelStatus for one loaded model from
// its registry info, a non-destructive stats peek, the per-model RSS map, the
// pre-computed source attachment list, the per-model load-failure counts, and
// the per-model last-detection cache. It is a pure function with no side
// effects and is safe to call concurrently.
func buildModelStatus(info *classifier.ModelInfo, snap inferencestats.PeekSnapshot, rss map[string]int64, sources []ModelSourceInfo, loadFailures map[string]int64, lastDetections map[string]*LastDetectionInfo) InferenceModelStatus {
	clipSec := info.Spec.ClipLength.Seconds()

	avgMs := 0.0
	if snap.InvokeCount > 0 {
		avgMs = float64(snap.InvokeTotalUs) / float64(snap.InvokeCount) / 1000.0
	}
	maxMs := float64(snap.InvokeMaxUsLifetime) / 1000.0

	var rtf *float64
	if snap.InvokeCount > 0 && clipSec > 0 {
		v := (avgMs / 1000.0) / clipSec
		rtf = &v
	}

	// ErrorRate = InvokeErrors / (InvokeCount + InvokeErrors) when total > 0.
	var errorRate *float64
	if total := snap.InvokeCount + snap.InvokeErrors; total > 0 {
		v := float64(snap.InvokeErrors) / float64(total)
		errorRate = &v
	}

	// LoadFailures from the orchestrator's per-model map.
	var loadFail *int64
	if loadFailures != nil {
		if n, ok := loadFailures[info.ID]; ok {
			v := n
			loadFail = &v
		}
	}

	// LastDetection from the processor cache.
	var lastDet *LastDetectionInfo
	if lastDetections != nil {
		lastDet = lastDetections[info.ID]
	}

	mem := ModelMemoryInfo{Approximate: true}
	if rss != nil {
		if b, ok := rss[info.ID]; ok {
			mem.ApproxRssBytes = &b
		}
	}

	if sources == nil {
		sources = []ModelSourceInfo{}
	}

	return InferenceModelStatus{
		ID:               info.ID,
		Name:             info.Name,
		Backend:          info.Backend,
		DetectionName:    info.DetectionName,
		DetectionVersion: info.DetectionVersion,
		Quantization:     string(info.Quantization),
		IsStock:          info.IsStock,
		Spec:             ModelSpecInfo{SampleRate: info.Spec.SampleRate, ClipLengthSec: clipSec},
		NumSpecies:       info.NumSpecies,
		Stats: ModelStats{
			Invocations:  snap.InvokeCount,
			AvgMs:        avgMs,
			MaxMs:        maxMs,
			RTF:          rtf,
			ErrorRate:    errorRate,
			LoadFailures: loadFail,
		},
		Memory:  mem,
		Sources: sources,
		MetricKeys: ModelMetricKeys{
			AvgMs:      inferencestats.MetricKey(info.ID),
			RTF:        inferencestats.RTFMetricKey(info.ID),
			Throughput: inferencestats.ThroughputMetricKey(info.ID),
			ErrorRate:  inferencestats.ErrorRateMetricKey(info.ID),
		},
		LastDetection: lastDet,
	}
}

// modelRuntime is the live device/backend/precision triplet for one model,
// resolved atomically from the orchestrator (one GetModelRuntimeInfo call) so the
// status card never shows a mixed-generation triplet when a reload races a poll.
type modelRuntime struct {
	device    string
	backend   string
	precision string
}

// applyRuntimeBackend overrides a model status's static file metadata (Backend,
// Quantization) with the live values resolved from the loaded instance: the real
// execution provider and effective runtime precision. An empty live value means
// the model is not loaded or the value is unknown, so the static ModelInfo values
// set by buildModelStatus are kept as the fallback. This is what makes an ONNX
// model executed on the OpenVINO runtime report "OpenVINO" with its FP16/FP32
// compute precision instead of the static "ONNX" file type.
func applyRuntimeBackend(status *InferenceModelStatus, backend, precision string) {
	if backend != "" {
		status.Backend = backend
	}
	if precision != "" {
		status.Quantization = precision
	}
}

// GetInferenceStatus handles GET /api/v2/system/inference. It returns a
// read-only snapshot of the inference subsystem: hardware, backends, loaded
// models with per-model stats and memory, source attachment, and audio pipeline
// metrics. The snapshot is assembled from live sources on every request so it
// reflects hot-reload changes without any caching.
func (c *Handler) GetInferenceStatus(ctx echo.Context) error {
	settings := c.CurrentSettings()

	resp := InferenceStatusResponse{
		SnapshotAtUnix: time.Now().Unix(),
	}

	// Backends: TFLite is always compiled in; ORT and OpenVINO are probed.
	// Probed before hardware because the ORT result feeds capability derivation.
	resp.Backends.TFLite = BackendStatus{Available: hwprofile.TFLiteLinked()}
	ort := inference.CheckORTAvailability(settings.BirdNET.ONNXRuntimePath)
	resp.Backends.ONNX = BackendStatus{Available: ort.Available, Initialized: ort.Initialized, Version: ort.Version}
	ov := inference.CheckOpenVINOAvailability()
	resp.Backends.OpenVINO = OpenVINOBackendStatus{Supported: ov.Supported, Active: ov.Active}
	if ov.Supported {
		for _, d := range []string{deviceCPU, deviceGPU} {
			if inference.OpenVINOHasDevice(d) {
				resp.Backends.OpenVINO.Devices = append(resp.Backends.OpenVINO.Devices, d)
			}
		}
	}

	// Hardware. The backends probed just above are the authoritative ones: they
	// honour the user-configured ONNX Runtime path, which hwprofile cannot see
	// because it carries no settings dependency. Handing them to the profile
	// rather than letting it probe again keeps one probe behind every field of
	// this response, so the backends card and the capability tokens cannot
	// disagree, and halves the per-request OpenVINO device queries.
	profile := hwprofile.Hardware().WithBackends(hwprofile.Backends{
		TFLite: hwprofile.BackendStatus{Available: resp.Backends.TFLite.Available},
		ONNX: hwprofile.BackendStatus{
			Available:   ort.Available,
			Initialized: ort.Initialized,
			Version:     ort.Version,
		},
		OpenVINO: hwprofile.OpenVINOStatus{
			Supported: ov.Supported,
			Active:    ov.Active,
			Devices:   resp.Backends.OpenVINO.Devices,
		},
	})
	envType, _ := sysinfo.GetEnvironment() // detail (sub-type) intentionally omitted in Phase 1
	resp.Hardware = buildHardwareInfo(profile, envType)

	// Models: fetch loaded model list, RSS, and inference counters.
	var infos []classifier.ModelInfo
	if c.ModelManager != nil {
		infos = c.ModelManager.ModelInfos()
	}
	// Fetch the orchestrator once: it is the live source for RSS, primary ID,
	// load failures, per-model device, and per-model schedule status. The
	// Processor guard mirrors the GetLastDetection guard below.
	var orch *classifier.Orchestrator
	if c.Processor != nil {
		orch = c.Processor.GetBirdNET()
	}
	var rss map[string]int64
	primaryID := ""
	var loadFailures map[string]int64
	if orch != nil {
		rss, resp.RuntimeBaselineBytes = orch.ModelRSS()
		primaryID = orch.PrimaryModelID()
		loadFailures = orch.LoadFailures()
	}
	counters := classifier.GetInferenceCounters().PeekAll()
	attachments := buildSourceAttachments(settings, infos, primaryID, c.runningModelsBySource())

	// Compute per-model device, backend, precision, and schedule status from the
	// live orchestrator. The device/backend/precision triplet is read in one
	// GetModelRuntimeInfo call so a reload completing mid-read cannot yield a mixed
	// triplet (e.g. old device + new backend). Backend and precision come from the
	// loaded instance (the real execution provider and runtime precision) rather
	// than the static ModelInfo file metadata, so an ONNX model executed on
	// OpenVINO reports "OpenVINO" and its effective precision. Empty values fall
	// back to the static metadata in the assembly loop below.
	runtimes := make(map[string]modelRuntime, len(infos))
	paused := make(map[string]bool, len(infos))
	scheduleLabels := make(map[string]string, len(infos))
	if orch != nil {
		for i := range infos {
			id := infos[i].ID
			device, backend, precision := orch.GetModelRuntimeInfo(id)
			runtimes[id] = modelRuntime{device: device, backend: backend, precision: precision}
			active, reason := orch.ModelScheduleStatus(id)
			paused[id] = !active
			scheduleLabels[id] = reason
		}
	}

	// Compute per-model last detection and the recent-detections list (newest
	// first), converting from processor.LastDetection to the API-local
	// LastDetectionInfo via field assignment (no type import needed).
	var lastDetections map[string]*LastDetectionInfo
	recentDetections := make(map[string][]LastDetectionInfo, len(infos))
	if c.Processor != nil {
		lastDetections = make(map[string]*LastDetectionInfo, len(infos))
		for i := range infos {
			id := infos[i].ID
			// Derive both the recent-detections list and the single most-recent
			// detection from one GetRecentDetections snapshot (newest first), so
			// lastDetection and recentDetections[0] are always consistent and we
			// take only one read lock per model.
			recent := c.Processor.GetRecentDetections(id)
			if len(recent) == 0 {
				continue
			}
			converted := make([]LastDetectionInfo, len(recent))
			for j := range recent {
				converted[j] = LastDetectionInfo{
					Species:        recent[j].Species,
					ScientificName: recent[j].ScientificName,
					Confidence:     recent[j].Confidence,
					AtUnix:         recent[j].AtUnix,
					InRange:        recent[j].InRange,
				}
			}
			latest := converted[0]
			lastDetections[id] = &latest
			recentDetections[id] = converted
		}
	}

	// Audio pipeline metrics: sum queue depth and drops across all active sources.
	audioSnaps := c.buildAudioRouterSnapshotProvider()()
	var totalQueueDepth int
	var totalDrops int64
	for _, s := range audioSnaps {
		totalQueueDepth += int(s.QueueDepth)
		totalDrops += s.Drops
	}
	// QueueCapacity tracks QueueDepth's scale: one RouteInboxCapacity per active
	// source, summed. With multiple sources this keeps depth <= capacity instead
	// of comparing a summed depth against a single route's capacity.
	queueCapacity := audiocore.RouteInboxCapacity
	if len(audioSnaps) > 1 {
		queueCapacity = len(audioSnaps) * audiocore.RouteInboxCapacity
	}
	resp.Audio = AudioMetricsInfo{
		QueueDepth:         totalQueueDepth,
		DroppedChunksTotal: totalDrops,
		QueueCapacity:      queueCapacity,
		MetricKeys:         AudioMetricKeys{QueueDepth: observability.MetricKeyAudioQueueDepthAggregate},
	}

	// Privacy-filter Silero VAD speech gate. Reported only when the privacy filter
	// is enabled; a nil VAD block hides the dashboard panel. The runtime stats come
	// from the processor's always-on counters (independent of Prometheus), so the
	// panel works with telemetry disabled.
	if settings.Realtime.PrivacyFilter.Enabled {
		vadCfg := settings.Realtime.PrivacyFilter.VAD
		info := &VADStatusInfo{
			Enabled:   vadCfg.Enabled,
			Available: vad.HasEmbeddedModel() || vadCfg.ModelPath != "",
			Threshold: vadCfg.Threshold,
		}
		if c.Processor != nil {
			st := c.Processor.VADStatus()
			info.Loaded = st.Loaded
			info.ModelSource = st.Source
			info.Strategy = st.Strategy
			info.SampleRate = st.SampleRate
			info.Stats = VADStatsInfo{
				Invocations: st.Invocations,
				AvgMs:       st.AvgMs,
				MaxMs:       st.MaxMs,
				SpeechHits:  st.SpeechHits,
			}
			info.LastSpeechAtUnix = st.LastSpeechUnix
			info.LastSpeechProbability = st.LastSpeechProbability
			if len(st.RecentHits) > 0 {
				info.RecentHits = make([]VADHitInfo, len(st.RecentHits))
				for i, h := range st.RecentHits {
					info.RecentHits[i] = VADHitInfo{AtUnix: h.AtUnix, Probability: h.Probability, Source: h.Source}
				}
			}
		}
		if info.RecentHits == nil {
			info.RecentHits = []VADHitInfo{}
		}
		resp.VAD = info
	}

	resp.Models = make([]InferenceModelStatus, 0, len(infos))
	for i := range infos {
		id := infos[i].ID
		status := buildModelStatus(&infos[i], counters[id], rss, attachments[id], loadFailures, lastDetections)
		// Live runtime fields resolved from the orchestrator/processor, kept out of
		// the pure buildModelStatus so it stays a side-effect-free assembler.
		rt := runtimes[id]
		status.Device = rt.device
		if status.Device == "" {
			status.Device = deviceUnknown
		}
		// Override the static file metadata with the live backend/precision when the
		// model is loaded (see applyRuntimeBackend); an empty value means "not loaded
		// / unknown", so the static ModelInfo values set by buildModelStatus survive.
		applyRuntimeBackend(&status, rt.backend, rt.precision)
		status.Paused = paused[id]
		status.ScheduleLabel = scheduleLabels[id]
		status.RecentDetections = recentDetections[id]
		if status.RecentDetections == nil {
			status.RecentDetections = []LastDetectionInfo{}
		}
		resp.Models = append(resp.Models, status)
	}
	sortInferenceModelsByName(resp.Models)

	return ctx.JSON(http.StatusOK, resp)
}

// buildHardwareInfo maps a hardware profile onto the API payload. It is a pure
// function with no side effects, so the mapping can be asserted without a live
// host. environment stays a parameter rather than being read off the profile so
// a test can vary it independently of everything else the profile carries.
//
//nolint:gocritic // hugeParam: the value parameter keeps this a pure mapper.
func buildHardwareInfo(profile hwprofile.Profile, environment string) HardwareInfo {
	info := HardwareInfo{
		Arch:          profile.CPUArch,
		CPUModel:      profile.CPUModel,
		Environment:   environment,
		FP16:          profile.HasNativeF16,
		TotalRAMBytes: profile.TotalRAMBytes,
		PhysicalCores: profile.PhysicalCores,
		Capabilities:  profile.Capabilities(),
	}

	// A board is reported only when the device tree named one. Every PC would
	// otherwise carry an empty "generic" row that tells the user nothing.
	if profile.Board.Model != "" || profile.Board.SoC != "" {
		info.Board = &BoardInfo{
			Kind:  profile.Board.Kind,
			Model: profile.Board.Model,
			SoC:   profile.Board.SoC,
			Tier:  profile.Board.Tier,
		}
	}

	if len(profile.Accelerators) > 0 {
		info.Accelerators = make([]AcceleratorInfo, 0, len(profile.Accelerators))
		for i := range profile.Accelerators {
			accelerator := profile.Accelerators[i]
			info.Accelerators = append(info.Accelerators, AcceleratorInfo{
				Kind:       accelerator.Kind,
				Vendor:     accelerator.Vendor,
				Name:       accelerator.Name,
				Accessible: accelerator.Accessible,
				Reasons:    accelerator.Reasons,
			})
		}
	}

	return info
}

// sortInferenceModelsByName orders model statuses by display name
// (case-insensitive), tie-broken by ID, so the API returns a deterministic
// order regardless of the orchestrator's map iteration order.
func sortInferenceModelsByName(models []InferenceModelStatus) {
	slices.SortStableFunc(models, func(a, b InferenceModelStatus) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.ID, b.ID),
		)
	})
}

// buildSourceAttachments computes, per loaded model registry ID, the audio
// sources attached to it. A source whose Models resolve to a loaded model
// attaches there; a source with no resolvable model falls back to the primary
// model with Fallback=true.
//
// running carries the audio router's actual per-source model set, keyed by
// source display name (see (*Handler).runningModelsBySource). Configuration
// alone is not evidence that a model analyzes a source: the router fans a
// source out only to the models it has allocated an analysis buffer for, and a
// model can be configured, loaded, and still receive nothing. Reporting from
// config alone is what let the UI show Perch running while it analyzed no audio
// (GitHub #4201, #4204). When running is nil the audio engine is not available
// (the pipeline has not started, or this is a test), and the config-derived
// view is the best answer available, so it is used unmarked.
func buildSourceAttachments(settings *conf.Settings, models []classifier.ModelInfo, primaryID string, running map[string]map[string]bool) map[string][]ModelSourceInfo {
	loaded := make(map[string]bool, len(models))
	for i := range models {
		loaded[models[i].ID] = true
	}
	out := make(map[string][]ModelSourceInfo)

	attach := func(name, sourceType string, configModels []string) {
		// A source can feed several models at once. The runtime fans its audio out
		// to every assigned+loaded model (see analysis.resolveModelTargets), so the
		// status view must attach the source to all of them, not just the first.
		live, haveLive := running[name]
		resolvedToLoaded := false
		for _, cm := range configModels {
			regID, ok := classifier.ResolveConfigModelID(cm)
			if !ok || !loaded[regID] {
				continue
			}
			// A configured model that resolves to a loaded model is a target,
			// regardless of whether it currently has an analysis buffer. This mirrors
			// the pipeline: resolveModelTargets filters on loaded, so a loaded target
			// here is exactly what stops registerConsumersForSources from falling back.
			resolvedToLoaded = true
			// Surface the source either way, but say which it is: a model the router
			// really feeds, or one the config assigns that gets no audio.
			notRunning := haveLive && !live[regID]
			out[regID] = append(out[regID], ModelSourceInfo{
				ID: name, Name: name, Type: sourceType, Fallback: false, NotRunning: notRunning,
			})
		}
		// The runtime falls back to the primary model only when a source resolves to
		// NO loaded target (see registerConsumersForSources). Liveness does not enter
		// that decision: a configured model that is loaded but currently has no
		// analysis buffer is still the resolved target (surfaced with NotRunning
		// above), not replaced by a primary-fallback row the runtime never creates.
		// Keying the fallback on resolvedToLoaded restores parity with the pipeline.
		if !resolvedToLoaded && primaryID != "" {
			// The fallback row describes the primary model that actually analyzes this
			// source, so it carries the same liveness verdict as a resolved row. A
			// primary whose own analysis buffer is absent is not analyzing either, and
			// reporting it as healthy is the "looks running while analyzing nothing"
			// state this endpoint exists to remove.
			out[primaryID] = append(out[primaryID], ModelSourceInfo{
				ID: name, Name: name, Type: sourceType, Fallback: true,
				NotRunning: haveLive && !live[primaryID],
			})
		}
	}

	for i := range settings.Realtime.Audio.Sources {
		src := settings.Realtime.Audio.Sources[i]
		attach(src.Name, sourceTypeSoundCard, src.Models)
	}
	for i := range settings.Realtime.RTSP.Streams {
		st := settings.Realtime.RTSP.Streams[i]
		attach(st.Name, st.Type, st.Models)
	}
	return out
}

// runningModelsBySource reports which models the audio router actually feeds,
// keyed by source display name then registry model ID.
//
// The buffer manager is the ground truth here, not the router's route table:
// the router holds a single multiplexing BufferConsumer per source and cannot
// say which models sit behind it, whereas an analysis buffer exists for exactly
// the (source, model) pairs currently set up to analyze. The invariant: a buffer
// exists for (source, model) iff that model is configured to analyze that source
// and is loaded. Buffers are created by bufMgr.AllocateAnalysis (three call
// sites: the initial per-source registration, a later model-change reconfigure,
// and the engine's own pre-allocation of the primary model's buffer in
// AddSource) and removed by deallocateStaleAnalysisBuffers. That is the same
// state sourceModelsChanged diffs, so the status view and the pipeline's own
// reconfigure decision agree on what is running.
//
// Two source states are reported as "no live evidence" (the key is left absent,
// so the caller keeps the unmarked config-derived view) rather than as negative:
//   - A DisplayName shared by more than one registry source. DisplayName is
//     unique only within audio.sources and within rtsp.streams, never across
//     them; on a collision a live set cannot be mapped to one config entry, so
//     the key is dropped rather than risk marking a healthy model not running.
//   - An empty buffer set. A source is in the registry from AddSource before its
//     buffers are allocated, so an empty set is INDETERMINATE ("not yet"), not a
//     claim that the source runs nothing.
//
// Returns nil when the audio engine is not wired up (before the pipeline starts,
// or in tests), which the caller reads as "no live evidence available".
func (c *Handler) runningModelsBySource() map[string]map[string]bool {
	if c == nil || c.Core == nil {
		return nil
	}
	eng := c.Engine.Load()
	if eng == nil {
		return nil
	}
	registry := eng.Registry()
	bufMgr := eng.BufferManager()
	if registry == nil || bufMgr == nil {
		return nil
	}

	sources := registry.List()

	// Count DisplayName occurrences first so a name shared by more than one
	// registry source can be dropped entirely below (see the doc comment).
	nameCounts := make(map[string]int, len(sources))
	for _, src := range sources {
		if src == nil {
			continue
		}
		nameCounts[src.DisplayName]++
	}

	out := make(map[string]map[string]bool, len(sources))
	for _, src := range sources {
		if src == nil {
			continue
		}
		// Collision: cannot map a live set to a single config entry, so omit it.
		if nameCounts[src.DisplayName] > 1 {
			continue
		}
		// Key by DisplayName: it is what the registry carries for both audio
		// sources and RTSP streams, and it is what the config-side attachment
		// above uses as the source name.
		models := make(map[string]bool)
		for modelID := range bufMgr.AnalysisBuffers(src.ID) {
			models[modelID] = true
		}
		// An empty buffer set is indeterminate (source registered, buffers not yet
		// allocated), so omit the source: the caller then reads haveLive=false for
		// it and keeps the unmarked config-derived view rather than reporting every
		// assigned model as not running.
		if len(models) == 0 {
			continue
		}
		out[src.DisplayName] = models
	}
	return out
}

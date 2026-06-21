// internal/api/v2/inference_status.go
package api

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
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/inference"
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
	Hardware             HardwareInfo           `json:"hardware"`
	Backends             BackendsInfo           `json:"backends"`
	Models               []InferenceModelStatus `json:"models"`
	Audio                AudioMetricsInfo       `json:"audio"`
	RuntimeBaselineBytes int64                  `json:"runtimeBaselineBytes,omitempty"`
	SnapshotAtUnix       int64                  `json:"snapshotAtUnix"`
}

// HardwareInfo describes the host CPU/environment reported at snapshot time.
type HardwareInfo struct {
	Arch        string `json:"arch"`
	CPUModel    string `json:"cpuModel"`
	Environment string `json:"environment"`
	FP16        bool   `json:"fp16"`
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
func (c *Controller) GetInferenceStatus(ctx echo.Context) error {
	settings := c.currentSettings()

	resp := InferenceStatusResponse{
		SnapshotAtUnix: time.Now().Unix(),
	}

	// Hardware.
	envType, _ := sysinfo.GetEnvironment() // detail (sub-type) intentionally omitted in Phase 1
	resp.Hardware = HardwareInfo{
		Arch:        sysinfo.GetCPUArch(),
		CPUModel:    sysinfo.GetCPUModel(),
		Environment: envType,
		FP16:        cpuspec.HasNativeF16(),
	}

	// Backends: TFLite is always compiled in; ORT and OpenVINO are probed.
	resp.Backends.TFLite = BackendStatus{Available: true}
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
	attachments := buildSourceAttachments(settings, infos, primaryID)

	// Compute per-model device, backend, precision, and schedule status from the
	// live orchestrator. Backend and precision come from the loaded instance (the
	// real execution provider and runtime precision) rather than the static
	// ModelInfo file metadata, so an ONNX model executed on OpenVINO reports
	// "OpenVINO" and its effective precision. Empty values fall back to the static
	// metadata in the assembly loop below.
	devices := make(map[string]string, len(infos))
	backends := make(map[string]string, len(infos))
	precisions := make(map[string]string, len(infos))
	paused := make(map[string]bool, len(infos))
	scheduleLabels := make(map[string]string, len(infos))
	if orch != nil {
		for i := range infos {
			id := infos[i].ID
			devices[id] = orch.GetModelDevice(id)
			backends[id] = orch.GetModelBackend(id)
			precisions[id] = orch.GetModelPrecision(id)
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

	resp.Models = make([]InferenceModelStatus, 0, len(infos))
	for i := range infos {
		id := infos[i].ID
		status := buildModelStatus(&infos[i], counters[id], rss, attachments[id], loadFailures, lastDetections)
		// Live runtime fields resolved from the orchestrator/processor, kept out of
		// the pure buildModelStatus so it stays a side-effect-free assembler.
		status.Device = devices[id]
		if status.Device == "" {
			status.Device = deviceUnknown
		}
		// Override the static file metadata with the live backend/precision when the
		// model is loaded (see applyRuntimeBackend); an empty value means "not loaded
		// / unknown", so the static ModelInfo values set by buildModelStatus survive.
		applyRuntimeBackend(&status, backends[id], precisions[id])
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
// sources attached to it from configuration. A source whose Models resolve to a
// loaded model attaches there; a source with no resolvable model falls back to
// the primary model with Fallback=true. Live registry enrichment (DisplayName,
// State) is deferred to Phase 2; Phase 1 uses config identity.
func buildSourceAttachments(settings *conf.Settings, models []classifier.ModelInfo, primaryID string) map[string][]ModelSourceInfo {
	loaded := make(map[string]bool, len(models))
	for i := range models {
		loaded[models[i].ID] = true
	}
	out := make(map[string][]ModelSourceInfo)

	attach := func(name, sourceType string, configModels []string) {
		// A source can feed several models at once. The runtime fans its audio out
		// to every assigned+loaded model (see analysis.resolveModelTargets), so the
		// status view must attach the source to all of them, not just the first.
		matched := false
		for _, cm := range configModels {
			regID, ok := classifier.ResolveConfigModelID(cm)
			if !ok || !loaded[regID] {
				continue
			}
			out[regID] = append(out[regID], ModelSourceInfo{ID: name, Name: name, Type: sourceType, Fallback: false})
			matched = true
		}
		// No assigned model resolved to a loaded one: the primary model analyzes the
		// source as the runtime fallback, so surface it there with Fallback=true.
		if !matched && primaryID != "" {
			out[primaryID] = append(out[primaryID], ModelSourceInfo{ID: name, Name: name, Type: sourceType, Fallback: true})
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

// BroadcastInferenceTopologyChanged signals all metrics-stream SSE clients that
// the inference topology (models or source attachment) changed so they re-fetch
// the /api/v2/system/inference snapshot. Safe to call when no metrics store is set.
func (c *Controller) BroadcastInferenceTopologyChanged() {
	if c == nil || c.metricsStore == nil {
		return
	}
	c.metricsStore.BroadcastTopologyChanged()
}

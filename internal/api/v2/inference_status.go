// internal/api/v2/inference_status.go
package api

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// sourceTypeSoundCard is the source type label for local ALSA/sound card captures.
const sourceTypeSoundCard = "soundcard"

// InferenceStatusResponse is the top-level payload for GET /api/v2/system/inference.
type InferenceStatusResponse struct {
	Hardware             HardwareInfo           `json:"hardware"`
	Backends             BackendsInfo           `json:"backends"`
	Models               []InferenceModelStatus `json:"models"`
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
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Backend          string            `json:"backend"`
	DetectionName    string            `json:"detectionName,omitempty"`
	DetectionVersion string            `json:"detectionVersion,omitempty"`
	Quantization     string            `json:"quantization,omitempty"`
	IsStock          bool              `json:"isStock"`
	Spec             ModelSpecInfo     `json:"spec"`
	NumSpecies       int               `json:"numSpecies"`
	Stats            ModelStats        `json:"stats"`
	Memory           ModelMemoryInfo   `json:"memory"`
	Sources          []ModelSourceInfo `json:"sources"`
	MetricKeys       ModelMetricKeys   `json:"metricKeys"`
}

// ModelSpecInfo carries the audio input requirements of a model.
type ModelSpecInfo struct {
	SampleRate    int     `json:"sampleRate"`
	ClipLengthSec float64 `json:"clipLengthSec"`
}

// ModelStats holds invocation counts and latency for a single model.
// avgMs is the lifetime average; maxMs is the peak since the last metrics
// collection tick (a recent-window peak, since the collector resets it every
// interval); rtf is nil when there have been no invocations.
type ModelStats struct {
	Invocations int64    `json:"invocations"`
	AvgMs       float64  `json:"avgMs"`
	MaxMs       float64  `json:"maxMs"`
	RTF         *float64 `json:"rtf,omitempty"`
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
// latency and real-time-factor time series, so clients can look them up without
// hardcoding.
type ModelMetricKeys struct {
	AvgMs string `json:"avgMs"`
	RTF   string `json:"rtf"`
}

// deviceCPU and deviceGPU are the OpenVINO device name strings used when
// probing which compute devices are available for inference.
const (
	deviceCPU = "CPU"
	deviceGPU = "GPU"
)

// buildModelStatus assembles an InferenceModelStatus for one loaded model from
// its registry info, a non-destructive stats peek, the per-model RSS map, and
// the pre-computed source attachment list. It is a pure function with no side
// effects and is safe to call concurrently.
func buildModelStatus(info *classifier.ModelInfo, snap inferencestats.PeekSnapshot, rss map[string]int64, sources []ModelSourceInfo) InferenceModelStatus {
	clipSec := info.Spec.ClipLength.Seconds()

	avgMs := 0.0
	if snap.InvokeCount > 0 {
		avgMs = float64(snap.InvokeTotalUs) / float64(snap.InvokeCount) / 1000.0
	}
	maxMs := float64(snap.InvokeMaxUs) / 1000.0

	var rtf *float64
	if snap.InvokeCount > 0 && clipSec > 0 {
		v := (avgMs / 1000.0) / clipSec
		rtf = &v
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
		Stats:            ModelStats{Invocations: snap.InvokeCount, AvgMs: avgMs, MaxMs: maxMs, RTF: rtf},
		Memory:           mem,
		Sources:          sources,
		MetricKeys:       ModelMetricKeys{AvgMs: inferencestats.MetricKey(info.ID), RTF: inferencestats.RTFMetricKey(info.ID)},
	}
}

// GetInferenceStatus handles GET /api/v2/system/inference. It returns a
// read-only snapshot of the inference subsystem: hardware, backends, loaded
// models with per-model stats and memory, and source attachment. The snapshot
// is assembled from live sources on every request so it reflects hot-reload
// changes without any caching.
func (c *Controller) GetInferenceStatus(ctx echo.Context) error {
	settings := c.currentSettings()

	resp := InferenceStatusResponse{
		SnapshotAtUnix: time.Now().Unix(),
	}

	// Hardware.
	envType, _ := sysinfo.GetEnvironment()
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
	var rss map[string]int64
	primaryID := ""
	if c.Processor != nil {
		if bn := c.Processor.GetBirdNET(); bn != nil {
			rss, resp.RuntimeBaselineBytes = bn.ModelRSS()
			primaryID = bn.PrimaryModelID()
		}
	}
	counters := classifier.GetInferenceCounters().PeekAll()
	attachments := buildSourceAttachments(settings, infos, primaryID)

	resp.Models = make([]InferenceModelStatus, 0, len(infos))
	for i := range infos {
		resp.Models = append(resp.Models, buildModelStatus(&infos[i], counters[infos[i].ID], rss, attachments[infos[i].ID]))
	}

	return ctx.JSON(http.StatusOK, resp)
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
		target := ""
		for _, cm := range configModels {
			if regID, ok := classifier.ResolveConfigModelID(cm); ok && loaded[regID] {
				target = regID
				break
			}
		}
		if target != "" {
			out[target] = append(out[target], ModelSourceInfo{ID: name, Name: name, Type: sourceType, Fallback: false})
			return
		}
		if primaryID != "" {
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

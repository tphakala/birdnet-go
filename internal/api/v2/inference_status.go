// internal/api/v2/inference_status.go
package api

import (
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
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

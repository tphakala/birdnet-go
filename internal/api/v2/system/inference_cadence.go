// internal/api/v2/system/inference_cadence.go
package system

import (
	"cmp"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// AnalysisCadenceInfo describes the planned analysis cadence. The configured
// overlap (birdnet.overlap) is never modified; the effective overlap is what the
// analysis buffers use, which the planner may hold below the configured one when
// the false positive filter is on and the hardware cannot sustain it.
type AnalysisCadenceInfo struct {
	// Status is "ok", "capped", "overloaded" or "filterOff" (the false positive
	// filter is disabled, so the configured overlap is used as-is).
	Status string `json:"status"`
	// ConfiguredOverlapSec is birdnet.overlap in seconds on the 3 s base clip.
	ConfiguredOverlapSec float64 `json:"configuredOverlapSec"`
	// EffectiveOverlapSec is the overlap in use, in seconds on the 3 s base clip.
	EffectiveOverlapSec float64 `json:"effectiveOverlapSec"`
	// MinBaseStepMs is the smallest base analysis step the hardware sustains, in
	// milliseconds; 0 means unknown or not applicable.
	MinBaseStepMs int64 `json:"minBaseStepMs"`
	// EstimatedDutyConfigured is the estimated inference duty at the configured overlap.
	EstimatedDutyConfigured float64 `json:"estimatedDutyConfigured"`
	// EstimatedDutyEffective is the estimated inference duty at the effective overlap.
	EstimatedDutyEffective float64 `json:"estimatedDutyEffective"`
	// DutyCeiling is the duty the planner keeps the load under.
	DutyCeiling float64 `json:"dutyCeiling"`
	// SourceCount is the number of audio sources in the plan.
	SourceCount int `json:"sourceCount"`
	// ModelCount is the number of models in the plan.
	ModelCount int `json:"modelCount"`
	// UnknownLatencyModels lists models whose latency could not be measured and
	// so are not counted in the duty estimate. Never null.
	UnknownLatencyModels []string `json:"unknownLatencyModels"`
	// Models lists the per-model cadence of the loaded models.
	Models []CadenceModelInfo `json:"models"`
}

// CadenceModelInfo is one model's cadence under the effective overlap.
type CadenceModelInfo struct {
	// ID is the model registry ID.
	ID string `json:"id"`
	// Name is the model display name.
	Name string `json:"name"`
	// ClipMs is the model clip length in milliseconds.
	ClipMs int64 `json:"clipMs"`
	// StepMs is how often a new analysis window is produced, in milliseconds.
	StepMs int64 `json:"stepMs"`
	// ProbeLatencyMs is the measured inference latency in milliseconds; omitted
	// when it could not be measured or rounds down to 0 ms.
	ProbeLatencyMs int64 `json:"probeLatencyMs,omitempty"`
	// Confirmations is how many analysis windows within the reference window must
	// agree for the false positive filter to accept a detection.
	Confirmations int `json:"confirmations"`
	// WindowsInReference is how many analysis windows fit in the reference window.
	WindowsInReference float64 `json:"windowsInReference"`
}

// buildAnalysisCadence converts the published plan and the loaded models into the
// API shape. It returns nil when no plan has been published. Steps and
// confirmation counts are computed at the plan's effective overlap, so they match
// what the buffers deliver and what the filter requires.
func buildAnalysisCadence(plan *cadence.Plan, infos []classifier.ModelInfo, settings *conf.Settings) *AnalysisCadenceInfo {
	if plan == nil {
		return nil
	}
	latency := make(map[string]time.Duration, len(plan.Models))
	for _, m := range plan.Models {
		latency[m.ModelID] = m.Latency
	}
	out := &AnalysisCadenceInfo{
		Status:                  string(plan.Status),
		ConfiguredOverlapSec:    plan.ConfiguredBaseOverlap.Seconds(),
		EffectiveOverlapSec:     plan.EffectiveBaseOverlap.Seconds(),
		MinBaseStepMs:           plan.MinBaseStep.Milliseconds(),
		EstimatedDutyConfigured: plan.DutyAtConfigured,
		EstimatedDutyEffective:  plan.DutyAtEffective,
		DutyCeiling:             plan.DutyCeiling,
		SourceCount:             plan.SourceCount,
		ModelCount:              plan.ModelCount,
		UnknownLatencyModels:    slices.Clone(plan.UnknownLatencyModels),
		Models:                  make([]CadenceModelInfo, 0, len(infos)),
	}
	if out.UnknownLatencyModels == nil {
		out.UnknownLatencyModels = []string{}
	}
	for i := range infos {
		mi := &infos[i]
		step := mi.Spec.BufferInterval(classifier.ResolveModelOverlap(mi.ID, mi.Spec, plan.EffectiveBaseOverlap))
		windows := 0.0
		if step > 0 {
			windows = processor.ReferenceWindowSeconds / step.Seconds()
		}
		out.Models = append(out.Models, CadenceModelInfo{
			ID:                 mi.ID,
			Name:               mi.Name,
			ClipMs:             mi.Spec.ClipLength.Milliseconds(),
			StepMs:             step.Milliseconds(),
			ProbeLatencyMs:     latency[mi.ID].Milliseconds(),
			Confirmations:      processor.MinDetectionsForModel(settings, mi.ID, plan.EffectiveBaseOverlap),
			WindowsInReference: windows,
		})
	}
	slices.SortFunc(out.Models, func(a, b CadenceModelInfo) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

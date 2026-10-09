package analysis

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// cadenceDutyCeiling is the highest inference duty the cadence planner allows.
	// The remaining quarter covers capture, resampling, spectrograms, the UI and the
	// gap between the probed median latency and runtime p95.
	cadenceDutyCeiling = 0.75
	// cadenceGrid is the granularity the planned analysis step is snapped up to.
	cadenceGrid = 100 * time.Millisecond
)

// cadenceAction is what the pipeline does with a freshly solved cadence plan.
type cadenceAction int

const (
	// cadencePublish publishes the plan; the buffers already carry its step.
	cadencePublish cadenceAction = iota
	// cadenceRestart requests a full capture restart, which re-plans and
	// reallocates every analysis buffer with the new step.
	cadenceRestart
)

// String returns a log-friendly name for the action.
func (a cadenceAction) String() string {
	if a == cadenceRestart {
		return "restart"
	}
	return "publish"
}

// buildCadencePairs lists the (source, model) analysis buffers the pipeline
// allocates for the given source configs, resolving models exactly as
// registerConsumersForSources does (explicit list, else default-target fallback).
// Pairs come from the desired configs, not from AddSource successes, so a
// persistently failing source cannot make the plan differ between planning sites.
func buildCadencePairs(configs []sourceConfigWithModels, loaded map[string]classifier.ModelInfo, defaults []classifier.ModelInfo) []cadence.Pair {
	var pairs []cadence.Pair
	for _, scm := range configs {
		targets, _ := matchModelTargets(scm.modelIDs, loaded, nil)
		if len(targets) == 0 {
			targets = fallbackTargets(scm.modelIDs, defaults)
		}
		seen := make(map[string]bool, len(targets))
		for i := range targets {
			info := &targets[i]
			if seen[info.ID] {
				continue // one buffer per (source, model), as AllocateAnalysis allocates
			}
			seen[info.ID] = true
			pair := cadence.Pair{
				SourceKey: scm.config.ConnectionString,
				ModelID:   info.ID,
				Clip:      info.Spec.ClipLength,
			}
			if info.ID == classifier.RegistryIDBat {
				pair.FixedStep = info.Spec.ClipLength / 2
			}
			pairs = append(pairs, pair)
		}
	}
	return pairs
}

// planCadence solves the cadence for the given pairs and probed latencies. The
// cap applies only while the false positive filter is on.
func planCadence(settings *conf.Settings, pairs []cadence.Pair, latencies map[string]time.Duration) cadence.Plan {
	return cadence.Solve(cadence.Input{
		FilterActive:          settings.Realtime.FalsePositiveFilter.Level >= cadence.FilterActiveMinLevel,
		ConfiguredBaseOverlap: classifier.ConfiguredBaseOverlap(settings),
		BaseClip:              classifier.AnalysisBaseClipLength,
		Grid:                  cadenceGrid,
		DutyCeiling:           cadenceDutyCeiling,
		Pairs:                 pairs,
		Latency:               latencies,
	})
}

// decideCadence compares the published plan with a candidate for an incremental
// path (reconfigure, single-source restart). Buffers keep the step of the plan
// that was published when they were allocated, so a candidate with a different
// effective overlap needs a full restart, which also re-plans. It does not rely on
// the overlap-change restart signal: that signal is dropped when the restart
// channel is full, and treating the plan as unchanged would then pin stale
// buffers.
func decideCadence(published, candidate *cadence.Plan) cadenceAction {
	if published == nil {
		if candidate.Capped() {
			return cadenceRestart
		}
		return cadencePublish
	}
	if !cadence.SameCadence(published, candidate) {
		return cadenceRestart
	}
	return cadencePublish
}

// drainRestartSignals consumes any further restart tokens already queued on ch
// and returns how many it removed, so several queued restarts run as one.
func drainRestartSignals(ch <-chan struct{}) int {
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			return drained
		}
	}
}

// planCadenceForConfigs solves the cadence for the desired source configs against
// the loaded models. It returns false when no classifier is attached.
func (p *AudioPipelineService) planCadenceForConfigs(configs []sourceConfigWithModels) (cadence.Plan, bool) {
	if p.bnAnalyzer == nil || p.bnAnalyzer.BirdNET() == nil {
		return cadence.Plan{}, false
	}
	bn := p.bnAnalyzer.BirdNET()
	infos := bn.ModelInfos()
	loaded := make(map[string]classifier.ModelInfo, len(infos))
	for i := range infos {
		loaded[infos[i].ID] = infos[i]
	}
	pairs := buildCadencePairs(configs, loaded, bn.DefaultTargets())
	return planCadence(conf.Setting(), pairs, bn.ProbedLatencies()), true
}

// publishCadencePlan publishes the plan on the orchestrator, logs it, and tells
// open UIs to refetch when the plan changed.
func (p *AudioPipelineService) publishCadencePlan(plan *cadence.Plan, operation string) {
	bn := p.bnAnalyzer.BirdNET()
	prev := bn.CadencePlan()
	bn.SetCadencePlan(plan)

	audiocore.GetLogger().Info("analysis cadence planned",
		logger.Float64("configured_overlap", plan.ConfiguredBaseOverlap.Seconds()),
		logger.Float64("effective_overlap", plan.EffectiveBaseOverlap.Seconds()),
		logger.String("status", string(plan.Status)),
		logger.Float64("duty_configured", plan.DutyAtConfigured),
		logger.Float64("duty_effective", plan.DutyAtEffective),
		logger.Int("source_count", plan.SourceCount),
		logger.Int("model_count", plan.ModelCount),
		logger.Any("unknown_latency_models", plan.UnknownLatencyModels),
		logger.String("operation", operation))

	if prev != nil && prev.EffectiveBaseOverlap == plan.EffectiveBaseOverlap && prev.Status == plan.Status {
		return
	}
	if p.apiService != nil {
		if ctrl := p.apiService.APIController(); ctrl != nil {
			ctrl.BroadcastInferenceTopologyChanged()
		}
	}
}

// planAndPublishCadence plans for the given configs and publishes the result
// unconditionally. Used by full rebuilds, which allocate every buffer afresh.
func (p *AudioPipelineService) planAndPublishCadence(configs []sourceConfigWithModels, operation string) {
	plan, ok := p.planCadenceForConfigs(configs)
	if !ok {
		return
	}
	p.publishCadencePlan(&plan, operation)
}

// applyCadenceDecision plans for the given configs and applies decideCadence for
// an incremental path. It returns true when a full restart was queued and the
// caller must stop its incremental work; when the restart could not be queued it
// returns false so the caller keeps its incremental path and the next reconfigure
// detects the stale plan again.
func (p *AudioPipelineService) applyCadenceDecision(configs []sourceConfigWithModels, operation string) bool {
	plan, ok := p.planCadenceForConfigs(configs)
	if !ok {
		return false
	}
	action := decideCadence(p.bnAnalyzer.BirdNET().CadencePlan(), &plan)
	if action == cadenceRestart {
		audiocore.GetLogger().Info("analysis cadence changed, requesting capture restart",
			logger.Float64("effective_overlap", plan.EffectiveBaseOverlap.Seconds()),
			logger.String("status", string(plan.Status)),
			logger.String("operation", operation))
		return p.requestCaptureRestart()
	}
	p.publishCadencePlan(&plan, operation)
	return false
}

// requestCaptureRestart queues a full capture restart without blocking and
// reports whether the request was queued.
func (p *AudioPipelineService) requestCaptureRestart() bool {
	ResetOverrunTrackers()
	select {
	case p.restartChan <- struct{}{}:
		return true
	default:
		audiocore.GetLogger().Warn("restart channel full, could not signal capture restart for the cadence plan")
		return false
	}
}

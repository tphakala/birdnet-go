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

// buildCadencePairs lists the (source, model) analysis buffers the pipeline
// allocates for the given source configs, resolving models exactly as
// registerConsumersForSources does (explicit list, else default-target fallback).
// Pairs come from the desired configs, not from AddSource successes, so a
// persistently failing source cannot make the plan differ between planning sites.
// Each pair's step uses the buffers' own step math, so a fixed-cadence model
// (bat) keeps its fixed step whatever the base overlap.
func buildCadencePairs(configs []sourceConfigWithModels, loaded map[string]classifier.ModelInfo, defaults []classifier.ModelInfo) []cadence.Pair {
	var pairs []cadence.Pair
	for _, scm := range configs {
		targets, _ := matchModelTargets(scm.modelIDs, loaded, nil)
		if len(targets) == 0 {
			targets = fallbackTargets(scm.modelIDs, defaults)
		}
		seen := make(map[string]bool, len(targets))
		for i := range targets {
			id, spec := targets[i].ID, targets[i].Spec
			if seen[id] {
				continue // one buffer per (source, model), as AllocateAnalysis allocates
			}
			seen[id] = true
			pairs = append(pairs, cadence.Pair{
				SourceKey: scm.config.ConnectionString,
				ModelID:   id,
				StepAt: func(base time.Duration) time.Duration {
					return spec.BufferInterval(classifier.ResolveModelOverlap(id, spec, base))
				},
			})
		}
	}
	return pairs
}

// planCadence solves the cadence for the given pairs and probed latencies. The
// cap applies only while the false positive filter is on. Nil settings (settings
// that could not be loaded) plan as empty settings: filter off, no configured
// overlap.
func planCadence(settings *conf.Settings, pairs []cadence.Pair, latencies map[string]time.Duration) cadence.Plan {
	if settings == nil {
		settings = &conf.Settings{}
	}
	return cadence.Solve(cadence.Input{
		FilterActive:          cadence.FilterActive(settings.Realtime.FalsePositiveFilter.Level),
		ConfiguredBaseOverlap: classifier.ConfiguredBaseOverlap(settings),
		BaseClip:              classifier.AnalysisBaseClipLength,
		Grid:                  cadenceGrid,
		DutyCeiling:           cadenceDutyCeiling,
		Pairs:                 pairs,
		Latency:               latencies,
	})
}

// needsRestart reports whether an incremental path (reconfigure, single-source
// restart) must request a full capture restart instead of publishing the
// candidate. Buffers keep the step of the plan that was published when they were
// allocated, so a candidate with a different effective overlap needs a full
// restart, which also re-plans. With nothing published yet the buffers use the
// configured overlap, so only a candidate below the configured overlap (capped
// or overloaded) needs one. It does not rely on
// the overlap-change restart signal: that signal is dropped when the restart
// channel is full, and treating the plan as unchanged would then pin stale
// buffers.
func needsRestart(published, candidate *cadence.Plan) bool {
	if published == nil {
		return candidate.EffectiveBaseOverlap < candidate.ConfiguredBaseOverlap
	}
	return !cadence.SameCadence(published, candidate)
}

// drainRestartSignals consumes any further restart tokens already queued on ch,
// so several queued restarts run as one.
func drainRestartSignals(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// loadedModelMap returns the backend's loaded models keyed by registry ID.
func loadedModelMap(bn classifierBackend) map[string]classifier.ModelInfo {
	infos := bn.ModelInfos()
	loaded := make(map[string]classifier.ModelInfo, len(infos))
	for i := range infos {
		loaded[infos[i].ID] = infos[i]
	}
	return loaded
}

// birdNET returns the attached classifier, or nil when none is attached.
func (p *AudioPipelineService) birdNET() *classifier.Orchestrator {
	if p.bnAnalyzer == nil {
		return nil
	}
	return p.bnAnalyzer.BirdNET()
}

// planCadenceForConfigs solves the cadence for the desired source configs against
// the backend's loaded models and default targets.
func planCadenceForConfigs(bn *classifier.Orchestrator, configs []sourceConfigWithModels, loaded map[string]classifier.ModelInfo, defaults []classifier.ModelInfo) cadence.Plan {
	return planCadence(conf.Setting(), buildCadencePairs(configs, loaded, defaults), bn.ProbedLatencies())
}

// publishCadencePlan publishes the plan on the orchestrator, logs it, and tells
// open UIs to refetch when the plan's outcome or its configured overlap changed;
// a change in the duty estimate alone does not. The configured overlap counts
// because an overlap save re-plans through restart_audio_capture, which
// broadcasts nothing itself, and on a capped device the outcome can stay the
// same. Source and model count changes need no check here: the reconfigure
// handlers that change them broadcast after re-planning. A plan identical to the
// published one is left in place and logged at debug level only.
func (p *AudioPipelineService) publishCadencePlan(bn *classifier.Orchestrator, plan *cadence.Plan, operation string) {
	log := audiocore.GetLogger()
	prev := bn.CadencePlan()
	if cadence.Equal(prev, plan) {
		log.Debug("analysis cadence unchanged",
			logger.Float64("effective_overlap", plan.EffectiveBaseOverlap.Seconds()),
			logger.String("status", string(plan.Status)),
			logger.String("operation", operation))
		return
	}
	bn.SetCadencePlan(plan)
	p.cadenceNotice.observe(plan)

	log.Info("analysis cadence planned",
		logger.Float64("configured_overlap", plan.ConfiguredBaseOverlap.Seconds()),
		logger.Float64("effective_overlap", plan.EffectiveBaseOverlap.Seconds()),
		logger.String("status", string(plan.Status)),
		logger.Float64("duty_configured", plan.DutyAtConfigured),
		logger.Float64("duty_effective", plan.DutyAtEffective),
		logger.Int("source_count", plan.SourceCount),
		logger.Int("model_count", plan.ModelCount),
		logger.Any("unknown_latency_models", plan.UnknownLatencyModels),
		logger.String("operation", operation))

	if !cadencePlanNeedsBroadcast(prev, plan) {
		return
	}
	if p.apiService != nil {
		if ctrl := p.apiService.APIController(); ctrl != nil {
			ctrl.BroadcastInferenceTopologyChanged()
		}
	}
}

// cadencePlanNeedsBroadcast reports whether open UIs must refetch after plan
// replaces prev: the first plan, or the outcome or the configured overlap
// changed. plan is non-nil.
func cadencePlanNeedsBroadcast(prev, plan *cadence.Plan) bool {
	if prev == nil {
		return true
	}
	return !cadence.SameOutcome(prev, plan) || prev.ConfiguredBaseOverlap != plan.ConfiguredBaseOverlap
}

// planAndPublishCadence plans for the given configs and publishes the result
// unconditionally. Used by full rebuilds, which allocate every buffer afresh.
func (p *AudioPipelineService) planAndPublishCadence(configs []sourceConfigWithModels, operation string) {
	bn := p.birdNET()
	if bn == nil {
		return
	}
	plan := planCadenceForConfigs(bn, configs, loadedModelMap(bn), bn.DefaultTargets())
	p.publishCadencePlan(bn, &plan, operation)
}

// applyCadenceDecision plans for the given configs and applies needsRestart for
// an incremental path. loaded and defaults are bn's loaded models and default
// targets, which the caller already holds. When the effective overlap changes it
// queues a full restart and leaves the published plan as it is; otherwise it
// publishes the new plan. Either way the caller finishes its incremental work
// under the published plan, so the buffers it allocates match the step of the
// others, and per-source work the full restart does not repeat (Home Assistant
// cleanup for deleted or renamed streams, the restarted source's probed
// parameters) is not lost. The queued restart then rebuilds every buffer at the
// new step.
func (p *AudioPipelineService) applyCadenceDecision(bn *classifier.Orchestrator, configs []sourceConfigWithModels, loaded map[string]classifier.ModelInfo, defaults []classifier.ModelInfo, operation string) {
	plan := planCadenceForConfigs(bn, configs, loaded, defaults)
	if needsRestart(bn.CadencePlan(), &plan) {
		audiocore.GetLogger().Info("analysis cadence changed, requesting capture restart",
			logger.Float64("effective_overlap", plan.EffectiveBaseOverlap.Seconds()),
			logger.String("status", string(plan.Status)),
			logger.String("operation", operation))
		p.requestCaptureRestart()
		return
	}
	p.publishCadencePlan(bn, &plan, operation)
}

// replanCadence re-plans the analysis cadence for the configured sources without
// probing streams or diffing sources. A BirdNET reload re-probes model latency
// but leaves the sources as they are, so it needs only the plan; a full source
// reconfigure would contact every stream and could restart a kept one. It
// restarts capture only when the effective overlap changes.
func (p *AudioPipelineService) replanCadence() {
	bn := p.birdNET()
	if bn == nil {
		return
	}
	p.sourcesMu.Lock()
	defer p.sourcesMu.Unlock()
	configs := p.buildSourceConfigs(nil, false)
	p.applyCadenceDecision(bn, configs, loadedModelMap(bn), bn.DefaultTargets(), operationReplanCadence)
}

// requestCaptureRestart queues a full capture restart without blocking. A full
// channel already holds a pending restart, which re-plans when it runs, so a
// dropped token is only logged.
func (p *AudioPipelineService) requestCaptureRestart() {
	ResetOverrunTrackers()
	if !trySignalCaptureRestart(p.restartChan) {
		audiocore.GetLogger().Warn("restart channel full, could not signal capture restart for the cadence plan")
	}
}

// trySignalCaptureRestart queues one capture restart token on ch without
// blocking and reports whether it was queued.
func trySignalCaptureRestart(ch chan<- struct{}) bool {
	select {
	case ch <- struct{}{}:
		return true
	default:
		return false
	}
}

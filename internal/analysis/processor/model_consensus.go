package processor

import (
	"maps"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// reasonModelConsensus is the discard reason of the model consensus filter. It is
// fixed text because discard reasons are aggregated by their exact string.
const reasonModelConsensus = "insufficient model consensus"

// modelConsensusDiscardOperation tags the line logged for each consensus discard.
const modelConsensusDiscardOperation = "model_consensus_discard"

// consensusModels is what the filter reads about the models for one flush tick.
// Production fills it from the orchestrator snapshot and the model registry; tests
// inject their own so models can be grouped without touching the registry.
type consensusModels struct {
	knows   func(modelID, key string) bool             // reports whether the model can classify the species with this canonical key
	groupOf func(modelID string) classifier.TaxonGroup // taxon group of the model, empty when unknown
	clipOf  func(modelID string) (time.Duration, bool) // audio length one window of the model scores, false when unknown

	// registryIDOf resolves a config alias from a rule's models list to a registry model ID.
	registryIDOf func(alias string) (string, bool)
}

// consensusRule is one compiled conf.ModelConsensusRule. Whether it names models is
// decided by the configured list, never by the resolved set: a rule whose aliases all
// fail to resolve stays a rule that names models and is inert, and must not turn into
// a rule for any model.
type consensusRule struct {
	index       int                 // 0-based position in the configured rules, for logs
	species     []string            // as configured; empty covers every species
	models      []string            // as configured; empty means any model
	listed      map[string]struct{} // registry IDs of the models entries that resolve
	minModels   int                 // any-model rules only
	allEligible bool                // any-model rules only
}

// namesModels reports whether the rule lists models, as configured.
func (r *consensusRule) namesModels() bool { return len(r.models) > 0 }

// compileConsensusRules resolves each rule's model aliases once per tick.
func compileConsensusRules(rules []conf.ModelConsensusRule, registryIDOf func(string) (string, bool)) []consensusRule {
	compiled := make([]consensusRule, len(rules))
	for i := range rules {
		r := &rules[i]
		c := consensusRule{
			index: i, species: r.Species, models: r.Models,
			minModels: r.MinModels, allEligible: r.AllEligible,
		}
		if len(r.Models) > 0 {
			c.listed = make(map[string]struct{}, len(r.Models))
			for _, alias := range r.Models {
				if id, ok := registryIDOf(alias); ok {
					c.listed[id] = struct{}{}
				}
			}
		}
		compiled[i] = c
	}
	return compiled
}

// scoredRunKey identifies the windows one model scored on one audio source.
type scoredRunKey struct{ source, model string }

// scoredRun is the audio, by capture time, that one model has scored on one source
// without a gap since its last Gap window. from is the end of the run's first
// window (conservative: nothing before it is claimed). It is kept apart from the
// privacy coverage store on purpose: that store is published only while the privacy
// filter is on and only for speech-band models, while this one is recorded for
// every analysed window so the consensus filter needs no warm-up.
type scoredRun struct{ from, to time.Time }

// covers reports whether the run spans all of [from, to].
func (r scoredRun) covers(from, to time.Time) bool {
	return spanContains(audioSpan{from: r.from, to: r.to}, audioSpan{from: from, to: to})
}

// consensusTick is the immutable view of the models for one flush tick.
type consensusTick struct {
	models consensusModels
	runs   map[scoredRunKey]scoredRun // copy of the scored runs taken at the start of the tick
	rules  []consensusRule            // the settings' rules compiled for this tick
}

// consensusVerdict is the outcome of one consensus evaluation.
type consensusVerdict struct {
	discard            bool
	agreeing, eligible []string       // sorted model IDs, for the log
	required           int            // models an any-model rule needed, when it failed
	rule               *consensusRule // the first failing rule in config order, nil when none failed
	outside            []string       // eligible models outside a failing rule's list, sorted
}

// recordScoredRun extends or restarts the scored run of the window's model on its
// source. It runs for every analysed window, whether or not the filter is on, so
// switching the filter on needs no warm-up. A window without capture timing
// records nothing, which leaves detections of that source unfiltered. Call it
// after the window's hits are merged into the pending map, so a run never claims
// audio whose hits are still in flight.
func (p *Processor) recordScoredRun(item *classifier.Results) {
	end := item.Audio.EndAt
	if end.IsZero() {
		return
	}
	key := scoredRunKey{source: item.Source.ID, model: item.ModelID}

	p.scoredRunsMu.Lock()
	defer p.scoredRunsMu.Unlock()
	run, exists := p.scoredRuns[key]
	if !exists || item.Audio.Gap {
		if p.scoredRuns == nil {
			p.scoredRuns = make(map[scoredRunKey]scoredRun)
		}
		p.scoredRuns[key] = scoredRun{from: end, to: end}
		return
	}
	if end.After(run.to) {
		run.to = end
		p.scoredRuns[key] = run
	}
}

// prepareModelConsensus builds the model view for one flush tick, or returns nil
// when the filter is off or no model view exists (the check is then skipped).
// It MUST run before pendingMutex is taken: the first call after a model topology
// change rebuilds the orchestrator snapshot and can wait on the orchestrator lock
// for the length of a concurrent model load and on one inference. Only the flusher
// waits, and only with the filter on.
func (p *Processor) prepareModelConsensus(settings *conf.Settings) *consensusTick {
	if !settings.Realtime.ModelConsensus.Enabled() {
		return nil
	}
	var models consensusModels
	switch {
	case p.consensusModelsFn != nil:
		models = p.consensusModelsFn()
	case p.Bn != nil:
		models = consensusModels{
			knows:   p.Bn.ModelSpecies().Knows,
			groupOf: classifier.TaxonGroupFor,
			clipOf:  registryClipLength,
		}
		models.registryIDOf = classifier.ResolveConfigModelID
	default:
		return nil
	}

	p.scoredRunsMu.Lock()
	runs := make(map[scoredRunKey]scoredRun, len(p.scoredRuns))
	maps.Copy(runs, p.scoredRuns)
	p.scoredRunsMu.Unlock()
	return &consensusTick{
		models: models,
		runs:   runs,
		rules:  compileConsensusRules(settings.Realtime.ModelConsensus.Rules, models.registryIDOf),
	}
}

// registryClipLength is the audio length one window of the registered model scores.
func registryClipLength(modelID string) (time.Duration, bool) {
	spec, ok := classifier.GetModelSpec(modelID)
	return spec.ClipLength, ok && spec.ClipLength > 0
}

// ruleCoversSpecies reports whether a rule's species list covers the detection's
// species: an empty list covers every species. isSpeciesExcluded is a generic list
// matcher by common or scientific name.
func ruleCoversSpecies(item *PendingDetection, species []string) bool {
	if len(species) == 0 {
		return true
	}
	sp := &item.Detection.Result.Species
	return isSpeciesExcluded(sp.CommonName, sp.ScientificName, species)
}

// consensusRequired is the number of agreeing models a detection needs: every
// eligible model, or minModels capped at the eligible count.
func consensusRequired(minModels int, allEligible bool, eligible int) int {
	if allEligible {
		return eligible
	}
	return min(minModels, eligible)
}

// evaluate decides whether the pending detection passes every rule that covers its
// species. An any-model rule needs enough agreeing models. A rule that names models
// applies only when every agreeing model is listed, and then needs a second model: it
// fails when an eligible model outside the list exists, and passes when none does.
// Agreeing models are the same-group models that hit it. Eligible models are those
// plus same-group models that know the species, pass the range filter for it and
// scored all the audio the agreeing models heard (from the start of the earliest
// hit window to the end of the latest one) without a gap. Missing evidence
// passes: an ungrouped best model, an empty species key, an agreeing model without
// a clip length, or an earliest contributor without a scored run. It reads no
// Processor state.
func (t *consensusTick) evaluate(item *PendingDetection, settings *conf.Settings) consensusVerdict {
	var covering []*consensusRule
	for i := range t.rules {
		if ruleCoversSpecies(item, t.rules[i].species) {
			covering = append(covering, &t.rules[i])
		}
	}
	if len(covering) == 0 {
		return consensusVerdict{}
	}

	group := t.models.groupOf(item.BestModelID)
	if group == "" {
		return consensusVerdict{}
	}
	sci := item.Detection.Result.Species.ScientificName
	key := speciesindex.CanonicalKey(sci)
	if key == "" {
		return consensusVerdict{}
	}

	var agreeing []string
	for id := range item.ModelContributions {
		if t.models.groupOf(id) == group {
			agreeing = append(agreeing, id)
		}
	}
	if len(agreeing) == 0 {
		return consensusVerdict{}
	}
	// Sorted, so that when two models tie on the first hit the smaller ID is the
	// earliest and the verdict does not depend on map order.
	slices.Sort(agreeing)
	// A candidate must have scored everything the agreeing models heard, from the
	// start of the earliest hit window of any of them to the end of the latest one.
	// Window lengths differ per model, so the start is the earliest of each model's
	// first hit minus its own clip length. A candidate that is still behind has not
	// had its chance to agree yet, so it is not eligible.
	var earliestModel string
	var earliestAt, latestAt, spanFrom time.Time
	for i, id := range agreeing {
		c := item.ModelContributions[id]
		clip, ok := t.models.clipOf(id)
		if !ok {
			return consensusVerdict{}
		}
		if i == 0 || c.FirstHitAt.Before(earliestAt) {
			earliestModel, earliestAt = id, c.FirstHitAt
		}
		if start := c.FirstHitAt.Add(-clip); i == 0 || start.Before(spanFrom) {
			spanFrom = start
		}
		if c.LastHitAt.After(latestAt) {
			latestAt = c.LastHitAt
		}
	}
	if _, timed := t.runs[scoredRunKey{source: item.Source, model: earliestModel}]; !timed {
		return consensusVerdict{}
	}
	spanTo := earliestAt
	if latestAt.After(spanTo) {
		spanTo = latestAt
	}

	eligible := slices.Clone(agreeing)
	for k, run := range t.runs {
		if k.source != item.Source || slices.Contains(eligible, k.model) {
			continue
		}
		if run.covers(spanFrom, spanTo) &&
			t.models.groupOf(k.model) == group &&
			t.models.knows(k.model, key) &&
			classifier.PassesRangeFilter(k.model, settings, sci) {
			eligible = append(eligible, k.model)
		}
	}
	slices.Sort(eligible)

	return checkRules(covering, agreeing, eligible)
}

// checkRules applies the covering rules in config order to the evidence and returns
// the verdict, carrying the first failing rule. The verdict does not depend on rule
// order beyond which failure is reported: agreeing and eligible are sorted, and
// outside is filtered from the sorted eligible.
func checkRules(covering []*consensusRule, agreeing, eligible []string) consensusVerdict {
	v := consensusVerdict{agreeing: agreeing, eligible: eligible}
	for _, r := range covering {
		if !r.namesModels() {
			required := consensusRequired(r.minModels, r.allEligible, len(eligible))
			if len(agreeing) < required {
				v.discard, v.rule, v.required = true, r, required
				return v
			}
			continue
		}
		if !allListed(agreeing, r.listed) {
			continue
		}
		var outside []string
		for _, id := range eligible {
			if _, in := r.listed[id]; !in {
				outside = append(outside, id)
			}
		}
		if len(outside) > 0 {
			v.discard, v.rule, v.outside = true, r, outside
			return v
		}
	}
	return v
}

// allListed reports whether every model ID is in the set.
func allListed(ids []string, set map[string]struct{}) bool {
	for _, id := range ids {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

// discardForModelConsensus reports whether the detection should be discarded for
// lacking model consensus, logging each discard.
func (p *Processor) discardForModelConsensus(item *PendingDetection, settings *conf.Settings, t *consensusTick) bool {
	cfg := &settings.Realtime.ModelConsensus
	v := t.evaluate(item, settings)
	if !v.discard {
		return false
	}
	fields := []logger.Field{
		logger.String("species", item.Detection.Result.Species.CommonName),
		logger.String("scientific_name", item.Detection.Result.Species.ScientificName),
		logger.String("source", p.getDisplayNameForSource(item.Source)),
		logger.String("best_model_id", item.BestModelID),
		logger.Any("agreeing_models", v.agreeing),
		logger.Any("eligible_models", v.eligible),
		logger.Int("rule_index", v.rule.index),
		logger.Any("rule_models", v.rule.models),
		logger.String("operation", modelConsensusDiscardOperation),
	}
	if v.rule.namesModels() {
		fields = append(fields, logger.Any("outside_models", v.outside))
	} else {
		fields = append(fields, logger.Int("required", v.required))
	}
	if cfg.Debug {
		GetLogger().Info("model consensus discarded detection", fields...)
	} else {
		GetLogger().Debug("Detection discarded by model consensus filter", fields...)
	}
	return true
}

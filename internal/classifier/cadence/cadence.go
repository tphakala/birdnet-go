// Package cadence plans the analysis cadence: the effective base overlap that
// keeps the serialized inference load of every (source, model) pair under a
// duty ceiling. The package is a pure leaf (stdlib only) so the solver can be
// tested without an orchestrator, and the resulting Plan can be read by any
// consumer without importing the pipeline.
//
// The configured overlap (birdnet.overlap) is never modified; the plan carries
// the effective value separately.
package cadence

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"time"
)

// Status describes how the planned cadence relates to the configured one.
type Status string

const (
	// StatusOK means the configured overlap fits under the duty ceiling.
	StatusOK Status = "ok"
	// StatusCapped means the overlap was lowered to fit under the duty ceiling.
	StatusCapped Status = "capped"
	// StatusOverloaded means the load exceeds the ceiling even at the lowest
	// overlap the solver may pick (zero overlap, or fixed load alone).
	StatusOverloaded Status = "overloaded"
	// StatusFilterOff means the false positive filter is disabled, so the
	// configured overlap is used as-is and no cap is applied.
	StatusFilterOff Status = "filterOff"
)

// FilterActiveMinLevel is the lowest false positive filter level at which the
// cadence cap applies; level 0 leaves the configured overlap untouched.
const FilterActiveMinLevel = 1

// epsilon absorbs floating point noise when snapping to the grid.
const epsilon = 1e-9

// minStep is the smallest step the duty model accepts, mirroring the
// one-millisecond floor that the classifier applies to analysis steps.
const minStep = time.Millisecond

// Pair is one (source, model) analysis buffer.
type Pair struct {
	// SourceKey identifies the audio source.
	SourceKey string
	// ModelID identifies the model whose latency is looked up in Input.Latency.
	ModelID string
	// Clip is the model clip length.
	Clip time.Duration
	// FixedStep is non-zero for pairs whose cadence does not follow the
	// configured overlap (bat); such pairs count as fixed load.
	FixedStep time.Duration
}

// Input is everything the solver depends on.
type Input struct {
	// FilterActive reports whether the false positive filter is enabled
	// (level >= 1). When false the configured overlap is used unchanged.
	FilterActive bool
	// ConfiguredBaseOverlap is birdnet.overlap on the base clip.
	ConfiguredBaseOverlap time.Duration
	// BaseClip is the clip length the base overlap is defined against.
	BaseClip time.Duration
	// Grid is the step granularity the effective step is snapped up to.
	Grid time.Duration
	// DutyCeiling is the highest acceptable inference duty (0..1).
	DutyCeiling float64
	// Pairs lists every allocated (source, model) buffer.
	Pairs []Pair
	// Latency maps model ID to probed inference latency; absent or non-positive
	// means unknown.
	Latency map[string]time.Duration
}

// ModelCost is the probed cost of one model in the plan.
type ModelCost struct {
	// ModelID identifies the model.
	ModelID string
	// Latency is the probed inference latency; zero when unknown.
	Latency time.Duration
	// Known reports whether a latency was available.
	Known bool
}

// Plan is the solver result.
type Plan struct {
	// ConfiguredBaseOverlap is the user's setting.
	ConfiguredBaseOverlap time.Duration
	// EffectiveBaseOverlap is the overlap the pipeline must use.
	EffectiveBaseOverlap time.Duration
	// MinBaseStep is the smallest base step meeting the ceiling; zero when
	// unknown or not applicable.
	MinBaseStep time.Duration
	// Status relates effective to configured.
	Status Status
	// DutyAtConfigured is the estimated duty at the configured overlap.
	DutyAtConfigured float64
	// DutyAtEffective is the estimated duty at the effective overlap.
	DutyAtEffective float64
	// DutyCeiling is the ceiling the plan was solved against.
	DutyCeiling float64
	// SourceCount is the number of distinct sources.
	SourceCount int
	// ModelCount is the number of distinct models.
	ModelCount int
	// UnknownLatencyModels lists models excluded from the duty sum, sorted.
	UnknownLatencyModels []string
	// Models lists the per-model costs, sorted by model ID.
	Models []ModelCost
}

// Capped reports whether the effective overlap is below the configured one.
func (p *Plan) Capped() bool {
	return p != nil && p.EffectiveBaseOverlap < p.ConfiguredBaseOverlap
}

// SameCadence reports whether two plans produce the same analysis step. It is
// nil-safe: two nil plans are equal, a nil and a non-nil plan are not.
func SameCadence(a, b *Plan) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.EffectiveBaseOverlap == b.EffectiveBaseOverlap
}

// StepFor returns the analysis step of a pair at a base overlap. It mirrors
// the classifier's overlap scaling: the overlap fraction is preserved across
// clip lengths, and the step never drops below one millisecond.
func StepFor(clip, fixedStep, baseOverlap, baseClip time.Duration) time.Duration {
	if fixedStep > 0 {
		return fixedStep
	}
	if baseClip <= 0 {
		return clip
	}
	overlap := time.Duration(math.Round(float64(baseOverlap) * float64(clip) / float64(baseClip)))
	overlap = min(max(overlap, 0), max(clip-minStep, 0))
	return clip - overlap
}

// Solve computes the plan. It is pure and deterministic: the result does not
// depend on the order of Pairs. A configured overlap whose duty already fits under
// the ceiling is never lowered.
func Solve(in Input) Plan {
	plan := Plan{
		ConfiguredBaseOverlap: in.ConfiguredBaseOverlap,
		EffectiveBaseOverlap:  in.ConfiguredBaseOverlap,
		DutyCeiling:           in.DutyCeiling,
		Status:                StatusOK,
	}
	sources := make(map[string]struct{})
	models := make(map[string]struct{})
	for _, p := range in.Pairs {
		sources[p.SourceKey] = struct{}{}
		models[p.ModelID] = struct{}{}
	}
	plan.SourceCount = len(sources)
	plan.ModelCount = len(models)
	plan.UnknownLatencyModels = []string{}
	for id := range models {
		lat := in.Latency[id]
		known := lat > 0
		plan.Models = append(plan.Models, ModelCost{ModelID: id, Latency: max(lat, 0), Known: known})
		if !known {
			plan.UnknownLatencyModels = append(plan.UnknownLatencyModels, id)
		}
	}
	sort.Slice(plan.Models, func(i, j int) bool { return plan.Models[i].ModelID < plan.Models[j].ModelID })
	slices.Sort(plan.UnknownLatencyModels)

	// k is the variable load in seconds of inference per base-step second;
	// f is the fixed duty of pairs whose cadence ignores the overlap.
	// Pairs are summed in a canonical order because float addition is not
	// associative; the plan must not depend on the caller's ordering.
	pairs := slices.Clone(in.Pairs)
	slices.SortFunc(pairs, func(a, b Pair) int {
		return cmp.Or(cmp.Compare(a.SourceKey, b.SourceKey), cmp.Compare(a.ModelID, b.ModelID))
	})
	var k, f float64
	for _, p := range pairs {
		lat := in.Latency[p.ModelID]
		if lat <= 0 {
			continue
		}
		if p.FixedStep > 0 {
			f += lat.Seconds() / p.FixedStep.Seconds()
		} else if p.Clip > 0 {
			k += lat.Seconds() * in.BaseClip.Seconds() / p.Clip.Seconds()
		}
	}
	duty := func(overlap time.Duration) float64 {
		step := max(in.BaseClip-overlap, minStep)
		return k/step.Seconds() + f
	}
	plan.DutyAtConfigured = duty(in.ConfiguredBaseOverlap)
	plan.DutyAtEffective = plan.DutyAtConfigured

	if !in.FilterActive {
		plan.Status = StatusFilterOff
		return plan
	}
	if in.DutyCeiling-f <= 0 {
		plan.EffectiveBaseOverlap = 0
		plan.DutyAtEffective = duty(0)
		plan.Status = StatusOverloaded
		return plan
	}
	if k <= 0 {
		return plan
	}

	sMin := k / (in.DutyCeiling - f)
	grid := in.Grid.Seconds()
	if grid > 0 {
		sMin = math.Ceil(sMin/grid-epsilon) * grid
	}
	plan.MinBaseStep = time.Duration(math.Round(sMin * float64(time.Second)))
	if plan.DutyAtConfigured <= in.DutyCeiling+epsilon {
		// The configured overlap already fits; snapping the minimum step up to the
		// grid must not cut an overlap the hardware sustains. MinBaseStep stays set
		// so the readout still shows the smallest step the hardware sustains.
		return plan
	}
	ovMax := max(in.BaseClip-plan.MinBaseStep, 0)
	plan.EffectiveBaseOverlap = min(in.ConfiguredBaseOverlap, ovMax)
	plan.DutyAtEffective = duty(plan.EffectiveBaseOverlap)

	switch {
	case plan.DutyAtEffective > in.DutyCeiling+epsilon:
		plan.Status = StatusOverloaded
	case plan.EffectiveBaseOverlap < in.ConfiguredBaseOverlap:
		plan.Status = StatusCapped
	}
	return plan
}

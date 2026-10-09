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
	"reflect"
	"slices"
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

// filterActiveMinLevel is the lowest false positive filter level at which the
// cadence cap applies; level 0 leaves the configured overlap untouched.
const filterActiveMinLevel = 1

// FilterActive reports whether a false positive filter level enables the
// cadence cap.
func FilterActive(level int) bool {
	return level >= filterActiveMinLevel
}

// epsilon absorbs floating point noise in duty comparisons.
const epsilon = 1e-9

// Pair is one (source, model) analysis buffer.
type Pair struct {
	// SourceKey identifies the audio source.
	SourceKey string
	// ModelID identifies the model whose latency is looked up in Input.Latency.
	ModelID string
	// StepAt returns the buffer's analysis step at a base overlap. The caller
	// supplies the same step math the buffers use, so the duty estimate matches
	// what they deliver; a pair whose cadence ignores the base overlap (bat)
	// returns a constant step and counts as fixed load. It must return a
	// positive step that does not grow as the base overlap grows.
	StepAt func(baseOverlap time.Duration) time.Duration
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
	// Grid is the step granularity the effective step is snapped up to. A
	// non-positive grid is treated as one millisecond.
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
}

// Plan is the solver result.
type Plan struct {
	// ConfiguredBaseOverlap is the user's setting.
	ConfiguredBaseOverlap time.Duration
	// EffectiveBaseOverlap is the overlap the pipeline must use.
	EffectiveBaseOverlap time.Duration
	// MinBaseStep is the smallest grid base step meeting the ceiling, capped at
	// BaseClip (which need not be a grid multiple); zero when
	// unknown or not applicable, including when no load depends on the overlap
	// and when even zero overlap exceeds the ceiling.
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

// SameCadence reports whether two plans give the analysis buffers the same step,
// which depends only on the effective overlap. Buffers keep the step they were
// allocated with, so this is the rule for whether a new plan needs a capture
// restart. It is nil-safe: two nil plans are equal, a nil and a non-nil plan are
// not.
func SameCadence(a, b *Plan) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.EffectiveBaseOverlap == b.EffectiveBaseOverlap
}

// SameOutcome reports whether two plans have the same cadence and the same
// status. It is the rule for whether open UIs must refetch: a status change
// (for example ok to overloaded) is news even when the step is unchanged, while
// duty estimates drift with every probe and are not worth a broadcast.
func SameOutcome(a, b *Plan) bool {
	return SameCadence(a, b) && (a == nil || a.Status == b.Status)
}

// Equal reports whether two plans are identical in every field. It is nil-safe.
func Equal(a, b *Plan) bool {
	return reflect.DeepEqual(a, b)
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
		plan.Models = append(plan.Models, ModelCost{ModelID: id, Latency: max(lat, 0)})
		if lat <= 0 {
			plan.UnknownLatencyModels = append(plan.UnknownLatencyModels, id)
		}
	}
	slices.SortFunc(plan.Models, func(a, b ModelCost) int { return cmp.Compare(a.ModelID, b.ModelID) })
	slices.Sort(plan.UnknownLatencyModels)

	// Pairs with a known latency are summed in a canonical order because float
	// addition is not associative; the plan must not depend on the caller's
	// ordering.
	pairs := make([]Pair, 0, len(in.Pairs))
	for _, p := range in.Pairs {
		if in.Latency[p.ModelID] > 0 {
			pairs = append(pairs, p)
		}
	}
	slices.SortFunc(pairs, func(a, b Pair) int {
		return cmp.Or(cmp.Compare(a.SourceKey, b.SourceKey), cmp.Compare(a.ModelID, b.ModelID))
	})
	// duty is the inference load in seconds of inference per second of audio.
	duty := func(overlap time.Duration) float64 {
		var d float64
		for _, p := range pairs {
			d += in.Latency[p.ModelID].Seconds() / p.StepAt(overlap).Seconds()
		}
		return d
	}
	plan.DutyAtConfigured = duty(in.ConfiguredBaseOverlap)
	plan.DutyAtEffective = plan.DutyAtConfigured

	if !in.FilterActive {
		plan.Status = StatusFilterOff
		return plan
	}

	grid := in.Grid
	if grid <= 0 {
		grid = time.Millisecond
	}
	// Steps are grid multiples up to the first one that reaches the base clip,
	// where the overlap is zero. The duty does not rise as the step grows, so the
	// smallest step meeting the ceiling is found by binary search.
	maxSteps := int((in.BaseClip + grid - 1) / grid)
	overlapAt := func(n int) time.Duration {
		return max(in.BaseClip-time.Duration(n)*grid, 0)
	}
	fits := func(n int) bool { return duty(overlapAt(n)) <= in.DutyCeiling+epsilon }
	if maxSteps < 1 || !fits(maxSteps) {
		// Even zero overlap exceeds the ceiling: no step the pipeline can use
		// sustains the load.
		plan.EffectiveBaseOverlap = 0
		plan.DutyAtEffective = duty(0)
		plan.Status = StatusOverloaded
		return plan
	}
	if duty(0) == duty(overlapAt(1)) && duty(0) == duty(in.ConfiguredBaseOverlap) {
		// No load depends on the overlap (no known latency, or fixed-cadence pairs
		// only), so there is no smallest step to report and nothing to cap. The
		// configured overlap is compared too: with a grid as coarse as the base
		// clip, overlapAt(1) is already zero and the first test proves nothing.
		return plan
	}
	lo, hi := 1, maxSteps // fits(hi) holds
	for lo < hi {
		mid := lo + (hi-lo)/2
		if fits(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	// A grid coarser than the base clip would otherwise report a step no buffer
	// can use; the longest real step is the base clip itself (zero overlap).
	plan.MinBaseStep = min(time.Duration(lo)*grid, in.BaseClip)
	if plan.DutyAtConfigured <= in.DutyCeiling+epsilon {
		// The configured overlap already fits; snapping the minimum step up to the
		// grid must not cut an overlap the hardware sustains. MinBaseStep stays set
		// so the readout still shows the smallest step the hardware sustains.
		return plan
	}
	plan.EffectiveBaseOverlap = min(in.ConfiguredBaseOverlap, overlapAt(lo))
	plan.DutyAtEffective = duty(plan.EffectiveBaseOverlap)
	if plan.EffectiveBaseOverlap < in.ConfiguredBaseOverlap {
		plan.Status = StatusCapped
	}
	return plan
}

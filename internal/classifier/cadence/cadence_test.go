package cadence

import (
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testBaseClip = 3 * time.Second
	testGrid     = 100 * time.Millisecond
	testCeiling  = 0.75
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// ratioStep mirrors the classifier's step for a model whose overlap follows the
// base overlap: the overlap fraction is preserved across clip lengths and the
// step never drops below one millisecond. The real step function is supplied by
// the analysis package and covered by its tests; this package cannot import the
// classifier.
func ratioStep(clip time.Duration) func(time.Duration) time.Duration {
	return func(base time.Duration) time.Duration {
		overlap := time.Duration(math.Round(float64(base) * float64(clip) / float64(testBaseClip)))
		overlap = min(max(overlap, 0), max(clip-time.Millisecond, 0))
		return max(clip-overlap, time.Millisecond)
	}
}

func pair(src, model string, clip time.Duration) Pair {
	return Pair{SourceKey: src, ModelID: model, StepAt: ratioStep(clip)}
}

func batPair(src string) Pair {
	return Pair{SourceKey: src, ModelID: "bat", StepAt: func(time.Duration) time.Duration { return 1500 * time.Millisecond }}
}

func input(active bool, overlap time.Duration, pairs []Pair, lat map[string]time.Duration) Input {
	return Input{
		FilterActive:          active,
		ConfiguredBaseOverlap: overlap,
		BaseClip:              testBaseClip,
		Grid:                  testGrid,
		DutyCeiling:           testCeiling,
		Pairs:                 pairs,
		Latency:               lat,
	}
}

func TestSolve_Examples(t *testing.T) {
	t.Parallel()
	v24 := pair("s", "v24", 3*time.Second)
	v30 := pair("s", "v30", 5*time.Second)

	tests := []struct {
		name          string
		in            Input
		wantEffective time.Duration
		wantStatus    Status
		wantUnknown   []string
	}{
		{"no pairs", input(true, ms(2800), nil, nil), ms(2800), StatusOK, []string{}},
		{
			"single v2.4 under ceiling",
			input(true, ms(2000), []Pair{v24}, map[string]time.Duration{"v24": ms(166)}),
			ms(2000), StatusOK, []string{},
		},
		{
			// K = 0.166 + 3*0.874/5 = 0.6904; s_min = 0.9205 -> 1.0 s; effective 2.0 s.
			"Pi 4B v2.4 plus v3.0 capped",
			input(true, ms(2800), []Pair{v24, v30}, map[string]time.Duration{"v24": ms(166), "v30": ms(874)}),
			ms(2000), StatusCapped, []string{},
		},
		{
			"same load, level off keeps configured overlap",
			input(false, ms(2800), []Pair{v24, v30}, map[string]time.Duration{"v24": ms(166), "v30": ms(874)}),
			ms(2800), StatusFilterOff, []string{},
		},
		{
			"configured already below the cap",
			input(true, ms(500), []Pair{v24, v30}, map[string]time.Duration{"v24": ms(166), "v30": ms(874)}),
			ms(500), StatusOK, []string{},
		},
		{
			"configured zero with load over ceiling",
			input(true, 0, []Pair{v24}, map[string]time.Duration{"v24": ms(2500)}),
			0, StatusOverloaded, []string{},
		},
		{
			"bat only fixed load over ceiling",
			input(true, ms(2800), []Pair{batPair("s")}, map[string]time.Duration{"bat": ms(1400)}),
			0, StatusOverloaded, []string{},
		},
		{
			"unknown latency excluded and listed",
			input(true, ms(2000), []Pair{v24, v30}, map[string]time.Duration{"v24": ms(166)}),
			ms(2000), StatusOK, []string{"v30"},
		},
		{
			// K = 0.3453 -> s_min = 0.4604 -> 0.5 s.
			"grid snapping rounds the step up",
			input(true, ms(2900), []Pair{v24}, map[string]time.Duration{"v24": ms(345)}),
			ms(2500), StatusCapped, []string{},
		},
		{
			// Configured step 50 ms, duty 5 ms / 50 ms = 0.1 fits; the 100 ms grid must not cut it.
			"configured fits but is finer than the grid",
			input(true, ms(2950), []Pair{v24}, map[string]time.Duration{"v24": ms(5)}),
			ms(2950), StatusOK, []string{},
		},
		{
			// K = 0.6 -> s_min = 0.8 s exactly, which must stay 0.8 s.
			"exact grid value stays",
			input(true, ms(2900), []Pair{v24}, map[string]time.Duration{"v24": ms(450)}),
			ms(2400), StatusCapped, []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Solve(tt.in)
			assert.Equal(t, tt.wantEffective, got.EffectiveBaseOverlap)
			assert.Equal(t, tt.wantStatus, got.Status)
			assert.Equal(t, tt.wantUnknown, got.UnknownLatencyModels)
			assert.Equal(t, tt.in.ConfiguredBaseOverlap, got.ConfiguredBaseOverlap)
			if tt.wantStatus == StatusOK && len(tt.in.Pairs) > 0 && len(tt.wantUnknown) == 0 {
				assert.Positive(t, got.MinBaseStep, "a plan with known load reports the smallest sustained step")
			}
		})
	}
}

func TestSolve_Bounds(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	for range 500 {
		in := randomInput(rng)
		p := Solve(in)
		require.GreaterOrEqual(t, p.EffectiveBaseOverlap, time.Duration(0))
		require.LessOrEqual(t, p.EffectiveBaseOverlap, p.ConfiguredBaseOverlap)
		if p.Status == StatusCapped {
			assert.Zero(t, p.EffectiveBaseOverlap%testGrid, "capped overlap sits on the grid")
		}
		if p.Status == StatusCapped || p.Status == StatusOK {
			assert.LessOrEqual(t, p.DutyAtEffective, testCeiling+1e-6)
		}
		if !in.FilterActive {
			assert.Equal(t, in.ConfiguredBaseOverlap, p.EffectiveBaseOverlap)
			assert.Equal(t, StatusFilterOff, p.Status)
		}
	}
}

func randomInput(rng *rand.Rand) Input {
	models := []string{"a", "b", "c", "bat"}
	clips := map[string]time.Duration{"a": 3 * time.Second, "b": 5 * time.Second, "c": 5 * time.Second, "bat": 3 * time.Second}
	lat := map[string]time.Duration{}
	for _, m := range models {
		if rng.IntN(4) > 0 {
			lat[m] = ms(20 + rng.IntN(1500))
		}
	}
	var pairs []Pair
	for s := range 1 + rng.IntN(3) {
		for _, m := range models {
			if rng.IntN(2) == 0 {
				continue
			}
			pp := pair(string(rune('0'+s)), m, clips[m])
			if m == "bat" {
				pp = batPair(pp.SourceKey)
			}
			pairs = append(pairs, pp)
		}
	}
	return input(rng.IntN(5) > 0, ms(rng.IntN(2991)), pairs, lat)
}

func TestSolve_Transitions(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(7, 11))
	for range 500 {
		in := randomInput(rng)
		in.FilterActive = true
		prev := Solve(in)
		for range 8 {
			next := in
			next.Pairs = slices.Clone(in.Pairs)
			next.Latency = map[string]time.Duration{}
			maps.Copy(next.Latency, in.Latency)
			var monotone int // +1 must not raise effective, -1 must not lower it, 0 free
			switch rng.IntN(5) {
			case 0:
				next.Pairs = append(next.Pairs, pair("x", "a", 3*time.Second))
				monotone = 1
			case 1:
				if len(next.Pairs) > 0 {
					i := rng.IntN(len(next.Pairs))
					next.Pairs = append(next.Pairs[:i], next.Pairs[i+1:]...)
					monotone = -1
				}
			case 2:
				rng.Shuffle(len(next.Pairs), func(i, j int) { next.Pairs[i], next.Pairs[j] = next.Pairs[j], next.Pairs[i] })
				assert.Equal(t, prev, Solve(next), "permutation changes the plan")
			case 3:
				next.Latency["a"] += ms(100)
				monotone = 1
			case 4:
				next.ConfiguredBaseOverlap = ms(rng.IntN(2991))
			}
			got := Solve(next)
			assert.Equal(t, got, Solve(next), "solver is not deterministic")
			assert.True(t, SameCadence(&got, &got))
			if monotone == 1 {
				assert.LessOrEqual(t, got.EffectiveBaseOverlap, prev.EffectiveBaseOverlap)
			}
			if monotone == -1 {
				assert.GreaterOrEqual(t, got.EffectiveBaseOverlap, prev.EffectiveBaseOverlap)
			}
			in, prev = next, got
		}
	}
}

func TestPlanComparisons(t *testing.T) {
	t.Parallel()
	a := &Plan{EffectiveBaseOverlap: ms(1000), DutyAtEffective: 0.1, Status: StatusOK}
	b := &Plan{EffectiveBaseOverlap: ms(1000), DutyAtEffective: 0.2, Status: StatusOK}
	c := &Plan{EffectiveBaseOverlap: ms(900), Status: StatusOK}
	d := &Plan{EffectiveBaseOverlap: ms(1000), DutyAtEffective: 0.1, Status: StatusOverloaded}

	assert.True(t, SameCadence(nil, nil))
	assert.False(t, SameCadence(a, nil))
	assert.True(t, SameCadence(a, b))
	assert.False(t, SameCadence(a, c))
	assert.True(t, SameCadence(a, d), "status does not change the step")

	assert.True(t, SameOutcome(nil, nil))
	assert.False(t, SameOutcome(nil, a))
	assert.True(t, SameOutcome(a, b), "duty drift is not a new outcome")
	assert.False(t, SameOutcome(a, d))

	assert.True(t, Equal(nil, nil))
	assert.False(t, Equal(a, nil))
	assert.False(t, Equal(a, b))
	same := *a
	assert.True(t, Equal(a, &same))
	same.Models = []ModelCost{{ModelID: "x", Latency: ms(5)}}
	assert.False(t, Equal(a, &same))
}

func TestFilterActive(t *testing.T) {
	t.Parallel()
	assert.False(t, FilterActive(0))
	assert.True(t, FilterActive(1))
	assert.True(t, FilterActive(5))
}

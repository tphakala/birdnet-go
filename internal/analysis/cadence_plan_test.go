package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/notification"
)

func cadenceModelInfo(id string) classifier.ModelInfo {
	return classifier.ModelRegistry[id]
}

func cadenceAlias(t *testing.T, id string) string {
	t.Helper()
	aliases := classifier.ModelRegistry[id].ConfigAliases
	require.NotEmpty(t, aliases, id)
	return aliases[0]
}

func cadenceConfig(conn string, modelIDs ...string) sourceConfigWithModels {
	return sourceConfigWithModels{
		config:   &audiocore.SourceConfig{ConnectionString: conn},
		modelIDs: modelIDs,
	}
}

func cadenceLoaded(ids ...string) map[string]classifier.ModelInfo {
	out := make(map[string]classifier.ModelInfo, len(ids))
	for _, id := range ids {
		out[id] = cadenceModelInfo(id)
	}
	return out
}

func pairKeys(pairs []cadence.Pair) []string {
	keys := make([]string, 0, len(pairs))
	for _, p := range pairs {
		keys = append(keys, p.SourceKey+"/"+p.ModelID)
	}
	return keys
}

func TestBuildCadencePairs(t *testing.T) {
	t.Parallel()
	v24 := classifier.RegistryIDBirdNETV24
	perch := classifier.RegistryIDPerchV2
	bat := classifier.RegistryIDBat
	defaults := []classifier.ModelInfo{cadenceModelInfo(v24), cadenceModelInfo(perch)}

	tests := []struct {
		name    string
		configs func(t *testing.T) []sourceConfigWithModels
		loaded  map[string]classifier.ModelInfo
		want    []string
	}{
		{
			"explicit per-source list",
			func(t *testing.T) []sourceConfigWithModels {
				t.Helper()
				return []sourceConfigWithModels{cadenceConfig("a", cadenceAlias(t, perch))}
			},
			cadenceLoaded(v24, perch), []string{"a/" + perch},
		},
		{
			"empty list fans out to all defaults",
			func(*testing.T) []sourceConfigWithModels { return []sourceConfigWithModels{cadenceConfig("a")} },
			cadenceLoaded(v24, perch), []string{"a/" + v24, "a/" + perch},
		},
		{
			"all unresolvable falls back to first default",
			func(*testing.T) []sourceConfigWithModels {
				return []sourceConfigWithModels{cadenceConfig("a", "no-such-model")}
			},
			cadenceLoaded(v24, perch), []string{"a/" + v24},
		},
		{
			"unloaded model skipped",
			func(t *testing.T) []sourceConfigWithModels {
				t.Helper()
				return []sourceConfigWithModels{cadenceConfig("a", cadenceAlias(t, v24), cadenceAlias(t, perch))}
			},
			cadenceLoaded(v24), []string{"a/" + v24},
		},
		{
			"two sources double the pairs",
			func(t *testing.T) []sourceConfigWithModels {
				t.Helper()
				return []sourceConfigWithModels{
					cadenceConfig("a", cadenceAlias(t, v24)),
					cadenceConfig("b", cadenceAlias(t, v24)),
				}
			},
			cadenceLoaded(v24), []string{"a/" + v24, "b/" + v24},
		},
		{
			"duplicate model in a list is one buffer",
			func(t *testing.T) []sourceConfigWithModels {
				t.Helper()
				return []sourceConfigWithModels{cadenceConfig("a", cadenceAlias(t, v24), cadenceAlias(t, v24))}
			},
			cadenceLoaded(v24), []string{"a/" + v24},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configs := tt.configs(t)
			got := buildCadencePairs(configs, tt.loaded, defaults)
			assert.Equal(t, tt.want, pairKeys(got))

			// Parity with the registration path: same target set per source.
			for _, scm := range configs {
				targets, _ := resolveModelTargets(scm.modelIDs, tt.loaded)
				if len(targets) == 0 {
					targets = fallbackTargets(scm.modelIDs, defaults)
				}
				var ids []string
				for _, p := range got {
					if p.SourceKey == scm.config.ConnectionString {
						ids = append(ids, p.ModelID)
					}
				}
				var want []string
				seen := map[string]bool{}
				for i := range targets {
					if !seen[targets[i].ID] {
						seen[targets[i].ID] = true
						want = append(want, targets[i].ID)
					}
				}
				assert.Equal(t, want, ids)
			}
		})
	}

	t.Run("bat keeps its fixed step", func(t *testing.T) {
		t.Parallel()
		got := buildCadencePairs([]sourceConfigWithModels{cadenceConfig("a", cadenceAlias(t, bat))},
			cadenceLoaded(bat), nil)
		require.Len(t, got, 1)
		for _, base := range []time.Duration{0, time.Second, 2800 * time.Millisecond} {
			assert.Equal(t, cadenceModelInfo(bat).Spec.ClipLength/2, got[0].StepAt(base))
		}
	})
}

// TestBuildCadencePairs_StepMatchesBufferInterval pins that every pair's step is
// the real buffer step for every registered model, so the duty estimate matches
// what the buffers deliver.
func TestBuildCadencePairs_StepMatchesBufferInterval(t *testing.T) {
	t.Parallel()
	for id, info := range classifier.ModelRegistry {
		pairs := buildCadencePairs([]sourceConfigWithModels{cadenceConfig("a")}, cadenceLoaded(id),
			[]classifier.ModelInfo{info})
		require.Len(t, pairs, 1, id)
		for _, ovSec := range []float64{0, 1.0, 2.0, 2.4, 2.8, 2.99} {
			base := time.Duration(ovSec * float64(time.Second))
			want := info.Spec.BufferInterval(classifier.ResolveModelOverlap(id, info.Spec, base))
			assert.Equal(t, want, pairs[0].StepAt(base), "model %s overlap %.2f", id, ovSec)
		}
	}
}

func TestMatchModelTargets_NilCallbackMatchesResolve(t *testing.T) {
	t.Parallel()
	v24 := classifier.RegistryIDBirdNETV24
	loaded := cadenceLoaded(v24)
	ids := []string{cadenceAlias(t, v24), "bogus", cadenceAlias(t, classifier.RegistryIDPerchV2)}
	a, skippedA := matchModelTargets(ids, loaded, nil)
	b, skippedB := resolveModelTargets(ids, loaded)
	assert.Equal(t, a, b)
	assert.Equal(t, skippedA, skippedB)
	assert.Len(t, skippedA, 2)
}

func TestNeedsRestart(t *testing.T) {
	t.Parallel()
	mk := func(configured, effective int) *cadence.Plan {
		return &cadence.Plan{
			ConfiguredBaseOverlap: time.Duration(configured) * time.Millisecond,
			EffectiveBaseOverlap:  time.Duration(effective) * time.Millisecond,
		}
	}
	tests := []struct {
		name      string
		published *cadence.Plan
		candidate *cadence.Plan
		want      bool
	}{
		{"nothing published, uncapped", nil, mk(2000, 2000), false},
		{"nothing published, capped", nil, mk(2800, 2000), true},
		{"same effective", mk(2800, 2000), mk(2800, 2000), false},
		{"configured differs, effective equal", mk(2800, 2000), mk(2600, 2000), false},
		{"configured and effective differ", mk(2800, 2000), mk(1500, 1500), true},
		{"cap tightened", mk(2800, 2000), mk(2800, 1500), true},
		{"cap lifted", mk(2800, 2000), mk(2800, 2800), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, needsRestart(tt.published, tt.candidate))
		})
	}
}

func TestDrainRestartSignals(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{}, 10)
	drainRestartSignals(ch) // an empty channel must not block
	assert.Empty(t, ch)
	for range 3 {
		ch <- struct{}{}
	}
	drainRestartSignals(ch)
	assert.Empty(t, ch)
}

// TestPlanCadence_NilSettingsPlansFilterOff pins that settings that could not be
// loaded plan as empty settings instead of panicking.
func TestPlanCadence_NilSettingsPlansFilterOff(t *testing.T) {
	t.Parallel()
	v24 := classifier.RegistryIDBirdNETV24
	pairs := buildCadencePairs([]sourceConfigWithModels{cadenceConfig("a")}, cadenceLoaded(v24),
		[]classifier.ModelInfo{cadenceModelInfo(v24)})
	var plan cadence.Plan
	require.NotPanics(t, func() {
		plan = planCadence(nil, pairs, map[string]time.Duration{v24: 400 * time.Millisecond})
	})
	assert.Equal(t, cadence.StatusFilterOff, plan.Status)
	assert.Equal(t, time.Duration(0), plan.EffectiveBaseOverlap)
}

func TestPlanCadence_FilterOffKeepsConfigured(t *testing.T) {
	t.Parallel()
	v24 := classifier.RegistryIDBirdNETV24
	pairs := buildCadencePairs([]sourceConfigWithModels{cadenceConfig("a")}, cadenceLoaded(v24),
		[]classifier.ModelInfo{cadenceModelInfo(v24)})
	lat := map[string]time.Duration{v24: 400 * time.Millisecond}

	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.8
	s.Realtime.FalsePositiveFilter.Level = 0
	off := planCadence(s, pairs, lat)
	assert.Equal(t, cadence.StatusFilterOff, off.Status)
	assert.Equal(t, 2800*time.Millisecond, off.EffectiveBaseOverlap)

	s.Realtime.FalsePositiveFilter.Level = 1
	on := planCadence(s, pairs, lat)
	assert.Equal(t, cadence.StatusCapped, on.Status)
	assert.Less(t, on.EffectiveBaseOverlap, 2800*time.Millisecond)
}

// replanHarness replays the needsRestart decision against the pure planner, so a
// sequence of plan changes can be asserted without audio hardware. It does not
// call the pipeline; TestApplyCadenceDecision_* and
// TestPlanAndPublishCadence_Publishes cover the pipeline methods.
type replanHarness struct {
	t         *testing.T
	published *cadence.Plan
	restarts  int
}

func (h *replanHarness) plan(level int, overlap float64, configs []sourceConfigWithModels, loaded map[string]classifier.ModelInfo, lat map[string]time.Duration) *cadence.Plan {
	s := &conf.Settings{}
	s.BirdNET.Overlap = overlap
	s.Realtime.FalsePositiveFilter.Level = level
	var defaults []classifier.ModelInfo
	if info, ok := loaded[classifier.RegistryIDBirdNETV24]; ok {
		defaults = []classifier.ModelInfo{info}
	}
	plan := planCadence(s, buildCadencePairs(configs, loaded, defaults), lat)
	return &plan
}

// setup publishes unconditionally, as a full rebuild does.
func (h *replanHarness) setup(p *cadence.Plan) {
	h.published = p
}

// incremental applies needsRestart: count a restart or publish. It reports
// whether a restart was requested.
func (h *replanHarness) incremental(p *cadence.Plan) bool {
	if needsRestart(h.published, p) {
		h.restarts++
		return true
	}
	h.published = p
	return false
}

func TestCadenceReplanSequence(t *testing.T) {
	t.Parallel()
	v24 := classifier.RegistryIDBirdNETV24
	v3 := classifier.RegistryIDBirdNETV3
	perch := classifier.RegistryIDPerchV2
	lat := map[string]time.Duration{v24: 166 * time.Millisecond, v3: 874 * time.Millisecond, perch: 900 * time.Millisecond}
	two := cadenceLoaded(v24, v3)
	three := cadenceLoaded(v24, v3, perch)

	// Sources with an explicit model list so the loaded set decides the pairs.
	explicit := func(t *testing.T, ids ...string) []sourceConfigWithModels {
		t.Helper()
		aliases := make([]string, 0, len(ids))
		for _, id := range ids {
			aliases = append(aliases, cadenceAlias(t, id))
		}
		return []sourceConfigWithModels{cadenceConfig("a", aliases...)}
	}

	h := &replanHarness{t: t}

	// 1. Start: v2.4 and v3.0 at level 5 with the configured 2.8 s: capped.
	h.setup(h.plan(5, 2.8, explicit(t, v24, v3), two, lat))
	require.Equal(t, cadence.StatusCapped, h.published.Status)
	capped := h.published.EffectiveBaseOverlap
	assert.Less(t, capped, 2800*time.Millisecond)

	// 2. A classifier is added: the step moves, so a restart is requested.
	assert.True(t, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 1, h.restarts)
	// 3. The restart rebuilds and publishes.
	h.setup(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat))
	assert.Less(t, h.published.EffectiveBaseOverlap, capped)

	// 4. Reconfigure with no change: publish, no restart.
	assert.False(t, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 1, h.restarts)

	// 5. Overlap lowered below the cap but its restart signal was dropped: the
	// reconfigure restarts on its own.
	assert.True(t, h.incremental(h.plan(5, 1.0, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 2, h.restarts)
	h.setup(h.plan(5, 1.0, explicit(t, v24, v3, perch), three, lat))

	// 6. Level to 0 and back: crossing re-plans, same-side changes do not.
	assert.False(t, h.incremental(h.plan(3, 1.0, explicit(t, v24, v3, perch), three, lat)), "level change on one side of 0")
	h.setup(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat))
	assert.True(t, h.incremental(h.plan(0, 2.8, explicit(t, v24, v3, perch), three, lat)), "level to 0 lifts the cap")
	h.setup(h.plan(0, 2.8, explicit(t, v24, v3, perch), three, lat))
	assert.Equal(t, cadence.StatusFilterOff, h.published.Status)
	assert.Equal(t, 2800*time.Millisecond, h.published.EffectiveBaseOverlap)
	before := h.restarts
	assert.True(t, h.incremental(h.plan(4, 2.8, explicit(t, v24, v3, perch), three, lat)), "level back on re-applies the cap")
	assert.Equal(t, before+1, h.restarts)

	// 7. RestartSource after a model-assignment edit that adds a pair.
	h.setup(h.plan(5, 2.8, explicit(t, v24), cadenceLoaded(v24, v3, perch), lat))
	before = h.restarts
	assert.True(t, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, before+1, h.restarts)
}

// cadenceBackend is a classifierBackend fake that reports a fixed effective overlap.
type cadenceBackend struct {
	classifierBackend
	effective time.Duration
}

func (c cadenceBackend) EffectiveBaseOverlap() time.Duration { return c.effective }

func (c cadenceBackend) ModelSpecFor(id string) (classifier.ModelSpec, bool) {
	info, ok := classifier.ModelRegistry[id]
	return info.Spec, ok
}

// TestBufferIntervalFor_UsesEffectiveBaseOverlap pins that the overrun threshold
// follows the published (effective) overlap and not the configured one.
func TestBufferIntervalFor_UsesEffectiveBaseOverlap(t *testing.T) {
	t.Parallel()
	bn := cadenceBackend{effective: 1800 * time.Millisecond}
	assert.Equal(t, 1200*time.Millisecond, bufferIntervalFor(bn, classifier.RegistryIDBirdNETV24))
	assert.Equal(t, 1500*time.Millisecond, bufferIntervalFor(bn, classifier.RegistryIDBat), "bat stays fixed")
	assert.Equal(t, 3*time.Second-1800*time.Millisecond, bufferIntervalFor(bn, "unregistered"))
}

// TestRequestCaptureRestart_DoesNotBlockWhenFull pins that a restart request on
// a full channel returns instead of blocking the reconfigure that asked for it.
func TestRequestCaptureRestart_DoesNotBlockWhenFull(t *testing.T) {
	t.Parallel()
	p := &AudioPipelineService{restartChan: make(chan struct{}, 1)}
	p.requestCaptureRestart()
	p.requestCaptureRestart()
	assert.Len(t, p.restartChan, 1)
}

// cadenceService returns a pipeline with a zero orchestrator and a one-slot
// restart channel, under global settings at FP level 5 and overlap 2.8 s.
func cadenceService(t *testing.T) (*AudioPipelineService, *classifier.Orchestrator) {
	t.Helper()
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.8
	s.Realtime.FalsePositiveFilter.Level = 5
	conftest.SetTestSettings(s)

	orch := &classifier.Orchestrator{Settings: s}
	return &AudioPipelineService{bnAnalyzer: &BirdNETAnalyzer{bn: orch}, restartChan: make(chan struct{}, 1)}, orch
}

// TestPlanAndPublishCadence_Publishes pins that a full rebuild publishes its plan
// unconditionally, even when the step differs from the one published before,
// where the incremental decision would queue a restart instead.
func TestPlanAndPublishCadence_Publishes(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	p, orch := cadenceService(t)
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
		Status:                cadence.StatusCapped,
	})
	p.planAndPublishCadence(nil, "test")
	plan := orch.CadencePlan()
	require.NotNil(t, plan)
	assert.Equal(t, 2800*time.Millisecond, plan.EffectiveBaseOverlap, "no load, configured overlap kept")
	assert.Empty(t, p.restartChan)
}

// TestApplyCadenceDecision_QueuesRestartAndKeepsPlan pins that a changed step
// queues a full restart and leaves the published plan for the buffers in use.
func TestApplyCadenceDecision_QueuesRestartAndKeepsPlan(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	p, orch := cadenceService(t)
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
		Status:                cadence.StatusCapped,
	})
	p.applyCadenceDecision(orch, nil, nil, nil, "test")
	assert.Len(t, p.restartChan, 1, "the step changes from 1.8 s to 2.8 s")
	assert.Equal(t, 1800*time.Millisecond, orch.CadencePlan().EffectiveBaseOverlap, "the plan in use stays published")
}

// TestApplyCadenceDecision_PublishesWhenStepUnchanged pins that a plan with the
// same step is published without a restart.
func TestApplyCadenceDecision_PublishesWhenStepUnchanged(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	p, orch := cadenceService(t)
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  2800 * time.Millisecond,
		Status:                cadence.StatusOK,
		SourceCount:           3,
	})
	p.applyCadenceDecision(orch, nil, nil, nil, "test")
	assert.Empty(t, p.restartChan)
	assert.Equal(t, 0, orch.CadencePlan().SourceCount, "the new plan is published")
}

// TestReplanCadence_AppliesDecision pins that the reload re-plan runs the same
// decision as the incremental paths: a step change queues a full restart and
// keeps the plan in use published.
func TestReplanCadence_AppliesDecision(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	p, orch := cadenceService(t)
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
		Status:                cadence.StatusCapped,
	})
	p.replanCadence()
	assert.Len(t, p.restartChan, 1, "no load now, so the step changes from 1.8 s to 2.8 s")
	assert.Equal(t, 1800*time.Millisecond, orch.CadencePlan().EffectiveBaseOverlap)
}

// TestReplanCadence_NoBackendIsNoOp pins that the re-plan does nothing without a
// classifier backend.
func TestReplanCadence_NoBackendIsNoOp(t *testing.T) {
	t.Parallel()
	p := &AudioPipelineService{restartChan: make(chan struct{}, 1)}
	assert.NotPanics(t, p.replanCadence)
	assert.Empty(t, p.restartChan)
}

func TestSameCadenceInputs(t *testing.T) {
	t.Parallel()
	base := cadence.Plan{ConfiguredBaseOverlap: 2 * time.Second, SourceCount: 1, ModelCount: 2, DutyAtEffective: 0.5}

	drift := base
	drift.DutyAtEffective = 0.6
	assert.True(t, sameCadenceInputs(&base, &drift), "a duty change alone is not a new input")

	overlap := base
	overlap.ConfiguredBaseOverlap = 2500 * time.Millisecond
	assert.False(t, sameCadenceInputs(&base, &overlap), "a new configured overlap must reach the settings page")

	sources := base
	sources.SourceCount = 2
	assert.False(t, sameCadenceInputs(&base, &sources))

	models := base
	models.ModelCount = 3
	assert.False(t, sameCadenceInputs(&base, &models))
}

// TestPublishCadencePlan_FeedsTheBellNotice pins that publishing a capped first
// plan raises the bell notice.
func TestPublishCadencePlan_FeedsTheBellNotice(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	p, orch := cadenceService(t)
	svc := &fakeCadenceNoticeService{}
	p.cadenceNotice.service = func() notification.NoticeService { return svc }

	p.publishCadencePlan(orch, noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond), "test")

	created, _ := svc.counts()
	assert.Equal(t, 1, created)
	require.NotNil(t, svc.last())
	assert.Equal(t, notification.MsgCadenceCappedTitle, svc.last().TitleKey)
}

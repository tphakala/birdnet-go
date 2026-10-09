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

	t.Run("bat gets a fixed step", func(t *testing.T) {
		t.Parallel()
		got := buildCadencePairs([]sourceConfigWithModels{cadenceConfig("a", cadenceAlias(t, bat))},
			cadenceLoaded(bat), nil)
		require.Len(t, got, 1)
		assert.Equal(t, cadenceModelInfo(bat).Spec.ClipLength/2, got[0].FixedStep)
	})
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

func TestDecideCadence(t *testing.T) {
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
		want      cadenceAction
	}{
		{"nothing published, uncapped", nil, mk(2000, 2000), cadencePublish},
		{"nothing published, capped", nil, mk(2800, 2000), cadenceRestart},
		{"same effective", mk(2800, 2000), mk(2800, 2000), cadencePublish},
		{"configured differs, effective equal", mk(2800, 2000), mk(2600, 2000), cadencePublish},
		{"configured and effective differ", mk(2800, 2000), mk(1500, 1500), cadenceRestart},
		{"cap tightened", mk(2800, 2000), mk(2800, 1500), cadenceRestart},
		{"cap lifted", mk(2800, 2000), mk(2800, 2800), cadenceRestart},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, decideCadence(tt.published, tt.candidate))
		})
	}
}

func TestDrainRestartSignals(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{}, 10)
	assert.Zero(t, drainRestartSignals(ch), "empty channel must not block")
	for range 3 {
		ch <- struct{}{}
	}
	assert.Equal(t, 3, drainRestartSignals(ch))
	assert.Empty(t, ch)
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

// replanHarness replays the publish/restart decisions of the pipeline against the
// pure planner, so the sequence of restarts can be asserted without audio hardware.
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

// setup mirrors setupAudioSources: publish unconditionally.
func (h *replanHarness) setup(p *cadence.Plan) {
	h.published = p
}

// incremental mirrors reconfigureChangedSources and RestartSource: restart or publish.
func (h *replanHarness) incremental(p *cadence.Plan) cadenceAction {
	a := decideCadence(h.published, p)
	if a == cadenceRestart {
		h.restarts++
		return a
	}
	h.published = p
	return a
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
	assert.Equal(t, cadenceRestart, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 1, h.restarts)
	// 3. The restart rebuilds and publishes.
	h.setup(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat))
	assert.Less(t, h.published.EffectiveBaseOverlap, capped)

	// 4. Reconfigure with no change: publish, no restart.
	assert.Equal(t, cadencePublish, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 1, h.restarts)

	// 5. A source that would fail AddSource still counts (pairs come from configs):
	// the same configs yield the same plan, so no restart.
	assert.Equal(t, cadencePublish, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 1, h.restarts)

	// 6. Overlap lowered below the cap but its restart signal was dropped: the
	// reconfigure restarts on its own.
	assert.Equal(t, cadenceRestart, h.incremental(h.plan(5, 1.0, explicit(t, v24, v3, perch), three, lat)))
	assert.Equal(t, 2, h.restarts)
	h.setup(h.plan(5, 1.0, explicit(t, v24, v3, perch), three, lat))

	// 7. Level to 0 and back: crossing re-plans, same-side changes do not.
	assert.Equal(t, cadencePublish, h.incremental(h.plan(3, 1.0, explicit(t, v24, v3, perch), three, lat)), "level change on one side of 0")
	h.setup(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat))
	assert.Equal(t, cadenceRestart, h.incremental(h.plan(0, 2.8, explicit(t, v24, v3, perch), three, lat)), "level to 0 lifts the cap")
	h.setup(h.plan(0, 2.8, explicit(t, v24, v3, perch), three, lat))
	assert.Equal(t, cadence.StatusFilterOff, h.published.Status)
	assert.Equal(t, 2800*time.Millisecond, h.published.EffectiveBaseOverlap)
	before := h.restarts
	assert.Equal(t, cadenceRestart, h.incremental(h.plan(4, 2.8, explicit(t, v24, v3, perch), three, lat)), "level back on re-applies the cap")
	assert.Equal(t, before+1, h.restarts)

	// 8. RestartSource after a model-assignment edit that adds a pair.
	h.setup(h.plan(5, 2.8, explicit(t, v24), cadenceLoaded(v24, v3, perch), lat))
	before = h.restarts
	assert.Equal(t, cadenceRestart, h.incremental(h.plan(5, 2.8, explicit(t, v24, v3, perch), three, lat)))
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

// TestRequestCaptureRestart_ReportsWhetherQueued pins that a dropped restart
// token is reported, so RestartSource re-adds the source instead of leaving it
// removed with no restart pending.
func TestRequestCaptureRestart_ReportsWhetherQueued(t *testing.T) {
	t.Parallel()
	p := &AudioPipelineService{restartChan: make(chan struct{}, 1)}
	assert.True(t, p.requestCaptureRestart(), "empty channel queues the restart")
	assert.False(t, p.requestCaptureRestart(), "full channel drops the restart and says so")
	assert.Len(t, p.restartChan, 1)
}

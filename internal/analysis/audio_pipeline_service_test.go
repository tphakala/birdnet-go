package analysis

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Compile-time interface compliance check.
var _ app.Service = (*AudioPipelineService)(nil)

func TestAudioPipelineService_Name(t *testing.T) {
	t.Parallel()

	svc := NewAudioPipelineService(&conf.Settings{}, nil, nil, nil, nil)
	assert.Equal(t, "audio-pipeline", svc.Name())
}

func TestAudioPipelineService_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	svc := NewAudioPipelineService(&conf.Settings{}, nil, nil, nil, nil)
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := svc.Stop(t.Context())
		assert.NoError(t, err)
	})
}

func TestResolveModelTargets_EmptyInput(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_V2.4": {ID: "BirdNET_V2.4", Spec: classifier.ModelSpec{SampleRate: 48000}},
	}
	targets := resolveModelTargets(nil, loaded)
	assert.Empty(t, targets, "nil config IDs should return nil")

	targets = resolveModelTargets([]string{}, loaded)
	assert.Empty(t, targets, "empty config IDs should return empty")
}

func TestResolveModelTargets_SingleModel(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_V2.4": {
			ID:   "BirdNET_V2.4",
			Name: "BirdNET",
			Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		},
	}

	targets := resolveModelTargets([]string{"birdnet"}, loaded)
	require.Len(t, targets, 1)
	assert.Equal(t, "BirdNET_V2.4", targets[0].ID)
	assert.Equal(t, 48000, targets[0].Spec.SampleRate)
}

func TestResolveModelTargets_MultiModel(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_V2.4": {
			ID:   "BirdNET_V2.4",
			Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		},
		"Perch_V2": {
			ID:   "Perch_V2",
			Spec: classifier.ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		},
	}

	targets := resolveModelTargets([]string{"birdnet", "perch_v2"}, loaded)
	require.Len(t, targets, 2)

	// Collect results into a map for order-independent assertion.
	targetMap := make(map[string]int, len(targets))
	for _, tgt := range targets {
		targetMap[tgt.ID] = tgt.Spec.SampleRate
	}
	assert.Equal(t, 48000, targetMap["BirdNET_V2.4"])
	assert.Equal(t, 32000, targetMap["Perch_V2"])
}

func TestResolveModelTargets_UnknownConfigID(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_V2.4": {
			ID:   "BirdNET_V2.4",
			Spec: classifier.ModelSpec{SampleRate: 48000},
		},
	}

	// "unknown_model" is not in configToRegistryID, so it should be skipped.
	targets := resolveModelTargets([]string{"unknown_model"}, loaded)
	assert.Empty(t, targets)
}

func TestResolveModelTargets_KnownButNotLoaded(t *testing.T) {
	t.Parallel()

	// perch_v2 is a known config ID but is not in the loaded models map.
	loaded := map[string]classifier.ModelInfo{
		"BirdNET_V2.4": {
			ID:   "BirdNET_V2.4",
			Spec: classifier.ModelSpec{SampleRate: 48000},
		},
	}

	targets := resolveModelTargets([]string{"birdnet", "perch_v2"}, loaded)
	require.Len(t, targets, 1, "only birdnet should resolve, perch_v2 is not loaded")
	assert.Equal(t, "BirdNET_V2.4", targets[0].ID)
}

func TestBuildLivenessConfig_AllDefaults(t *testing.T) {
	t.Parallel()

	cfg := buildLivenessConfig(conf.WatchdogSettings{})
	defaults := audiocore.DefaultLivenessConfig()

	assert.Equal(t, defaults.CheckInterval, cfg.CheckInterval)
	assert.Equal(t, defaults.SilenceThreshold, cfg.SilenceThreshold)
	assert.Equal(t, defaults.MaxRetries, cfg.MaxRetries)
	assert.Equal(t, defaults.RetryBackoff, cfg.RetryBackoff)
	assert.Equal(t, defaults.CooldownAfterRecov, cfg.CooldownAfterRecov)
	assert.Equal(t, defaults.EscalationTimeout, cfg.EscalationTimeout)
}

func TestBuildLivenessConfig_CustomValues(t *testing.T) {
	t.Parallel()

	ws := conf.WatchdogSettings{
		CheckInterval:     5,
		SilenceThreshold:  60,
		MaxRetries:        5,
		RetryBackoff:      10,
		Cooldown:          120,
		EscalationTimeout: 90,
	}
	cfg := buildLivenessConfig(ws)

	assert.Equal(t, 5*time.Second, cfg.CheckInterval)
	assert.Equal(t, 60*time.Second, cfg.SilenceThreshold)
	assert.Equal(t, 5, cfg.MaxRetries)
	assert.Equal(t, 10*time.Second, cfg.RetryBackoff)
	assert.Equal(t, 120*time.Second, cfg.CooldownAfterRecov)
	assert.Equal(t, 90*time.Second, cfg.EscalationTimeout)
}

func TestBuildLivenessConfig_PartialOverride(t *testing.T) {
	t.Parallel()

	ws := conf.WatchdogSettings{
		SilenceThreshold: 45,
		MaxRetries:       10,
	}
	cfg := buildLivenessConfig(ws)
	defaults := audiocore.DefaultLivenessConfig()

	assert.Equal(t, defaults.CheckInterval, cfg.CheckInterval, "unset field should use default")
	assert.Equal(t, 45*time.Second, cfg.SilenceThreshold)
	assert.Equal(t, 10, cfg.MaxRetries)
	assert.Equal(t, defaults.RetryBackoff, cfg.RetryBackoff, "unset field should use default")
	assert.Equal(t, defaults.CooldownAfterRecov, cfg.CooldownAfterRecov, "unset field should use default")
	assert.Equal(t, defaults.EscalationTimeout, cfg.EscalationTimeout, "unset field should use default")
}

// TestClassifyExportDir covers the clip cleanup guard's directory classification:
// a usable directory runs cleanup, a missing directory is the benign export-off
// default (Debug/skip), and a stat error or a non-directory path is unexpected
// (Warn/skip).
func TestClassifyExportDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	realDir := filepath.Join(base, "clips")
	require.NoError(t, os.Mkdir(realDir, 0o755))

	regularFile := filepath.Join(base, "clips.txt")
	require.NoError(t, os.WriteFile(regularFile, []byte("not a dir"), 0o600))

	missing := filepath.Join(base, "does-not-exist")

	tests := []struct {
		name         string
		path         string
		wantState    exportDirState
		wantErrIsNil bool
	}{
		{name: "existing directory is usable", path: realDir, wantState: exportDirUsable, wantErrIsNil: true},
		{name: "missing directory is benign", path: missing, wantState: exportDirMissing, wantErrIsNil: false},
		{name: "regular file is bad without error", path: regularFile, wantState: exportDirBad, wantErrIsNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state, err := classifyExportDir(tt.path)
			assert.Equal(t, tt.wantState, state)
			if tt.wantErrIsNil {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.True(t, os.IsNotExist(err), "missing path should report a not-exist error")
			}
		})
	}
}

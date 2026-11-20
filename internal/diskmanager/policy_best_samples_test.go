package diskmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

func TestBestSamplesCleanupKeepsRecentAndHighestConfidenceClips(t *testing.T) {
	previous := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(previous) })

	baseDir := t.TempDir()
	settings := conftest.GetTestSettings()
	settings.Realtime.Audio.Export.Enabled = true
	settings.Realtime.Audio.Export.Path = baseDir
	settings.Realtime.Audio.Export.Retention.Policy = conf.RetentionPolicyBestSamples
	settings.Realtime.Audio.Export.Retention.MaxAge = "7d"
	settings.Realtime.Audio.Export.Retention.MinClips = 1
	conftest.SetTestSettings(settings)

	createClip := func(confidence int, age time.Duration) string {
		t.Helper()
		timestamp := time.Now().Add(-age).Format("20060102T150405Z")
		name := fmt.Sprintf("bubo_bubo_%dp_%s.wav", confidence, timestamp)
		path := filepath.Join(baseDir, name)
		require.NoError(t, os.WriteFile(path, []byte("audio"), 0o644)) //nolint:gosec // G306: test clip
		return path
	}

	highConfidenceOld := createClip(95, 30*24*time.Hour)
	lowConfidenceOld := createClip(40, 20*24*time.Hour)
	recentLowConfidence := createClip(20, 3*24*time.Hour)

	result := BestSamplesCleanup(make(chan struct{}), &MockDB{})

	require.NoError(t, result.Err)
	assert.Equal(t, 1, result.ClipsRemoved)
	assert.FileExists(t, highConfidenceOld, "best sample survives beyond the recent-sample window")
	assert.NoFileExists(t, lowConfidenceOld, "older low-confidence sample is removed")
	assert.FileExists(t, recentLowConfidence, "all samples within maxage survive for review")
}

func TestSelectBestClipsPerSpeciesRanksConfidenceAndBreaksTiesByRecency(t *testing.T) {
	now := time.Now()
	files := []FileInfo{
		{Path: "/clips/species_old_high.wav", Species: "species", Confidence: 95, Timestamp: now.Add(-30 * 24 * time.Hour)},
		{Path: "/clips/species_new_tied.wav", Species: "species", Confidence: 90, Timestamp: now.Add(-2 * 24 * time.Hour)},
		{Path: "/clips/species_old_tied.wav", Species: "species", Confidence: 90, Timestamp: now.Add(-20 * 24 * time.Hour)},
		{Path: "/clips/other_only.wav", Species: "other", Confidence: 50, Timestamp: now.Add(-20 * 24 * time.Hour)},
	}

	best := selectBestClipsPerSpecies(files, 2)
	assert.Contains(t, best, files[0].Path, "higher confidence takes priority over recency")
	assert.Contains(t, best, files[1].Path, "newer clip wins a confidence tie")
	assert.NotContains(t, best, files[2].Path)
	assert.Contains(t, best, files[3].Path, "species with fewer than minclips keeps all its clips")
}

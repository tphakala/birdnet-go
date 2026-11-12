package processor

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

const (
	noiseGateTestLowConfidence  = 0.6
	noiseGateTestHighConfidence = 0.9
)

// readJSONLines returns the decoded lines of a JSON log file.
func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var lines []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var entry map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &entry), "line %q", sc.Text())
		lines = append(lines, entry)
	}
	require.NoError(t, sc.Err())
	return lines
}

// linesWithOperation returns the entries whose operation field is op.
func linesWithOperation(entries []map[string]any, op string) []map[string]any {
	var out []map[string]any
	for _, e := range entries {
		if e["operation"] == op {
			out = append(out, e)
		}
	}
	return out
}

// TestNoiseGateLines_ShippedLoggingWritesOnlyTheNoiseBankFile pins where the gate
// decision lines go. With the shipped logging defaults the per-detection outcome
// line is in the noise bank log file and nowhere else, and the per-window line is
// written nowhere. With the module level set to debug in the config file, the
// window line also lands in the noise bank file and still not in the files that
// carry a console copy or that other code reads.
func TestNoiseGateLines_ShippedLoggingWritesOnlyTheNoiseBankFile(t *testing.T) {
	tests := []struct {
		name       string
		extraYAML  string
		wantWindow bool
	}{
		{"shipped defaults", "", false},
		{"module level debug", "logging:\n  module_levels:\n    analysis.noisebank: debug\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runShippedNoiseGateLogging(t, tt.extraYAML, tt.wantWindow)
		})
	}
}

// runShippedNoiseGateLogging loads a config with the given extra YAML, runs one
// window through the gate and flushes one compared detection, then checks which
// log files hold the two decision lines.
func runShippedNoiseGateLogging(t *testing.T, extraYAML string, wantWindow bool) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("security:\n  sessionsecret: noise-gate-log-test\n"+extraYAML), 0o600))

	prevConfigPath := conf.ConfigPath
	viper.Reset()
	conf.ConfigPath = configPath
	t.Cleanup(func() {
		viper.Reset()
		conf.ConfigPath = prevConfigPath
		conf.StoreSettings(nil)
	})
	t.Chdir(dir)
	settings, err := conf.Load()
	require.NoError(t, err)

	cl, err := logger.NewCentralLogger(&settings.Logging)
	require.NoError(t, err)
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() {
		logger.SetGlobal(prev)
		_ = cl.Close()
	})

	out, ok := settings.Logging.ModuleOutputs[noisebank.LogModule]
	require.True(t, ok, "the shipped defaults carry an output for the module")
	assert.True(t, out.Enabled)
	assert.False(t, out.ConsoleAlso)

	// The shipped defaults enable the privacy filter, whose coverage gate can defer a
	// flush; publish bare settings as the flush tests do.
	p := mergeTestProcessor(t)
	p.JobQueue = jobqueue.NewJobQueue()
	conf.StoreSettings(&conf.Settings{})
	p.SetNoiseBank(noiseGateBank())

	low := detectionFor("Parus major", "Great Tit")
	low.Result.Confidence = noiseGateTestLowConfidence
	high := detectionFor("Cyanistes caeruleus", "Blue Tit")
	high.Result.Confidence = noiseGateTestHighConfidence
	window := classifier.Results{
		ModelID:        classifier.RegistryIDBat,
		Source:         datastore.AudioSource{ID: mergeTestSource},
		Embedding:      noiseGateEmbedding(noiseGateAbove),
		EmbeddingSpace: noiseGateSpace,
	}
	p.applyNoiseExampleGate(&window, []Detections{*low, *high})

	now := time.Now()
	item := PendingDetection{
		Source:        mergeTestSource,
		BestModelID:   classifier.RegistryIDBat,
		Confidence:    noiseGateTestHighConfidence,
		Count:         3,
		FirstDetected: now.Add(-10 * time.Second),
		FlushDeadline: now.Add(-time.Second),
		noiseGate:     noiseTally{compared: 3, matched: 2, minMatch: 0.85, maxMiss: 0.7},
	}
	item.Detection = *high
	p.pendingDetections[pendingKeyForDetection(mergeTestSource, high)] = item
	_, flushed := p.flushPendingDetections()
	require.Equal(t, 1, flushed)
	require.NoError(t, cl.Flush())

	noiseLines := readJSONLines(t, filepath.Join(dir, logger.DefaultNoiseBankLogPath))
	windowLines := linesWithOperation(noiseLines, noiseGateWindowOperation)
	if !wantWindow {
		assert.Empty(t, windowLines, "the per-window line is Debug and the default level is Info")
	} else {
		require.Len(t, windowLines, 1)
		assertWindowLine(t, windowLines[0])
	}

	detectionLines := linesWithOperation(noiseLines, noiseGateDetectionOperation)
	require.Len(t, detectionLines, 1)
	d := detectionLines[0]
	assert.Equal(t, noisebank.LogModule, d["module"])
	assert.Equal(t, "noise examples: detection outcome", d["msg"])
	assert.Equal(t, "INFO", d["level"])
	assert.Equal(t, "blue tit", d["species"])
	assert.Equal(t, classifier.RegistryIDBat, d["best_model_id"])
	assert.Equal(t, "kept", d["outcome"])
	assert.Equal(t, false, d["unlikely"])
	assert.Equal(t, "none", d["unlikely_sources"])
	assert.InDelta(t, 3, d["hit_windows"], 0)
	assert.InDelta(t, 3, d["compared_windows"], 0)
	assert.InDelta(t, 2, d["matched_windows"], 0)
	assert.InDelta(t, 0.85, d["min_match_similarity"], noiseGateTolerance)
	assert.InDelta(t, 0.7, d["max_miss_similarity"], noiseGateTolerance)
	assert.InDelta(t, noiseGateTestHighConfidence, d["confidence"], noiseGateTolerance)
	assert.InDelta(t, noisebank.MatchSimilarity, d["threshold"], noiseGateTolerance)
	assert.Contains(t, d, "source")
	assert.Contains(t, d, "detection_time")
	assert.NotContains(t, d, "reason")

	// The routes that carry a console copy or that other code parses hold neither line.
	actions := readJSONLines(t, filepath.Join(dir, logger.DefaultActionsLogPath))
	assert.NotEmpty(t, linesWithOperation(actions, "flush_detection"), "the control line lands in the actions log")
	assert.Empty(t, linesWithOperation(actions, noiseGateWindowOperation))
	assert.Empty(t, linesWithOperation(actions, noiseGateDetectionOperation))

	appLog := filepath.Join(dir, "logs", "application.log")
	if _, statErr := os.Stat(appLog); statErr == nil {
		app := readJSONLines(t, appLog)
		assert.Empty(t, linesWithOperation(app, noiseGateWindowOperation))
		assert.Empty(t, linesWithOperation(app, noiseGateDetectionOperation))
	}
}

// assertWindowLine checks the fields of the window line the shipped-defaults test
// drives: a matching window of two detections, the better one a blue tit.
func assertWindowLine(t *testing.T, w map[string]any) {
	t.Helper()
	assert.Equal(t, noisebank.LogModule, w["module"])
	assert.Equal(t, "noise examples: window compared", w["msg"])
	assert.Equal(t, "DEBUG", w["level"])
	assert.Equal(t, true, w["matched"])
	assert.Equal(t, noiseGateSpace, w["space"])
	assert.Equal(t, classifier.RegistryIDBat, w["model_id"])
	assert.Equal(t, "blue tit", w["best_species"])
	assert.InDelta(t, noiseGateTestHighConfidence, w["best_confidence"], noiseGateTolerance)
	assert.InDelta(t, noiseGateAbove, w["similarity"], noiseGateTolerance)
	assert.InDelta(t, noisebank.MatchSimilarity, w["threshold"], noiseGateTolerance)
	assert.InDelta(t, 1, w["examples_compared"], 0)
	assert.InDelta(t, 2, w["detections"], 0)
	assert.Contains(t, w, "source")

}

// TestNoiseGateOutcome_DeferredFlushWritesNothingUntilTheDetectionEnds pins that a
// compared detection held back by the privacy coverage gate writes no outcome line
// on the ticks it stays pending, and exactly one when it is finally approved.
func TestNoiseGateOutcome_DeferredFlushWritesNothingUntilTheDetectionEnds(t *testing.T) {
	buf := logtest.CaptureBufferAt(t, slog.LevelInfo)
	r := newPrivacyRig(t, "BirdNET_V2.4", 60*time.Second)
	r.pend(40 * time.Second)
	key := pendingDetectionKey(e2eSource, e2eSpecies)
	item := r.p.pendingDetections[key]
	item.noiseGate = noiseTally{compared: 2, matched: 1, minMatch: 0.85, maxMiss: 0.7}
	r.p.pendingDetections[key] = item
	endOff := clipSpan(r.settings, &item).to.Sub(r.base)

	r.feed(r.clip, endOff-sec(2), sec(1))
	require.Equal(t, "pending", r.outcome())
	require.Equal(t, "pending", r.outcome(), "a second deferred tick")
	assert.Empty(t, logLinesWithOperation(buf.String(), noiseGateDetectionOperation), "a deferred detection writes no outcome line")

	r.feed(endOff-sec(1), endOff+sec(3), sec(1))
	require.Equal(t, "approved", r.outcome())
	assert.Len(t, logLinesWithOperation(buf.String(), noiseGateDetectionOperation), 1, "one line when it ends")
}

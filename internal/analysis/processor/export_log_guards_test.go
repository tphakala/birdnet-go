package processor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// fallbackTestTime is a fixed timestamp so the generated clip names are
// deterministic; the tests here assert on log counts, not on names.
var fallbackTestTime = time.Date(2026, 7, 20, 9, 37, 1, 0, time.UTC)

// unmeasurableSampleRate is below audionorm's K-weighting minimum, so a
// loudness measurement of a clip at this rate fails. It is a property of the
// capture source rather than of any one clip, which is exactly why the WARN it
// produces is guarded per (reason, format, rate).
const unmeasurableSampleRate = 4000

// countLines is how many captured log lines mention the substring. The guards
// under test are about repetition, so the assertions need a count rather than a
// contains check: "the line appears" passes just as well when it appears on
// every detection, which is the bug.
func countLines(t *testing.T, logs, substr string) int {
	t.Helper()
	n := 0
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// resetExportLogGuards re-arms every keyed guard this file exercises, so the
// test observes its own emissions rather than whichever earlier test in the
// package happened to consume the guard first.
func resetExportLogGuards(t *testing.T) {
	t.Helper()
	rearm := func() {
		normalizeSkipLogged.seen.Clear()
		resampleFailureLogged.seen.Clear()
	}
	rearm()
	t.Cleanup(rearm)
}

// TestOnceByKeyEmitsPerDistinctKey is the property every guard in the export
// path depends on, asserted directly rather than through a log site: once per
// distinct key, and never a second time for a key already seen. A bare
// sync.Once would fail the second half of this.
func TestOnceByKeyEmitsPerDistinctKey(t *testing.T) {
	t.Parallel()

	var guard onceByKey
	emitted := make([]string, 0, 4)

	for range 3 {
		guard.do("mp3@48000", func() { emitted = append(emitted, "mp3@48000") })
		guard.do("opus@48000", func() { emitted = append(emitted, "opus@48000") })
	}

	assert.Equal(t, []string{"mp3@48000", "opus@48000"}, emitted,
		"each distinct key explains itself exactly once; neither silences the other")
}

// TestNormalizeSkipKeyDistinguishesReasons pins why the targets/bit-depth guard
// keys on the reason as well as the format and rate. The bit-depth arm is
// decided by a build constant, but the targets arm is decided by hot-reloadable
// settings, so a format+rate-only key would let whichever arm fired first
// silence the other for the rest of the process.
func TestNormalizeSkipKeyDistinguishesReasons(t *testing.T) {
	t.Parallel()

	depth := reasonFormatRateKey("unsupported_bit_depth", "mp3", 48000)
	targets := reasonFormatRateKey("normalization_targets_out_of_native_range", "mp3", 48000)

	assert.NotEqual(t, depth, targets,
		"two causes at the same format and rate must not share a key")
	assert.Equal(t, depth, reasonFormatRateKey("unsupported_bit_depth", "mp3", 48000),
		"the same cause at the same format and rate must share a key")
}

// TestResampleKeyDistinguishesRatePairs guards the resample flood: resampling a
// fixed rate pair fails deterministically, so an affected source would emit on
// every detection forever without a key, and a differently-configured second
// source must not be silenced by the first.
func TestResampleKeyDistinguishesRatePairs(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, resampleKey(96000, 48000), resampleKey(192000, 48000),
		"two source rates converting to the same target must not share a key")
	assert.Equal(t, "96000->48000", resampleKey(96000, 48000),
		"the key names both ends of the conversion, so it stays readable in a guard dump")
}

// TestNormalizeSkipLogsOncePerFormat drives the real log site. Normalization is
// enabled with a target audionorm cannot accept, which is the branch that used
// to emit on every single detection of an affected install.
func TestNormalizeSkipLogsOncePerFormat(t *testing.T) {
	logs := captureExportLogs(t)
	resetExportLogGuards(t)

	a := newExportLogAction(t, t.TempDir(), "clip.mp3", "mp3")
	// Out of audionorm's accepted range, so resolveExportGainDB takes the
	// targets arm rather than measuring. Unreachable for a validated config;
	// set directly here because that branch is the one under test.
	a.Settings.Realtime.Audio.Export.Normalization.Enabled = true
	a.Settings.Realtime.Audio.Export.Normalization.TargetLUFS = audionormMinTargetLUFS - 10
	a.Settings.Realtime.Audio.Export.Normalization.TruePeak = -2

	for range 5 {
		_, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, "mp3")
		require.NoError(t, err)
	}

	assert.Equal(t, 1, countLines(t, logs.String(), "audio_export_normalize_skip"),
		"five detections on one misconfiguration must produce one line, not five")

	// A second format is a different key and must still be explained.
	for range 3 {
		_, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, "opus")
		require.NoError(t, err)
	}
	assert.Equal(t, 2, countLines(t, logs.String(), "audio_export_normalize_skip"),
		"a different format must not be silenced by the first one")
}

// TestNormalizeSkipReasonsAreNotConflated pins the reason-in-key decision at the
// log site, which the key-builder unit test above cannot: a pure-function test
// cannot observe which key the production code actually passes. Both arms run at
// one format AND one rate, so dropping the reason from the key collapses them
// onto a single key and the count drops to 1.
//
// This also covers the measurement-failure arm, which had no execution at all.
func TestNormalizeSkipReasonsAreNotConflated(t *testing.T) {
	logs := captureExportLogs(t)
	resetExportLogGuards(t)

	const format = "mp3"

	// BOTH arms are driven at unmeasurableSampleRate, which is what makes this
	// test discriminating. The rate must be the shared one rather than
	// conf.SampleRate: arm 2 reaches the measurement-failure branch only because
	// the rate is below audionorm's K-weighting minimum, and with mono int16 PCM
	// no other input can force that failure. Arm 1 does not care about the rate
	// at all, so it is the one that moves.
	//
	// Drive them at DIFFERENT rates and the test proves nothing: the two keys
	// stay distinct on the format+rate alone, so it stays green through exactly
	// the revert it exists to catch.
	const rate = unmeasurableSampleRate

	// Arm 1: targets outside audionorm's accepted range, so the targets branch
	// is taken before any measurement is attempted.
	a := newExportLogAction(t, t.TempDir(), "clip.mp3", format)
	a.Settings.Realtime.Audio.Export.Normalization.Enabled = true
	a.Settings.Realtime.Audio.Export.Normalization.TargetLUFS = audionormMinTargetLUFS - 10
	a.Settings.Realtime.Audio.Export.Normalization.TruePeak = -2
	_, normalized, err := a.resolveExportGainDB(t.Context(), rate, format)
	require.NoError(t, err)
	assert.False(t, normalized, "a skipped normalization must not report itself as normalized")

	// Arm 2: valid targets, so the measurement is attempted and fails on the
	// rate. Same format, same rate, same guard, different reason.
	b := newExportLogAction(t, t.TempDir(), "clip.mp3", format)
	b.Settings.Realtime.Audio.Export.Normalization.Enabled = true
	b.Settings.Realtime.Audio.Export.Normalization.TargetLUFS = -23
	b.Settings.Realtime.Audio.Export.Normalization.TruePeak = -2
	_, normalized, err = b.resolveExportGainDB(t.Context(), rate, format)
	require.NoError(t, err, "an unmeasurable clip must degrade to the static gain, not fail the export")
	assert.False(t, normalized, "a failed measurement must not report itself as normalized")

	assert.Equal(t, 2, countLines(t, logs.String(), "audio_export_normalize_skip"),
		"two distinct causes at one format and rate must each be explained; "+
			"a key without the reason would let the first silence the second")
	assert.Equal(t, 1, countLines(t, logs.String(), normalizeSkipMeasureFailed))
	assert.Equal(t, 1, countLines(t, logs.String(), normalizeSkipTargetsOutOfRange))
}

// TestNormalizeSkipLineOmitsDetectionID pins the field-set decision. Under a
// guard, detection_id would name only the first detection to trip a standing
// misconfiguration, reading as if the problem belonged to that one clip.
func TestNormalizeSkipLineOmitsDetectionID(t *testing.T) {
	logs := captureExportLogs(t)
	resetExportLogGuards(t)

	a := newExportLogAction(t, t.TempDir(), "clip.mp3", "mp3")
	a.Settings.Realtime.Audio.Export.Normalization.Enabled = true
	a.Settings.Realtime.Audio.Export.Normalization.TargetLUFS = audionormMinTargetLUFS - 10
	a.Settings.Realtime.Audio.Export.Normalization.TruePeak = -2

	_, _, err := a.resolveExportGainDB(t.Context(), conf.SampleRate, "mp3")
	require.NoError(t, err)

	line := ""
	for l := range strings.SplitSeq(logs.String(), "\n") {
		if strings.Contains(l, "audio_export_normalize_skip") {
			line = l
			break
		}
	}
	require.NotEmpty(t, line, "the normalize-skip line must have been emitted")
	assert.NotContains(t, line, "detection_id")
	// Both normalize-skip sites carry the same operation tag, so they must carry
	// the same field set; sample_rate used to be on only one of them.
	assert.Contains(t, line, "sample_rate")
	assert.Contains(t, line, "reason")
}

// TestResampleFailureLogsOncePerRatePair drives the real resample log site,
// which the key-builder test above does not reach. Without this, a regression
// dropping the .do() wrapper (back to the per-detection flood this change
// exists to stop) would not be caught by anything.
func TestResampleFailureLogsOncePerRatePair(t *testing.T) {
	logs := captureExportLogs(t)
	resetExportLogGuards(t)

	a := newExportLogAction(t, t.TempDir(), "clip.mp3", "mp3")
	// An odd byte count cannot be whole int16 samples, so ResampleBytes rejects
	// it. The rate is above conf.SampleRate so resolveExportParams takes the
	// resampling branch rather than passing the clip through.
	a.pcmData = []byte{0x01, 0x02, 0x03}
	a.sourceSampleRate = conf.SampleRate * 2

	for range 4 {
		// resolveExportParams returns no error; a failed resample is reported
		// through the log line under test and the export continues at the source
		// rate, which is the behaviour being pinned.
		_, _, _ = a.resolveExportParams(filepath.Join(t.TempDir(), "clip.mp3"))
		// resolveExportParams rewrites sourceSampleRate only on success; on the
		// failure path under test it stays put, so the loop keeps re-entering.
		a.sourceSampleRate = conf.SampleRate * 2
	}

	assert.Equal(t, 1, countLines(t, logs.String(), "audio_export_resample"),
		"a rate pair that cannot resample fails identically every time, so it explains itself once")

	line := logLineContaining(t, logs.String(), "operation=audio_export_resample")
	assert.NotContains(t, line, "detection_id",
		"a guarded line must not name the first detection to trip a standing condition")
}

// TestNormalizedReportsWhetherMeasurementRan pins the field that makes each clip
// self-describing. gain_db cannot answer this on its own: the default static
// Export.Gain is 0 and so is a measured gain of 0, so without this flag a dump
// taken after the one-time skip warning aged out of the log window cannot tell
// a normalized clip from an unnormalized one.
func TestNormalizedReportsWhetherMeasurementRan(t *testing.T) {
	// Captured only to keep the guarded WARNs out of the test output; this test
	// asserts the returned flag, not the log.
	captureExportLogs(t)
	resetExportLogGuards(t)

	// Normalization off: the static gain is used, so nothing was measured.
	off := newExportLogAction(t, t.TempDir(), "clip.mp3", "mp3")
	off.Settings.Realtime.Audio.Export.Normalization.Enabled = false
	_, normalized, err := off.resolveExportGainDB(t.Context(), conf.SampleRate, "mp3")
	require.NoError(t, err)
	assert.False(t, normalized, "normalization disabled must not report as normalized")

	// Normalization on with targets audionorm accepts, at a measurable rate:
	// the measurement runs and the gain comes from it.
	on := newExportLogAction(t, t.TempDir(), "clip.mp3", "mp3")
	on.Settings.Realtime.Audio.Export.Normalization.Enabled = true
	on.Settings.Realtime.Audio.Export.Normalization.TargetLUFS = -23
	on.Settings.Realtime.Audio.Export.Normalization.TruePeak = -2
	_, normalized, err = on.resolveExportGainDB(t.Context(), conf.SampleRate, "mp3")
	require.NoError(t, err)
	assert.True(t, normalized, "a real loudness measurement must report as normalized")
}

// TestClipPathFallbackWarnsPerExportType is the hot-reload half of #1373's
// argument: Export.Type is the value this WARN reports and is changeable from
// the UI, so a process-wide sync.Once let the first bad value silence every
// later, different one.
func TestClipPathFallbackWarnsPerExportType(t *testing.T) {
	logs := captureExportLogs(t)
	resetBuildClipPathFallbackOnce()
	t.Cleanup(resetBuildClipPathFallbackOnce)

	p := &Processor{}
	settings := &conf.Settings{}

	emit := func(exportType string) {
		settings.Realtime.Audio.Export.Type = exportType
		p.buildClipPath(settings, "Turdus merula", 0.82, 0, fallbackTestTime)
	}

	// Whitespace-only types produce an empty extension, which is the fallback
	// branch. Two distinct bad values, each repeated.
	emit("  ")
	emit("  ")
	emit("\t")

	assert.Equal(t, 2, countLines(t, logs.String(), "buildClipPath_fallback"),
		"each distinct bad Export.Type must be reported once; the first must not silence the second")
	assert.True(t, buildClipPathFallbackWarned())
}

// TestNoPCMSkipLineNamesSpecies covers the branch where the clip is lost
// outright, which is the export log where naming the bird matters most. It
// carried detection_id and clip_name only.
func TestNoPCMSkipLineNamesSpecies(t *testing.T) {
	logs := captureExportLogs(t)

	a := newExportLogAction(t, t.TempDir(), "clip.mp3", "mp3")
	a.pcmData = nil
	a.species = "eurasian blackbird"

	require.NoError(t, a.Execute(t.Context(), nil))

	// Scoped to the line and asserted key-and-value: a buffer-wide substring
	// check would pass just as well if the species were emitted under some other
	// field name, which is not the contract.
	line := logLineContaining(t, logs.String(), "operation=audio_export_skip")
	assert.Contains(t, line, `species="eurasian blackbird"`,
		"the line reporting a lost clip must say which bird was lost")
}

// TestMQTTNotReadyWarnsPerTopic covers the guard that used to log a per-call
// topic inside a process-wide sync.Once, so the single line it emitted named a
// topic that did not generalise. The topic derives from the hot-reloadable
// Realtime.MQTT.Topic setting.
func TestMQTTNotReadyWarnsPerTopic(t *testing.T) {
	logs := captureExportLogs(t)

	p := &Processor{}
	for range 3 {
		require.ErrorIs(t, p.PublishMQTT(t.Context(), "birdnet/soundlevel", "{}"), ErrMQTTClientNotReady)
	}
	require.ErrorIs(t, p.PublishMQTT(t.Context(), "birdnet-renamed/soundlevel", "{}"), ErrMQTTClientNotReady)

	assert.Equal(t, 2, countLines(t, logs.String(), "publish_mqtt_not_ready"),
		"one line per topic: repeats stay silent, a renamed topic is explained again")
	assert.Contains(t, logs.String(), "birdnet-renamed/soundlevel")
}

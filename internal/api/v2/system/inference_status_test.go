// internal/api/v2/system/inference_status_test.go
package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestBuildSourceAttachments verifies that buildSourceAttachments correctly
// routes audio sources to their configured model, or to the primary fallback
// when no configured model resolves to a loaded model.
func TestBuildSourceAttachments(t *testing.T) {
	t.Parallel()

	// classifier.DefaultModelVersion is the registry key for the primary BirdNET model.
	const primaryID = classifier.DefaultModelVersion

	// Two loaded models: the primary BirdNET and Perch.
	models := []classifier.ModelInfo{
		{ID: primaryID},
		{ID: classifier.RegistryIDPerchV2},
	}

	settings := &conf.Settings{}
	// Front Yard uses conf.ModelIDPerchV2 ("perch_v2"), which ResolveConfigModelID
	// maps to classifier.RegistryIDPerchV2 ("Perch_V2"). Fallback must be false.
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Front Yard", Models: []string{conf.ModelIDPerchV2}},
		{Name: "Garage", Models: nil}, // no models: falls back to primary
	}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{Name: "Cam1", Type: "rtsp", Models: []string{"unknown_model"}}, // unresolved: falls back to primary
	}

	got := buildSourceAttachments(settings, models, primaryID, nil)

	// Perch_V2 should have exactly Front Yard, attached without fallback.
	perch := got[classifier.RegistryIDPerchV2]
	require.Len(t, perch, 1, "Perch_V2 attachments")
	assert.Equal(t, "Front Yard", perch[0].Name, "Perch_V2 source name")
	assert.False(t, perch[0].Fallback, "Perch_V2 source must not be a fallback")

	// Primary should have Garage and Cam1, both as fallbacks.
	prim := got[primaryID]
	require.Len(t, prim, 2, "primary attachments must have 2 entries (Garage, Cam1)")
	for _, s := range prim {
		assert.True(t, s.Fallback, "primary attachment %q should have Fallback=true", s.Name)
	}
}

// TestBuildSourceAttachments_ResolvesButNotLoaded verifies that a source whose
// config model alias resolves to a registry ID, but that registry ID is NOT in
// the loaded models list, falls back to primary with Fallback=true. This catches
// regressions where the guard `ok && loaded[regID]` is loosened to just `ok`.
func TestBuildSourceAttachments_ResolvesButNotLoaded(t *testing.T) {
	t.Parallel()

	const primaryID = classifier.DefaultModelVersion

	// Only BirdNET is loaded; Perch is deliberately NOT loaded.
	models := []classifier.ModelInfo{
		{ID: primaryID},
	}

	settings := &conf.Settings{}
	// Studio uses conf.ModelIDPerchV2, which resolves to classifier.RegistryIDPerchV2,
	// but Perch is not in the loaded models. Must fall back to primary with Fallback=true.
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Studio", Models: []string{conf.ModelIDPerchV2}},
	}

	got := buildSourceAttachments(settings, models, primaryID, nil)

	// Perch_V2 should have NO attachments (not loaded).
	perch := got[classifier.RegistryIDPerchV2]
	assert.Empty(t, perch, "Perch_V2 attachments must be empty (Perch not loaded)")

	// Primary should have Studio as a fallback.
	prim := got[primaryID]
	require.Len(t, prim, 1, "primary attachments must have 1 entry (Studio)")
	assert.Equal(t, "Studio", prim[0].Name, "primary source name")
	assert.True(t, prim[0].Fallback, "primary source must be a fallback")
}

// TestBuildSourceAttachments_MultiModelSourceAttachesAll verifies that a single
// source assigned to several models (the real multi-model setup: one soundcard
// feeding BirdNET + Perch + Bat) is attached to EVERY loaded model in its Models
// list, not just the first. This mirrors the runtime fan-out in
// resolveModelTargets, which routes the source's audio to all assigned models.
func TestBuildSourceAttachments_MultiModelSourceAttachesAll(t *testing.T) {
	t.Parallel()

	const primaryID = classifier.DefaultModelVersion

	// All three models loaded.
	models := []classifier.ModelInfo{
		{ID: primaryID},
		{ID: classifier.RegistryIDPerchV2},
		{ID: classifier.RegistryIDBat},
	}

	settings := &conf.Settings{}
	// One soundcard source explicitly assigned to all three models.
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Äänikortti", Models: []string{conf.ModelIDBirdNET, conf.ModelIDPerchV2, conf.ModelIDBat}},
	}

	got := buildSourceAttachments(settings, models, primaryID, nil)

	// Every assigned, loaded model must show the source, none as a fallback.
	for _, id := range []string{primaryID, classifier.RegistryIDPerchV2, classifier.RegistryIDBat} {
		att := got[id]
		require.Len(t, att, 1, "model %q must have the source attached", id)
		assert.Equal(t, "Äänikortti", att[0].Name, "source name for %q", id)
		assert.False(t, att[0].Fallback, "explicit assignment must not be a fallback for %q", id)
	}
}

// TestBuildModelStatus verifies that buildModelStatus correctly computes
// average latency, peak latency, RTF, and memory from a non-zero PeekSnapshot.
func TestBuildModelStatus(t *testing.T) {
	t.Parallel()
	rssVal := int64(125_000_000)
	info := classifier.ModelInfo{
		ID:           "BirdNET_V2.4",
		Name:         "BirdNET v2.4",
		Backend:      "ONNX",
		Quantization: classifier.QuantizationINT8,
		IsStock:      true,
		NumSpecies:   6522,
		Spec:         classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	// MaxMs is sourced from the lifetime max (the model card uses the all-time peak).
	snap := inferencestats.PeekSnapshot{InvokeCount: 1000, InvokeTotalUs: 47_200_000, InvokeMaxUsLifetime: 130_000}
	rss := map[string]int64{"BirdNET_V2.4": rssVal}

	got := buildModelStatus(&info, snap, rss, nil, nil, nil)

	assert.Equal(t, int64(1000), got.Stats.Invocations, "invocations")
	assert.InDelta(t, 47.2, got.Stats.AvgMs, 0.1, "avgMs")
	assert.InDelta(t, 130.0, got.Stats.MaxMs, 0.01, "maxMs")
	require.NotNil(t, got.Stats.RTF, "rtf must not be nil with non-zero invocations")
	assert.InDelta(t, 0.0157, *got.Stats.RTF, 0.0001, "rtf")
	require.NotNil(t, got.Memory.ApproxRssBytes, "approxRssBytes must not be nil when RSS is available")
	assert.Equal(t, rssVal, *got.Memory.ApproxRssBytes, "approxRssBytes")
	assert.Equal(t, "inference.BirdNET_V2_4.rtf", got.MetricKeys.RTF, "metricKeys.rtf")
}

// TestBuildModelStatus_ZeroInvocations verifies that buildModelStatus returns
// nil RTF and nil ApproxRssBytes when there are no invocations or no RSS data.
func TestBuildModelStatus_ZeroInvocations(t *testing.T) {
	t.Parallel()
	info := classifier.ModelInfo{ID: "X", Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}}
	got := buildModelStatus(&info, inferencestats.PeekSnapshot{}, nil, nil, nil, nil)
	assert.Nil(t, got.Stats.RTF, "rtf must be nil with zero invocations (no divide-by-zero)")
	assert.Nil(t, got.Memory.ApproxRssBytes, "approxRssBytes must be nil when RSS unavailable")
	assert.True(t, got.Memory.Approximate, "memory.approximate must always be true")
}

// TestApplyRuntimeBackend verifies that live backend/precision values override the
// static file metadata, while empty live values preserve the static fallback that
// buildModelStatus set. This is the core of the runtime-sourced fix: an ONNX model
// executed on OpenVINO must report "OpenVINO" with its FP16 compute precision, but
// a model that is not loaded (empty live values) must keep its static metadata.
func TestApplyRuntimeBackend(t *testing.T) {
	t.Parallel()

	t.Run("live values override static file metadata", func(t *testing.T) {
		t.Parallel()
		// Static metadata says ONNX/FP32 (the file), live says OpenVINO/FP16 (running).
		status := InferenceModelStatus{Backend: classifier.BackendONNX, Quantization: string(classifier.QuantizationFP32)}
		applyRuntimeBackend(&status, classifier.BackendOpenVINO, string(classifier.QuantizationFP16))
		assert.Equal(t, classifier.BackendOpenVINO, status.Backend, "live backend must win over static ONNX")
		assert.Equal(t, string(classifier.QuantizationFP16), status.Quantization, "live precision must win over static FP32")
	})

	t.Run("live precision fills an empty static quantization", func(t *testing.T) {
		t.Parallel()
		// Perch has no static quantization; the live INT8 (from the int8_arm filename)
		// must surface on the card.
		status := InferenceModelStatus{Backend: classifier.BackendONNX, Quantization: ""}
		applyRuntimeBackend(&status, classifier.BackendONNX, string(classifier.QuantizationINT8))
		assert.Equal(t, string(classifier.QuantizationINT8), status.Quantization, "live INT8 must surface for perch_v2_int8_arm")
	})

	t.Run("empty live values preserve the static fallback", func(t *testing.T) {
		t.Parallel()
		// Model not loaded: live values are empty, so the static metadata is kept.
		status := InferenceModelStatus{Backend: classifier.BackendTFLite, Quantization: string(classifier.QuantizationFP32)}
		applyRuntimeBackend(&status, "", "")
		assert.Equal(t, classifier.BackendTFLite, status.Backend, "empty live backend must keep the static value")
		assert.Equal(t, string(classifier.QuantizationFP32), status.Quantization, "empty live precision must keep the static value")
	})
}

// TestGetInferenceStatus_HTTP200 verifies that GetInferenceStatus returns HTTP
// 200 and a valid InferenceStatusResponse whose TFLite backend availability
// tracks the compiled-in state (true by default, false under the notflite build
// tag). It uses an apitest.NewCore-backed Handler over httptest to exercise the
// handler without starting any background goroutines.
func TestGetInferenceStatus_HTTP200(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes settings to the process-global snapshot.
	e := echo.New()
	controller := &Handler{Core: apitest.NewCore(t, apitest.WithEcho(e))}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/inference", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.GetInferenceStatus(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp InferenceStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), "response body must unmarshal to InferenceStatusResponse")
	assert.Equal(t, hwprofile.TFLiteLinked(), resp.Backends.TFLite.Available, "TFLite backend availability must match the compiled-in state (false under the notflite build tag)")
	assert.NotZero(t, resp.SnapshotAtUnix, "SnapshotAtUnix must be a non-zero Unix timestamp")
}

// eventInferenceTopologyChangedName is asserted against the package constant so
// the SSE event name stays the single source of truth shared with the frontend.
const eventInferenceTopologyChangedName = "system.inference_topology_changed"

// TestGetInferenceStatus_AudioBlock verifies that GetInferenceStatus returns an
// audio block with the expected metric key for queue depth and a non-negative
// queue capacity matching RouteInboxCapacity.
func TestGetInferenceStatus_AudioBlock(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes settings to the process-global snapshot.
	e := echo.New()
	controller := &Handler{Core: apitest.NewCore(t, apitest.WithEcho(e))}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/inference", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.GetInferenceStatus(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp InferenceStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.Equal(t, observability.MetricKeyAudioQueueDepthAggregate, resp.Audio.MetricKeys.QueueDepth,
		"audio.metricKeys.queueDepth must match the shared constant")
	assert.Equal(t, audiocore.RouteInboxCapacity, resp.Audio.QueueCapacity,
		"audio.queueCapacity must equal RouteInboxCapacity")
	assert.GreaterOrEqual(t, resp.Audio.QueueDepth, 0, "audio.queueDepth must be non-negative")
}

// TestVADStatusInfo_JSONContract pins the wire field names of the VAD block and
// the omitempty behaviour the frontend InferenceVAD type depends on.
func TestVADStatusInfo_JSONContract(t *testing.T) {
	t.Parallel()

	loaded := VADStatusInfo{
		Enabled:     true,
		Available:   true,
		Loaded:      true,
		Threshold:   0.35,
		ModelSource: "embedded",
		Strategy:    "sequence",
		SampleRate:  16000,
		Stats: VADStatsInfo{
			Invocations: 42,
			AvgMs:       2.5,
			MaxMs:       9.1,
			SpeechHits:  3,
		},
		LastSpeechAtUnix:      1_700_000_000,
		LastSpeechProbability: 0.87,
		RecentHits: []VADHitInfo{
			{AtUnix: 1_700_000_000, Probability: 0.87, Source: "Front Yard"},
		},
	}
	raw, err := json.Marshal(loaded)
	require.NoError(t, err)
	for _, key := range []string{
		`"enabled":true`, `"available":true`, `"loaded":true`, `"threshold":0.35`,
		`"modelSource":"embedded"`, `"strategy":"sequence"`, `"sampleRate":16000`,
		`"invocations":42`, `"avgMs":2.5`, `"maxMs":9.1`, `"speechHits":3`,
		`"lastSpeechAtUnix":1700000000`, `"lastSpeechProbability":0.87`,
		`"recentHits":[`, `"atUnix":1700000000`, `"probability":0.87`, `"source":"Front Yard"`,
	} {
		assert.Contains(t, string(raw), key, "VAD JSON must carry %s", key)
	}

	// A disabled/unloaded gate omits the optional descriptors so the panel renders
	// a clean "disabled" state without stale strategy/source/last-speech values.
	off := VADStatusInfo{Enabled: false, Available: false}
	rawOff, err := json.Marshal(off)
	require.NoError(t, err)
	for _, absent := range []string{"modelSource", "strategy", "sampleRate", "lastSpeechAtUnix", "lastSpeechProbability"} {
		assert.NotContains(t, string(rawOff), absent, "disabled VAD must omit %s", absent)
	}
	// The pointer field on the parent response omits entirely when nil.
	rawResp, err := json.Marshal(InferenceStatusResponse{})
	require.NoError(t, err)
	assert.NotContains(t, string(rawResp), `"vad"`, "nil VAD must be omitted so the panel hides")
}

// TestBuildModelStatus_MetricKeys verifies that buildModelStatus populates
// throughput and error-rate metric keys using the inferencestats helpers.
func TestBuildModelStatus_MetricKeys(t *testing.T) {
	t.Parallel()
	info := classifier.ModelInfo{
		ID:   "BirdNET_V2.4",
		Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	snap := inferencestats.PeekSnapshot{InvokeCount: 100, InvokeErrors: 5}
	got := buildModelStatus(&info, snap, nil, nil, nil, nil)

	assert.Equal(t, inferencestats.ThroughputMetricKey("BirdNET_V2.4"), got.MetricKeys.Throughput,
		"metricKeys.throughput must equal ThroughputMetricKey(id)")
	assert.Equal(t, inferencestats.ErrorRateMetricKey("BirdNET_V2.4"), got.MetricKeys.ErrorRate,
		"metricKeys.errorRate must equal ErrorRateMetricKey(id)")
}

// TestBuildModelStatus_ErrorRateAndLoadFailures verifies that buildModelStatus
// computes error rate and populates load failures when data is available.
func TestBuildModelStatus_ErrorRateAndLoadFailures(t *testing.T) {
	t.Parallel()
	info := classifier.ModelInfo{
		ID:   "BirdNET_V2.4",
		Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	// 10 successes, 5 errors: errorRate = 5/15 ~= 0.333
	snap := inferencestats.PeekSnapshot{InvokeCount: 10, InvokeErrors: 5}
	loadFailures := map[string]int64{"BirdNET_V2.4": 3}

	got := buildModelStatus(&info, snap, nil, nil, loadFailures, nil)

	require.NotNil(t, got.Stats.ErrorRate, "errorRate must be non-nil when errors exist")
	assert.InDelta(t, 5.0/15.0, *got.Stats.ErrorRate, 0.001, "errorRate = errors/(invocations+errors)")
	require.NotNil(t, got.Stats.LoadFailures, "loadFailures must be non-nil when entry exists")
	assert.Equal(t, int64(3), *got.Stats.LoadFailures, "loadFailures value")
}

// TestBuildModelStatus_ErrorRateNilWhenNoErrors verifies that error rate and
// load failures are nil when there are no invocations and no map entry.
func TestBuildModelStatus_ErrorRateNilWhenNoErrors(t *testing.T) {
	t.Parallel()
	info := classifier.ModelInfo{
		ID:   "X",
		Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	got := buildModelStatus(&info, inferencestats.PeekSnapshot{}, nil, nil, nil, nil)
	assert.Nil(t, got.Stats.ErrorRate, "errorRate must be nil when total is zero")
	assert.Nil(t, got.Stats.LoadFailures, "loadFailures must be nil when map is nil")
}

// TestBuildModelStatus_LastDetection verifies that buildModelStatus populates
// LastDetection when the processor cache has an entry for the model.
func TestBuildModelStatus_LastDetection(t *testing.T) {
	t.Parallel()
	info := classifier.ModelInfo{
		ID:   "BirdNET_V2.4",
		Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	lastDetections := map[string]*LastDetectionInfo{
		"BirdNET_V2.4": {
			Species:        "European Robin",
			ScientificName: "Erithacus rubecula",
			Confidence:     0.92,
			AtUnix:         1718000000,
			InRange:        true,
		},
	}

	got := buildModelStatus(&info, inferencestats.PeekSnapshot{}, nil, nil, nil, lastDetections)

	require.NotNil(t, got.LastDetection, "lastDetection must be non-nil when cache has entry")
	assert.Equal(t, "European Robin", got.LastDetection.Species)
	assert.Equal(t, "Erithacus rubecula", got.LastDetection.ScientificName)
	assert.InDelta(t, 0.92, got.LastDetection.Confidence, 0.001)
	assert.Equal(t, int64(1718000000), got.LastDetection.AtUnix)
	assert.True(t, got.LastDetection.InRange)
}

// TestInferenceModelStatus_JSONContract locks in the Phase A JSON field names
// and shapes the frontend depends on: device, paused, scheduleLabel, and a
// newest-first recentDetections array. recentDetections must serialize as an
// array (never null) so the frontend can iterate it unconditionally, while an
// empty scheduleLabel must be omitted.
func TestInferenceModelStatus_JSONContract(t *testing.T) {
	t.Parallel()

	status := InferenceModelStatus{
		ID:            "Bat",
		Name:          "Bat",
		Device:        deviceCPU,
		Paused:        true,
		ScheduleLabel: "Night schedule",
		RecentDetections: []LastDetectionInfo{
			{Species: "Common Pipistrelle", ScientificName: "Pipistrellus pipistrellus", Confidence: 0.81, AtUnix: 1718000200, InRange: true},
			{Species: "Soprano Pipistrelle", ScientificName: "Pipistrellus pygmaeus", Confidence: 0.74, AtUnix: 1718000100, InRange: false},
		},
	}

	raw, err := json.Marshal(status)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	// The Phase A keys are present under their contract names.
	assert.JSONEq(t, `"CPU"`, string(m["device"]))
	assert.JSONEq(t, `true`, string(m["paused"]))
	assert.JSONEq(t, `"Night schedule"`, string(m["scheduleLabel"]))
	require.Contains(t, m, "recentDetections", "recentDetections key must always be present")

	// recentDetections is newest-first and serializes its nested fields.
	var recent []LastDetectionInfo
	require.NoError(t, json.Unmarshal(m["recentDetections"], &recent))
	require.Len(t, recent, 2)
	assert.Equal(t, "Common Pipistrelle", recent[0].Species, "recentDetections must be newest-first")
	assert.Equal(t, int64(1718000200), recent[0].AtUnix)

	// The nested field names are part of the contract: assert their JSON keys.
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(m["recentDetections"], &rows))
	require.NotEmpty(t, rows)
	firstRow := rows[0]
	for _, key := range []string{"species", "scientificName", "confidence", "atUnix", "inRange"} {
		require.Contains(t, firstRow, key, "recentDetections element must carry the %q key", key)
	}
	assert.JSONEq(t, `true`, string(firstRow["inRange"]))

	// An empty list still serializes as [] (never null) and an empty
	// scheduleLabel is omitted from the object entirely.
	active := InferenceModelStatus{ID: "x", Device: deviceUnknown, RecentDetections: []LastDetectionInfo{}}
	rawActive, err := json.Marshal(active)
	require.NoError(t, err)
	var ma map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawActive, &ma))
	assert.JSONEq(t, `[]`, string(ma["recentDetections"]), "empty recentDetections must serialize as [] not null")
	assert.NotContains(t, ma, "scheduleLabel", "empty scheduleLabel must be omitted")
	assert.JSONEq(t, `"Unknown"`, string(ma["device"]))
}

// TestSortInferenceModelsByName verifies that model statuses are ordered by
// display name (case-insensitive), tie-broken by ID, so the API response order
// is deterministic regardless of the orchestrator's map iteration order.
func TestSortInferenceModelsByName(t *testing.T) {
	t.Parallel()
	models := []InferenceModelStatus{
		{ID: "b", Name: "Zebra"},
		{ID: "a", Name: "alpha"},
		{ID: "c", Name: "Alpha"},
	}
	sortInferenceModelsByName(models)
	got := []string{models[0].ID, models[1].ID, models[2].ID}
	// "alpha" and "Alpha" tie case-insensitively; tie broken by ID (a before c).
	require.Equal(t, []string{"a", "c", "b"}, got)
}

// TestEventInferenceTopologyChangedNameContract pins the SSE event name constant
// shared with the frontend so the metrics-stream contract stays stable.
func TestEventInferenceTopologyChangedNameContract(t *testing.T) {
	t.Parallel()
	assert.Equal(t, eventInferenceTopologyChangedName, eventInferenceTopologyChanged)
}

// TestBuildHardwareInfo verifies the mapping from a hardware profile onto the
// API payload, including the two shapes that decide whether a field appears at
// all: a board is reported only when the device tree named one, and an
// accelerator list is reported only when a GPU was found.
func TestBuildHardwareInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile hwprofile.Profile
		assert  func(t *testing.T, info HardwareInfo)
	}{
		{
			name: "raspberry pi 5 reports its board and tier",
			profile: hwprofile.Profile{
				Arch:          "arm64",
				CPUArch:       "aarch64",
				CPUModel:      "Cortex-A76",
				PhysicalCores: 4,
				TotalRAMBytes: 4 * 1024 * 1024 * 1024,
				HasNativeF16:  true,
				Board: hwprofile.Board{
					Kind:  hwprofile.BoardRaspberryPi,
					Model: "Raspberry Pi 5 Model B Rev 1.0",
					SoC:   "bcm2712",
					Tier:  hwprofile.TierPi5,
				},
				Backends: hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
			},
			assert: func(t *testing.T, info HardwareInfo) {
				t.Helper()
				assert.Equal(t, "aarch64", info.Arch)
				assert.Equal(t, "Cortex-A76", info.CPUModel)
				assert.True(t, info.FP16)
				assert.Equal(t, 4, info.PhysicalCores)
				require.NotNil(t, info.Board)
				assert.Equal(t, hwprofile.TierPi5, info.Board.Tier)
				assert.Equal(t, "bcm2712", info.Board.SoC)
				assert.Nil(t, info.Accelerators)
				assert.Equal(t,
					[]string{hwprofile.CapAArch64, hwprofile.CapAArch64A76, hwprofile.CapTFLite, hwprofile.CapFP16Native},
					info.Capabilities)
			},
		},
		{
			name: "generic amd64 host reports no board but does report its gpu",
			profile: hwprofile.Profile{
				Arch:          "amd64",
				CPUArch:       "x86_64",
				PhysicalCores: 8,
				Board:         hwprofile.Board{Kind: hwprofile.BoardGeneric},
				Backends:      hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
				Accelerators: []hwprofile.Accelerator{{
					Kind:       hwprofile.AcceleratorIGPU,
					Vendor:     hwprofile.VendorIntel,
					Name:       "Intel Graphics [8086:46a6]",
					Generation: 12,
					Reasons:    []string{hwprofile.ReasonRenderNodeUnavailable},
				}},
			},
			assert: func(t *testing.T, info HardwareInfo) {
				t.Helper()
				// A "generic" board row would tell the user nothing, so it is
				// omitted rather than sent empty.
				assert.Nil(t, info.Board)
				require.Len(t, info.Accelerators, 1)
				assert.False(t, info.Accelerators[0].Accessible)
				assert.Equal(t,
					[]string{hwprofile.ReasonRenderNodeUnavailable},
					info.Accelerators[0].Reasons,
					"every blocker must survive the mapping, not just the first")
				// Fields the earlier mapping silently dropped.
				assert.Equal(t, hwprofile.AcceleratorIGPU, info.Accelerators[0].Kind)
				assert.Equal(t, hwprofile.VendorIntel, info.Accelerators[0].Vendor)
				assert.Equal(t, "Intel Graphics [8086:46a6]", info.Accelerators[0].Name)
			},
		},
		{
			name:    "an unprobed profile produces an empty payload rather than wrong values",
			profile: hwprofile.Profile{},
			assert: func(t *testing.T, info HardwareInfo) {
				t.Helper()
				assert.Nil(t, info.Board)
				assert.Nil(t, info.Accelerators)
				assert.Zero(t, info.TotalRAMBytes)
				assert.Zero(t, info.PhysicalCores)
				assert.Empty(t, info.Capabilities)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := buildHardwareInfo(tt.profile, "Docker")

			assert.Equal(t, "Docker", info.Environment, "environment always comes from the caller")
			tt.assert(t, info)
		})
	}
}

// TestHardwareInfo_JSONContract pins the wire names. The first four fields
// predate the hardware profile and the frontend already reads them, so the
// extension has to be additive: renaming or retyping any of them is a breaking
// change this test is here to catch.
// TestGetInferenceStatus_TFLiteFollowsTheBuildTag pins the endpoint to the
// compile-time fact rather than a hardcoded true. Hardcoding it fed straight
// into capability derivation, which would have offered a notflite build models
// it cannot execute.
func TestGetInferenceStatus_TFLiteFollowsTheBuildTag(t *testing.T) {
	t.Parallel()

	e := echo.New()
	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v2/system/inference", http.NoBody), rec)
	h := &Handler{Core: apitest.NewCore(t)}

	require.NoError(t, h.GetInferenceStatus(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp InferenceStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, hwprofile.TFLiteLinked(), resp.Backends.TFLite.Available)
	assert.Equal(t, hwprofile.TFLiteLinked(),
		slices.Contains(resp.Hardware.Capabilities, hwprofile.CapTFLite),
		"the capability token must agree with the backend the build actually links")
}

func TestHardwareInfo_JSONContract(t *testing.T) {
	t.Parallel()

	info := HardwareInfo{
		Arch:          "x86_64",
		CPUModel:      "12th Gen Intel(R) Core(TM) i7-1260P",
		Environment:   "Docker",
		FP16:          false,
		TotalRAMBytes: 32 * 1024 * 1024 * 1024,
		PhysicalCores: 12,
		Capabilities:  []string{"x86-64", "tflite"},
		Board:         &BoardInfo{Kind: "raspberry-pi", Model: "Raspberry Pi 5 Model B Rev 1.0", SoC: "bcm2712", Tier: "pi5"},
		Accelerators: []AcceleratorInfo{{
			Kind:       "igpu",
			Vendor:     "intel",
			Name:       "Intel Graphics [8086:46a6]",
			Accessible: false,
			Reasons:    []string{"render-node-unavailable"},
		}},
	}

	raw, err := json.Marshal(info)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	for _, key := range []string{"arch", "cpuModel", "environment", "fp16"} {
		require.Contains(t, m, key, "pre-existing key %q must keep its name", key)
	}
	assert.JSONEq(t, `"x86_64"`, string(m["arch"]))
	assert.JSONEq(t, `false`, string(m["fp16"]))

	for _, key := range []string{"board", "accelerators", "totalRamBytes", "physicalCores", "capabilities"} {
		require.Contains(t, m, key, "added key %q missing", key)
	}
	assert.JSONEq(t, `{"kind":"raspberry-pi","model":"Raspberry Pi 5 Model B Rev 1.0","soc":"bcm2712","tier":"pi5"}`, string(m["board"]))
	assert.JSONEq(t, `[{"kind":"igpu","vendor":"intel","name":"Intel Graphics [8086:46a6]","accessible":false,"reasons":["render-node-unavailable"]}]`, string(m["accelerators"]))

	// An unprobed host omits every added key, so a client that only knows the
	// original four fields sees exactly the payload it saw before.
	rawEmpty, err := json.Marshal(HardwareInfo{Arch: "x86_64", Environment: "Bare Metal"})
	require.NoError(t, err)
	var empty map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawEmpty, &empty))
	for _, key := range []string{"board", "accelerators", "totalRamBytes", "physicalCores", "capabilities"} {
		assert.NotContains(t, empty, key, "added key %q must be omitted when unset", key)
	}
}

// TestBuildSourceAttachments_LiveRouterState verifies that the status view
// reports what the audio router is actually doing rather than what the config
// asks for. A model that is loaded and assigned but that receives no audio must
// be marked NotRunning instead of being presented as running: reporting it as
// running is what hid the model-loading failure behind GitHub #4201 and #4204
// for days.
func TestBuildSourceAttachments_LiveRouterState(t *testing.T) {
	t.Parallel()

	const primaryID = classifier.DefaultModelVersion
	models := []classifier.ModelInfo{
		{ID: primaryID},
		{ID: classifier.RegistryIDPerchV2},
	}

	settings := &conf.Settings{}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Front Yard", Models: []string{conf.ModelIDBirdNET, conf.ModelIDPerchV2}},
	}

	t.Run("assigned but not routed is marked not running", func(t *testing.T) {
		t.Parallel()

		// The router feeds only BirdNET, though the config assigns Perch too.
		running := map[string]map[string]bool{
			"Front Yard": {primaryID: true},
		}

		got := buildSourceAttachments(settings, models, primaryID, running)

		perch := got[classifier.RegistryIDPerchV2]
		require.Len(t, perch, 1)
		assert.True(t, perch[0].NotRunning,
			"a model that receives no audio must not be reported as running")

		prim := got[primaryID]
		require.Len(t, prim, 1)
		assert.False(t, prim[0].NotRunning, "BirdNET really is routed")
		assert.False(t, prim[0].Fallback,
			"BirdNET is genuinely assigned here, so it is not a fallback attachment")
	})

	t.Run("routed models are reported as running", func(t *testing.T) {
		t.Parallel()

		running := map[string]map[string]bool{
			"Front Yard": {primaryID: true, classifier.RegistryIDPerchV2: true},
		}

		got := buildSourceAttachments(settings, models, primaryID, running)

		perch := got[classifier.RegistryIDPerchV2]
		require.Len(t, perch, 1)
		assert.False(t, perch[0].NotRunning)
	})

	t.Run("assigned models loaded but idle do not invent a primary fallback", func(t *testing.T) {
		t.Parallel()

		// The source is known to the router but has no analysis buffers at all, so
		// both assigned models are idle. Both still resolve to LOADED models, so the
		// runtime does NOT fall back to the primary: registerConsumersForSources
		// falls back only when a source resolves to no loaded target. The status
		// must not invent a fallback row the runtime never creates.
		running := map[string]map[string]bool{"Front Yard": {}}

		got := buildSourceAttachments(settings, models, primaryID, running)

		prim := got[primaryID]
		require.Len(t, prim, 1, "only the genuine BirdNET assignment, no invented fallback row")
		assert.True(t, prim[0].NotRunning, "BirdNET is assigned but idle")
		assert.False(t, prim[0].Fallback,
			"BirdNET is a genuine assignment here, not a runtime fallback")

		perch := got[classifier.RegistryIDPerchV2]
		require.Len(t, perch, 1)
		assert.True(t, perch[0].NotRunning, "Perch is assigned but idle")
	})

	t.Run("nil live state keeps the config-derived view unmarked", func(t *testing.T) {
		t.Parallel()

		got := buildSourceAttachments(settings, models, primaryID, nil)

		perch := got[classifier.RegistryIDPerchV2]
		require.Len(t, perch, 1)
		assert.False(t, perch[0].NotRunning,
			"without live evidence nothing may be claimed to be broken")
	})

	t.Run("source absent from a non-nil running map stays unmarked", func(t *testing.T) {
		t.Parallel()

		// running is non-nil but does not contain "Front Yard": the branch a
		// DisplayName collision or an omitted (empty-buffer) source lands in. Without
		// live evidence for THIS source, nothing may be marked not running, even
		// though live evidence exists for a different source.
		running := map[string]map[string]bool{"A Different Source": {primaryID: true}}

		got := buildSourceAttachments(settings, models, primaryID, running)

		perch := got[classifier.RegistryIDPerchV2]
		require.Len(t, perch, 1)
		assert.False(t, perch[0].NotRunning,
			"a source absent from a non-nil running map has no live evidence")

		prim := got[primaryID]
		require.Len(t, prim, 1)
		assert.False(t, prim[0].NotRunning)
	})
}

// TestBuildSourceAttachments_RTSPStream covers an RTSP stream source (the
// reporters run RTSP; only audio.sources were covered before). An assigned model
// that the router does not feed for the stream is marked NotRunning.
func TestBuildSourceAttachments_RTSPStream(t *testing.T) {
	t.Parallel()

	const primaryID = classifier.DefaultModelVersion
	models := []classifier.ModelInfo{
		{ID: primaryID},
		{ID: classifier.RegistryIDPerchV2},
	}

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{Name: "Cam1", Type: "rtsp", Models: []string{conf.ModelIDBirdNET, conf.ModelIDPerchV2}},
	}

	// The router feeds only BirdNET for this stream; Perch is assigned but idle.
	running := map[string]map[string]bool{"Cam1": {primaryID: true}}

	got := buildSourceAttachments(settings, models, primaryID, running)

	perch := got[classifier.RegistryIDPerchV2]
	require.Len(t, perch, 1)
	assert.Equal(t, "Cam1", perch[0].Name)
	assert.Equal(t, "rtsp", perch[0].Type)
	assert.True(t, perch[0].NotRunning, "the assigned but unrouted Perch model on the RTSP stream")

	prim := got[primaryID]
	require.Len(t, prim, 1)
	assert.False(t, prim[0].NotRunning, "BirdNET is genuinely routed for the stream")
}

// TestBuildSourceAttachments_FallbackRowCarriesLiveness pins that the
// primary-fallback attachment is subject to the same liveness verdict as an
// explicitly assigned row. A source that resolves to no loaded target is
// analyzed by the primary model, so if the primary's own analysis buffer is
// absent the source is not being analyzed at all. Reporting that row as healthy
// reproduces the "looks running while analyzing nothing" state this endpoint
// exists to remove, just on the fallback branch. Caught on PR review.
func TestBuildSourceAttachments_FallbackRowCarriesLiveness(t *testing.T) {
	t.Parallel()

	const primaryID = classifier.DefaultModelVersion
	// Only the primary is loaded, so a source assigning Perch resolves to nothing
	// and takes the primary-fallback branch.
	models := []classifier.ModelInfo{{ID: primaryID}}

	settings := &conf.Settings{}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Front Yard", Models: []string{conf.ModelIDPerchV2}},
	}

	t.Run("fallback row is marked not running when the primary has no buffer", func(t *testing.T) {
		t.Parallel()

		// The router knows this source and feeds some other model on it, but not the
		// primary, so live evidence exists and it says the primary is not fed.
		running := map[string]map[string]bool{
			"Front Yard": {"SomeOtherModel": true},
		}

		got := buildSourceAttachments(settings, models, primaryID, running)

		prim := got[primaryID]
		require.Len(t, prim, 1)
		assert.True(t, prim[0].Fallback, "this row is the primary fallback")
		assert.True(t, prim[0].NotRunning,
			"the primary analyzes this source, so an absent primary buffer means it is not analyzing")
	})

	t.Run("fallback row is running when the primary is routed", func(t *testing.T) {
		t.Parallel()

		running := map[string]map[string]bool{
			"Front Yard": {primaryID: true},
		}

		got := buildSourceAttachments(settings, models, primaryID, running)

		prim := got[primaryID]
		require.Len(t, prim, 1)
		assert.True(t, prim[0].Fallback)
		assert.False(t, prim[0].NotRunning,
			"the primary really is routed for this source")
	})

	t.Run("fallback row makes no claim without live evidence", func(t *testing.T) {
		t.Parallel()

		// A nil router map means the pipeline has not reported yet. Absence of
		// evidence must not be rendered as a failure.
		got := buildSourceAttachments(settings, models, primaryID, nil)

		prim := got[primaryID]
		require.Len(t, prim, 1)
		assert.True(t, prim[0].Fallback)
		assert.False(t, prim[0].NotRunning,
			"with no live evidence the row must not assert that the model is failing")
	})
}

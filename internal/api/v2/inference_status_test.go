// internal/api/v2/inference_status_test.go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/conf"
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

	got := buildSourceAttachments(settings, models, primaryID)

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

	got := buildSourceAttachments(settings, models, primaryID)

	// Perch_V2 should have NO attachments (not loaded).
	perch := got[classifier.RegistryIDPerchV2]
	assert.Empty(t, perch, "Perch_V2 attachments must be empty (Perch not loaded)")

	// Primary should have Studio as a fallback.
	prim := got[primaryID]
	require.Len(t, prim, 1, "primary attachments must have 1 entry (Studio)")
	assert.Equal(t, "Studio", prim[0].Name, "primary source name")
	assert.True(t, prim[0].Fallback, "primary source must be a fallback")
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
	snap := inferencestats.PeekSnapshot{InvokeCount: 1000, InvokeTotalUs: 47_200_000, InvokeMaxUs: 130_000}
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

// TestGetInferenceStatus_HTTP200 verifies that GetInferenceStatus returns HTTP
// 200 and a valid InferenceStatusResponse with TFLite marked available. It uses
// the shared setupTestEnvironment harness (minimalController + Echo) to exercise
// the handler over httptest without starting any background goroutines.
func TestGetInferenceStatus_HTTP200(t *testing.T) {
	// NOT parallel: publishTestSettings in setupTestEnvironment mutates global state.
	e, _, controller := setupTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/system/inference", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.GetInferenceStatus(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp InferenceStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), "response body must unmarshal to InferenceStatusResponse")
	assert.True(t, resp.Backends.TFLite.Available, "TFLite backend must always report Available=true")
	assert.NotZero(t, resp.SnapshotAtUnix, "SnapshotAtUnix must be a non-zero Unix timestamp")
}

// eventInferenceTopologyChangedName is asserted against the package constant so
// the SSE event name stays the single source of truth shared with the frontend.
const eventInferenceTopologyChangedName = "system.inference_topology_changed"

// TestBroadcastInferenceTopologyChanged_ReachesConsumer verifies that the
// controller broadcast reaches a topology subscriber on the wired metrics store.
func TestBroadcastInferenceTopologyChanged_ReachesConsumer(t *testing.T) {
	t.Parallel()

	// Constant matches the shared event-name contract.
	assert.Equal(t, eventInferenceTopologyChangedName, eventInferenceTopologyChanged)

	store := observability.NewMemoryStore(10)
	controller := &Controller{metricsStore: store}

	topoCh, cancel := store.SubscribeTopology()
	t.Cleanup(cancel)

	controller.BroadcastInferenceTopologyChanged()

	select {
	case <-topoCh:
		// Expected: broadcast reached the subscriber.
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for topology broadcast to reach the subscriber")
	}
}

// TestBroadcastInferenceTopologyChanged_NilSafe verifies the broadcast is a
// no-op (no panic) when the controller or its metrics store is nil.
func TestBroadcastInferenceTopologyChanged_NilSafe(t *testing.T) {
	t.Parallel()

	var nilController *Controller
	assert.NotPanics(t, nilController.BroadcastInferenceTopologyChanged)

	noStore := &Controller{}
	assert.NotPanics(t, noStore.BroadcastInferenceTopologyChanged)
}

// TestGetInferenceStatus_AudioBlock verifies that GetInferenceStatus returns an
// audio block with the expected metric key for queue depth and a non-negative
// queue capacity matching RouteInboxCapacity.
func TestGetInferenceStatus_AudioBlock(t *testing.T) {
	// NOT parallel: publishTestSettings in setupTestEnvironment mutates global state.
	e, _, controller := setupTestEnvironment(t)

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
		},
	}

	got := buildModelStatus(&info, inferencestats.PeekSnapshot{}, nil, nil, nil, lastDetections)

	require.NotNil(t, got.LastDetection, "lastDetection must be non-nil when cache has entry")
	assert.Equal(t, "European Robin", got.LastDetection.Species)
	assert.Equal(t, "Erithacus rubecula", got.LastDetection.ScientificName)
	assert.InDelta(t, 0.92, got.LastDetection.Confidence, 0.001)
	assert.Equal(t, int64(1718000000), got.LastDetection.AtUnix)
}

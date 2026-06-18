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
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBuildSourceAttachments verifies that buildSourceAttachments correctly
// routes audio sources to their configured model, or to the primary fallback
// when no configured model resolves to a loaded model.
func TestBuildSourceAttachments(t *testing.T) {
	t.Parallel()

	// "BirdNET_V2.4" has no exported constant; use the literal that ModelRegistry
	// uses as its key (verified in internal/classifier/model_registry.go line 131).
	const primaryID = "BirdNET_V2.4"

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
	if len(perch) != 1 || perch[0].Name != "Front Yard" || perch[0].Fallback {
		t.Fatalf("Perch_V2 attachments = %+v, want [{Name:Front Yard Fallback:false}]", perch)
	}

	// Primary should have Garage and Cam1, both as fallbacks.
	prim := got[primaryID]
	if len(prim) != 2 {
		t.Fatalf("primary attachments = %+v, want 2 entries (Garage, Cam1)", prim)
	}
	for _, s := range prim {
		if !s.Fallback {
			t.Fatalf("primary attachment %q has Fallback=false, want true", s.Name)
		}
	}
}

// TestBuildSourceAttachments_ResolvesButNotLoaded verifies that a source whose
// config model alias resolves to a registry ID, but that registry ID is NOT in
// the loaded models list, falls back to primary with Fallback=true. This catches
// regressions where the guard `ok && loaded[regID]` is loosened to just `ok`.
func TestBuildSourceAttachments_ResolvesButNotLoaded(t *testing.T) {
	t.Parallel()

	const primaryID = "BirdNET_V2.4"

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
	if len(perch) != 0 {
		t.Fatalf("Perch_V2 attachments = %+v, want empty (Perch not loaded)", perch)
	}

	// Primary should have Studio as a fallback.
	prim := got[primaryID]
	if len(prim) != 1 {
		t.Fatalf("primary attachments = %+v, want 1 entry (Studio)", prim)
	}
	if prim[0].Name != "Studio" || !prim[0].Fallback {
		t.Fatalf("primary[0] = {Name:%q Fallback:%v}, want {Name:Studio Fallback:true}", prim[0].Name, prim[0].Fallback)
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
	snap := inferencestats.PeekSnapshot{InvokeCount: 1000, InvokeTotalUs: 47_200_000, InvokeMaxUs: 130_000}
	rss := map[string]int64{"BirdNET_V2.4": rssVal}

	got := buildModelStatus(&info, snap, rss, nil)

	if got.Stats.Invocations != 1000 {
		t.Errorf("invocations = %d, want 1000", got.Stats.Invocations)
	}
	if got.Stats.AvgMs < 47.1 || got.Stats.AvgMs > 47.3 {
		t.Errorf("avgMs = %v, want ~47.2", got.Stats.AvgMs)
	}
	if got.Stats.MaxMs != 130 {
		t.Errorf("maxMs = %v, want 130", got.Stats.MaxMs)
	}
	if got.Stats.RTF == nil || *got.Stats.RTF < 0.0156 || *got.Stats.RTF > 0.0158 {
		t.Errorf("rtf = %v, want ~0.0157", got.Stats.RTF)
	}
	if got.Memory.ApproxRssBytes == nil || *got.Memory.ApproxRssBytes != rssVal {
		t.Errorf("approxRssBytes = %v, want %d", got.Memory.ApproxRssBytes, rssVal)
	}
	if got.MetricKeys.RTF != "inference.BirdNET_V2_4.rtf" {
		t.Errorf("metricKeys.rtf = %q", got.MetricKeys.RTF)
	}
}

// TestBuildModelStatus_ZeroInvocations verifies that buildModelStatus returns
// nil RTF and nil ApproxRssBytes when there are no invocations or no RSS data.
func TestBuildModelStatus_ZeroInvocations(t *testing.T) {
	t.Parallel()
	info := classifier.ModelInfo{ID: "X", Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}}
	got := buildModelStatus(&info, inferencestats.PeekSnapshot{}, nil, nil)
	if got.Stats.RTF != nil {
		t.Error("rtf must be nil with zero invocations (no divide-by-zero)")
	}
	if got.Memory.ApproxRssBytes != nil {
		t.Error("approxRssBytes must be nil when RSS unavailable")
	}
	if !got.Memory.Approximate {
		t.Error("memory.approximate must always be true")
	}
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

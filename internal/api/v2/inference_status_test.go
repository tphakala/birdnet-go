// internal/api/v2/inference_status_test.go
package api

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/classifier"
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

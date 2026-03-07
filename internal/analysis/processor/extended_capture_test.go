package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestIsExtendedCaptureSpecies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		enabled        bool
		allSpecies     bool
		speciesMap     map[string]bool
		scientificName string
		expected       bool
	}{
		{
			name:           "disabled returns false",
			enabled:        false,
			scientificName: "Strix aluco",
			expected:       false,
		},
		{
			name:           "all species mode returns true",
			enabled:        true,
			allSpecies:     true,
			scientificName: "Strix aluco",
			expected:       true,
		},
		{
			name:           "matching species returns true",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"strix aluco": true},
			scientificName: "Strix aluco",
			expected:       true,
		},
		{
			name:           "non-matching species returns false",
			enabled:        true,
			allSpecies:     false,
			speciesMap:     map[string]bool{"strix aluco": true},
			scientificName: "Parus major",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: tt.enabled},
					},
				},
				extendedCaptureAll:     tt.allSpecies,
				extendedCaptureSpecies: tt.speciesMap,
			}
			assert.Equal(t, tt.expected, p.isExtendedCaptureSpecies(tt.scientificName))
		})
	}
}

func TestResolveExtendedCaptureFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configSpecies []string
		labels        []string
		expectAll     bool
		expectSpecies []string
	}{
		{
			name:          "empty list means all species",
			configSpecies: []string{},
			labels:        []string{},
			expectAll:     true,
		},
		{
			name:          "nil list means all species",
			configSpecies: nil,
			labels:        []string{},
			expectAll:     true,
		},
		{
			name:          "common name resolved via labels",
			configSpecies: []string{"Eurasian Eagle-Owl"},
			labels:        []string{"Bubo bubo_Eurasian Eagle-Owl", "Strix aluco_Tawny Owl"},
			expectAll:     false,
			expectSpecies: []string{"bubo bubo"},
		},
		{
			name:          "scientific name resolved directly",
			configSpecies: []string{"Bubo bubo"},
			labels:        []string{"Bubo bubo_Eurasian Eagle-Owl"},
			expectAll:     false,
			expectSpecies: []string{"bubo bubo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			isAll, resolved := resolveSpeciesFilter(tt.configSpecies, tt.labels, nil, "test")
			assert.Equal(t, tt.expectAll, isAll)
			if !tt.expectAll {
				for _, expected := range tt.expectSpecies {
					assert.True(t, resolved[expected],
						"expected %q in resolved set", expected)
				}
			}
		})
	}
}

func TestResolveExtendedCaptureFilter_WithTaxonomy(t *testing.T) {
	t.Parallel()

	db, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err)

	// Resolve "Strigidae" (owl family) via taxonomy DB
	isAll, resolved := resolveSpeciesFilter([]string{"Strigidae"}, nil, db, "test")
	assert.False(t, isAll)
	assert.NotEmpty(t, resolved)
	// Should include well-known owls
	assert.True(t, resolved["strix aluco"] || resolved["bubo bubo"] || len(resolved) > 5,
		"Strigidae should resolve to multiple owl species, got %d", len(resolved))
}

func TestResolveExtendedCaptureFilter_WithGenus(t *testing.T) {
	t.Parallel()

	db, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err)

	// Resolve "Strix" (genus) via taxonomy DB — should include Tawny Owl, Ural Owl, etc.
	isAll, resolved := resolveSpeciesFilter([]string{"Strix"}, nil, db, "test")
	assert.False(t, isAll)
	assert.NotEmpty(t, resolved)
	assert.True(t, resolved["strix aluco"],
		"Strix genus should include Strix aluco (Tawny Owl), got %v", resolved)
}

func TestExtendedCapture_FlushDeadlineExtension(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				ExtendedCapture: conf.ExtendedCaptureSettings{
					Enabled:     true,
					MaxDuration: 600, // 10 minutes
				},
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{Length: 15, PreCapture: 6},
				},
			},
		},
		pendingDetections:  make(map[string]PendingDetection),
		extendedCaptureAll: true,
	}

	species := "tawny owl"
	sourceID := "test_source"
	mapKey := pendingDetectionKey(sourceID, species)
	now := time.Now()

	// First detection: should get minimum 15s deadline
	p.pendingDetections[mapKey] = PendingDetection{
		Confidence:    0.85,
		Source:        sourceID,
		FirstDetected: now,
		FlushDeadline: now.Add(9 * time.Second), // Normal detection window
		Count:         1,
	}

	// Apply extended capture logic
	p.applyExtendedCapture(mapKey, now, 9*time.Second)
	item := p.pendingDetections[mapKey]

	assert.True(t, item.ExtendedCapture)
	assert.False(t, item.MaxDeadline.IsZero())
	// Initial deadline should be at least 15s from now
	assert.GreaterOrEqual(t, item.FlushDeadline.Sub(now), 15*time.Second)

	// Simulate detection 45 seconds later (medium session phase)
	later := now.Add(45 * time.Second)
	item.Count = 5
	item.LastUpdated = later
	p.pendingDetections[mapKey] = item

	p.applyExtendedCapture(mapKey, later, 9*time.Second)
	item = p.pendingDetections[mapKey]

	// Should now wait 30s (medium phase)
	assert.GreaterOrEqual(t, item.FlushDeadline.Sub(later), 30*time.Second)
}

func TestProcessorInitExtendedCapture(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				ExtendedCapture: conf.ExtendedCaptureSettings{
					Enabled: true,
					Species: []string{},
				},
			},
		},
	}

	p.initExtendedCapture()

	assert.True(t, p.extendedCaptureAll)
	assert.Nil(t, p.extendedCaptureSpecies)
}

func TestProcessorInitExtendedCapture_Disabled(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: false},
			},
		},
	}

	p.initExtendedCapture()

	assert.False(t, p.extendedCaptureAll)
	assert.Nil(t, p.extendedCaptureSpecies)
}

func TestExtendedCapture_EndToEnd_ContinuousSession(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				ExtendedCapture: conf.ExtendedCaptureSettings{
					Enabled:     true,
					MaxDuration: 300, // 5 minutes
				},
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{Length: 15, PreCapture: 6},
				},
			},
		},
		pendingDetections:  make(map[string]PendingDetection),
		extendedCaptureAll: true,
	}

	species := "strix uralensis"
	sourceID := "test_mic"
	mapKey := pendingDetectionKey(sourceID, species)
	detectionWindow := 9 * time.Second // 15 - 6

	// Simulate 20 detections over 2 minutes
	startTime := time.Now()
	for i := range 20 {
		now := startTime.Add(time.Duration(i) * 6 * time.Second) // Every 6 seconds

		if _, exists := p.pendingDetections[mapKey]; !exists {
			p.pendingDetections[mapKey] = PendingDetection{
				Confidence:    0.85,
				Source:        sourceID,
				FirstDetected: now,
				FlushDeadline: now.Add(detectionWindow),
				Count:         1,
			}
		} else {
			item := p.pendingDetections[mapKey]
			item.Count++
			item.LastUpdated = now
			if item.Confidence < 0.92 {
				item.Confidence = 0.92
			}
			p.pendingDetections[mapKey] = item
		}

		p.applyExtendedCapture(mapKey, now, detectionWindow)
	}

	item := p.pendingDetections[mapKey]

	assert.True(t, item.ExtendedCapture)
	assert.Equal(t, 20, item.Count)
	assert.InDelta(t, 0.92, item.Confidence, 1e-9)

	// Flush deadline should be well after the last detection
	lastDetectionTime := startTime.Add(19 * 6 * time.Second)
	assert.True(t, item.FlushDeadline.After(lastDetectionTime),
		"FlushDeadline %v should be after last detection %v", item.FlushDeadline, lastDetectionTime)

	// MaxDeadline should be FirstDetected + maxDuration
	expectedMaxDeadline := item.FirstDetected.Add(300 * time.Second)
	assert.Equal(t, expectedMaxDeadline, item.MaxDeadline)
}

func TestExtendedCapture_MultiSource_Independence(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				ExtendedCapture: conf.ExtendedCaptureSettings{
					Enabled:     true,
					MaxDuration: 120,
				},
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{Length: 15, PreCapture: 6},
				},
			},
		},
		pendingDetections:  make(map[string]PendingDetection),
		extendedCaptureAll: true,
	}

	species := "strix aluco"
	now := time.Now()

	// Source A detection
	keyA := pendingDetectionKey("mic_a", species)
	p.pendingDetections[keyA] = PendingDetection{
		Source: "mic_a", FirstDetected: now, Count: 1,
		FlushDeadline: now.Add(9 * time.Second),
	}
	p.applyExtendedCapture(keyA, now, 9*time.Second)

	// Source B detection 5 seconds later
	keyB := pendingDetectionKey("mic_b", species)
	p.pendingDetections[keyB] = PendingDetection{
		Source: "mic_b", FirstDetected: now.Add(5 * time.Second), Count: 1,
		FlushDeadline: now.Add(14 * time.Second),
	}
	p.applyExtendedCapture(keyB, now.Add(5*time.Second), 9*time.Second)

	// Verify independence
	require.Len(t, p.pendingDetections, 2)
	assert.Equal(t, "mic_a", p.pendingDetections[keyA].Source)
	assert.Equal(t, "mic_b", p.pendingDetections[keyB].Source)
	assert.NotEqual(t, p.pendingDetections[keyA].FirstDetected,
		p.pendingDetections[keyB].FirstDetected)
}

func TestCalculateExtendedFlushDeadline(t *testing.T) {
	t.Parallel()

	now := time.Now()
	maxDuration := 10 * time.Minute
	normalDetectionWindow := 5 * time.Second

	tests := []struct {
		name            string
		firstDetected   time.Time
		now             time.Time
		maxDeadline     time.Time
		expectedMinWait time.Duration
		expectedMaxWait time.Duration
	}{
		{
			name:            "short session under 30s uses minimum 15s",
			firstDetected:   now.Add(-10 * time.Second),
			now:             now,
			maxDeadline:     now.Add(maxDuration),
			expectedMinWait: 15 * time.Second,
			expectedMaxWait: 15 * time.Second,
		},
		{
			name:            "medium session 30s-2m waits 30s",
			firstDetected:   now.Add(-45 * time.Second),
			now:             now,
			maxDeadline:     now.Add(maxDuration),
			expectedMinWait: 30 * time.Second,
			expectedMaxWait: 30 * time.Second,
		},
		{
			name:            "long session over 2m waits 60s",
			firstDetected:   now.Add(-3 * time.Minute),
			now:             now,
			maxDeadline:     now.Add(maxDuration),
			expectedMinWait: 60 * time.Second,
			expectedMaxWait: 60 * time.Second,
		},
		{
			name:            "capped at maxDeadline",
			firstDetected:   now.Add(-9*time.Minute - 50*time.Second),
			now:             now,
			maxDeadline:     now.Add(10 * time.Second),
			expectedMinWait: 0,
			expectedMaxWait: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deadline := calculateExtendedFlushDeadline(
				tt.now, tt.firstDetected, tt.maxDeadline, normalDetectionWindow,
			)
			waitTime := deadline.Sub(tt.now)
			assert.GreaterOrEqual(t, waitTime, tt.expectedMinWait,
				"wait time %v should be >= %v", waitTime, tt.expectedMinWait)
			assert.LessOrEqual(t, waitTime, tt.expectedMaxWait,
				"wait time %v should be <= %v", waitTime, tt.expectedMaxWait)
		})
	}
}

func TestNormalizeDetectionTimes_NonExtendedCapture(t *testing.T) {
	t.Parallel()

	// Settings: captureLength=60, preCapture=6, normalWindow=54s
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{Length: 60, PreCapture: 6},
				},
				ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: false},
			},
		},
	}

	firstDetected := time.Date(2026, 3, 7, 8, 50, 0, 0, time.UTC)
	captureWindow := 54 * time.Second // Length - PreCapture = 60 - 6

	// Simulate: Detection struct was replaced by a higher-confidence detection
	// that arrived 30s into the pending window. Its BeginTime/EndTime reflect
	// the later analysis chunk, not the first detection.
	laterStart := firstDetected.Add(30 * time.Second)
	item := &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				BeginTime: laterStart,
				EndTime:   laterStart.Add(captureWindow),
				Species: detection.Species{
					CommonName:     "käpytikka",
					ScientificName: "Dendrocopos major",
				},
			},
		},
		Confidence:      0.85,
		Source:          "test_source",
		FirstDetected:   firstDetected,
		LastUpdated:     laterStart,
		Count:           27,
		ExtendedCapture: false,
	}

	p.normalizeDetectionTimes(item)

	// BeginTime should be backdated to FirstDetected
	assert.Equal(t, firstDetected, item.Detection.Result.BeginTime)

	// EndTime should be recalculated: FirstDetected + (Length - PreCapture)
	expectedEndTime := firstDetected.Add(captureWindow)
	assert.Equal(t, expectedEndTime, item.Detection.Result.EndTime,
		"EndTime should be recalculated from FirstDetected for non-extended species")

	// The derived capture length should equal the configured length
	preCapture := p.Settings.Realtime.Audio.Export.PreCapture
	derivedLength := int(item.Detection.Result.EndTime.Sub(item.Detection.Result.BeginTime).Seconds()) + preCapture
	assert.Equal(t, p.Settings.Realtime.Audio.Export.Length, derivedLength,
		"derived capture length should match configured export length")
}

func TestNormalizeDetectionTimes_ExtendedCapture(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{Length: 60, PreCapture: 6},
				},
				ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: true, MaxDuration: 600},
			},
		},
	}

	firstDetected := time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC)
	lastUpdated := firstDetected.Add(3 * time.Minute)

	item := &PendingDetection{
		Detection: Detections{
			Result: detection.Result{
				BeginTime: firstDetected.Add(30 * time.Second),
				EndTime:   firstDetected.Add(84 * time.Second),
				Species: detection.Species{
					CommonName:     "lehtopöllö",
					ScientificName: "Strix aluco",
				},
			},
		},
		Confidence:      0.92,
		Source:          "test_source",
		FirstDetected:   firstDetected,
		LastUpdated:     lastUpdated,
		Count:           45,
		ExtendedCapture: true,
	}

	p.normalizeDetectionTimes(item)

	// BeginTime should be backdated to FirstDetected
	assert.Equal(t, firstDetected, item.Detection.Result.BeginTime)

	// EndTime should be LastUpdated + normalDetectionWindow (54s)
	expectedEndTime := lastUpdated.Add(54 * time.Second)
	assert.Equal(t, expectedEndTime, item.Detection.Result.EndTime,
		"EndTime should span from LastUpdated + normal window for extended capture")

	// Duration should be > configured length (this is the whole point of extended capture)
	preCapture := p.Settings.Realtime.Audio.Export.PreCapture
	derivedLength := int(item.Detection.Result.EndTime.Sub(item.Detection.Result.BeginTime).Seconds()) + preCapture
	assert.Greater(t, derivedLength, p.Settings.Realtime.Audio.Export.Length,
		"extended capture should produce longer duration than configured length")
}

package audiocore

import (
	"context"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
)

// TestProcessingPipelineQueueIntegration tests that audiocore results are sent to birdnet.ResultsQueue
func TestProcessingPipelineQueueIntegration(t *testing.T) {
	// Save original queue and restore after test
	originalQueue := birdnet.ResultsQueue
	defer func() {
		birdnet.ResultsQueue = originalQueue
	}()

	// Create a test queue
	testQueue := make(chan birdnet.Results, 10)
	birdnet.ResultsQueue = testQueue

	// Create mock components
	source := &mockAudioSource{
		id:     "test-source",
		format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
	}

	analyzer := &mockQueueAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			// Return a mock detection
			return AnalysisResult{
				Timestamp: data.Timestamp,
				Duration:  data.Duration,
				Detections: []Detection{
					{
						Label:      "Test Bird",
						Confidence: 0.95,
						Attributes: map[string]any{
							"species_string":  "Testus_birdus_Test Bird",
							"scientific_name": "Testus birdus",
							"common_name":     "Test Bird",
							"species_code":    "TESBIR",
						},
					},
				},
				Metadata: map[string]any{
					"processingTime": 100 * time.Millisecond,
				},
				AnalyzerID: "test-analyzer",
				SourceID:   data.SourceID,
			}, nil
		},
	}

	// Create pipeline
	config := &ProcessingPipelineConfig{
		Source:     source,
		Analyzer:   analyzer,
		BufferPool: NewBufferPool(BufferPoolConfig{}),
		Config: ProcessingConfig{
			ChunkDuration:  3 * time.Second,
			OverlapPercent: 0.1,
		},
	}

	pipeline, err := NewProcessingPipeline(config)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	// Start pipeline
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = pipeline.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}
	defer pipeline.Stop()

	// Send test audio data
	testData := AudioData{
		Buffer:    make([]byte, 1024), // Test PCM data
		Format:    source.format,
		Timestamp: time.Now(),
		Duration:  3 * time.Second,
		SourceID:  "test-source",
	}

	// Process the test data
	pipeline.processAnalysisResult(&AnalysisResult{
		Timestamp: testData.Timestamp,
		Duration:  testData.Duration,
		Detections: []Detection{
			{
				Label:      "Test Bird",
				Confidence: 0.95,
				Attributes: map[string]any{
					"species_string":  "Testus_birdus_Test Bird",
					"scientific_name": "Testus birdus",
					"common_name":     "Test Bird",
					"species_code":    "TESBIR",
				},
			},
		},
		Metadata: map[string]any{
			"processingTime": 100 * time.Millisecond,
		},
		AnalyzerID: "test-analyzer",
		SourceID:   testData.SourceID,
	}, &testData)

	// Wait for result in queue
	select {
	case result := <-testQueue:
		// Verify the result
		if result.Source != "test-source" {
			t.Errorf("Expected source 'test-source', got '%s'", result.Source)
		}
		if len(result.Results) != 1 {
			t.Errorf("Expected 1 detection, got %d", len(result.Results))
		}
		if result.Results[0].Species != "Testus_birdus_Test Bird" {
			t.Errorf("Expected species 'Testus_birdus_Test Bird', got '%s'", result.Results[0].Species)
		}
		if result.Results[0].Confidence != 0.95 {
			t.Errorf("Expected confidence 0.95, got %f", result.Results[0].Confidence)
		}
		if len(result.PCMdata) != 1024 {
			t.Errorf("Expected PCM data length 1024, got %d", len(result.PCMdata))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for result in queue")
	}
}

// mockQueueAnalyzer for testing with custom analyze function
type mockQueueAnalyzer struct {
	id          string
	analyzeFunc func(ctx context.Context, data *AudioData) (AnalysisResult, error)
}

func (m *mockQueueAnalyzer) ID() string { return m.id }
func (m *mockQueueAnalyzer) Analyze(ctx context.Context, data *AudioData) (AnalysisResult, error) {
	if m.analyzeFunc != nil {
		return m.analyzeFunc(ctx, data)
	}
	return AnalysisResult{}, nil
}
func (m *mockQueueAnalyzer) GetRequiredFormat() AudioFormat {
	return AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16}
}
func (m *mockQueueAnalyzer) GetConfiguration() AnalyzerConfig {
	return AnalyzerConfig{Type: "mock"}
}
func (m *mockQueueAnalyzer) Close() error { return nil }

// Test that species string fallback works correctly
func TestSpeciesStringFallback(t *testing.T) {
	// Save original queue and restore after test
	originalQueue := birdnet.ResultsQueue
	defer func() {
		birdnet.ResultsQueue = originalQueue
	}()

	// Create a test queue
	testQueue := make(chan birdnet.Results, 10)
	birdnet.ResultsQueue = testQueue

	// Create test pipeline
	source := &mockAudioSource{
		id:     "test-source",
		format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
	}
	
	config := &ProcessingPipelineConfig{
		Source:     source,
		Analyzer:   &mockQueueAnalyzer{id: "test"},
		BufferPool: NewBufferPool(BufferPoolConfig{}),
		Config: ProcessingConfig{
			ChunkDuration: 3 * time.Second,
		},
	}

	pipeline, _ := NewProcessingPipeline(config)
	ctx := context.Background()
	pipeline.Start(ctx)
	defer pipeline.Stop()

	testCases := []struct {
		name           string
		detection      Detection
		expectedSpecies string
	}{
		{
			name: "with species_string attribute",
			detection: Detection{
				Label:      "Common Bird",
				Confidence: 0.9,
				Attributes: map[string]any{
					"species_string": "Birdus_commonus_Common Bird",
				},
			},
			expectedSpecies: "Birdus_commonus_Common Bird",
		},
		{
			name: "fallback to scientific_common reconstruction",
			detection: Detection{
				Label:      "Another Bird",
				Confidence: 0.85,
				Attributes: map[string]any{
					"scientific_name": "Birdus anothericus",
					"common_name":     "Another Bird",
				},
			},
			expectedSpecies: "Birdus anothericus_Another Bird",
		},
		{
			name: "fallback to label only",
			detection: Detection{
				Label:      "Mystery Bird",
				Confidence: 0.8,
				Attributes: map[string]any{},
			},
			expectedSpecies: "Mystery Bird",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear queue
			select {
			case <-testQueue:
			default:
			}

			// Process result
			result := &AnalysisResult{
				Timestamp:  time.Now(),
				Duration:   3 * time.Second,
				Detections: []Detection{tc.detection},
				SourceID:   "test-source",
			}
			chunk := &AudioData{
				Buffer: make([]byte, 100),
			}

			pipeline.processAnalysisResult(result, chunk)

			// Check queue
			select {
			case queueResult := <-testQueue:
				if len(queueResult.Results) != 1 {
					t.Fatalf("Expected 1 result, got %d", len(queueResult.Results))
				}
				if queueResult.Results[0].Species != tc.expectedSpecies {
					t.Errorf("Expected species '%s', got '%s'", 
						tc.expectedSpecies, queueResult.Results[0].Species)
				}
			case <-time.After(1 * time.Second):
				t.Fatal("Timeout waiting for result")
			}
		})
	}
}
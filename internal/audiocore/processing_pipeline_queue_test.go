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
						Label:      "Testus_birdus_Test Bird", // Species string in BirdNET format
						Confidence: 0.95,
						Attributes: nil, // No longer needed
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
	defer func() {
		if err := pipeline.Stop(); err != nil {
			t.Errorf("Failed to stop pipeline: %v", err)
		}
	}()

	// Send test audio data with known pattern for verification
	testPCMData := make([]byte, 1024)
	// Fill with a recognizable pattern
	for i := range testPCMData {
		testPCMData[i] = byte(i % 256)
	}
	
	testData := AudioData{
		Buffer:    testPCMData,
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
				Label:      "Testus_birdus_Test Bird",
				Confidence: 0.95,
				Attributes: nil,
			},
		},
		Metadata: map[string]any{
			"processingTime": 100 * time.Millisecond,
		},
		AnalyzerID: "test-analyzer",
		SourceID:   testData.SourceID,
	}, &testData)

	// Create a done channel for deterministic synchronization
	done := make(chan struct{})
	
	// Start a goroutine to wait for the result
	go func() {
		result := <-testQueue
		
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
		// Verify PCM data content matches the pattern
		for i := 0; i < len(result.PCMdata); i++ {
			expected := byte(i % 256)
			if result.PCMdata[i] != expected {
				t.Errorf("PCM data mismatch at index %d: expected %d, got %d", i, expected, result.PCMdata[i])
				break // Don't flood with errors
			}
		}
		
		close(done)
	}()
	
	// Wait for completion with a reasonable timeout
	testCtx, testCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer testCancel()
	
	select {
	case <-done:
		// Test completed successfully
	case <-testCtx.Done():
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

// Test that simplified species string handling works correctly
func TestSimplifiedSpeciesHandling(t *testing.T) {
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

	pipeline, err := NewProcessingPipeline(config)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}
	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}
	defer func() {
		if err := pipeline.Stop(); err != nil {
			t.Errorf("Failed to stop pipeline: %v", err)
		}
	}()

	testCases := []struct {
		name           string
		detection      Detection
		expectedSpecies string
	}{
		{
			name: "standard BirdNET format",
			detection: Detection{
				Label:      "Turdus_migratorius_American Robin",
				Confidence: 0.9,
				Attributes: nil,
			},
			expectedSpecies: "Turdus_migratorius_American Robin",
		},
		{
			name: "species with spaces",
			detection: Detection{
				Label:      "Passer_domesticus_House Sparrow",
				Confidence: 0.85,
				Attributes: nil,
			},
			expectedSpecies: "Passer_domesticus_House Sparrow",
		},
		{
			name: "direct label passthrough",
			detection: Detection{
				Label:      "Custom_Species_Format",
				Confidence: 0.8,
				Attributes: nil,
			},
			expectedSpecies: "Custom_Species_Format",
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
			// Create test data with a different pattern
			testBuffer := make([]byte, 100)
			for i := range testBuffer {
				testBuffer[i] = byte((i * 2) % 256) // Different pattern
			}
			chunk := &AudioData{
				Buffer: testBuffer,
			}

			pipeline.processAnalysisResult(result, chunk)

			// Check queue with deterministic synchronization
			done := make(chan struct{})
			
			go func() {
				queueResult := <-testQueue
				if len(queueResult.Results) != 1 {
					t.Errorf("Expected 1 result, got %d", len(queueResult.Results))
				} else if queueResult.Results[0].Species != tc.expectedSpecies {
					t.Errorf("Expected species '%s', got '%s'", 
						tc.expectedSpecies, queueResult.Results[0].Species)
				}
				close(done)
			}()
			
			// Use context for timeout
			testCtx, testCancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer testCancel()
			
			select {
			case <-done:
				// Success
			case <-testCtx.Done():
				t.Fatal("Timeout waiting for result")
			}
		})
	}
}
package audiocore

import (
	"context"
	"sync"
	"testing"
	"time"
)

// waitForCondition polls a condition with a specified interval until it returns true or timeout
func waitForCondition(t *testing.T, timeout, pollInterval time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(pollInterval)
	}
	
	t.Fatalf("timeout waiting for %s after %v", description, timeout)
}

// TestProcessingPipelineBasic tests basic pipeline functionality
func TestProcessingPipelineBasic(t *testing.T) {
	t.Parallel()

	// Create mock components
	source := &mockAudioSource{
		id:     "test-source",
		format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
		output: make(chan AudioData, 10),
		errors: make(chan error, 1),
	}

	analyzer := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			return AnalysisResult{
				Detections: []Detection{{Label: "test", Confidence: 0.9}},
			}, nil
		},
	}

	pool := &mockBufferPool{
		buffers: make(map[int][]*mockBuffer),
	}

	config := &ProcessingPipelineConfig{
		Source:   source,
		Analyzer: analyzer,
		BufferPool: pool,
		Config: ProcessingConfig{
			ChunkDuration:  time.Second,
			OverlapPercent: 0.25,
			BufferAhead:    10,
		},
	}

	// Create pipeline
	pipeline, err := NewProcessingPipeline(config)
	if err != nil {
		t.Fatalf("failed to create pipeline: %v", err)
	}

	// Start pipeline
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err = pipeline.Start(ctx); err != nil {
		t.Fatalf("failed to start pipeline: %v", err)
	}

	// Send some test data
	testData := make([]byte, 48000*2) // 1 second of audio
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	source.output <- AudioData{
		Buffer:    testData,
		Format:    source.format,
		Timestamp: time.Now(),
		SourceID:  source.id,
	}

	// Wait for processing with more efficient polling
	waitForCondition(t, 2*time.Second, 10*time.Millisecond, func() bool {
		metrics := pipeline.GetMetrics()
		if count, ok := metrics["processed_chunks"].(int64); ok && count > 0 {
			return true
		}
		return false
	}, "chunk processing")

	// Stop pipeline
	err = pipeline.Stop()
	if err != nil {
		t.Errorf("failed to stop pipeline: %v", err)
	}
}

// TestProcessingPipelineValidation tests configuration validation
func TestProcessingPipelineValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      *ProcessingPipelineConfig
		shouldFail  bool
	}{
		{
			name:       "nil config",
			config:     nil,
			shouldFail: true,
		},
		{
			name: "missing source",
			config: &ProcessingPipelineConfig{
				Analyzer:   &mockAnalyzer{},
				BufferPool: &mockBufferPool{},
			},
			shouldFail: true,
		},
		{
			name: "zero chunk duration",
			config: &ProcessingPipelineConfig{
				Source:     &mockAudioSource{format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16}},
				Analyzer:   &mockAnalyzer{},
				BufferPool: &mockBufferPool{},
				Config: ProcessingConfig{
					ChunkDuration: 0,
				},
			},
			shouldFail: true,
		},
		{
			name: "overlap exceeds chunk",
			config: &ProcessingPipelineConfig{
				Source:     &mockAudioSource{format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16}},
				Analyzer:   &mockAnalyzer{},
				BufferPool: &mockBufferPool{},
				Config: ProcessingConfig{
					ChunkDuration:  time.Second,
					OverlapPercent: 1.5, // 150% overlap
				},
			},
			shouldFail: true,
		},
		{
			name: "valid config",
			config: &ProcessingPipelineConfig{
				Source:     &mockAudioSource{format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16}},
				Analyzer:   &mockAnalyzer{requiredFormat: AudioFormat{SampleRate: 48000}},
				BufferPool: &mockBufferPool{},
				Config: ProcessingConfig{
					ChunkDuration:  time.Second,
					OverlapPercent: 0.25,
				},
			},
			shouldFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, err := NewProcessingPipeline(tt.config)
			if tt.shouldFail && err == nil {
				t.Error("expected pipeline creation to fail")
			}
			if !tt.shouldFail && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.shouldFail && pipeline == nil {
				t.Error("expected pipeline creation to succeed")
			}
		})
	}
}

// TestProcessingPipelineBackpressure tests backpressure handling
func TestProcessingPipelineBackpressure(t *testing.T) {
	t.Parallel()

	// Create a slow analyzer to trigger backpressure
	slowAnalyzer := &mockAnalyzer{
		id: "slow-analyzer",
		requiredFormat: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			// Simulate slow processing
			select {
			case <-ctx.Done():
				return AnalysisResult{}, ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return AnalysisResult{}, nil
			}
		},
	}

	source := &mockAudioSource{
		id:     "test-source",
		format: AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
		output: make(chan AudioData, 100),
		errors: make(chan error, 1),
	}

	config := &ProcessingPipelineConfig{
		Source:   source,
		Analyzer: slowAnalyzer,
		BufferPool: &mockBufferPool{buffers: make(map[int][]*mockBuffer)},
		Config: ProcessingConfig{
			ChunkDuration:  100 * time.Millisecond,
			OverlapPercent: 0.1,
			BufferAhead:    2, // Small buffer to trigger backpressure
		},
	}

	pipeline, err := NewProcessingPipeline(config)
	if err != nil {
		t.Fatalf("failed to create pipeline: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = pipeline.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start pipeline: %v", err)
	}

	// Send multiple chunks quickly
	// 100ms at 48kHz, mono, 16-bit = 48000 * 1 * 2 * 0.1 = 9600 bytes
	chunkSize := 9600 // 100ms at 48kHz, mono, 16-bit
	for i := 0; i < 10; i++ {
		source.output <- AudioData{
			Buffer:    make([]byte, chunkSize),
			Format:    source.format,
			Timestamp: time.Now(),
			SourceID:  source.id,
		}
	}

	// Wait for drops to occur due to backpressure
	waitForCondition(t, 2*time.Second, 10*time.Millisecond, func() bool {
		metrics := pipeline.GetMetrics()
		if dropped, ok := metrics["dropped_chunks"].(int64); ok && dropped > 0 {
			t.Logf("Detected %d dropped chunks due to backpressure", dropped)
			return true
		}
		return false
	}, "backpressure drops")

	err = pipeline.Stop()
	if err != nil {
		t.Errorf("failed to stop pipeline: %v", err)
	}
}

// mockAudioSource for testing
type mockAudioSource struct {
	id     string
	format AudioFormat
	output chan AudioData
	errors chan error
	active bool
	mu     sync.Mutex
}

func (m *mockAudioSource) ID() string                    { return m.id }
func (m *mockAudioSource) Name() string                  { return m.id }
func (m *mockAudioSource) GetFormat() AudioFormat        { return m.format }
func (m *mockAudioSource) AudioOutput() <-chan AudioData { return m.output }
func (m *mockAudioSource) Errors() <-chan error          { return m.errors }
func (m *mockAudioSource) IsActive() bool               { return m.active }
func (m *mockAudioSource) SetGain(gain float64) error   { return nil }
func (m *mockAudioSource) GetConfig() SourceConfig      { return SourceConfig{ID: m.id} }
func (m *mockAudioSource) Start(ctx context.Context) error { 
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	return nil 
}
func (m *mockAudioSource) Stop() error { 
	m.mu.Lock()
	m.active = false
	if m.output != nil {
		close(m.output)
	}
	m.mu.Unlock()
	return nil 
}
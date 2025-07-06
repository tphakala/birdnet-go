package audiocore

import (
	"context"
	"sync"
	"testing"
	"time"
)

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
	pipeline := NewProcessingPipeline(config)
	if pipeline == nil {
		t.Fatal("failed to create pipeline")
	}

	// Start pipeline
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := pipeline.Start(ctx)
	if err != nil {
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

	// Give pipeline time to process
	time.Sleep(100 * time.Millisecond)

	// Stop pipeline
	err = pipeline.Stop()
	if err != nil {
		t.Errorf("failed to stop pipeline: %v", err)
	}

	// Check metrics
	metrics := pipeline.GetMetrics()
	if processed, ok := metrics["processed_chunks"].(int64); !ok || processed == 0 {
		t.Error("expected at least one processed chunk")
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
			pipeline := NewProcessingPipeline(tt.config)
			if tt.shouldFail && pipeline != nil {
				t.Error("expected pipeline creation to fail")
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
			time.Sleep(200 * time.Millisecond)
			return AnalysisResult{}, nil
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

	pipeline := NewProcessingPipeline(config)
	if pipeline == nil {
		t.Fatal("failed to create pipeline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := pipeline.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start pipeline: %v", err)
	}

	// Send multiple chunks quickly
	chunkSize := 4800 // 100ms at 48kHz
	for i := 0; i < 10; i++ {
		source.output <- AudioData{
			Buffer:    make([]byte, chunkSize),
			Format:    source.format,
			Timestamp: time.Now(),
			SourceID:  source.id,
		}
	}

	// Let it process
	time.Sleep(500 * time.Millisecond)

	err = pipeline.Stop()
	if err != nil {
		t.Errorf("failed to stop pipeline: %v", err)
	}

	// Check that some chunks were dropped due to backpressure
	metrics := pipeline.GetMetrics()
	if dropped, ok := metrics["dropped_chunks"].(int64); !ok || dropped == 0 {
		t.Error("expected some dropped chunks due to backpressure")
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
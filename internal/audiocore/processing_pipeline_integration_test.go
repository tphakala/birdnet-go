package audiocore

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestProcessingPipelineWithSafeAnalyzer tests the pipeline with analyzer timeout protection
func TestProcessingPipelineWithSafeAnalyzer(t *testing.T) {
	t.Parallel()

	// Create a mock source
	source := &testAudioSource{
		id:     "test-source",
		name:   "Test Source",
		format: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		outputChan: make(chan AudioData, 10),
		errorChan:  make(chan error, 10),
	}

	// Create a mock analyzer that sometimes blocks
	blockCount := atomic.Int32{}
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			count := blockCount.Add(1)
			if count%3 == 0 {
				// Every third analysis blocks until cancelled
				<-ctx.Done()
				return AnalysisResult{}, ctx.Err()
			}
			// Normal analysis
			return AnalysisResult{
				Timestamp:  data.Timestamp,
				Duration:   data.Duration,
				Detections: []Detection{{Label: "test", Confidence: 0.9}},
				AnalyzerID: "test-analyzer",
				SourceID:   data.SourceID,
			}, nil
		},
		requiredFormat: source.format,
	}

	// Wrap analyzer with safety features
	safeAnalyzer := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer:              mock,
		Timeout:               100 * time.Millisecond,
		MaxConcurrentAnalyses: 2,
	})
	defer func() { _ = safeAnalyzer.Close() }()

	// Create buffer pool
	bufferPool := &mockBufferPool{
		buffers: make(map[int][]*mockBuffer),
	}

	// Create pipeline
	pipelineConfig := &ProcessingPipelineConfig{
		Source:   source,
		Analyzer: safeAnalyzer,
		BufferPool: bufferPool,
		Config: ProcessingConfig{
			ChunkDuration:  100 * time.Millisecond,
			OverlapPercent: 0.1,
			BufferAhead:    2,
		},
	}
	pipeline := NewProcessingPipeline(pipelineConfig)

	// Start source
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	source.running.Store(true)
	go source.simulate(ctx)

	// Start pipeline
	err := pipeline.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start pipeline: %v", err)
	}
	defer func() { _ = pipeline.Stop() }()

	// Let it run for a while
	time.Sleep(2 * time.Second)

	// Check metrics
	metrics := pipeline.GetMetrics()
	t.Logf("Pipeline metrics: %+v", metrics)

	// Verify we processed some chunks
	processed := metrics["processed_chunks"].(int64)
	if processed == 0 {
		t.Error("no chunks were processed")
	}

	// Check analyzer metrics
	analyzerMetrics := safeAnalyzer.GetMetrics()
	t.Logf("Analyzer metrics: %+v", analyzerMetrics)

	// Verify some timeouts occurred (since we block every 3rd analysis)
	timeouts := analyzerMetrics["timeout_count"].(int64)
	if timeouts == 0 {
		t.Error("expected some timeouts, got none")
	}

	// Verify the analyzer didn't completely fail
	totalAnalyses := analyzerMetrics["total_analyses"].(int64)
	if totalAnalyses < 5 {
		t.Errorf("expected at least 5 analyses, got %d", totalAnalyses)
	}

	// Check that timeout rate is reasonable (around 33%)
	timeoutRate := analyzerMetrics["timeout_rate"].(float64)
	if timeoutRate < 0.2 || timeoutRate > 0.5 {
		t.Errorf("unexpected timeout rate: %f, expected around 0.33", timeoutRate)
	}
}

// testAudioSource implements AudioSource for testing
type testAudioSource struct {
	id         string
	name       string
	format     AudioFormat
	outputChan chan AudioData
	errorChan  chan error
	running    atomic.Bool
	mu         sync.RWMutex
}

func (s *testAudioSource) ID() string { return s.id }
func (s *testAudioSource) Name() string { return s.name }
func (s *testAudioSource) Start(ctx context.Context) error {
	s.running.Store(true)
	return nil
}
func (s *testAudioSource) Stop() error {
	s.running.Store(false)
	close(s.outputChan)
	close(s.errorChan)
	return nil
}
func (s *testAudioSource) AudioOutput() <-chan AudioData { return s.outputChan }
func (s *testAudioSource) Errors() <-chan error { return s.errorChan }
func (s *testAudioSource) IsActive() bool { return s.running.Load() }
func (s *testAudioSource) GetFormat() AudioFormat { return s.format }
func (s *testAudioSource) SetGain(gain float64) error { return nil }
func (s *testAudioSource) GetConfig() SourceConfig {
	return SourceConfig{
		ID:     s.id,
		Name:   s.name,
		Format: s.format,
	}
}

// simulate generates test audio data
func (s *testAudioSource) simulate(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.running.Load() {
				return
			}
			
			// Generate test data
			data := AudioData{
				Buffer:    make([]byte, 4800), // 100ms at 48kHz
				Format:    s.format,
				Timestamp: time.Now(),
				Duration:  100 * time.Millisecond,
				SourceID:  s.id,
			}
			
			select {
			case s.outputChan <- data:
			default:
				// Channel full, drop data
			}
		}
	}
}
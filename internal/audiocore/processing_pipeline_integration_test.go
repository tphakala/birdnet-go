package audiocore

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestProcessingPipelineWithSafeAnalyzer tests the pipeline with analyzer timeout protection
func TestProcessingPipelineWithSafeAnalyzer(t *testing.T) {
	t.Parallel()
	
	// Track goroutines at start to detect leaks
	startGoroutines := runtime.NumGoroutine()
	defer func() {
		// Give goroutines time to clean up with exponential backoff
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			endGoroutines := runtime.NumGoroutine()
			if endGoroutines <= startGoroutines {
				return // No leak detected
			}
			time.Sleep(10 * time.Millisecond)
		}
		// Final check after timeout
		endGoroutines := runtime.NumGoroutine()
		if endGoroutines > startGoroutines {
			t.Errorf("goroutine leak detected: started with %d, ended with %d goroutines", startGoroutines, endGoroutines)
		}
	}()

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
		Workers: 2,
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
	pipeline, err := NewProcessingPipeline(pipelineConfig)
	if err != nil {
		t.Fatalf("failed to create processing pipeline: %v", err)
	}

	// Start source
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	source.running.Store(true)
	go source.simulate(ctx)

	// Start pipeline
	if err = pipeline.Start(ctx); err != nil {
		t.Fatalf("failed to start pipeline: %v", err)
	}
	defer func() { _ = pipeline.Stop() }()

	// Wait for sufficient processing to occur
	timeout := time.After(5 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("timeout waiting for chunk processing")
		default:
			metrics := pipeline.GetMetrics()
			if processed, ok := metrics["processed_chunks"].(int64); ok && processed >= 10 {
				t.Logf("Pipeline metrics: %+v", metrics)
				goto checkAnalyzer
			}
			// Brief yield to avoid busy loop
			runtime.Gosched()
		}
	}

checkAnalyzer:

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
	if timeoutRate < 0.15 || timeoutRate > 0.6 {
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
			// 100ms at 48kHz, mono, 16-bit = 48000 * 1 * 2 * 0.1 = 9600 bytes
			data := AudioData{
				Buffer:    make([]byte, 9600), // 100ms at 48kHz, mono, 16-bit
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
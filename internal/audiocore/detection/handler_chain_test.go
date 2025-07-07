package detection

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Mock handler for testing
type mockHandler struct {
	id          string
	name        string
	shouldError bool
	errorMsg    string
	results     []*AnalysisResult
	metadata    map[string]interface{}
}

func (m *mockHandler) HandleDetection(ctx context.Context, detection *Detection) error {
	if m.shouldError {
		return errors.New(m.errorMsg)
	}
	return nil
}

func (m *mockHandler) HandleAnalysisResult(ctx context.Context, result *AnalysisResult) error {
	if m.shouldError {
		return errors.New(m.errorMsg)
	}
	m.results = append(m.results, result)
	return nil
}

func (m *mockHandler) ID() string {
	return m.id
}

func (m *mockHandler) Close() error {
	return nil
}


func TestNewHandlerChain(t *testing.T) {
	chain := NewHandlerChain()
	if chain == nil {
		t.Fatal("NewHandlerChain returned nil")
	}

	// Just verify it's not nil - internal implementation details may vary
}

func TestHandlerChain_AddHandler(t *testing.T) {
	chain := NewHandlerChain()

	// Add first handler
	handler1 := &mockHandler{id: "handler1", name: "handler1"}
	err := chain.AddHandler(handler1)
	if err != nil {
		t.Errorf("Failed to add handler1: %v", err)
	}

	// Add second handler
	handler2 := &mockHandler{id: "handler2", name: "handler2"}
	err = chain.AddHandler(handler2)
	if err != nil {
		t.Errorf("Failed to add handler2: %v", err)
	}

	// Verify handlers were added
	handlers := chain.GetHandlers()
	if len(handlers) != 2 {
		t.Errorf("Expected 2 handlers, got %d", len(handlers))
	}

	// Verify order
	if handlers[0].ID() != "handler1" {
		t.Error("First handler should be handler1")
	}
	if handlers[1].ID() != "handler2" {
		t.Error("Second handler should be handler2")
	}
}

func TestHandlerChain_HandleAnalysisResult(t *testing.T) {
	chain := NewHandlerChain()

	// Create mock handlers
	handler1 := &mockHandler{id: "handler1", name: "handler1"}
	handler2 := &mockHandler{id: "handler2", name: "handler2"}
	handler3 := &mockHandler{id: "handler3", name: "handler3"}

	_ = chain.AddHandler(handler1)
	_ = chain.AddHandler(handler2)
	_ = chain.AddHandler(handler3)

	// Create test result
	testResult := &AnalysisResult{
		SourceID:  "test_source",
		Timestamp: time.Now(),
		Duration:  3 * time.Second,
		AnalyzerID: "test_analyzer",
		Detections: []Detection{
			{
				SourceID:   "test_source",
				Species:    "Test Bird",
				Confidence: 0.95,
				StartTime:  0.0,
				EndTime:    3.0,
			},
		},
	}

	// Process through chain
	ctx := context.Background()
	err := chain.HandleAnalysisResult(ctx, testResult)
	if err != nil {
		t.Errorf("HandleAnalysisResult failed: %v", err)
	}

	// Verify all handlers received the results
	if len(handler1.results) != 1 {
		t.Error("Handler1 didn't receive results")
	}
	if len(handler2.results) != 1 {
		t.Error("Handler2 didn't receive results")
	}
	if len(handler3.results) != 1 {
		t.Error("Handler3 didn't receive results")
	}
}

func TestHandlerChain_HandleAnalysisResultWithError(t *testing.T) {
	chain := NewHandlerChain()

	// Create handlers with one that errors
	handler1 := &mockHandler{id: "handler1", name: "handler1"}
	handler2 := &mockHandler{
		id:          "handler2",
		name:        "handler2",
		shouldError: true,
		errorMsg:    "handler2 error",
	}
	handler3 := &mockHandler{id: "handler3", name: "handler3"}

	_ = chain.AddHandler(handler1)
	_ = chain.AddHandler(handler2)
	_ = chain.AddHandler(handler3)

	// Process through chain
	ctx := context.Background()
	testResult := &AnalysisResult{SourceID: "test"}
	
	err := chain.HandleAnalysisResult(ctx, testResult)
	if err != nil {
		t.Errorf("HandleAnalysisResult should not return error from handler: %v", err)
	}

	// Handler1 should have processed
	if len(handler1.results) != 1 {
		t.Error("Handler1 should have processed results")
	}

	// Handler3 should still have processed (errors don't stop the chain)
	if len(handler3.results) != 1 {
		t.Error("Handler3 should have processed results despite handler2 error")
	}
}

func TestHandlerChain_HandleAnalysisResultWithContextCancellation(t *testing.T) {
	chain := NewHandlerChain()

	// Create a slow handler
	slowHandler := &mockHandler{
		id:   "slow_handler",
		name: "slow_handler",
	}

	_ = chain.AddHandler(slowHandler)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	testResult := &AnalysisResult{SourceID: "test"}
	err := chain.HandleAnalysisResult(ctx, testResult)
	if err == nil {
		t.Error("Expected error for cancelled context")
	}
}

func TestHandlerChain_HandleAnalysisResultEmptyChain(t *testing.T) {
	chain := NewHandlerChain()

	// Process with no handlers
	ctx := context.Background()
	testResult := &AnalysisResult{SourceID: "test"}
	
	err := chain.HandleAnalysisResult(ctx, testResult)
	if err != nil {
		t.Errorf("Empty chain should not error: %v", err)
	}
}

func TestHandlerChain_HandleAnalysisResultNil(t *testing.T) {
	chain := NewHandlerChain()
	handler := &mockHandler{id: "handler", name: "handler"}
	_ = chain.AddHandler(handler)

	// Process nil result
	ctx := context.Background()
	err := chain.HandleAnalysisResult(ctx, nil)
	if err != nil {
		t.Errorf("HandleAnalysisResult with nil result should not error: %v", err)
	}
}

func TestHandlerChain_HandleDetection(t *testing.T) {
	chain := NewHandlerChain()
	handler := &mockHandler{id: "handler", name: "handler"}
	_ = chain.AddHandler(handler)

	// Test single detection
	ctx := context.Background()
	detection := &Detection{
		SourceID:   "test_source",
		Timestamp:  time.Now(),
		Species:    "Test Bird",
		Confidence: 0.95,
	}
	
	err := chain.HandleDetection(ctx, detection)
	if err != nil {
		t.Errorf("HandleDetection should not error: %v", err)
	}
}

func TestHandlerChain_RemoveHandler(t *testing.T) {
	chain := NewHandlerChain()

	// Add handlers
	handler1 := &mockHandler{id: "handler1", name: "handler1"}
	handler2 := &mockHandler{id: "handler2", name: "handler2"}

	_ = chain.AddHandler(handler1)
	_ = chain.AddHandler(handler2)

	// Remove handler1
	err := chain.RemoveHandler("handler1")
	if err != nil {
		t.Errorf("RemoveHandler failed: %v", err)
	}

	// Verify only handler2 remains
	handlers := chain.GetHandlers()
	if len(handlers) != 1 {
		t.Errorf("Expected 1 handler, got %d", len(handlers))
	}
	if handlers[0].ID() != "handler2" {
		t.Error("Expected handler2 to remain")
	}
}

func TestHandlerChain_ConcurrentAccess(t *testing.T) {
	chain := NewHandlerChain()

	// Add some handlers
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("handler_%d", i)
		handler := &mockHandler{id: id, name: id}
		_ = chain.AddHandler(handler)
	}

	// Run concurrent processes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			ctx := context.Background()
			result := &AnalysisResult{
				SourceID: string(rune('0' + id)),
				Detections: []Detection{
					{Species: "Test", Confidence: 0.9},
				},
			}
			_ = chain.HandleAnalysisResult(ctx, result)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Benchmark handler chain processing
func BenchmarkHandlerChain_HandleAnalysisResult(b *testing.B) {
	chain := NewHandlerChain()

	// Add multiple handlers
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("handler_%d", i)
		handler := &mockHandler{id: id, name: id}
		_ = chain.AddHandler(handler)
	}

	// Create test result
	testResult := &AnalysisResult{
		SourceID:  "bench_source",
		Timestamp: time.Now(),
		Detections: []Detection{
			{Species: "Bird1", Confidence: 0.95},
			{Species: "Bird2", Confidence: 0.85},
			{Species: "Bird3", Confidence: 0.75},
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = chain.HandleAnalysisResult(ctx, testResult)
	}
}
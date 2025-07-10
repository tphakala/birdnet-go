package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	
	"github.com/tphakala/birdnet-go/internal/logging"
)

// mockErrorEvent implements ErrorEvent for testing
type mockErrorEvent struct {
	component string
	category  string
	message   string
	context   map[string]any
	timestamp time.Time
	reported  atomic.Bool
}

func (m *mockErrorEvent) GetComponent() string                { return m.component }
func (m *mockErrorEvent) GetCategory() string                 { return m.category }
func (m *mockErrorEvent) GetContext() map[string]any          { return m.context }
func (m *mockErrorEvent) GetTimestamp() time.Time             { return m.timestamp }
func (m *mockErrorEvent) GetError() error                     { return nil }
func (m *mockErrorEvent) GetMessage() string                  { return m.message }
func (m *mockErrorEvent) IsReported() bool                    { return m.reported.Load() }
func (m *mockErrorEvent) MarkReported()                       { m.reported.Store(true) }

// mockConsumer implements EventConsumer for testing
type mockConsumer struct {
	name           string
	processedCount atomic.Int32
	errorOnProcess bool
	supportsBatch  bool
	processDelay   time.Duration
	mu             sync.Mutex
	events         []ErrorEvent
}

func (m *mockConsumer) Name() string { return m.name }

func (m *mockConsumer) ProcessEvent(event ErrorEvent) error {
	if m.processDelay > 0 {
		time.Sleep(m.processDelay)
	}
	
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()
	
	m.processedCount.Add(1)
	
	if m.errorOnProcess {
		return fmt.Errorf("mock error")
	}
	return nil
}

func (m *mockConsumer) ProcessBatch(events []ErrorEvent) error {
	for _, event := range events {
		if err := m.ProcessEvent(event); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockConsumer) SupportsBatching() bool { return m.supportsBatch }

func (m *mockConsumer) GetProcessedCount() int32 {
	return m.processedCount.Load()
}

func (m *mockConsumer) GetEvents() []ErrorEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	events := make([]ErrorEvent, len(m.events))
	copy(events, m.events)
	return events
}

// waitForProcessed waits for the consumer to process n events or times out
func waitForProcessed(t *testing.T, consumer *mockConsumer, expected int32, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for %d events, got %d", expected, consumer.GetProcessedCount())
		case <-ticker.C:
			if consumer.GetProcessedCount() >= expected {
				return
			}
		}
	}
}

// createTestEventBus creates a properly initialized EventBus for testing
func createTestEventBus(t *testing.T, bufferSize, workers int) *EventBus {
	t.Helper()
	
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel() })
	
	eb := &EventBus{
		errorEventChan:    make(chan ErrorEvent, bufferSize),
		resourceEventChan: make(chan ResourceEvent, bufferSize),
		bufferSize:        bufferSize,
		workers:           workers,
		consumers:         make([]EventConsumer, 0),
		resourceConsumers: make([]ResourceEventConsumer, 0),
		ctx:               ctx,
		cancel:            cancel,
		logger:            logging.ForService("test"),
		startTime:         time.Now(),
		config:            &Config{Workers: workers, BufferSize: bufferSize},
	}
	eb.initialized.Store(true)
	
	return eb
}

// ensureEventBusStarted ensures the event bus workers are started
func ensureEventBusStarted(t *testing.T, eb *EventBus) {
	t.Helper()
	
	// If not already running, start the bus
	if !eb.running.Load() {
		eb.start()
	}
	
	// Verify it's running
	if !eb.running.Load() {
		t.Fatal("event bus failed to start")
	}
}

// resetGlobalStateForTesting resets global state for test isolation
// This is a test-only function to help manage global state between tests
func resetGlobalStateForTesting() {
	hasActiveConsumers.Store(false)
	// Reset other global state if needed in the future
}

// TestEventBusInitialization tests event bus initialization
func TestEventBusInitialization(t *testing.T) {
	// Don't run in parallel due to global state modifications
	
	// Initialize logging for tests
	logging.Init()
	
	// Reset global state
	ResetForTesting()
	
	t.Run("default initialization", func(t *testing.T) {
		t.Parallel()
		
		// Reset global state for this test
		ResetForTesting()
		
		eb, err := Initialize(nil)
		if err != nil {
			t.Fatalf("failed to initialize event bus: %v", err)
		}
		
		if eb == nil {
			t.Fatal("expected non-nil event bus")
		}
		
		if !eb.initialized.Load() {
			t.Error("event bus should be marked as initialized")
		}
		
		if eb.bufferSize != 10000 {
			t.Errorf("expected buffer size 10000, got %d", eb.bufferSize)
		}
		
		if eb.workers != 4 {
			t.Errorf("expected 4 workers, got %d", eb.workers)
		}
	})
	
	t.Run("disabled configuration", func(t *testing.T) {
		t.Parallel()
		
		// Reset global state
		ResetForTesting()
		
		config := &Config{
			Enabled: false,
		}
		
		eb, err := Initialize(config)
		if err != nil {
			t.Fatalf("failed to initialize: %v", err)
		}
		
		if eb != nil {
			t.Error("expected nil event bus when disabled")
		}
	})
}

// TestEventBusPublish tests event publishing
// Note: This test cannot run in parallel because RegisterConsumer modifies the global
// hasActiveConsumers flag. Future refactoring should consider making this state
// injectable to improve test isolation.
func TestEventBusPublish(t *testing.T) {
	// Don't run main test in parallel, but subtests can be parallel
	
	logging.Init()
	
	t.Run("publish without consumers", func(t *testing.T) {
		t.Parallel()
		
		// Create isolated event bus using helper
		eb := createTestEventBus(t, 100, 2)
		eb.running.Store(true) // Manually set running since no consumers to trigger start
		
		event := &mockErrorEvent{
			component: "test",
			category:  "test-category",
			message:   "test message",
			timestamp: time.Now(),
		}
		
		// Should return false with no consumers
		if eb.TryPublish(event) {
			t.Error("expected publish to fail with no consumers")
		}
	})
	
	t.Run("publish with consumer", func(t *testing.T) {
		// Don't run in parallel - RegisterConsumer modifies global state
		
		// Create isolated event bus using helper
		eb := createTestEventBus(t, 100, 2)
		
		// Register consumer
		consumer := &mockConsumer{name: "test-consumer"}
		err := eb.RegisterConsumer(consumer)
		if err != nil {
			t.Fatalf("failed to register consumer: %v", err)
		}
		
		// Ensure workers are started
		ensureEventBusStarted(t, eb)
		
		// Defer shutdown
		defer func() {
			if err := eb.Shutdown(1 * time.Second); err != nil {
				t.Logf("shutdown error: %v", err)
			}
		}()
		
		// Publish event
		event := &mockErrorEvent{
			component: "test",
			category:  "test-category",
			message:   "test message",
			timestamp: time.Now(),
		}
		
		if !eb.TryPublish(event) {
			t.Error("expected publish to succeed")
		}
		
		// Wait for processing
		waitForProcessed(t, consumer, 1, 100*time.Millisecond)
		
		if consumer.GetProcessedCount() != 1 {
			t.Errorf("expected 1 processed event, got %d", consumer.GetProcessedCount())
		}
		
		// Verify event details
		events := consumer.GetEvents()
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		
		if events[0].GetComponent() != "test" {
			t.Errorf("expected component 'test', got %s", events[0].GetComponent())
		}
	})
}

// TestEventBusOverflow tests buffer overflow handling
// Note: This test cannot run in parallel because it modifies the global
// hasActiveConsumers flag. Future refactoring should consider making this state
// injectable to improve test isolation.
func TestEventBusOverflow(t *testing.T) {
	// Don't run in parallel - modifies global state
	
	logging.Init()
	
	// Reset global state after test
	defer resetGlobalStateForTesting()
	
	// Create event bus with very small buffer for predictable overflow
	eb := createTestEventBus(t, 2, 1)
	
	// Create blocking consumer 
	blockChan := make(chan struct{}, 1) // Buffered to prevent blocking
	releaseChan := make(chan struct{})
	consumer := &blockingConsumer{
		name:        "blocking-consumer",
		blockChan:   blockChan,
		releaseChan: releaseChan,
	}
	err := eb.RegisterConsumer(consumer)
	if err != nil {
		t.Fatalf("failed to register consumer: %v", err)
	}
	
	// Ensure workers are started
	ensureEventBusStarted(t, eb)
	
	// Fill the buffer and test overflow
	published := 0
	dropped := 0
	
	// Send buffer size + extra events (2 + 3 = 5 total)
	// First 2 should succeed, rest should fail
	for i := range 5 {
		event := &mockErrorEvent{
			component: "test",
			category:  "overflow-test",
			message:   fmt.Sprintf("event %d", i),
			timestamp: time.Now(),
		}
		
		if eb.TryPublish(event) {
			published++
		} else {
			dropped++
		}
	}
	
	// Verify results
	// Buffer capacity is 2, we sent 5 events
	// Without workers processing, we expect 2 to succeed and 3 to be dropped
	if published != 2 {
		t.Errorf("expected 2 published events, got %d", published)
	}
	if dropped != 3 {
		t.Errorf("expected 3 dropped events, got %d", dropped)
	}
	
	// Verify stats
	stats := eb.GetStats()
	if stats.EventsDropped != uint64(dropped) {
		t.Errorf("stats mismatch: expected %d dropped events, got %d", dropped, stats.EventsDropped)
	}
	
	if stats.EventsReceived != uint64(published) {
		t.Errorf("stats mismatch: expected %d received events, got %d", published, stats.EventsReceived)
	}

	// Clean up
	close(releaseChan) // Release any blocked consumers
	_ = eb.Shutdown(1 * time.Second)
}

// TestEventBusShutdown tests graceful shutdown
func TestEventBusShutdown(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	
	// Create event bus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	eb := &EventBus{
		errorEventChan:    make(chan ErrorEvent, 100),
		resourceEventChan: make(chan ResourceEvent, 100),
		bufferSize:        100,
		workers:    2,
		consumers:  make([]EventConsumer, 0),
		resourceConsumers: make([]ResourceEventConsumer, 0),
		ctx:        ctx,
		cancel:     cancel,
		logger:     logging.ForService("test"),
	}
	eb.initialized.Store(true)
	
	// Register consumer
	consumer := &mockConsumer{name: "test-consumer"}
	err := eb.RegisterConsumer(consumer)
	if err != nil {
		t.Fatalf("failed to register consumer: %v", err)
	}
	
	// Publish some events
	for i := range 5 {
		event := &mockErrorEvent{
			component: "test",
			category:  "shutdown-test",
			message:   fmt.Sprintf("event %d", i),
			timestamp: time.Now(),
		}
		eb.TryPublish(event)
	}
	
	// Shutdown with timeout
	err = eb.Shutdown(1 * time.Second)
	if err != nil {
		t.Errorf("shutdown failed: %v", err)
	}
	
	// Verify no longer accepting events
	if eb.running.Load() {
		t.Error("event bus should not be running after shutdown")
	}
	
	event := &mockErrorEvent{
		component: "test",
		category:  "post-shutdown",
		message:   "should not be accepted",
		timestamp: time.Now(),
	}
	
	if eb.TryPublish(event) {
		t.Error("event bus should not accept events after shutdown")
	}
}

// TestConsumerPanic tests handling of consumer panics
// Note: This test cannot run in parallel because it modifies the global
// hasActiveConsumers flag. Future refactoring should consider making this state
// injectable to improve test isolation.
func TestConsumerPanic(t *testing.T) {
	// Don't run in parallel - modifies global state
	
	logging.Init()
	
	// Reset global state after test
	defer resetGlobalStateForTesting()
	
	// Create event bus using helper
	eb := createTestEventBus(t, 100, 1)
	
	// Register panicking consumer
	panicConsumer := &panickyConsumer{name: "panic-consumer"}
	err := eb.RegisterConsumer(panicConsumer)
	if err != nil {
		t.Fatalf("failed to register consumer: %v", err)
	}
	
	// Also register normal consumer
	normalConsumer := &mockConsumer{name: "normal-consumer"}
	err = eb.RegisterConsumer(normalConsumer)
	if err != nil {
		t.Fatalf("failed to register consumer: %v", err)
	}
	
	// Ensure workers are started
	ensureEventBusStarted(t, eb)
	
	defer func() {
		if err := eb.Shutdown(1 * time.Second); err != nil {
			t.Logf("shutdown error: %v", err)
		}
	}()
	
	// Verify event bus is ready before publishing
	if !eb.running.Load() {
		t.Fatal("event bus not running after registering consumers")
	}
	
	// Publish event
	event := &mockErrorEvent{
		component: "test",
		category:  "panic-test",
		message:   "test message",
		timestamp: time.Now(),
	}
	
	if !eb.TryPublish(event) {
		t.Error("expected publish to succeed")
	}
	
	// Wait for processing
	waitForProcessed(t, normalConsumer, 1, 200*time.Millisecond)
	
	// Normal consumer should still process the event
	if normalConsumer.GetProcessedCount() != 1 {
		t.Errorf("expected normal consumer to process 1 event, got %d", normalConsumer.GetProcessedCount())
	}
	
	// Check error stats
	stats := eb.GetStats()
	if stats.ConsumerErrors == 0 {
		t.Error("expected consumer errors to be recorded")
	}
}

// panickyConsumer is a consumer that always panics
type panickyConsumer struct {
	name string
}

func (p *panickyConsumer) Name() string { return p.name }

func (p *panickyConsumer) ProcessEvent(event ErrorEvent) error {
	panic("intentional panic for testing")
}

func (p *panickyConsumer) ProcessBatch(events []ErrorEvent) error {
	panic("intentional panic for testing")
}

func (p *panickyConsumer) SupportsBatching() bool { return false }

// blockingConsumer is a consumer that blocks on the first event until signaled
type blockingConsumer struct {
	name        string
	blockChan   chan struct{} // Signals when first event is received
	releaseChan chan struct{} // Wait for this to be closed before processing
	firstEvent  atomic.Bool   // Track if we've seen the first event
}

func (b *blockingConsumer) Name() string { return b.name }

func (b *blockingConsumer) ProcessEvent(event ErrorEvent) error {
	// Signal that we received an event, but only for the first one
	if b.firstEvent.CompareAndSwap(false, true) {
		// Signal that we've received the first event
		b.blockChan <- struct{}{}
		// Wait for release signal (this will block until close(releaseChan))
		<-b.releaseChan
	}
	return nil
}

func (b *blockingConsumer) ProcessBatch(events []ErrorEvent) error {
	for _, event := range events {
		if err := b.ProcessEvent(event); err != nil {
			return err
		}
	}
	return nil
}

func (b *blockingConsumer) SupportsBatching() bool { return false }
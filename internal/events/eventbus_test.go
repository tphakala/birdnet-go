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
	context   map[string]interface{}
	timestamp time.Time
	reported  atomic.Bool
}

func (m *mockErrorEvent) GetComponent() string                { return m.component }
func (m *mockErrorEvent) GetCategory() string                 { return m.category }
func (m *mockErrorEvent) GetContext() map[string]interface{}  { return m.context }
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
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if consumer.GetProcessedCount() >= expected {
			return
		}
		time.Sleep(1 * time.Millisecond) // Small sleep to avoid busy loop
	}
	t.Fatalf("timeout waiting for %d events, got %d", expected, consumer.GetProcessedCount())
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
func TestEventBusPublish(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	
	t.Run("publish without consumers", func(t *testing.T) {
		t.Parallel()
		
		// Create isolated event bus
		eb := &EventBus{
			errorEventChan:    make(chan ErrorEvent, 100),
			resourceEventChan: make(chan ResourceEvent, 100),
			bufferSize:        100,
			workers:           2,
			consumers:         make([]EventConsumer, 0),
			resourceConsumers: make([]ResourceEventConsumer, 0),
			logger:            logging.ForService("test"),
		}
		eb.initialized.Store(true)
		eb.running.Store(true)
		
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
		t.Parallel()
		
		// Create isolated event bus
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		
		eb := &EventBus{
			errorEventChan:    make(chan ErrorEvent, 100),
			resourceEventChan: make(chan ResourceEvent, 100),
			bufferSize: 100,
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
func TestEventBusOverflow(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	
	// Create event bus with small buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	eb := &EventBus{
		errorEventChan:    make(chan ErrorEvent, 10), // Small buffer
		resourceEventChan: make(chan ResourceEvent, 10),
		bufferSize: 10,
		workers:    1,
		consumers:  make([]EventConsumer, 0),
		resourceConsumers: make([]ResourceEventConsumer, 0),
		ctx:        ctx,
		cancel:     cancel,
		logger:     logging.ForService("test"),
	}
	eb.initialized.Store(true)
	
	// Register slow consumer
	consumer := &mockConsumer{
		name:         "slow-consumer",
		processDelay: 100 * time.Millisecond, // Slow processing
	}
	err := eb.RegisterConsumer(consumer)
	if err != nil {
		t.Fatalf("failed to register consumer: %v", err)
	}
	
	// Send many events quickly
	published := 0
	dropped := 0
	
	for i := 0; i < 20; i++ {
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
	
	// Some events should be dropped
	if dropped == 0 {
		t.Error("expected some events to be dropped due to overflow")
	}
	
	// Verify stats
	stats := eb.GetStats()
	if stats.EventsDropped != uint64(dropped) {
		t.Errorf("expected %d dropped events, got %d", dropped, stats.EventsDropped)
	}
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
	for i := 0; i < 5; i++ {
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
func TestConsumerPanic(t *testing.T) {
	t.Parallel()
	
	logging.Init()
	
	// Create event bus
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	eb := &EventBus{
		errorEventChan:    make(chan ErrorEvent, 100),
		resourceEventChan: make(chan ResourceEvent, 100),
		bufferSize:        100,
		workers:    1,
		consumers:  make([]EventConsumer, 0),
		resourceConsumers: make([]ResourceEventConsumer, 0),
		ctx:        ctx,
		cancel:     cancel,
		logger:     logging.ForService("test"),
	}
	eb.initialized.Store(true)
	
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
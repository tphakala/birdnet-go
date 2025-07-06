package audiocore

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// ResourceTracker helps prevent resource leaks by tracking allocations
type ResourceTracker struct {
	resources map[string]*TrackedResource
	mu        sync.RWMutex
	logger    *slog.Logger
	
	// Lifecycle management
	ctx          context.Context
	cancel       context.CancelFunc
	cleanupQueue chan cleanupTask
	wg           sync.WaitGroup
	
	// Statistics
	totalAllocated atomic.Int64
	totalReleased  atomic.Int64
	activeCount    atomic.Int32
}

// cleanupTask represents a scheduled resource cleanup
type cleanupTask struct {
	resourceID string
	cleanupAt  time.Time
}

// TrackedResource represents a tracked resource
type TrackedResource struct {
	ID           string
	Type         string
	AllocatedAt  time.Time
	Stack        string
	Finalizer    func() // Cleanup function
	Released     atomic.Bool
	ReleasedAt   time.Time
}

// NewResourceTracker creates a new resource tracker
func NewResourceTracker() *ResourceTracker {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	tracker := &ResourceTracker{
		resources:    make(map[string]*TrackedResource),
		logger:       logger.With("component", "resource_tracker"),
		ctx:          ctx,
		cancel:       cancel,
		cleanupQueue: make(chan cleanupTask, 100),
	}
	
	// Start leak detector
	tracker.wg.Add(1)
	go tracker.leakDetector()
	
	// Start cleanup worker
	tracker.wg.Add(1)
	go tracker.cleanupWorker()
	
	return tracker
}

// Track registers a resource for tracking
func (rt *ResourceTracker) Track(id, resourceType string, finalizer func()) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	
	// Capture stack trace for debugging
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	stack := string(buf[:n])
	
	resource := &TrackedResource{
		ID:          id,
		Type:        resourceType,
		AllocatedAt: time.Now(),
		Stack:       stack,
		Finalizer:   finalizer,
	}
	
	rt.resources[id] = resource
	rt.totalAllocated.Add(1)
	rt.activeCount.Add(1)
	
	// Set finalizer to detect leaks
	runtime.SetFinalizer(resource, func(r *TrackedResource) {
		if !r.Released.Load() {
			rt.logger.Error("resource leaked - not properly closed",
				"resource_id", r.ID,
				"resource_type", r.Type,
				"allocated_at", r.AllocatedAt,
				"stack", r.Stack)
			
			// Call the cleanup function
			if r.Finalizer != nil {
				r.Finalizer()
			}
		}
	})
}

// Release marks a resource as released
func (rt *ResourceTracker) Release(id string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	
	resource, exists := rt.resources[id]
	if !exists {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryNotFound).
			Context("resource_id", id).
			Context("error", "resource not found").
			Build()
	}
	
	if resource.Released.Load() {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryState).
			Context("resource_id", id).
			Context("error", "resource already released").
			Build()
	}
	
	resource.Released.Store(true)
	resource.ReleasedAt = time.Now()
	rt.totalReleased.Add(1)
	rt.activeCount.Add(-1)
	
	// Remove finalizer
	runtime.SetFinalizer(resource, nil)
	
	// Call cleanup function
	if resource.Finalizer != nil {
		resource.Finalizer()
	}
	
	// Schedule cleanup after a delay (for debugging)
	select {
	case rt.cleanupQueue <- cleanupTask{
		resourceID: id,
		cleanupAt:  time.Now().Add(5 * time.Minute),
	}:
	case <-rt.ctx.Done():
		// Tracker is shutting down
	}
	
	return nil
}

// leakDetector periodically checks for potential leaks
func (rt *ResourceTracker) leakDetector() {
	defer rt.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			rt.checkForLeaks()
		case <-rt.ctx.Done():
			return
		}
	}
}

// checkForLeaks checks for resources that haven't been released
func (rt *ResourceTracker) checkForLeaks() {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	
	now := time.Now()
	threshold := 5 * time.Minute
	
	for id, resource := range rt.resources {
		if !resource.Released.Load() && now.Sub(resource.AllocatedAt) > threshold {
			rt.logger.Warn("potential resource leak detected",
				"resource_id", id,
				"resource_type", resource.Type,
				"age", now.Sub(resource.AllocatedAt),
				"allocated_at", resource.AllocatedAt)
		}
	}
}

// cleanupWorker processes scheduled resource cleanups
func (rt *ResourceTracker) cleanupWorker() {
	defer rt.wg.Done()
	
	// Use a map to track pending cleanups by time
	pending := make(map[string]time.Time)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case task := <-rt.cleanupQueue:
			// Add to pending cleanups
			pending[task.resourceID] = task.cleanupAt
			
		case <-ticker.C:
			// Process due cleanups
			now := time.Now()
			for id, cleanupTime := range pending {
				if now.After(cleanupTime) {
					rt.mu.Lock()
					delete(rt.resources, id)
					rt.mu.Unlock()
					delete(pending, id)
				}
			}
			
		case <-rt.ctx.Done():
			return
		}
	}
}

// Close stops the resource tracker and cleans up
func (rt *ResourceTracker) Close() error {
	rt.cancel()
	rt.wg.Wait()
	return nil
}

// Stats returns resource tracking statistics
func (rt *ResourceTracker) Stats() map[string]any {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	
	activeByType := make(map[string]int)
	for _, resource := range rt.resources {
		if !resource.Released.Load() {
			activeByType[resource.Type]++
		}
	}
	
	return map[string]any{
		"total_allocated": rt.totalAllocated.Load(),
		"total_released":  rt.totalReleased.Load(),
		"active_count":    rt.activeCount.Load(),
		"active_by_type":  activeByType,
		"leak_rate":       float64(rt.activeCount.Load()) / float64(rt.totalAllocated.Load()),
	}
}

// ManagedResource wraps a resource with automatic cleanup
type ManagedResource struct {
	resource    any
	closeFunc   func() error
	tracker     *ResourceTracker
	id          string
	ctx         context.Context
	cancel      context.CancelFunc
	closeOnce   sync.Once
	closed      atomic.Bool
}

// NewManagedResource creates a new managed resource
func NewManagedResource(id string, resource any, closeFunc func() error, tracker *ResourceTracker) *ManagedResource {
	ctx, cancel := context.WithCancel(context.Background())
	
	mr := &ManagedResource{
		resource:  resource,
		closeFunc: closeFunc,
		tracker:   tracker,
		id:        id,
		ctx:       ctx,
		cancel:    cancel,
	}
	
	// Track the resource
	if tracker != nil {
		tracker.Track(id, fmt.Sprintf("%T", resource), func() {
			_ = mr.Close()
		})
	}
	
	// Set finalizer as backup
	runtime.SetFinalizer(mr, func(m *ManagedResource) {
		if !m.closed.Load() {
			_ = m.Close()
		}
	})
	
	return mr
}

// Resource returns the wrapped resource
func (mr *ManagedResource) Resource() any {
	return mr.resource
}

// Context returns the resource context
func (mr *ManagedResource) Context() context.Context {
	return mr.ctx
}

// Close closes the resource
func (mr *ManagedResource) Close() error {
	var closeErr error
	
	mr.closeOnce.Do(func() {
		mr.closed.Store(true)
		mr.cancel()
		
		if mr.closeFunc != nil {
			closeErr = mr.closeFunc()
		}
		
		if mr.tracker != nil {
			_ = mr.tracker.Release(mr.id)
		}
		
		// Remove finalizer
		runtime.SetFinalizer(mr, nil)
	})
	
	return closeErr
}

// WithResource executes a function with a managed resource
func WithResource[T any](id string, resource T, closeFunc func() error, tracker *ResourceTracker, fn func(T) error) error {
	mr := NewManagedResource(id, resource, closeFunc, tracker)
	defer func() { _ = mr.Close() }()
	
	return fn(resource)
}

// ResourcePool manages a pool of reusable resources
type ResourcePool[T any] struct {
	pool        *sync.Pool
	factory     func() (T, error)
	resetFunc   func(T) error
	closeFunc   func(T) error
	tracker     *ResourceTracker
	activeCount atomic.Int32
	maxActive   int32
	
	// Track factory errors
	lastFactoryError atomic.Value // stores error
}

// NewResourcePool creates a new resource pool
func NewResourcePool[T any](factory func() (T, error), resetFunc, closeFunc func(T) error, maxActive int32, tracker *ResourceTracker) *ResourcePool[T] {
	rp := &ResourcePool[T]{
		factory:   factory,
		resetFunc: resetFunc,
		closeFunc: closeFunc,
		tracker:   tracker,
		maxActive: maxActive,
	}
	
	rp.pool = &sync.Pool{
		New: func() any {
			if factory == nil {
				return nil
			}
			resource, err := factory()
			if err != nil {
				// Store the error for later retrieval
				rp.lastFactoryError.Store(err)
				return nil
			}
			return resource
		},
	}
	
	return rp
}

// Get retrieves a resource from the pool
func (rp *ResourcePool[T]) Get() (T, error) {
	var zero T
	
	// Check active limit
	if rp.maxActive > 0 && rp.activeCount.Load() >= rp.maxActive {
		return zero, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryLimit).
			Context("max_active", rp.maxActive).
			Context("error", "resource pool limit reached").
			Build()
	}
	
	// Try to get from pool
	if resource := rp.pool.Get(); resource != nil {
		if typed, ok := resource.(T); ok {
			rp.activeCount.Add(1)
			return typed, nil
		}
	}
	
	// Check if pool.Get() returned nil due to factory error
	if err := rp.lastFactoryError.Load(); err != nil {
		// Clear the error and return it
		rp.lastFactoryError.Store(nil)
		if factoryErr, ok := err.(error); ok {
			return zero, errors.New(factoryErr).
				Component(ComponentAudioCore).
				Category(errors.CategoryResource).
				Context("error", "resource factory failed").
				Build()
		}
	}
	
	// Create new resource directly
	if rp.factory != nil {
		resource, err := rp.factory()
		if err != nil {
			return zero, err
		}
		rp.activeCount.Add(1)
		return resource, nil
	}
	
	return zero, errors.New(nil).
		Component(ComponentAudioCore).
		Category(errors.CategoryConfiguration).
		Context("error", "no resource factory configured").
		Build()
}

// Put returns a resource to the pool
func (rp *ResourcePool[T]) Put(resource T) error {
	rp.activeCount.Add(-1)
	
	// Reset the resource
	if rp.resetFunc != nil {
		if err := rp.resetFunc(resource); err != nil {
			// If reset fails, close the resource
			if rp.closeFunc != nil {
				_ = rp.closeFunc(resource)
			}
			return err
		}
	}
	
	rp.pool.Put(resource)
	return nil
}

// Close closes all resources in the pool
func (rp *ResourcePool[T]) Close() error {
	// Note: sync.Pool doesn't provide a way to iterate over pooled items
	// Resources will be garbage collected eventually
	return nil
}
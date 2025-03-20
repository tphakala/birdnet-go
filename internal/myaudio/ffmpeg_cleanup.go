package myaudio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
)

// CleanupManager provides utilities for safely cleaning up resources
type CleanupManager struct {
	// Add fields if needed for tracking state
	consumptionTracker *ConsumptionTracker
}

// NewCleanupManager creates a new cleanup manager
func NewCleanupManager() *CleanupManager {
	return &CleanupManager{
		consumptionTracker: NewConsumptionTracker(),
	}
}

// ConsumptionTracker tracks when data is consumed from channels
type ConsumptionTracker struct {
	mu                sync.Mutex
	lastConsumedMap   map[string]time.Time
	hasConsumersMap   map[string]bool
	consumptionWindow time.Duration
}

// NewConsumptionTracker creates a new consumption tracker
func NewConsumptionTracker() *ConsumptionTracker {
	return &ConsumptionTracker{
		lastConsumedMap:   make(map[string]time.Time),
		hasConsumersMap:   make(map[string]bool),
		consumptionWindow: 30 * time.Second,
	}
}

// CleanupStaleEntries removes entries that haven't been accessed recently
func (ct *ConsumptionTracker) CleanupStaleEntries() {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute)
	for id, lastAccessed := range ct.lastConsumedMap {
		if lastAccessed.Before(cutoff) {
			delete(ct.lastConsumedMap, id)
			delete(ct.hasConsumersMap, id)
		}
	}
}

// CleanupTrackers cleans up stale consumption tracker entries
func (cm *CleanupManager) CleanupTrackers() {
	cm.consumptionTracker.CleanupStaleEntries()
}

// CloseReader safely closes a reader
func (cm *CleanupManager) CloseReader(r io.ReadCloser, description string) {
	if r == nil {
		return
	}

	err := r.Close()
	if err != nil && !strings.Contains(err.Error(), "file already closed") {
		log.Printf("⚠️ Error closing %s: %v", description, err)
	}
}

// WaitWithTimeout waits for a channel with a timeout and returns whether it completed normally
func (cm *CleanupManager) WaitWithTimeout(ch <-chan struct{}, timeout time.Duration, description string) bool {
	if ch == nil {
		return true
	}

	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		log.Printf("⚠️ Timeout waiting for %s", description)
		return false
	}
}

// ExecuteWithTimeout executes a function with a timeout
func (cm *CleanupManager) ExecuteWithTimeout(ctx context.Context, timeout time.Duration, fn func() error, description string) error {
	resultCh := make(chan error, 1)

	// Create a new context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	go func() {
		resultCh <- fn()
	}()

	select {
	case err := <-resultCh:
		return err
	case <-execCtx.Done():
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timeout executing %s: %w", description, execCtx.Err())
		}
		return execCtx.Err()
	}
}

// SendNonBlocking sends a value to a channel without blocking
func (cm *CleanupManager) SendNonBlocking(ch chan<- struct{}, description string) bool {
	select {
	case ch <- struct{}{}:
		return true
	default:
		log.Printf("⚠️ Channel %s is full, dropping message", description)
		return false
	}
}

// GlobalCleanupManager is a singleton instance for convenience
var GlobalCleanupManager = NewCleanupManager()

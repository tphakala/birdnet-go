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

// RecordConsumption records that data was consumed from a channel
func (ct *ConsumptionTracker) RecordConsumption(channelID string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.lastConsumedMap[channelID] = time.Now()
	ct.hasConsumersMap[channelID] = true
}

// HasActiveConsumers checks if a channel has had recent consumption
func (ct *ConsumptionTracker) HasActiveConsumers(channelID string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	lastConsumed, exists := ct.lastConsumedMap[channelID]
	if !exists {
		return false
	}

	// If consumption was recent, consider it active
	if time.Since(lastConsumed) <= ct.consumptionWindow {
		return true
	}

	// Mark as inactive if no recent consumption
	ct.hasConsumersMap[channelID] = false
	return false
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

// TrackConsumption allows the cleanup manager to track channel consumption
func (cm *CleanupManager) TrackConsumption(channelID string) {
	cm.consumptionTracker.RecordConsumption(channelID)
}

// HasActiveConsumers checks if there are active consumers for a channel
func (cm *CleanupManager) HasActiveConsumers(channelID string) bool {
	return cm.consumptionTracker.HasActiveConsumers(channelID)
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

// CloseWriter safely closes a writer
func (cm *CleanupManager) CloseWriter(w io.WriteCloser, description string) {
	if w == nil {
		return
	}

	err := w.Close()
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

// WaitForErrorWithTimeout waits for an error channel with a timeout
func (cm *CleanupManager) WaitForErrorWithTimeout(ch <-chan error, timeout time.Duration, description string) (error, bool) {
	if ch == nil {
		return nil, true
	}

	select {
	case err := <-ch:
		return err, true
	case <-time.After(timeout):
		log.Printf("⚠️ Timeout waiting for %s", description)
		return fmt.Errorf("timeout waiting for %s", description), false
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

// SendWithTimeout sends a value to a channel with a timeout
func (cm *CleanupManager) SendWithTimeout(ctx context.Context, ch chan<- struct{}, timeout time.Duration, description string) bool {
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case ch <- struct{}{}:
		return true
	case <-sendCtx.Done():
		log.Printf("⚠️ Timeout sending to channel %s", description)
		return false
	}
}

// GlobalCleanupManager is a singleton instance for convenience
var GlobalCleanupManager = NewCleanupManager()

// internal/health/registry.go
package health

import (
	"context"
	"sync"
	"time"
)

// DefaultTimeout is the per-check timeout.
const DefaultTimeout = 10 * time.Second

// Registry stores health checks and runs them.
type Registry struct {
	mu     sync.RWMutex
	checks []Check
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a check to the registry.
func (r *Registry) Register(c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, c)
}

// RegisterAll adds multiple checks at once.
func (r *Registry) RegisterAll(checks ...Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, checks...)
}

// RunAll executes all checks in parallel with per-check timeout.
func (r *Registry) RunAll(ctx context.Context) []Result {
	r.mu.RLock()
	checks := make([]Check, len(r.checks))
	copy(checks, r.checks)
	r.mu.RUnlock()

	results := make([]Result, len(checks))
	var wg sync.WaitGroup

	for i, c := range checks {
		wg.Add(1)
		go func(idx int, check Check) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()
			start := time.Now()
			result := check.Run(checkCtx)
			result.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
			if result.Timestamp.IsZero() {
				result.Timestamp = time.Now()
			}
			results[idx] = result
		}(i, c)
	}

	wg.Wait()
	return results
}

// RunCategory executes only checks matching the given category.
func (r *Registry) RunCategory(ctx context.Context, cat Category) []Result {
	r.mu.RLock()
	var filtered []Check
	for _, c := range r.checks {
		if c.Category() == cat {
			filtered = append(filtered, c)
		}
	}
	r.mu.RUnlock()

	results := make([]Result, len(filtered))
	var wg sync.WaitGroup

	for i, c := range filtered {
		wg.Add(1)
		go func(idx int, check Check) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()
			start := time.Now()
			result := check.Run(checkCtx)
			result.DurationMS = float64(time.Since(start).Microseconds()) / 1000.0
			if result.Timestamp.IsZero() {
				result.Timestamp = time.Now()
			}
			results[idx] = result
		}(i, c)
	}

	wg.Wait()
	return results
}

// Count returns the number of registered checks.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.checks)
}

// Categories returns the distinct categories of registered checks.
func (r *Registry) Categories() []Category {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[Category]bool)
	var cats []Category
	for _, c := range r.checks {
		if !seen[c.Category()] {
			seen[c.Category()] = true
			cats = append(cats, c.Category())
		}
	}
	return cats
}

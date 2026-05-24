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

// Register adds a check to the registry. Nil checks are silently ignored.
func (r *Registry) Register(c Check) {
	if c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, c)
}

// RegisterAll adds multiple checks at once. Nil checks are silently ignored.
func (r *Registry) RegisterAll(checks ...Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range checks {
		if c != nil {
			r.checks = append(r.checks, c)
		}
	}
}

// RunAll executes all checks in parallel with per-check timeout.
func (r *Registry) RunAll(ctx context.Context) []Result {
	return r.RunAllWithWindow(ctx, 0)
}

// RunAllWithWindow executes all checks in parallel. For checks implementing
// WindowedCheck, the given window duration is applied before execution.
// A zero window leaves checks at their default window.
func (r *Registry) RunAllWithWindow(ctx context.Context, window time.Duration) []Result {
	r.mu.RLock()
	checks := make([]Check, len(r.checks))
	copy(checks, r.checks)
	r.mu.RUnlock()

	if window > 0 {
		for i, c := range checks {
			if wc, ok := c.(WindowedCheck); ok {
				checks[i] = wc.WithWindow(window)
			}
		}
	}

	return runChecks(ctx, checks)
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

	return runChecks(ctx, filtered)
}

// runChecks executes the given checks in parallel with per-check timeout.
// Checks that implement MultiResultCheck produce multiple results per check;
// all results are flattened into the returned slice in registration order.
func runChecks(ctx context.Context, checks []Check) []Result {
	type checkOutput struct {
		results []Result
	}
	outputs := make([]checkOutput, len(checks))
	var wg sync.WaitGroup

	for i, c := range checks {
		wg.Add(1)
		go func(idx int, check Check) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()
			start := time.Now()

			var rs []Result
			if mc, ok := check.(MultiResultCheck); ok {
				rs = mc.RunMulti(checkCtx)
			} else {
				rs = []Result{check.Run(checkCtx)}
			}

			dur := float64(time.Since(start).Microseconds()) / 1000.0
			now := time.Now()
			for j := range rs {
				if rs[j].DurationMS == 0 {
					rs[j].DurationMS = dur
				}
				if rs[j].Timestamp.IsZero() {
					rs[j].Timestamp = now
				}
			}
			outputs[idx] = checkOutput{results: rs}
		}(i, c)
	}

	wg.Wait()

	results := make([]Result, 0, len(checks))
	for _, o := range outputs {
		results = append(results, o.results...)
	}
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

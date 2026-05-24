// internal/health/registry.go
package health

import (
	"context"
	"slices"
	"sync"
	"time"
)

// DefaultTimeout is the per-check timeout.
const DefaultTimeout = 10 * time.Second

// contextGracePeriod is the time to wait after context cancellation for
// context-aware checks to finish before synthesizing StatusUnknown.
const contextGracePeriod = 100 * time.Millisecond

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
	checks := slices.Clone(r.checks)
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

// checkResult pairs a check's index with its results so the orchestrator
// can reconstruct registration order after collecting from a channel.
type checkResult struct {
	idx     int
	results []Result
}

// runChecks executes the given checks in parallel with per-check timeout.
// Checks that implement MultiResultCheck produce multiple results per check;
// all results are flattened into the returned slice in registration order.
//
// If the parent context expires before all checks finish, completed results
// are returned and unfinished checks receive a synthetic StatusUnknown result.
func runChecks(ctx context.Context, checks []Check) []Result {
	if len(checks) == 0 {
		return nil
	}

	resCh := make(chan checkResult, len(checks))

	for i, c := range checks {
		idx, check := i, c
		go func() {
			checkCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()
			start := time.Now()

			var rs []Result
			if mc, ok := check.(MultiResultCheck); ok {
				rs = slices.Clone(mc.RunMulti(checkCtx))
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
			resCh <- checkResult{idx: idx, results: rs}
		}()
	}

	// Collect results until all checks report or the parent context expires.
	// Count-based collection avoids spawning a waiter goroutine that would
	// leak if a check ignores its context and hangs indefinitely.
	completed := make([][]Result, len(checks))
	finished := make([]bool, len(checks))
	received := 0

collect:
	for received < len(checks) {
		select {
		case cr := <-resCh:
			completed[cr.idx] = cr.results
			finished[cr.idx] = true
			received++
		case <-ctx.Done():
			// Give context-aware checks a brief grace period to finish and
			// report their own status before we synthesize StatusUnknown.
			grace := time.NewTimer(contextGracePeriod)
			for received < len(checks) {
				select {
				case cr := <-resCh:
					completed[cr.idx] = cr.results
					finished[cr.idx] = true
					received++
				case <-grace.C:
					break collect
				}
			}
			grace.Stop()
		}
	}

	results := make([]Result, 0, len(checks))
	for i, c := range checks {
		if finished[i] {
			results = append(results, completed[i]...)
		} else {
			results = append(results, Result{
				Name:      c.Name(),
				Category:  c.Category(),
				Status:    StatusUnknown,
				Message:   "check did not complete within deadline",
				Timestamp: time.Now(),
			})
		}
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

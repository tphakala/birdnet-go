// internal/health/registry.go
package health

import (
	"context"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
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
	if isNilCheck(c) {
		logger.Global().Module("health").Error("refusing to register a nil check")
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
		if isNilCheck(c) {
			logger.Global().Module("health").Error("refusing to register a nil check")
			continue
		}
		r.checks = append(r.checks, c)
	}
}

// isNilCheck reports whether c is nil or a typed nil: a non-nil interface value
// wrapping a nil pointer, map, slice, channel, or function. A typed nil passes a
// plain c == nil guard but panics when its methods are called, so it must be
// rejected at registration to keep iteration sites (runChecks, RunCategory,
// Categories) crash-free.
func isNilCheck(c Check) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
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

	// Pre-compute each check's identity once, with per-check panic recovery, so
	// no later site (the per-check goroutine or the timeout fallback below) ever
	// calls Name()/Category() unguarded. A panic in an accessor falls back to a
	// placeholder identity instead of crashing the orchestrator goroutine.
	names := make([]string, len(checks))
	categories := make([]Category, len(checks))
	for i, c := range checks {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					names[i] = "unknown_check"
				}
			}()
			names[i] = c.Name()
			categories[i] = c.Category()
		}()
	}

	resCh := make(chan checkResult, len(checks))

	for i, c := range checks {
		idx, check := i, c
		go func() {
			var rs []Result
			name := names[idx]
			category := categories[idx]

			// sent guards against a double send on resCh. resCh is buffered to
			// exactly len(checks), so a second send would steal another check's
			// slot and block that goroutine forever. Today only the success send
			// below runs before any panic could occur, but the flag keeps the
			// invariant if code is ever added after the send.
			sent := false

			// Recover from a panic in the check so a single misbehaving check
			// cannot crash the process. A recovered panic is a real bug, not a
			// benign condition, so it is logged at ERROR with a stack trace before
			// being surfaced as StatusUnknown: the app stays up, but the panic is
			// not silently hidden. The recovery must still deliver a result on
			// resCh, otherwise the orchestrator would block waiting for a result
			// that never arrives. Identity comes from the pre-computed slices, so
			// the recover path itself never calls the check's accessors.
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				var panicErr error
				if e, ok := rec.(error); ok {
					panicErr = fmt.Errorf("check panicked: %w", e)
				} else {
					panicErr = fmt.Errorf("check panicked: %v", rec)
				}
				logger.Global().Module("health").Error("health check panicked",
					logger.String("check", name),
					logger.String("category", string(category)),
					logger.Error(panicErr),
					logger.String("stack", string(debug.Stack())))
				if !sent {
					resCh <- checkResult{
						idx: idx,
						results: []Result{{
							Name:      name,
							Category:  category,
							Status:    StatusUnknown,
							Message:   panicErr.Error(),
							Timestamp: time.Now(),
						}},
					}
				}
			}()

			checkCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
			defer cancel()
			start := time.Now()

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
			sent = true
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
	for i := range checks {
		if finished[i] {
			results = append(results, completed[i]...)
		} else {
			// Use the pre-computed identity: the timeout fallback runs on the
			// orchestrator goroutine, which has no recover of its own.
			results = append(results, Result{
				Name:      names[i],
				Category:  categories[i],
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

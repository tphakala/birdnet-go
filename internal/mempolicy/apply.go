package mempolicy

import (
	"math"
	"os"
	"runtime"
	"runtime/debug"
)

// Setters holds the side-effecting functions Apply uses. They are injectable so
// the policy can be unit-tested without touching real runtime/native state.
type Setters struct {
	// CurrentMemLimit returns the currently configured GOMEMLIMIT (math.MaxInt64
	// when unset), so Apply can avoid overriding an operator-provided limit.
	CurrentMemLimit func() int64
	// GoMemLimitEnvSet reports whether the operator set the GOMEMLIMIT env var.
	// This distinguishes "unset" from "explicitly set to a max/off value", which
	// both read back as math.MaxInt64 from the runtime; either way an explicit
	// env value is operator intent we must not override.
	GoMemLimitEnvSet func() bool
	// SetMemoryLimit sets the soft Go memory limit (debug.SetMemoryLimit).
	SetMemoryLimit func(int64) int64
	// SetArenaMax caps glibc malloc arenas (mallopt). Returns true on success.
	SetArenaMax func(int) bool
}

// Applied records what Apply actually changed, for logging and assertions.
type Applied struct {
	MemLimitApplied bool
	ArenaApplied    bool
	GoMemLimitBytes int64
	ArenaMax        int
}

// Apply enacts a Decision through the injected setters. It is a no-op when the
// decision is inactive. An operator-set GOMEMLIMIT is respected (not overridden).
func Apply(d Decision, s Setters) Applied {
	var a Applied
	if !d.Active {
		return a
	}

	if d.ArenaMax > 0 && s.SetArenaMax != nil {
		if ok := s.SetArenaMax(d.ArenaMax); ok {
			a.ArenaApplied = true
			a.ArenaMax = d.ArenaMax
		}
	}

	if d.GoMemLimitBytes > 0 && s.SetMemoryLimit != nil {
		operatorSet := s.GoMemLimitEnvSet != nil && s.GoMemLimitEnvSet()
		current := int64(math.MaxInt64)
		if s.CurrentMemLimit != nil {
			current = s.CurrentMemLimit()
		}
		// Respect an operator-set GOMEMLIMIT: skip when the env var is set (any
		// value, including a max/off value, both of which read back as MaxInt64)
		// or when a limit is already in effect.
		if !operatorSet && current == math.MaxInt64 {
			s.SetMemoryLimit(d.GoMemLimitBytes)
			a.MemLimitApplied = true
			a.GoMemLimitBytes = d.GoMemLimitBytes
		}
	}

	return a
}

// Result bundles the decision and what was applied, for the caller to log.
type Result struct {
	Decision Decision
	Applied  Applied
}

// Configure detects system memory, decides the policy for the given config mode,
// and applies it using the real runtime/native setters. Call this once, early in
// startup, before inference threads spin up.
func Configure(mode string) Result {
	total := DetectTotalMemory()
	d := Decide(total, runtime.NumCPU(), mode)
	a := Apply(d, Setters{
		CurrentMemLimit:  func() int64 { return debug.SetMemoryLimit(-1) },
		GoMemLimitEnvSet: func() bool { _, ok := os.LookupEnv("GOMEMLIMIT"); return ok },
		SetMemoryLimit:   debug.SetMemoryLimit,
		SetArenaMax:      setArenaMax,
	})
	return Result{Decision: d, Applied: a}
}

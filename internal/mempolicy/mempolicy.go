// Package mempolicy applies startup memory controls for memory-constrained
// systems (the foundation for the low-memory mode tracked in the RAM-reduction
// epic). It detects the effective system memory limit (host RAM or cgroup cap),
// derives a policy, and applies two native controls early in startup:
//
//   - mallopt(M_ARENA_MAX, N) to cap glibc per-thread malloc arenas, bounding
//     per-thread arena fragmentation.
//   - debug.SetMemoryLimit (GOMEMLIMIT) as a soft backstop on the Go heap.
//
// The model is held constant; this package only tunes the runtime. Both controls
// are gated behind detection plus a manual config override (auto/on/off), so
// high-memory systems are unaffected. They are cheap backstops, not the primary
// memory lever: profiling found that neither the arena cap nor GOMEMLIMIT
// meaningfully reduces resident memory, because the bulk is loaded model weights
// (and ONNX Runtime's own arena), not reclaimable glibc fragmentation. The
// dominant memory levers are model-level (gating and quantization) and live
// elsewhere; this package is the detection and apply seam they build on.
package mempolicy

import (
	"fmt"
	"strings"
)

// Mode values for the manual config override.
const (
	ModeAuto = "auto" // enable controls when detected memory is at/below the threshold
	ModeOn   = "on"   // force controls on regardless of detected memory
	ModeOff  = "off"  // force controls off regardless of detected memory
)

const bytesPerMiB = 1024 * 1024

// Policy tunables. Named to avoid magic numbers; documented for future tuning.
const (
	// autoThresholdBytes is the detected-memory ceiling at/below which auto mode
	// activates. 1.25 GiB covers 512 MB and 1 GB constrained boxes while leaving
	// 2 GB+ systems untouched.
	autoThresholdBytes = 1280 * bytesPerMiB

	// arenaMaxCeiling bounds the glibc malloc arena cap (M_ARENA_MAX). Capping
	// arenas bounds per-thread arena fragmentation, but glibc's default is 8*cores,
	// so capping too low throttles malloc concurrency on multi-core systems. The
	// effective cap is min(NumCPU, ceiling): the ceiling keeps the cap from
	// exceeding what fragmentation control needs, while NumCPU keeps it from
	// dropping below the machine's own parallelism on small-core boxes.
	arenaMaxCeiling = 4

	// nativeReserveBytes is the estimated non-Go resident footprint (embedded model
	// + TFLite/XNNPACK + ONNX CGO arenas + OS) carved out before deriving GOMEMLIMIT,
	// so the soft limit bounds only Go's own memory. Measured native footprint for
	// the default single-model config is ~330 MB; 350 MiB leaves margin.
	nativeReserveBytes = 350 * bytesPerMiB

	// minGoMemLimitBytes floors the derived GOMEMLIMIT so a very small box never
	// gets a limit so tight that the GC thrashes.
	minGoMemLimitBytes = 96 * bytesPerMiB
)

// Decision is the pure result of evaluating the policy. It carries no side
// effects; Apply turns it into runtime changes.
type Decision struct {
	Active          bool   // whether low-memory controls should be applied
	GoMemLimitBytes int64  // soft GOMEMLIMIT to set; 0 means leave unset
	ArenaMax        int    // glibc M_ARENA_MAX to set; 0 means do not cap
	TotalRAMBytes   int64  // effective detected memory (0 = unknown)
	Reason          string // human-readable explanation for logging
}

// Decide evaluates the memory policy from detected memory, the available CPU
// count, and the config mode. It is pure and fully unit-testable.
func Decide(totalRAMBytes int64, numCPU int, mode string) Decision {
	m := normalizeMode(mode)

	var active bool
	var reason string
	switch m {
	case ModeOn:
		active = true
		reason = "low-memory mode forced on by config"
	case ModeOff:
		reason = "low-memory mode forced off by config"
	default: // ModeAuto
		switch {
		case totalRAMBytes <= 0:
			reason = "auto: system memory unknown, low-memory controls left off"
		case totalRAMBytes <= autoThresholdBytes:
			active = true
			reason = fmt.Sprintf("auto: detected %d MiB at/below %d MiB threshold",
				totalRAMBytes/bytesPerMiB, autoThresholdBytes/bytesPerMiB)
		default:
			reason = fmt.Sprintf("auto: detected %d MiB above %d MiB threshold",
				totalRAMBytes/bytesPerMiB, autoThresholdBytes/bytesPerMiB)
		}
	}

	d := Decision{Active: active, TotalRAMBytes: totalRAMBytes, Reason: reason}
	if active {
		d.ArenaMax = arenaMaxFor(numCPU)
		d.GoMemLimitBytes = deriveGoMemLimit(totalRAMBytes)
	}
	return d
}

// arenaMaxFor returns the glibc malloc arena cap for the given CPU count:
// min(numCPU, arenaMaxCeiling), floored at 1 so an unknown/zero count still
// yields a valid cap. This keeps the cap from throttling malloc below the
// machine's parallelism while still bounding arena fragmentation.
func arenaMaxFor(numCPU int) int {
	if numCPU < 1 {
		numCPU = 1
	}
	if numCPU > arenaMaxCeiling {
		return arenaMaxCeiling
	}
	return numCPU
}

// deriveGoMemLimit returns a soft GOMEMLIMIT for the detected memory, or 0 when
// memory is unknown. The value reserves the native footprint and is floored to
// avoid GC thrashing on tiny boxes.
func deriveGoMemLimit(totalRAMBytes int64) int64 {
	if totalRAMBytes <= 0 {
		return 0
	}
	v := totalRAMBytes - nativeReserveBytes
	if v < minGoMemLimitBytes {
		return minGoMemLimitBytes
	}
	return v
}

// normalizeMode coerces user input to a known mode, defaulting to auto.
func normalizeMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ModeOn:
		return ModeOn
	case ModeOff:
		return ModeOff
	default:
		return ModeAuto
	}
}

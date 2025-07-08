// capture_buffer_tracker.go
package myaudio

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AllocationTracker tracks buffer allocations and provides diagnostic information
type AllocationTracker struct {
	allocations      map[string]*AllocationInfo
	mu               sync.RWMutex
	totalAllocations atomic.Uint64
	enabled          atomic.Bool
}

// AllocationInfo contains details about a buffer allocation
type AllocationInfo struct {
	Count         int
	LastAlloc     time.Time
	FirstAlloc    time.Time
	StackTraces   []string
	Sources       []string
	AllocSizes    []int
	AllocationIDs []string
}

var (
	// Global allocation tracker instance
	allocTracker = &AllocationTracker{
		allocations: make(map[string]*AllocationInfo),
	}
)

// EnableAllocationTracking enables or disables allocation tracking
func EnableAllocationTracking(enable bool) {
	allocTracker.enabled.Store(enable)
	if enable {
		log.Println("📊 Capture buffer allocation tracking enabled")
	} else {
		log.Println("📊 Capture buffer allocation tracking disabled")
	}
}

// TrackAllocation records a buffer allocation event
func TrackAllocation(source string, size int) string {
	if !allocTracker.enabled.Load() {
		return ""
	}

	allocID := fmt.Sprintf("alloc_%s_%d", source, time.Now().UnixNano())
	allocTracker.totalAllocations.Add(1)

	// Capture stack trace
	const maxStackDepth = 10
	pc := make([]uintptr, maxStackDepth)
	n := runtime.Callers(2, pc) // Skip runtime.Callers and this function
	frames := runtime.CallersFrames(pc[:n])

	stackLines := make([]string, 0, maxStackDepth)
	for {
		frame, more := frames.Next()
		// Skip internal runtime frames
		if strings.Contains(frame.File, "runtime/") {
			if !more {
				break
			}
			continue
		}
		stackLines = append(stackLines, fmt.Sprintf("%s:%d %s", frame.File, frame.Line, frame.Function))
		if !more {
			break
		}
	}
	stackTrace := strings.Join(stackLines, "\n")

	allocTracker.mu.Lock()
	defer allocTracker.mu.Unlock()

	now := time.Now()
	info, exists := allocTracker.allocations[source]
	if !exists {
		info = &AllocationInfo{
			FirstAlloc:    now,
			LastAlloc:     now,
			StackTraces:   make([]string, 0, 10),
			Sources:       make([]string, 0, 10),
			AllocSizes:    make([]int, 0, 10),
			AllocationIDs: make([]string, 0, 10),
		}
		allocTracker.allocations[source] = info
	}

	// Calculate time since last allocation before updating
	var timeSinceLastAlloc time.Duration
	if info.Count > 0 {
		timeSinceLastAlloc = now.Sub(info.LastAlloc)
	}

	info.Count++
	info.LastAlloc = now
	
	// Keep last 10 stack traces
	if len(info.StackTraces) >= 10 {
		info.StackTraces = info.StackTraces[1:]
		info.Sources = info.Sources[1:]
		info.AllocSizes = info.AllocSizes[1:]
		info.AllocationIDs = info.AllocationIDs[1:]
	}
	
	info.StackTraces = append(info.StackTraces, stackTrace)
	info.Sources = append(info.Sources, source)
	info.AllocSizes = append(info.AllocSizes, size)
	info.AllocationIDs = append(info.AllocationIDs, allocID)

	// Log if this is a repeated allocation
	if info.Count > 1 {
		log.Printf("⚠️ Repeated allocation detected for source %s: count=%d, size=%d bytes, id=%s",
			source, info.Count, size, allocID)
		log.Printf("   Time since last allocation: %v", timeSinceLastAlloc)
		log.Printf("   Stack trace:\n%s", stackTrace)
	}

	return allocID
}

// GetAllocationReport generates a report of all tracked allocations
func GetAllocationReport() string {
	if !allocTracker.enabled.Load() {
		return "Allocation tracking is disabled"
	}

	allocTracker.mu.RLock()
	defer allocTracker.mu.RUnlock()

	var report strings.Builder
	report.WriteString("=== Capture Buffer Allocation Report ===\n")
	report.WriteString(fmt.Sprintf("Total allocations tracked: %d\n", allocTracker.totalAllocations.Load()))
	report.WriteString(fmt.Sprintf("Unique sources: %d\n\n", len(allocTracker.allocations)))

	for source, info := range allocTracker.allocations {
		report.WriteString(fmt.Sprintf("Source: %s\n", source))
		report.WriteString(fmt.Sprintf("  Allocation count: %d\n", info.Count))
		report.WriteString(fmt.Sprintf("  First allocation: %s\n", info.FirstAlloc.Format(time.RFC3339)))
		report.WriteString(fmt.Sprintf("  Last allocation: %s\n", info.LastAlloc.Format(time.RFC3339)))
		
		if info.Count > 1 {
			report.WriteString("  ⚠️ REPEATED ALLOCATIONS DETECTED\n")
			report.WriteString("  Last few allocations:\n")
			for i := len(info.AllocationIDs) - 1; i >= 0 && i >= len(info.AllocationIDs)-3; i-- {
				report.WriteString(fmt.Sprintf("    - ID: %s, Size: %d bytes\n", info.AllocationIDs[i], info.AllocSizes[i]))
			}
			if len(info.StackTraces) > 0 {
				report.WriteString("  Most recent stack trace:\n")
				lastTrace := info.StackTraces[len(info.StackTraces)-1]
				for _, line := range strings.Split(lastTrace, "\n") {
					report.WriteString(fmt.Sprintf("    %s\n", line))
				}
			}
		}
		report.WriteString("\n")
	}

	return report.String()
}

// ResetAllocationTracking clears all tracked allocation data
func ResetAllocationTracking() {
	allocTracker.mu.Lock()
	defer allocTracker.mu.Unlock()

	allocTracker.allocations = make(map[string]*AllocationInfo)
	allocTracker.totalAllocations.Store(0)
	log.Println("📊 Allocation tracking data reset")
}

// GetAllocationCount returns the number of allocations for a specific source
func GetAllocationCount(source string) int {
	allocTracker.mu.RLock()
	defer allocTracker.mu.RUnlock()

	if info, exists := allocTracker.allocations[source]; exists {
		return info.Count
	}
	return 0
}

// HasRepeatedAllocations checks if any source has repeated allocations
func HasRepeatedAllocations() bool {
	allocTracker.mu.RLock()
	defer allocTracker.mu.RUnlock()

	for _, info := range allocTracker.allocations {
		if info.Count > 1 {
			return true
		}
	}
	return false
}

// PrintAllocationSummary prints a brief summary to the log
func PrintAllocationSummary() {
	if !allocTracker.enabled.Load() {
		return
	}

	allocTracker.mu.RLock()
	defer allocTracker.mu.RUnlock()

	repeatedCount := 0
	for _, info := range allocTracker.allocations {
		if info.Count > 1 {
			repeatedCount++
		}
	}

	if repeatedCount > 0 {
		log.Printf("⚠️ Capture buffer allocation summary: %d sources with repeated allocations out of %d total sources",
			repeatedCount, len(allocTracker.allocations))
	}
}
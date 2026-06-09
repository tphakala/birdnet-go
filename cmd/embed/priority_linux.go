//go:build linux

package embed

import (
	"fmt"
	"os"
	"syscall"
)

// lowestNiceness is the weakest CPU scheduling priority for non-realtime
// processes; lowering priority never requires privileges.
const lowestNiceness = 19

// lowerPriority drops this process to the lowest CPU scheduling priority so
// a concurrently running live server always wins contention. Best effort:
// a failure is reported but never blocks the run.
func lowerPriority() {
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, lowestNiceness); err != nil {
		fmt.Fprintf(os.Stderr, "embed: could not lower process priority: %v\n", err)
	}
}

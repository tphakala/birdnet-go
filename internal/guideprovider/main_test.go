package guideprovider

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs a package-wide goroutine-leak gate after all tests complete.
//
// This package is exactly the kind internal/CLAUDE.md asks for one: a GuideCache
// owns a refresh ticker, a detached DB pre-load, wait-group-tracked pre-fetch and
// warm goroutines, and singleflight executions that outlive their caller — and the
// suite deliberately exercises spawn-vs-Close races. Without the gate a cache that
// stopped honoring Close would pass CI silently.
//
// The ignore list is empty on purpose. TESTING.md requires starting from empty and
// adding only a named goroutine seen in a failure and impossible to stop. The gate
// previously ignored runtime.gopark, which matches ANY parked goroutine - including
// a refresh loop parked in select, the exact leak this gate exists to catch - plus
// testing.(*T).Run, which TESTING.md forbids because goleak already filters the test
// runner, and a lumberjack mill goroutine this package never starts (upstream #4424
// removed the other dead lumberjack ignores). goleak.Find retries internally, so the
// former sleep before it was redundant too.
func TestMain(m *testing.M) {
	testResult := m.Run()

	if err := goleak.Find(); err != nil {
		//nolint:forbidigo // a leak report must reach the test output directly
		println("goroutine leak detected after guideprovider tests:", err.Error())
		os.Exit(1)
	}

	os.Exit(testResult)
}

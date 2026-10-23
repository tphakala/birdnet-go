package control

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine behind, such as the
// delayed shutdown goroutine a restart request starts.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

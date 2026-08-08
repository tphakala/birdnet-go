package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHandleReconfigureEBirdNilController verifies the eBird reconfigure handler
// degrades gracefully when no API controller is wired (early startup or minimal
// test setups): it logs and returns instead of panicking or dereferencing nil.
func TestHandleReconfigureEBirdNilController(t *testing.T) {
	t.Parallel()
	cm := &ControlMonitor{} // apiController is nil
	assert.NotPanics(t, func() {
		cm.handleReconfigureEBird()
	})
}

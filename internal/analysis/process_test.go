// process_test.go tests the per-call inference gating and throttled logging
// helpers added to ProcessData for embedding extraction.
package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestShouldLogEmbeddingUnavailable_ThrottleAndReset verifies the throttle
// semantics of the embUnavailableLogged guard:
//   - first call with dim==0 logs (CAS false->true succeeds)
//   - second call with dim==0 is throttled (CAS false->true fails)
//   - call with dim>0 resets the guard to false (no log returned)
//   - after reset, next call with dim==0 logs again
//
// This test MUST NOT use t.Parallel() because it mutates the package-level
// embUnavailableLogged atomic.
func TestShouldLogEmbeddingUnavailable_ThrottleAndReset(t *testing.T) {
	embUnavailableLogged.Store(false)
	t.Cleanup(func() { embUnavailableLogged.Store(false) })

	assert.True(t, shouldLogEmbeddingUnavailable(0), "first unavailable should log")
	assert.False(t, shouldLogEmbeddingUnavailable(0), "subsequent unavailable throttled")
	assert.False(t, shouldLogEmbeddingUnavailable(1024), "capable resets without logging")
	assert.True(t, shouldLogEmbeddingUnavailable(0), "logs again after reset")
}

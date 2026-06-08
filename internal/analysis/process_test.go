// process_test.go tests the per-call inference gating and throttled logging
// helpers added to ProcessData for embedding extraction.
package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestShouldLogEmbeddingUnavailable_ThrottleAndReset verifies the per-model
// throttle semantics of shouldLogEmbeddingUnavailable:
//   - first call with dim==0 logs (CAS false->true succeeds)
//   - second call with dim==0 is throttled (CAS fails)
//   - call with dim>0 resets the per-model flag (no log returned)
//   - after reset, next call with dim==0 logs again
//   - a different model ID is unaffected by the first model's throttle state
//
// This test MUST NOT use t.Parallel() because it mutates the package-level
// embUnavailableLogged sync.Map.
func TestShouldLogEmbeddingUnavailable_ThrottleAndReset(t *testing.T) {
	const m = "test-model"
	const m2 = "test-model-2"
	embUnavailableLogged.Delete(m)
	embUnavailableLogged.Delete(m2)
	t.Cleanup(func() {
		embUnavailableLogged.Delete(m)
		embUnavailableLogged.Delete(m2)
	})

	assert.True(t, shouldLogEmbeddingUnavailable(m, 0), "first unavailable should log")
	assert.False(t, shouldLogEmbeddingUnavailable(m, 0), "subsequent unavailable throttled")
	assert.False(t, shouldLogEmbeddingUnavailable(m, 1024), "capable resets without logging")
	assert.True(t, shouldLogEmbeddingUnavailable(m, 0), "logs again after reset")

	// Per-model isolation: m is now throttled (flag true after the previous call).
	// m2 has never been seen, so its first unavailable call must still log even
	// though m is throttled. This proves the flags are keyed independently.
	assert.False(t, shouldLogEmbeddingUnavailable(m, 0), "m is throttled for isolation test")
	assert.True(t, shouldLogEmbeddingUnavailable(m2, 0), "different model ID logs independently")
}

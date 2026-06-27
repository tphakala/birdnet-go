// internal/api/v2/inference_broadcast_test.go
package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestBroadcastInferenceTopologyChanged_ReachesConsumer verifies that the
// controller broadcast reaches a topology subscriber on the wired metrics store.
func TestBroadcastInferenceTopologyChanged_ReachesConsumer(t *testing.T) {
	t.Parallel()

	store := observability.NewMemoryStore(10)
	controller := &Controller{Core: &apicore.Core{MetricsStore: store}}

	topoCh, cancel := store.SubscribeTopology()
	t.Cleanup(cancel)

	controller.BroadcastInferenceTopologyChanged()

	select {
	case <-topoCh:
		// Expected: broadcast reached the subscriber.
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for topology broadcast to reach the subscriber")
	}
}

// TestBroadcastInferenceTopologyChanged_NilSafe verifies the broadcast is a
// no-op (no panic) when the controller or its metrics store is nil.
func TestBroadcastInferenceTopologyChanged_NilSafe(t *testing.T) {
	t.Parallel()

	var nilController *Controller
	assert.NotPanics(t, nilController.BroadcastInferenceTopologyChanged)

	noStore := &Controller{Core: &apicore.Core{}}
	assert.NotPanics(t, noStore.BroadcastInferenceTopologyChanged)
}

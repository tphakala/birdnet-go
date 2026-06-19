package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGetModelDevice covers device resolution for loaded, unloaded, and
// torn-down instances.
func TestGetModelDevice(t *testing.T) {
	t.Parallel()

	const (
		cpuModelID = "cpu_model"
		gpuModelID = "gpu_model"
	)
	o := newTestOrchestrator(t,
		&mockModelInstance{id: cpuModelID, device: deviceCPU},
		&mockModelInstance{id: gpuModelID, device: "GPU"},
	)

	t.Run("returns the live instance device", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, deviceCPU, o.GetModelDevice(cpuModelID))
		assert.Equal(t, "GPU", o.GetModelDevice(gpuModelID))
	})

	t.Run("unknown model returns Unknown", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, deviceUnknown, o.GetModelDevice("not_loaded"))
	})
}

// TestGetModelDevice_NilInstance verifies a torn-down entry reports Unknown.
func TestGetModelDevice_NilInstance(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{
		models: map[string]*modelEntry{
			"closed": {instance: nil},
		},
		modelRSS: make(map[string]int64),
	}
	assert.Equal(t, deviceUnknown, o.GetModelDevice("closed"))
}

// TestModelScheduleStatus covers the schedule gating contract: only the bat
// model is gated, and only when its scheduler reports inactive.
func TestModelScheduleStatus(t *testing.T) {
	t.Parallel()

	t.Run("non-bat model is always active", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: "BirdNET_V2.4"})
		active, reason := o.ModelScheduleStatus("BirdNET_V2.4")
		assert.True(t, active)
		assert.Empty(t, reason)
	})

	t.Run("bat model with no scheduler is active", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBat})
		active, reason := o.ModelScheduleStatus(RegistryIDBat)
		assert.True(t, active, "no scheduler means no restriction")
		assert.Empty(t, reason)
	})

	t.Run("bat model active when scheduler is active", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBat})
		s := newNighttimeScheduler(nil) // fails open: active=true
		o.scheduler.Store(s)
		active, reason := o.ModelScheduleStatus(RegistryIDBat)
		assert.True(t, active)
		assert.Empty(t, reason)
	})

	t.Run("bat model paused when scheduler is inactive", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBat})
		s := newNighttimeScheduler(nil)
		s.active.Store(false) // force off-schedule (daytime)
		o.scheduler.Store(s)
		active, reason := o.ModelScheduleStatus(RegistryIDBat)
		assert.False(t, active, "bat must be paused when the scheduler is inactive")
		assert.Equal(t, scheduleReasonNight, reason)
	})
}

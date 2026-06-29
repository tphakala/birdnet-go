package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestGetModelRuntimeInfo covers the compound device/backend/precision accessor
// for loaded and unknown models. The triplet is resolved in a single call so the
// status card never observes a mixed-generation triplet when a reload races a
// poll. A loaded model returns its live device, backend, and precision; an
// unknown model returns the not-loaded triplet (Unknown device, empty
// backend/precision) so the caller falls back to the static ModelInfo metadata.
func TestGetModelRuntimeInfo(t *testing.T) {
	t.Parallel()

	const (
		cpuModelID = "cpu_model"
		ovModelID  = "ov_model"
	)
	o := newTestOrchestrator(t,
		&mockModelInstance{id: cpuModelID, device: deviceCPU, backend: BackendONNX, precision: string(QuantizationINT8)},
		&mockModelInstance{id: ovModelID, device: inference.OVDeviceGPU, backend: BackendOpenVINO, precision: string(QuantizationFP16)},
	)

	t.Run("returns the live instance triplet", func(t *testing.T) {
		t.Parallel()
		device, backend, precision := o.GetModelRuntimeInfo(cpuModelID)
		assert.Equal(t, deviceCPU, device)
		assert.Equal(t, BackendONNX, backend)
		assert.Equal(t, string(QuantizationINT8), precision)

		device, backend, precision = o.GetModelRuntimeInfo(ovModelID)
		assert.Equal(t, inference.OVDeviceGPU, device)
		assert.Equal(t, BackendOpenVINO, backend,
			"an ONNX model executed on OpenVINO must report OpenVINO, not ONNX")
		assert.Equal(t, string(QuantizationFP16), precision)
	})

	t.Run("unknown model reports Unknown device and empty backend/precision", func(t *testing.T) {
		t.Parallel()
		device, backend, precision := o.GetModelRuntimeInfo("not_loaded")
		assert.Equal(t, deviceUnknown, device)
		assert.Empty(t, backend, "empty backend signals static-metadata fallback")
		assert.Empty(t, precision, "empty precision signals static-metadata fallback")
	})
}

// TestGetModelRuntimeInfo_NilInstance verifies a torn-down entry reports the
// not-loaded triplet: Unknown device, empty backend/precision (static fallback).
func TestGetModelRuntimeInfo_NilInstance(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{
		models: map[string]*modelEntry{
			"closed": {instance: nil},
		},
		modelRSS: make(map[string]int64),
	}
	device, backend, precision := o.GetModelRuntimeInfo("closed")
	assert.Equal(t, deviceUnknown, device)
	assert.Empty(t, backend)
	assert.Empty(t, precision)
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

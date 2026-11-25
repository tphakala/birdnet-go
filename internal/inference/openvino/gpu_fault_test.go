package openvino

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

type gpuEventKind int

const (
	evInferFail gpuEventKind = iota
	evInferOK
	evSetupFail
)

type gpuEvent struct {
	kind    gpuEventKind
	gpu     bool
	text    string
	tripped bool // expected: this event returned the sentinel
}

const (
	issueOutOfResources = "[GPU] Exception from src/plugins/intel_gpu/src/runtime/ocl/ocl_stream.cpp: clFinish, error code: -5 CL_OUT_OF_RESOURCES"
	genericInvalidQueue = "openvino: ov_infer_request_infer failed: status=-1: out of order queue, error code: -36 CL_INVALID_COMMAND_QUEUE"
	hostMemory          = "openvino: ov_infer_request_infer failed: status=-1: error code: -6 CL_OUT_OF_HOST_MEMORY"
	unknownText         = "openvino: ov_infer_request_infer failed: status=-1: something else"
	unsupportedOp       = "openvino: ov_core_compile_model failed: status=-1: unsupported operation"
)

func TestGPUGuard_Sequences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		events []gpuEvent
	}{
		{"issue message trips on first GPU infer", []gpuEvent{{evInferFail, true, issueOutOfResources, true}}},
		{"generic plugin error-code format trips", []gpuEvent{{evInferFail, true, genericInvalidQueue, true}}},
		{"host memory failures never trip when interleaved with a success", []gpuEvent{
			{evInferFail, true, hostMemory, false}, {evInferFail, true, hostMemory, false},
			{evInferOK, true, "", false},
			{evInferFail, true, hostMemory, false}, {evInferFail, true, hostMemory, false},
		}},
		{"three unknown failures trip on the third", []gpuEvent{
			{evInferFail, true, unknownText, false}, {evInferFail, true, unknownText, false},
			{evInferFail, true, unknownText, true},
		}},
		{"CPU failures and a CL marker on CPU never trip", []gpuEvent{
			{evInferFail, false, unknownText, false}, {evInferFail, false, unknownText, false},
			{evInferFail, false, unknownText, false}, {evInferFail, false, unknownText, false},
			{evInferFail, false, unknownText, false}, {evInferFail, false, issueOutOfResources, false},
		}},
		{"GPU setup failures never streak", []gpuEvent{
			{evSetupFail, true, unsupportedOp, false}, {evSetupFail, true, unsupportedOp, false},
			{evSetupFail, true, unsupportedOp, false},
		}},
		{"GPU setup failure with a marker trips", []gpuEvent{{evSetupFail, true, issueOutOfResources, true}}},
		{"latch stays set after a GPU success", []gpuEvent{
			{evInferFail, true, issueOutOfResources, true},
			{evInferOK, true, "", false},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var g gpuGuard
			everTripped := false
			for i, ev := range tt.events {
				var err error
				switch ev.kind {
				case evInferFail:
					err = g.inferResult(ev.gpu, errors.NewStd(ev.text))
				case evInferOK:
					err = g.inferResult(ev.gpu, nil)
				case evSetupFail:
					err = g.setupFailed(ev.gpu, errors.NewStd(ev.text))
				}
				gotSentinel := errors.Is(err, ErrGPUDeviceFault)
				if ev.tripped {
					everTripped = true
				}
				assert.Equal(t, ev.tripped, gotSentinel, "event %d", i)
				if gotSentinel {
					assert.True(t, g.blocked(), "sentinel implies latch, event %d", i)
					faulted, cause := g.fault()
					assert.True(t, faulted, "event %d", i)
					require.Error(t, cause, "sentinel implies a cause, event %d", i)
				}
				assert.Equal(t, everTripped, g.blocked(), "latch state, event %d", i)
			}
		})
	}
}

func TestGPUGuard_BlockedErrIsSentinel(t *testing.T) {
	t.Parallel()
	var g gpuGuard
	err := g.blockedErr()
	require.ErrorIs(t, err, ErrGPUDeviceFault)
	assert.Contains(t, err.Error(), "restart")
	assert.Contains(t, err.Error(), "CPU")
}

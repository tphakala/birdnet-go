package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateListenAddress covers the telemetry listen-address validation used by
// the control monitor when (re)starting the metrics endpoint. validateListenAddress
// touches no struct fields, so a zero-value ControlMonitor is a valid receiver.
func TestValidateListenAddress(t *testing.T) {
	t.Parallel()

	cm := &ControlMonitor{}

	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "empty address", address: "", wantErr: true},
		{name: "ipv4 host and port", address: "0.0.0.0:8090", wantErr: false},
		{name: "loopback host and port", address: "127.0.0.1:9090", wantErr: false},
		{name: "hostname and port", address: "localhost:8090", wantErr: false},
		{name: "wildcard host with port", address: ":8090", wantErr: false},
		{name: "ipv6 bracketed host and port", address: "[::1]:8090", wantErr: false},
		{name: "max valid port", address: "0.0.0.0:65535", wantErr: false},
		{name: "min valid port", address: "0.0.0.0:1", wantErr: false},
		{name: "missing port", address: "0.0.0.0", wantErr: true},
		{name: "non-numeric port", address: "0.0.0.0:abc", wantErr: true},
		{name: "port zero out of range", address: "0.0.0.0:0", wantErr: true},
		{name: "port above range", address: "0.0.0.0:70000", wantErr: true},
		{name: "unbracketed ipv6 is ambiguous", address: "::1:8090", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := cm.validateListenAddress(tt.address)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestHandleControlSignal_UnknownSignalNoPanic pins that an unrecognized control
// signal is handled gracefully (logged and ignored) rather than panicking or
// dispatching to a handler. A zero-value monitor is sufficient because the default
// branch dereferences nothing.
func TestHandleControlSignal_UnknownSignalNoPanic(t *testing.T) {
	t.Parallel()

	cm := &ControlMonitor{}
	assert.NotPanics(t, func() {
		cm.handleControlSignal("definitely_not_a_real_signal")
	})
}

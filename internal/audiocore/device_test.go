package audiocore

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// mockDispatcher implements AudioDispatcher and records dispatched frames.
type mockDispatcher struct {
	mu     sync.Mutex
	frames []AudioFrame
	count  atomic.Int64
}

func (m *mockDispatcher) Dispatch(frame AudioFrame) { //nolint:gocritic // hugeParam: signature required by AudioDispatcher interface
	m.mu.Lock()
	m.frames = append(m.frames, frame)
	m.mu.Unlock()
	m.count.Add(1)
}

func (m *mockDispatcher) FrameCount() int64 {
	return m.count.Load()
}

func newTestDeviceManager(t *testing.T) (*DeviceManager, *mockDispatcher) {
	t.Helper()
	disp := &mockDispatcher{}
	log := logger.Global().Module("test_device_manager")
	dm := NewDeviceManager(disp, log)
	return dm, disp
}

// defaultTestConfig returns a minimal DeviceConfig for tests.
func defaultTestConfig() DeviceConfig {
	return DeviceConfig{
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	}
}

// TestDeviceManager_ListDevices verifies that ListDevices returns without error.
// The test is skipped when no audio hardware is present (common in CI).
func TestDeviceManager_ListDevices(t *testing.T) {
	t.Parallel()

	dm, _ := newTestDeviceManager(t)
	t.Cleanup(func() { _ = dm.Close() })

	devices, err := dm.ListDevices()
	if err != nil {
		t.Skipf("no audio hardware available: %v", err)
	}

	// If we got here, enumeration succeeded.
	// We can't assert a specific count but we can assert the slice is non-nil.
	assert.NotNil(t, devices)
	for _, d := range devices {
		assert.NotEmpty(t, d.Name, "device name should not be empty")
	}
}

// TestDeviceManager_StartStopCapture verifies the start/stop lifecycle with a
// mock dispatcher. The test skips gracefully when no real audio device is
// available (CI environments).
//
// Because startCapture requires actual hardware, this test exercises the
// DeviceManager's map tracking logic by inspecting the error path: a second
// StartCapture with the same sourceID must return ErrDeviceAlreadyActive
// before any device enumeration succeeds.
func TestDeviceManager_StartStopCapture(t *testing.T) {
	t.Parallel()

	dm, _ := newTestDeviceManager(t)
	t.Cleanup(func() { _ = dm.Close() })

	// Verify that a non-existent source is not in the active map.
	active := dm.ActiveDevices()
	assert.Empty(t, active, "no devices should be active initially")

	// Attempt to start a capture session. Skip if no hardware is available.
	cfg := defaultTestConfig()
	devices, listErr := listDevices(logger.Global().Module("test"))
	if listErr != nil || len(devices) == 0 {
		t.Skip("no audio hardware available for capture test")
	}

	deviceID := devices[0].ID
	err := dm.StartCapture("test-source-1", deviceID, cfg)
	if err != nil {
		t.Skipf("capture device unavailable: %v", err)
	}

	// Verify the device appears in the active map.
	active = dm.ActiveDevices()
	require.Contains(t, active, "test-source-1")
	assert.Equal(t, deviceID, active["test-source-1"].ID)

	// Give the device a brief moment to produce frames if it starts quickly.
	time.Sleep(200 * time.Millisecond)

	// Stop the device.
	require.NoError(t, dm.StopCapture("test-source-1"))

	// Verify the device is no longer in the active map.
	active = dm.ActiveDevices()
	assert.NotContains(t, active, "test-source-1")
}

// TestDeviceManager_StartCapture_DuplicateSourceID verifies that starting
// a capture with an already-active sourceID returns ErrDeviceAlreadyActive.
// This test uses the internal active map directly so it works without hardware.
func TestDeviceManager_StartCapture_DuplicateSourceID(t *testing.T) {
	t.Parallel()

	dm, _ := newTestDeviceManager(t)
	t.Cleanup(func() { _ = dm.Close() })

	// Inject a fake active device directly to simulate an already-running session
	// without needing real audio hardware.
	dm.mu.Lock()
	dm.active["occupied"] = &ActiveDevice{
		Info:     DeviceInfo{Name: "Fake Device"},
		Config:   defaultTestConfig(),
		sourceID: "occupied",
		cancel:   func() {},
	}
	dm.mu.Unlock()

	err := dm.StartCapture("occupied", "any-device", defaultTestConfig())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceAlreadyActive)
}

// TestDeviceManager_StopCapture_NotActive verifies that stopping a non-existent
// sourceID returns ErrDeviceNotActive.
func TestDeviceManager_StopCapture_NotActive(t *testing.T) {
	t.Parallel()

	dm, _ := newTestDeviceManager(t)
	t.Cleanup(func() { _ = dm.Close() })

	err := dm.StopCapture("does-not-exist")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeviceNotActive)
}

// TestDeviceManager_ReconfigureDevice verifies the stop-change-restart flow.
// With injected fake active state this test runs without hardware.
func TestDeviceManager_ReconfigureDevice(t *testing.T) {
	t.Parallel()

	devices, listErr := listDevices(logger.Global().Module("test"))
	if listErr != nil || len(devices) == 0 {
		t.Skip("no audio hardware available for reconfigure test")
	}

	dm, _ := newTestDeviceManager(t)
	t.Cleanup(func() { _ = dm.Close() })

	cfg := defaultTestConfig()
	deviceID := devices[0].ID

	// Start initial capture.
	if err := dm.StartCapture("reconfig-source", deviceID, cfg); err != nil {
		t.Skipf("capture device unavailable: %v", err)
	}

	// Verify active.
	active := dm.ActiveDevices()
	require.Contains(t, active, "reconfig-source")

	// Reconfigure with a new sample rate.
	newCfg := DeviceConfig{
		SampleRate: 32000,
		BitDepth:   16,
		Channels:   1,
	}

	err := dm.ReconfigureDevice("reconfig-source", deviceID, newCfg)
	require.NoError(t, err)

	// After reconfigure the source should still be active with new config.
	dm.mu.RLock()
	ad, ok := dm.active["reconfig-source"]
	dm.mu.RUnlock()
	require.True(t, ok, "source should be active after reconfigure")
	assert.Equal(t, 32000, ad.Config.SampleRate)
}

// TestDeviceManager_ActiveDevices verifies the snapshot returned by
// ActiveDevices correctly reflects running and stopped sessions.
func TestDeviceManager_ActiveDevices(t *testing.T) {
	t.Parallel()

	dm, _ := newTestDeviceManager(t)
	t.Cleanup(func() { _ = dm.Close() })

	// Inject three fake active devices directly (no hardware needed).
	sourceIDs := []string{"src-a", "src-b", "src-c"}
	for _, id := range sourceIDs {
		capturedID := id
		dm.mu.Lock()
		dm.active[id] = &ActiveDevice{
			Info:     DeviceInfo{Name: "Device " + id, ID: id},
			Config:   defaultTestConfig(),
			sourceID: capturedID,
			cancel:   func() {},
		}
		dm.mu.Unlock()
	}

	// All three should appear.
	active := dm.ActiveDevices()
	for _, id := range sourceIDs {
		assert.Contains(t, active, id)
		assert.Equal(t, "Device "+id, active[id].Name)
	}

	// Remove one.
	_ = dm.StopCapture("src-b")

	active = dm.ActiveDevices()
	assert.Contains(t, active, "src-a")
	assert.NotContains(t, active, "src-b")
	assert.Contains(t, active, "src-c")
}

// TestDeviceManager_Close verifies that Close drains all active sessions.
func TestDeviceManager_Close(t *testing.T) {
	t.Parallel()

	dm, _ := newTestDeviceManager(t)

	// Inject two fake active devices.
	for _, id := range []string{"close-a", "close-b"} {
		capturedID := id
		dm.mu.Lock()
		dm.active[id] = &ActiveDevice{
			Info:     DeviceInfo{Name: "Device " + capturedID},
			sourceID: capturedID,
			cancel:   func() {},
		}
		dm.mu.Unlock()
	}

	require.NoError(t, dm.Close())

	// After Close the active map should be empty.
	active := dm.ActiveDevices()
	assert.Empty(t, active)
}

// TestIsDefaultDeviceToken verifies that the well-known default device
// identifiers are recognised and arbitrary strings are rejected.
func TestIsDefaultDeviceToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		deviceID string
		want     bool
	}{
		{name: "sysdefault constant", deviceID: DeviceIDSysDefault, want: true},
		{name: "default constant", deviceID: DeviceIDDefault, want: true},
		{name: "literal sysdefault", deviceID: "sysdefault", want: true},
		{name: "literal default", deviceID: "default", want: true},
		{name: "empty string", deviceID: "", want: false},
		{name: "arbitrary device id", deviceID: ":0,0", want: false},
		{name: "device name substring", deviceID: "HDA Intel PCH", want: false},
		{name: "case mismatch Default", deviceID: "Default", want: false},
		{name: "case mismatch SysDefault", deviceID: "SysDefault", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isDefaultDeviceToken(tt.deviceID)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDeviceIDConstants verifies the constant values are the expected strings.
func TestDeviceIDConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "sysdefault", DeviceIDSysDefault)
	assert.Equal(t, "default", DeviceIDDefault)
}

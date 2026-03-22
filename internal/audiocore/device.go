// Package audiocore provides the core audio infrastructure for BirdNET-Go.
// device.go — DeviceManager for local audio capture device lifecycle management.
package audiocore

import (
	"context"
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// DeviceInfo describes an enumerated audio capture device.
type DeviceInfo struct {
	// Index is the position in the device enumeration list.
	Index int

	// Name is the human-readable device name (e.g., "HDA Intel PCH: ALC3202 Analog").
	Name string

	// ID is the decoded device identifier (e.g., ":0,0" on ALSA).
	ID string
}

// DeviceConfig carries the capture parameters for a device.
type DeviceConfig struct {
	// SampleRate in Hz (e.g., 48000).
	SampleRate int

	// BitDepth in bits (e.g., 16).
	BitDepth int

	// Channels count (e.g., 1 for mono).
	Channels int
}

// ActiveDevice holds the runtime state for a running capture session.
type ActiveDevice struct {
	// Info describes the audio device being captured.
	Info DeviceInfo

	// Config holds the capture parameters in use.
	Config DeviceConfig

	// sourceID uniquely identifies this capture session within the system.
	sourceID string

	// cancel stops the capture goroutine for this device.
	cancel context.CancelFunc
}

// ErrDeviceAlreadyActive is returned when StartCapture is called with a
// sourceID that already has an active capture session.
var ErrDeviceAlreadyActive = errors.Newf("device already active").
	Component("audiocore").Category(errors.CategoryState).Build()

// ErrDeviceNotActive is returned when StopCapture is called with a sourceID
// that is not currently capturing.
var ErrDeviceNotActive = errors.Newf("device not active").
	Component("audiocore").Category(errors.CategoryState).Build()

// DeviceManager manages the lifecycle of local audio capture devices.
// Each sourceID maps to exactly one active capture goroutine at any time.
// It is safe for concurrent use.
type DeviceManager struct {
	// dispatcher receives AudioFrames produced by active capture sessions.
	dispatcher AudioDispatcher

	// active maps sourceID → running capture session.
	active map[string]*ActiveDevice

	// mu guards the active map.
	mu sync.RWMutex

	// log is the structured logger for this manager.
	log logger.Logger
}

// NewDeviceManager creates a DeviceManager that dispatches captured frames
// to the given AudioDispatcher.
func NewDeviceManager(dispatcher AudioDispatcher, log logger.Logger) *DeviceManager {
	return &DeviceManager{
		dispatcher: dispatcher,
		active:     make(map[string]*ActiveDevice),
		log:        log.With(logger.String("component", "device_manager")),
	}
}

// ListDevices enumerates available audio capture devices on the host.
// It returns an error if the platform audio context cannot be initialised.
func (dm *DeviceManager) ListDevices() ([]DeviceInfo, error) {
	return listDevices(dm.log)
}

// StartCapture begins audio capture from the device identified by deviceID,
// dispatching AudioFrames tagged with sourceID to the manager's dispatcher.
//
// deviceID must match either the decoded device ID or a substring of the
// device name (same matching rule as the legacy myaudio package).
//
// Returns ErrDeviceAlreadyActive when the same sourceID is already running.
func (dm *DeviceManager) StartCapture(sourceID, deviceID string, cfg DeviceConfig) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if _, exists := dm.active[sourceID]; exists {
		return fmt.Errorf("start capture %s: %w", sourceID, ErrDeviceAlreadyActive)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ad := &ActiveDevice{
		Config:   cfg,
		sourceID: sourceID,
		cancel:   cancel,
	}

	info, err := startCapture(ctx, sourceID, deviceID, cfg, dm.dispatcher, dm.log)
	if err != nil {
		cancel()
		return fmt.Errorf("start capture for source %s: %w", sourceID, err)
	}

	ad.Info = info
	dm.active[sourceID] = ad

	dm.log.Info("capture started",
		logger.String("source_id", sourceID),
		logger.String("device", info.Name),
		logger.String("device_id", deviceID))

	return nil
}

// StopCapture stops the active capture session for the given sourceID.
// Returns ErrDeviceNotActive when no session is running for that ID.
func (dm *DeviceManager) StopCapture(sourceID string) error {
	dm.mu.Lock()
	ad, exists := dm.active[sourceID]
	if !exists {
		dm.mu.Unlock()
		return fmt.Errorf("stop capture %s: %w", sourceID, ErrDeviceNotActive)
	}
	delete(dm.active, sourceID)
	dm.mu.Unlock()

	ad.cancel()

	dm.log.Info("capture stopped",
		logger.String("source_id", sourceID),
		logger.String("device", ad.Info.Name))

	return nil
}

// ReconfigureDevice stops the existing capture session for sourceID (if any),
// then restarts it with the new deviceID and config.
// If no session is running, it behaves identically to StartCapture.
func (dm *DeviceManager) ReconfigureDevice(sourceID, deviceID string, cfg DeviceConfig) error {
	// Stop existing session if present (ignore ErrDeviceNotActive).
	if err := dm.StopCapture(sourceID); err != nil && !errors.Is(err, ErrDeviceNotActive) {
		return fmt.Errorf("reconfigure device %s: stop failed: %w", sourceID, err)
	}

	if err := dm.StartCapture(sourceID, deviceID, cfg); err != nil {
		return fmt.Errorf("reconfigure device %s: restart failed: %w", sourceID, err)
	}

	return nil
}

// ActiveDevices returns a snapshot of all currently running capture sessions,
// keyed by sourceID. The map is a copy and safe to read after the call returns.
func (dm *DeviceManager) ActiveDevices() map[string]DeviceInfo {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	result := make(map[string]DeviceInfo, len(dm.active))
	for id, ad := range dm.active {
		result[id] = ad.Info
	}
	return result
}

// Close stops all active capture sessions and releases manager resources.
// It is safe to call Close multiple times.
func (dm *DeviceManager) Close() error {
	dm.mu.Lock()
	active := dm.active
	dm.active = make(map[string]*ActiveDevice)
	dm.mu.Unlock()

	for _, ad := range active {
		ad.cancel()
		dm.log.Info("capture stopped on close",
			logger.String("source_id", ad.sourceID),
			logger.String("device", ad.Info.Name))
	}

	return nil
}

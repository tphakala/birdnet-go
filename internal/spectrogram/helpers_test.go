package spectrogram

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// Test audio parameters for PCM generation
const (
	defaultSampleRate = 48000        // 48kHz PCM
	defaultDuration   = 1            // 1 second clips
	defaultFrequency  = 440.0        // 440Hz (A4 note)
	defaultAmplitude  = int16(10000) // Moderate volume
)

// testEnv holds common test environment components
type testEnv struct {
	TempDir  string
	Settings *conf.Settings
	SFS      *securefs.SecureFS
	Ctx      context.Context
	Cancel   context.CancelFunc
}

// setupTestEnv creates a complete test environment with SecureFS and settings.
// Caller should defer env.Cleanup() to ensure proper cleanup.
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	tempDir := t.TempDir()

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	ctx, cancel := context.WithCancel(t.Context())

	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	env := &testEnv{
		TempDir:  tempDir,
		Settings: settings,
		SFS:      sfs,
		Ctx:      ctx,
		Cancel:   cancel,
	}

	t.Cleanup(func() {
		cancel()
		_ = sfs.Close()
	})

	return env
}

// PreRendererTestOptions configures PreRenderer settings for testing
type PreRendererTestOptions struct {
	SoxPath        string
	SpectrogramSize string
	RawEnabled     bool
}

// DefaultPreRendererOptions returns sensible defaults for testing
func DefaultPreRendererOptions() *PreRendererTestOptions {
	return &PreRendererTestOptions{
		SoxPath:        "/usr/bin/sox",
		SpectrogramSize: "sm",
		RawEnabled:     true,
	}
}

// configurePreRendererSettings updates settings for PreRenderer testing
func configurePreRendererSettings(settings *conf.Settings, opts *PreRendererTestOptions) {
	if opts == nil {
		opts = DefaultPreRendererOptions()
	}
	settings.Realtime.Audio.SoxPath = opts.SoxPath
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = opts.SpectrogramSize
	settings.Realtime.Dashboard.Spectrogram.Raw = opts.RawEnabled
}

// createTestPreRenderer creates a PreRenderer with test configuration.
// Returns the PreRenderer and a cleanup function.
func createTestPreRenderer(t *testing.T, opts *PreRendererTestOptions) (*PreRenderer, *testEnv) {
	t.Helper()

	env := setupTestEnv(t)
	configurePreRendererSettings(env.Settings, opts)

	pr := NewPreRenderer(env.Ctx, env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	return pr, env
}

// PCMOptions configures synthetic PCM data generation
type PCMOptions struct {
	SampleRate int
	Duration   int     // seconds
	Frequency  float64 // Hz
	Amplitude  int16
}

// DefaultPCMOptions returns standard test audio parameters
func DefaultPCMOptions() *PCMOptions {
	return &PCMOptions{
		SampleRate: defaultSampleRate,
		Duration:   defaultDuration,
		Frequency:  defaultFrequency,
		Amplitude:  defaultAmplitude,
	}
}

// generateTestPCMData creates synthetic PCM audio data (16-bit signed, little-endian, mono).
// Generates a sine wave at the specified frequency.
func generateTestPCMData(opts *PCMOptions) []byte {
	if opts == nil {
		opts = DefaultPCMOptions()
	}

	numSamples := opts.SampleRate * opts.Duration
	pcmData := make([]byte, numSamples*2) // 2 bytes per sample (16-bit)

	for i := range numSamples {
		// Calculate sine wave sample value
		sample := int16(float64(opts.Amplitude) * math.Sin(2*math.Pi*opts.Frequency*float64(i)/float64(opts.SampleRate)))
		// Convert to little-endian bytes
		pcmData[i*2] = byte(sample & 0xFF)
		pcmData[i*2+1] = byte((sample >> 8) & 0xFF)
	}

	return pcmData
}

// waitForFile polls for a file to exist within the timeout.
// Returns true if file exists, false if timeout.
func waitForFile(t *testing.T, path string, timeout, pollInterval time.Duration) bool {
	t.Helper()

	timeoutCh := time.After(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCh:
			return false
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
	}
}

// requireFileExists waits for a file and fails the test if not found
func requireFileExists(t *testing.T, path string, timeout, pollInterval time.Duration, msgAndArgs ...any) {
	t.Helper()
	if !waitForFile(t, path, timeout, pollInterval) {
		require.Fail(t, "Timeout waiting for file", append([]any{"path", path}, msgAndArgs...)...)
	}
}

// createTestJob creates a Job with the given parameters and sensible defaults.
func createTestJob(clipPath string, noteID uint) *Job {
	return &Job{
		PCMData:   []byte{0, 1, 2, 3},
		ClipPath:  clipPath,
		NoteID:    noteID,
		Timestamp: time.Now(),
	}
}

// createMinimalPreRenderer creates a minimal PreRenderer for unit tests.
// Unlike createTestPreRenderer, this does NOT use NewPreRenderer and allows
// fine-grained control over queue size for testing queue behavior.
func createMinimalPreRenderer(t *testing.T, queueSize int) (*PreRenderer, *testEnv) {
	t.Helper()

	env := setupTestEnv(t)

	pr := &PreRenderer{
		settings: env.Settings,
		sfs:      env.SFS,
		logger:   logger.Global().Module("spectrogram.test"),
		ctx:      env.Ctx,
		cancel:   env.Cancel,
		jobs:     make(chan *Job, queueSize),
	}

	return pr, env
}

// requireSoxAvailable checks if Sox is available in PATH.
// Skips the test if Sox is not found, otherwise returns the Sox path.
func requireSoxAvailable(t *testing.T) string {
	t.Helper()

	soxPath, err := exec.LookPath("sox")
	if err != nil {
		t.Skip("Sox binary not found in PATH; skipping integration test")
	}
	return soxPath
}

// integrationTestEnv extends testEnv with integration-specific fields
type integrationTestEnv struct {
	*testEnv
	PreRenderer *PreRenderer
	AudioDir    string
}

// createIntegrationPreRenderer creates a fully-configured PreRenderer for integration tests.
// This helper eliminates the repeated setup pattern across integration tests.
// The PreRenderer is started automatically; caller should defer env.PreRenderer.Stop().
func createIntegrationPreRenderer(t *testing.T, soxPath, audioDirName string) *integrationTestEnv {
	t.Helper()

	env := setupTestEnv(t)

	// Configure settings for integration testing
	env.Settings.Realtime.Audio.SoxPath = soxPath
	env.Settings.Realtime.Dashboard.Spectrogram.Enabled = true
	env.Settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	env.Settings.Realtime.Dashboard.Spectrogram.Raw = true

	pr := NewPreRenderer(env.Ctx, env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))
	pr.Start()

	// Create audio output directory
	audioDir := env.TempDir
	if audioDirName != "" {
		audioDir = filepath.Join(env.TempDir, audioDirName)
		err := os.MkdirAll(audioDir, 0o750)
		require.NoError(t, err, "Failed to create audio directory")
	}

	return &integrationTestEnv{
		testEnv:     env,
		PreRenderer: pr,
		AudioDir:    audioDir,
	}
}

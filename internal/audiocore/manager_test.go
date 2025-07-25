package audiocore

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSource implements a basic AudioSource for testing
type mockSource struct {
	id         string
	name       string
	active     bool
	outputChan chan AudioData
	errorChan  chan error
	startErr   error
	stopErr    error
	format     AudioFormat
}

func newMockSource(id, name string) *mockSource {
	return &mockSource{
		id:         id,
		name:       name,
		outputChan: make(chan AudioData, 10),
		errorChan:  make(chan error, 10),
		format: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
	}
}

func (m *mockSource) ID() string                      { return m.id }
func (m *mockSource) Name() string                    { return m.name }
func (m *mockSource) Start(ctx context.Context) error { m.active = true; return m.startErr }
func (m *mockSource) Stop() error                     { m.active = false; return m.stopErr }
func (m *mockSource) AudioOutput() <-chan AudioData   { return m.outputChan }
func (m *mockSource) Errors() <-chan error            { return m.errorChan }
func (m *mockSource) IsActive() bool                  { return m.active }
func (m *mockSource) GetFormat() AudioFormat          { return m.format }
func (m *mockSource) SetGain(gain float64) error      { return nil }

func TestManagerCreateAndStart(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources:        10,
		DefaultBufferSize: 4096,
		EnableMetrics:     false,
	}

	manager := NewAudioManager(config)
	require.NotNil(t, manager)

	// Add a mock source
	source := newMockSource("test-source", "Test Source")
	err := manager.AddSource(source)
	require.NoError(t, err)

	// Start the manager
	ctx := context.Background()
	err = manager.Start(ctx)
	require.NoError(t, err)

	// Verify source is active
	assert.True(t, source.IsActive())

	// Stop the manager
	err = manager.Stop()
	require.NoError(t, err)

	// Verify source is stopped
	assert.False(t, source.IsActive())
}

func TestManagerAddDuplicateSource(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Add first source
	source1 := newMockSource("duplicate-id", "Source 1")
	err := manager.AddSource(source1)
	require.NoError(t, err)

	// Try to add duplicate
	source2 := newMockSource("duplicate-id", "Source 2")
	err = manager.AddSource(source2)
	require.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "already exists")
	}
}

func TestManagerMaxSources(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 2,
	}

	manager := NewAudioManager(config)

	// Add sources up to limit
	for i := 0; i < 2; i++ {
		source := newMockSource("source-"+strconv.Itoa(i), "Source")
		err := manager.AddSource(source)
		require.NoError(t, err)
	}

	// Try to exceed limit
	source := newMockSource("extra-source", "Extra Source")
	err := manager.AddSource(source)
	require.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "max sources reached")
	}
}

func TestManagerRemoveSource(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Add a source
	source := newMockSource("remove-test", "Test Source")
	err := manager.AddSource(source)
	require.NoError(t, err)

	// Remove it
	err = manager.RemoveSource("remove-test")
	require.NoError(t, err)

	// Try to remove non-existent
	err = manager.RemoveSource("remove-test")
	require.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "not found")
	}
}

func TestManagerGetSource(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Add a source
	source := newMockSource("get-test", "Test Source")
	err := manager.AddSource(source)
	require.NoError(t, err)

	// Get existing source
	retrieved, exists := manager.GetSource("get-test")
	assert.True(t, exists)
	assert.Equal(t, source.ID(), retrieved.ID())

	// Get non-existent source
	_, exists = manager.GetSource("non-existent")
	assert.False(t, exists)
}

func TestManagerProcessorChain(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Add a source
	source := newMockSource("chain-test", "Test Source")
	err := manager.AddSource(source)
	require.NoError(t, err)

	// Set processor chain
	chain := NewProcessorChain()
	err = manager.SetProcessorChain("chain-test", chain)
	require.NoError(t, err)

	// Try to set chain for non-existent source
	err = manager.SetProcessorChain("non-existent", chain)
	require.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "not found")
	}
}

func TestManagerStartupErrors(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Add a source that fails to start
	source := newMockSource("fail-source", "Failing Source")
	source.startErr = assert.AnError
	err := manager.AddSource(source)
	require.NoError(t, err)

	// Start should fail
	ctx := context.Background()
	err = manager.Start(ctx)
	require.Error(t, err)
}

func TestManagerAlreadyStarted(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Start manager
	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)

	// Try to start again
	err = manager.Start(ctx)
	require.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "already started")
	}

	// Clean up
	err = manager.Stop()
	require.NoError(t, err)
}

func TestManagerNotStarted(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources: 10,
	}

	manager := NewAudioManager(config)

	// Try to stop without starting
	err := manager.Stop()
	require.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "not started")
	}
}

func TestManagerAudioOutput(t *testing.T) {
	t.Parallel()
	config := &ManagerConfig{
		MaxSources:        10,
		DefaultBufferSize: 4096,
	}

	manager := NewAudioManager(config)

	// Add a source
	source := newMockSource("output-test", "Test Source")
	err := manager.AddSource(source)
	require.NoError(t, err)

	// Start manager
	ctx := context.Background()
	err = manager.Start(ctx)
	require.NoError(t, err)

	// Send some audio data
	testData := AudioData{
		Buffer:    []byte{1, 2, 3, 4},
		Format:    source.format,
		Timestamp: time.Now(),
		Duration:  time.Millisecond * 100,
		SourceID:  source.ID(),
	}

	// Create channels for deterministic synchronization
	receivedChan := make(chan AudioData, 1)
	errChan := make(chan error, 1)
	done := make(chan struct{})
	
	// Create a context with cancellation for the goroutine
	goroutineCtx, goroutineCancel := context.WithCancel(context.Background())
	defer goroutineCancel()
	
	// Start a goroutine to receive from manager output
	go func() {
		defer close(done)
		select {
		case received := <-manager.AudioOutput():
			receivedChan <- received
		case <-goroutineCtx.Done():
			errChan <- fmt.Errorf("context cancelled while waiting for audio output")
		}
	}()

	// Send data to source
	source.outputChan <- testData

	// Wait for result with timeout
	testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer testCancel()
	
	select {
	case received := <-receivedChan:
		assert.Equal(t, testData.SourceID, received.SourceID)
		assert.Equal(t, testData.Buffer, received.Buffer)
	case err := <-errChan:
		t.Fatal(err)
	case <-testCtx.Done():
		goroutineCancel() // Cancel the goroutine
		<-done // Wait for goroutine to finish
		t.Fatal("timeout waiting for test to complete")
	}

	// Clean up
	err = manager.Stop()
	require.NoError(t, err)
}

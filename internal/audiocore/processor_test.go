package audiocore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProcessor implements AudioProcessor for testing
type mockProcessor struct {
	id             string
	processFunc    func(*AudioData) (*AudioData, error)
	requiredFormat *AudioFormat
	outputFormat   AudioFormat
}

func (m *mockProcessor) ID() string { return m.id }

func (m *mockProcessor) Process(ctx context.Context, input *AudioData) (*AudioData, error) {
	if m.processFunc != nil {
		return m.processFunc(input)
	}
	return input, nil
}

func (m *mockProcessor) GetRequiredFormat() *AudioFormat { return m.requiredFormat }

func (m *mockProcessor) GetOutputFormat(inputFormat AudioFormat) AudioFormat {
	if m.outputFormat.SampleRate == 0 {
		return inputFormat
	}
	return m.outputFormat
}

func TestProcessorChainAddRemove(t *testing.T) {
	chain := NewProcessorChain()

	// Add processors
	proc1 := &mockProcessor{id: "proc1"}
	proc2 := &mockProcessor{id: "proc2"}

	err := chain.AddProcessor(proc1)
	assert.NoError(t, err)

	err = chain.AddProcessor(proc2)
	assert.NoError(t, err)

	// Get processors
	processors := chain.GetProcessors()
	assert.Len(t, processors, 2)

	// Try to add duplicate
	err = chain.AddProcessor(proc1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Try to add nil
	err = chain.AddProcessor(nil)
	assert.Error(t, err)

	// Remove processor
	err = chain.RemoveProcessor("proc1")
	assert.NoError(t, err)

	processors = chain.GetProcessors()
	assert.Len(t, processors, 1)

	// Remove non-existent
	err = chain.RemoveProcessor("proc1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProcessorChainProcess(t *testing.T) {
	chain := NewProcessorChain()
	ctx := context.Background()

	// Test empty chain
	input := &AudioData{
		Buffer:    []byte{1, 2, 3, 4},
		Timestamp: time.Now(),
		SourceID:  "test",
	}

	output, err := chain.Process(ctx, input)
	assert.NoError(t, err)
	assert.Equal(t, input, output)

	// Add processors that modify data
	proc1 := &mockProcessor{
		id: "doubler",
		processFunc: func(data *AudioData) (*AudioData, error) {
			newData := *data
			newData.Buffer = make([]byte, len(data.Buffer))
			for i, b := range data.Buffer {
				newData.Buffer[i] = b * 2
			}
			return &newData, nil
		},
	}

	proc2 := &mockProcessor{
		id: "adder",
		processFunc: func(data *AudioData) (*AudioData, error) {
			newData := *data
			newData.Buffer = make([]byte, len(data.Buffer))
			for i, b := range data.Buffer {
				newData.Buffer[i] = b + 1
			}
			return &newData, nil
		},
	}

	err = chain.AddProcessor(proc1)
	assert.NoError(t, err)

	err = chain.AddProcessor(proc2)
	assert.NoError(t, err)

	// Process through chain
	output, err = chain.Process(ctx, input)
	assert.NoError(t, err)
	require.NotNil(t, output)

	// Verify processing: (1,2,3,4) -> double -> (2,4,6,8) -> add 1 -> (3,5,7,9)
	expected := []byte{3, 5, 7, 9}
	assert.Equal(t, expected, output.Buffer)
}

func TestProcessorChainProcessError(t *testing.T) {
	chain := NewProcessorChain()
	ctx := context.Background()

	// Add processor that returns error
	proc := &mockProcessor{
		id: "error-proc",
		processFunc: func(data *AudioData) (*AudioData, error) {
			return nil, assert.AnError
		},
	}

	err := chain.AddProcessor(proc)
	assert.NoError(t, err)

	input := &AudioData{
		Buffer:    []byte{1, 2, 3, 4},
		Timestamp: time.Now(),
		SourceID:  "test",
	}

	output, err := chain.Process(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, output)
}

func TestProcessorChainContextCancellation(t *testing.T) {
	chain := NewProcessorChain()
	ctx, cancel := context.WithCancel(context.Background())

	// Add a processor
	proc := &mockProcessor{id: "proc"}
	err := chain.AddProcessor(proc)
	assert.NoError(t, err)

	// Cancel context
	cancel()

	input := &AudioData{
		Buffer:    []byte{1, 2, 3, 4},
		Timestamp: time.Now(),
		SourceID:  "test",
	}

	output, err := chain.Process(ctx, input)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, output)
}

func TestProcessorChainGetProcessors(t *testing.T) {
	chain := NewProcessorChain()

	proc1 := &mockProcessor{id: "proc1"}
	proc2 := &mockProcessor{id: "proc2"}

	chain.AddProcessor(proc1)
	chain.AddProcessor(proc2)

	// Get processors should return a copy
	processors := chain.GetProcessors()
	assert.Len(t, processors, 2)

	// Modifying returned slice should not affect chain
	processors = processors[:1]
	assert.Len(t, processors, 1)

	// Chain should still have 2
	processors = chain.GetProcessors()
	assert.Len(t, processors, 2)
}
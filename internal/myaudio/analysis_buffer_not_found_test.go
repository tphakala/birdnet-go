package myaudio

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestWriteToAnalysisBuffer_NotFound verifies that writing to a non-existent
// buffer returns ErrBufferNotFound, enabling callers to handle it gracefully.
func TestWriteToAnalysisBuffer_NotFound(t *testing.T) {
	t.Parallel()

	err := WriteToAnalysisBuffer("nonexistent_source_id", []byte{0x01, 0x02})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBufferNotFound),
		"expected ErrBufferNotFound sentinel, got: %v", err)
	assert.Contains(t, err.Error(), "nonexistent_source_id")
}

// TestReadFromAnalysisBuffer_NotFound verifies that reading from a non-existent
// buffer returns ErrBufferNotFound, enabling the monitor to exit gracefully.
func TestReadFromAnalysisBuffer_NotFound(t *testing.T) {
	t.Parallel()

	data, err := ReadFromAnalysisBuffer("nonexistent_source_id")

	require.Error(t, err)
	assert.Nil(t, data)
	assert.True(t, errors.Is(err, ErrBufferNotFound),
		"expected ErrBufferNotFound sentinel, got: %v", err)
	assert.Contains(t, err.Error(), "nonexistent_source_id")
}

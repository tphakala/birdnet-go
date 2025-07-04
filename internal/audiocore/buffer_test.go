package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferPoolCreation(t *testing.T) {
	config := BufferPoolConfig{
		SmallBufferSize:   4 * 1024,
		MediumBufferSize:  64 * 1024,
		LargeBufferSize:   1024 * 1024,
		MaxBuffersPerSize: 100,
	}

	pool := NewBufferPool(config)
	require.NotNil(t, pool)

	stats := pool.Stats()
	assert.Equal(t, 0, stats.ActiveBuffers)
}

func TestBufferPoolGetAndPut(t *testing.T) {
	config := BufferPoolConfig{
		SmallBufferSize:   4 * 1024,
		MediumBufferSize:  64 * 1024,
		LargeBufferSize:   1024 * 1024,
		MaxBuffersPerSize: 100,
	}

	pool := NewBufferPool(config)

	// Test small buffer
	t.Run("SmallBuffer", func(t *testing.T) {
		buf := pool.Get(1024)
		require.NotNil(t, buf)
		assert.Equal(t, 1024, buf.Len())
		assert.GreaterOrEqual(t, buf.Cap(), 4*1024)

		stats := pool.Stats()
		assert.Equal(t, 1, stats.ActiveBuffers)

		buf.Release()
		stats = pool.Stats()
		assert.Equal(t, 0, stats.ActiveBuffers)
	})

	// Test medium buffer
	t.Run("MediumBuffer", func(t *testing.T) {
		buf := pool.Get(32 * 1024)
		require.NotNil(t, buf)
		assert.Equal(t, 32*1024, buf.Len())
		assert.GreaterOrEqual(t, buf.Cap(), 64*1024)

		buf.Release()
	})

	// Test large buffer
	t.Run("LargeBuffer", func(t *testing.T) {
		buf := pool.Get(512 * 1024)
		require.NotNil(t, buf)
		assert.Equal(t, 512*1024, buf.Len())
		assert.GreaterOrEqual(t, buf.Cap(), 1024*1024)

		buf.Release()
	})

	// Test custom size buffer
	t.Run("CustomBuffer", func(t *testing.T) {
		buf := pool.Get(2 * 1024 * 1024)
		require.NotNil(t, buf)
		assert.Equal(t, 2*1024*1024, buf.Len())
		assert.Equal(t, 2*1024*1024, buf.Cap())

		buf.Release()
	})
}

func TestBufferOperations(t *testing.T) {
	config := BufferPoolConfig{
		SmallBufferSize:   4 * 1024,
		MediumBufferSize:  64 * 1024,
		LargeBufferSize:   1024 * 1024,
		MaxBuffersPerSize: 100,
	}

	pool := NewBufferPool(config)
	buf := pool.Get(100)

	t.Run("Data", func(t *testing.T) {
		data := buf.Data()
		assert.NotNil(t, data)
		assert.Len(t, data, 100)
	})

	t.Run("Reset", func(t *testing.T) {
		buf.Reset()
		assert.Equal(t, 0, buf.Len())
		assert.GreaterOrEqual(t, buf.Cap(), 100)
	})

	t.Run("Resize", func(t *testing.T) {
		err := buf.Resize(50)
		assert.NoError(t, err)
		assert.Equal(t, 50, buf.Len())

		// Resize larger than capacity
		err = buf.Resize(10000)
		assert.NoError(t, err)
		assert.Equal(t, 10000, buf.Len())
		assert.GreaterOrEqual(t, buf.Cap(), 10000)

		// Invalid resize
		err = buf.Resize(-1)
		assert.Error(t, err)
	})

	t.Run("Slice", func(t *testing.T) {
		err := buf.Resize(100)
		assert.NoError(t, err)
		
		// Valid slice
		slice, err := buf.Slice(10, 20)
		assert.NoError(t, err)
		assert.Len(t, slice, 10)

		// Invalid slices
		_, err = buf.Slice(-1, 10)
		assert.Error(t, err)

		_, err = buf.Slice(0, 200)
		assert.Error(t, err)

		_, err = buf.Slice(20, 10)
		assert.Error(t, err)
	})

	buf.Release()
}

func TestBufferReferenceCount(t *testing.T) {
	config := BufferPoolConfig{
		SmallBufferSize:   4 * 1024,
		MediumBufferSize:  64 * 1024,
		LargeBufferSize:   1024 * 1024,
		MaxBuffersPerSize: 100,
	}

	pool := NewBufferPool(config)

	buf := pool.Get(100)
	stats := pool.Stats()
	assert.Equal(t, 1, stats.ActiveBuffers)

	// Acquire increases ref count
	buf.Acquire()
	buf.Release() // Should not return to pool yet
	stats = pool.Stats()
	assert.Equal(t, 1, stats.ActiveBuffers)

	// Final release returns to pool
	buf.Release()
	stats = pool.Stats()
	assert.Equal(t, 0, stats.ActiveBuffers)
}
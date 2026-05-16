package api

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeBNHM_Roundtrip(t *testing.T) {
	t.Parallel()

	cols, rows := 3, 2
	south := float32(60.0)
	west := float32(24.0)
	resolution := float32(0.5)

	data := make([]float32, bnhmWeeks*rows*cols)
	for i := range data {
		data[i] = float32(i) * 0.001
	}

	encoded := encodeBNHM(cols, rows, south, west, resolution, data)

	// Verify header
	assert.Len(t, encoded, bnhmHeaderSize+len(data)*4)
	assert.Equal(t, "BNHM", string(encoded[0:4]))
	assert.Equal(t, uint32(1), binary.LittleEndian.Uint32(encoded[4:8]))
	assert.Equal(t, uint32(cols), binary.LittleEndian.Uint32(encoded[8:12]))
	assert.Equal(t, uint32(rows), binary.LittleEndian.Uint32(encoded[12:16]))
	assert.Equal(t, uint32(bnhmWeeks), binary.LittleEndian.Uint32(encoded[16:20]))
	assert.InDelta(t, south, math.Float32frombits(binary.LittleEndian.Uint32(encoded[20:24])), 0.0001)
	assert.InDelta(t, west, math.Float32frombits(binary.LittleEndian.Uint32(encoded[24:28])), 0.0001)
	assert.InDelta(t, resolution, math.Float32frombits(binary.LittleEndian.Uint32(encoded[28:32])), 0.0001)

	// Verify data via decodeBNHMHeader
	hdr, err := decodeBNHMHeader(encoded)
	require.NoError(t, err)
	assert.Equal(t, cols, hdr.Cols)
	assert.Equal(t, rows, hdr.Rows)
	assert.Equal(t, bnhmWeeks, hdr.Weeks)
	assert.InDelta(t, south, hdr.South, 0.0001)
	assert.InDelta(t, west, hdr.West, 0.0001)
	assert.InDelta(t, resolution, hdr.Resolution, 0.0001)

	// Verify data values roundtrip
	for i, expected := range data {
		offset := bnhmHeaderSize + i*4
		got := math.Float32frombits(binary.LittleEndian.Uint32(encoded[offset : offset+4]))
		assert.InDelta(t, expected, got, 0.0001, "data[%d] mismatch", i)
	}
}

func TestDecodeBNHMHeader_InvalidMagic(t *testing.T) {
	t.Parallel()

	buf := make([]byte, bnhmHeaderSize)
	copy(buf[0:4], "XXXX")
	_, err := decodeBNHMHeader(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid magic")
}

func TestDecodeBNHMHeader_BufferTooSmall(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 10)
	_, err := decodeBNHMHeader(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer too small")
}

func TestDecodeBNHMHeader_UnsupportedVersion(t *testing.T) {
	t.Parallel()

	buf := make([]byte, bnhmHeaderSize)
	copy(buf[0:4], bnhmMagic)
	binary.LittleEndian.PutUint32(buf[4:8], 99)
	_, err := decodeBNHMHeader(buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported version")
}

func TestHeatmapGridDimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                       string
		south, north, west, east   float64
		resolution                 float64
		expectedRows, expectedCols int
	}{
		{
			name:  "simple 1 degree grid",
			south: 60, north: 62, west: 24, east: 26,
			resolution:   1.0,
			expectedRows: 2, expectedCols: 2,
		},
		{
			name:  "fractional grid rounds up",
			south: 60, north: 61.5, west: 24, east: 25.5,
			resolution:   1.0,
			expectedRows: 2, expectedCols: 2,
		},
		{
			name:  "finland at 0.25 degrees",
			south: 59.5, north: 70.0, west: 19.0, east: 31.5,
			resolution:   0.25,
			expectedRows: 42, expectedCols: 50,
		},
		{
			name:  "tiny area",
			south: 60, north: 60.05, west: 24, east: 24.05,
			resolution:   0.1,
			expectedRows: 1, expectedCols: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows, cols := heatmapGridDimensions(tt.south, tt.north, tt.west, tt.east, tt.resolution)
			assert.Equal(t, tt.expectedRows, rows)
			assert.Equal(t, tt.expectedCols, cols)
		})
	}
}

func TestHeatmapLRU_BasicOperations(t *testing.T) {
	t.Parallel()

	cache := newHeatmapLRU(3)

	// Miss on empty cache
	_, ok := cache.get("key1")
	assert.False(t, ok)

	// Put and get
	cache.put("key1", []byte("value1"))
	val, ok := cache.get("key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("value1"), val)

	// Overwrite existing
	cache.put("key1", []byte("updated"))
	val, ok = cache.get("key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("updated"), val)
}

func TestHeatmapLRU_Eviction(t *testing.T) {
	t.Parallel()

	cache := newHeatmapLRU(2)

	cache.put("key1", []byte("v1"))
	cache.put("key2", []byte("v2"))
	cache.put("key3", []byte("v3")) // should evict key1

	_, ok := cache.get("key1")
	assert.False(t, ok, "key1 should have been evicted")

	val, ok := cache.get("key2")
	assert.True(t, ok)
	assert.Equal(t, []byte("v2"), val)

	val, ok = cache.get("key3")
	assert.True(t, ok)
	assert.Equal(t, []byte("v3"), val)
}

func TestHeatmapLRU_LRUOrder(t *testing.T) {
	t.Parallel()

	cache := newHeatmapLRU(2)

	cache.put("key1", []byte("v1"))
	cache.put("key2", []byte("v2"))

	// Access key1 to make it most recently used
	cache.get("key1")

	// Adding key3 should evict key2 (least recently used)
	cache.put("key3", []byte("v3"))

	_, ok := cache.get("key2")
	assert.False(t, ok, "key2 should have been evicted")

	val, ok := cache.get("key1")
	assert.True(t, ok)
	assert.Equal(t, []byte("v1"), val)
}

func TestHeatmapLRU_GenerationInvalidation(t *testing.T) {
	t.Parallel()

	cache := newHeatmapLRU(10)

	cache.put("key1", []byte("v1"))
	cache.put("key2", []byte("v2"))

	// Invalidate
	cache.invalidate()

	// Old entries should be treated as stale
	_, ok := cache.get("key1")
	assert.False(t, ok, "key1 should be stale after invalidation")

	// New entries after invalidation should work
	cache.put("key3", []byte("v3"))
	val, ok := cache.get("key3")
	assert.True(t, ok)
	assert.Equal(t, []byte("v3"), val)
}

func TestHeatmapCacheKey(t *testing.T) {
	t.Parallel()

	key := heatmapCacheKey("Turdus_merula_Eurasian_Blackbird", 59.5, 70.0, 19.0, 31.5, 0.5)
	assert.Equal(t, "Turdus_merula_Eurasian_Blackbird|59.500000|70.000000|19.000000|31.500000|0.500000", key)
}

func TestInvalidateHeatmapCache_ClearsEntries(t *testing.T) {
	t.Parallel()

	cache := newHeatmapLRU(10)
	cache.put("k1", []byte("data1"))
	cache.put("k2", []byte("data2"))

	// Entries exist before invalidation
	_, ok := cache.get("k1")
	assert.True(t, ok)

	cache.invalidate()

	// Entries are stale after invalidation
	_, ok = cache.get("k1")
	assert.False(t, ok)
	_, ok = cache.get("k2")
	assert.False(t, ok)

	// New entries work after invalidation
	cache.put("k3", []byte("data3"))
	val, ok := cache.get("k3")
	assert.True(t, ok)
	assert.Equal(t, []byte("data3"), val)
}

package analytics

import (
	"container/list"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// BNHM binary format constants
const (
	bnhmMagic      = "BNHM"
	bnhmVersion    = 1
	bnhmHeaderSize = 40
	bnhmWeeks      = apicore.WeeksPerYear // 48 BirdNET weeks (shared BirdNET week model)
)

// Heatmap validation constraints
const (
	heatmapMinResolution = 0.1
	heatmapMaxResolution = 5.0
	heatmapMaxGridCells  = 50000
	heatmapBatchSize     = 512
	heatmapOptBatchSize  = 4096
	heatmapMaxConcurrent = 2
)

// Valid stride values (divisors of 48)
var validStrides = map[int]bool{
	1: true, 2: true, 4: true, 6: true, 8: true, 12: true, 16: true, 24: true, 48: true,
}

// heatmapFlight deduplicates concurrent identical heatmap requests.
var heatmapFlight singleflight.Group

// heatmapSem limits concurrent heatmap computations to prevent memory exhaustion.
var heatmapSem = make(chan struct{}, heatmapMaxConcurrent)

// heatmapLRU is a thread-safe LRU cache for pre-encoded BNHM responses.
// Keyed by normalized request parameters; values are ready-to-send []byte.
type heatmapLRU struct {
	mu         sync.Mutex
	maxEntries int
	generation atomic.Uint64
	items      map[string]*list.Element
	order      *list.List
}

type heatmapCacheEntry struct {
	key        string
	value      []byte
	generation uint64
}

func newHeatmapLRU(maxEntries int) *heatmapLRU {
	return &heatmapLRU{
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element, maxEntries),
		order:      list.New(),
	}
}

// get returns the cached value and true on hit. On miss it returns the current
// generation so the caller can pass it to put for generation-aware insertion.
func (c *heatmapLRU) get(key string) (value []byte, gen uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentGen := c.generation.Load()

	elem, found := c.items[key]
	if !found {
		return nil, currentGen, false
	}

	entry := elem.Value.(*heatmapCacheEntry)
	if entry.generation != currentGen {
		c.order.Remove(elem)
		delete(c.items, key)
		return nil, currentGen, false
	}

	c.order.MoveToFront(elem)
	return entry.value, currentGen, true
}

// put stores a value only if the generation has not changed since the cache miss.
// This prevents stale computation results from poisoning the cache when an
// invalidation fires between the miss and the put.
func (c *heatmapLRU) put(key string, value []byte, expectedGen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.generation.Load() != expectedGen {
		return
	}

	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		entry := elem.Value.(*heatmapCacheEntry)
		entry.value = value
		entry.generation = expectedGen
		return
	}

	entry := &heatmapCacheEntry{
		key:        key,
		value:      value,
		generation: expectedGen,
	}
	elem := c.order.PushFront(entry)
	c.items[key] = elem

	for c.order.Len() > c.maxEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*heatmapCacheEntry).key)
	}
}

func (c *heatmapLRU) invalidate() {
	c.generation.Add(1)
}

// heatmapCacheKey builds a normalized cache key from request parameters.
func heatmapCacheKey(species string, south, north, west, east, resolution float64) string {
	return fmt.Sprintf("%s|%.6f|%.6f|%.6f|%.6f|%.6f",
		species, south, north, west, east, resolution)
}

// encodeBNHM encodes a heatmap grid into the BNHM binary format.
// data layout: float32[weeks][rows][cols], little-endian.
func encodeBNHM(cols, rows int, south, west, resolution float32, data []float32) []byte {
	return encodeBNHMWithWeeks(cols, rows, bnhmWeeks, south, west, resolution, data)
}

// encodeBNHMWithWeeks encodes a heatmap grid with a custom week count.
// Used when stride > 1 reduces the number of computed weeks.
func encodeBNHMWithWeeks(cols, rows, weeks int, south, west, resolution float32, data []float32) []byte {
	size := bnhmHeaderSize + len(data)*4
	buf := make([]byte, size)

	copy(buf[0:4], bnhmMagic)
	binary.LittleEndian.PutUint32(buf[4:8], bnhmVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(cols))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(rows))
	binary.LittleEndian.PutUint32(buf[16:20], uint32(weeks))
	binary.LittleEndian.PutUint32(buf[20:24], math.Float32bits(south))
	binary.LittleEndian.PutUint32(buf[24:28], math.Float32bits(west))
	binary.LittleEndian.PutUint32(buf[28:32], math.Float32bits(resolution))
	// bytes 32-39: reserved (already zeroed)

	offset := bnhmHeaderSize
	for _, v := range data {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], math.Float32bits(v))
		offset += 4
	}

	return buf
}

// bnhmHeader holds the decoded fields from a BNHM binary payload header.
type bnhmHeader struct {
	Cols       int
	Rows       int
	Weeks      int
	South      float32
	West       float32
	Resolution float32
}

// decodeBNHMHeader parses the header from a BNHM binary payload.
func decodeBNHMHeader(buf []byte) (bnhmHeader, error) {
	if len(buf) < bnhmHeaderSize {
		return bnhmHeader{}, fmt.Errorf("buffer too small: %d < %d", len(buf), bnhmHeaderSize)
	}
	if string(buf[0:4]) != bnhmMagic {
		return bnhmHeader{}, fmt.Errorf("invalid magic: %q", buf[0:4])
	}
	version := binary.LittleEndian.Uint32(buf[4:8])
	if version != bnhmVersion {
		return bnhmHeader{}, fmt.Errorf("unsupported version: %d", version)
	}

	return bnhmHeader{
		Cols:       int(binary.LittleEndian.Uint32(buf[8:12])),
		Rows:       int(binary.LittleEndian.Uint32(buf[12:16])),
		Weeks:      int(binary.LittleEndian.Uint32(buf[16:20])),
		South:      math.Float32frombits(binary.LittleEndian.Uint32(buf[20:24])),
		West:       math.Float32frombits(binary.LittleEndian.Uint32(buf[24:28])),
		Resolution: math.Float32frombits(binary.LittleEndian.Uint32(buf[28:32])),
	}, nil
}

// heatmapGridDimensions computes the number of rows and columns for a grid
// covering the given bounding box at the specified resolution.
func heatmapGridDimensions(south, north, west, east, resolution float64) (rows, cols int) {
	rows = int(math.Ceil((north - south) / resolution))
	cols = int(math.Ceil((east - west) / resolution))
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	return rows, cols
}

// heatmapParams holds validated request parameters for heatmap generation.
type heatmapParams struct {
	species    string
	south      float64
	north      float64
	west       float64
	east       float64
	resolution float64
	rows       int
	cols       int
	stride     int
}

// validateHeatmapParams parses and validates heatmap query parameters.
func (c *Handler) validateHeatmapParams(ctx echo.Context) (*heatmapParams, error) {
	species := ctx.QueryParam("species")
	if species == "" {
		return nil, fmt.Errorf("species parameter is required")
	}

	south, err := apicore.ParseFloat64(ctx.QueryParam("south"))
	if err != nil {
		return nil, fmt.Errorf("invalid south parameter: %w", err)
	}
	north, err := apicore.ParseFloat64(ctx.QueryParam("north"))
	if err != nil {
		return nil, fmt.Errorf("invalid north parameter: %w", err)
	}
	west, err := apicore.ParseFloat64(ctx.QueryParam("west"))
	if err != nil {
		return nil, fmt.Errorf("invalid west parameter: %w", err)
	}
	east, err := apicore.ParseFloat64(ctx.QueryParam("east"))
	if err != nil {
		return nil, fmt.Errorf("invalid east parameter: %w", err)
	}
	resolution, err := apicore.ParseFloat64(ctx.QueryParam("resolution"))
	if err != nil {
		return nil, fmt.Errorf("invalid resolution parameter: %w", err)
	}

	if math.IsNaN(south) || math.IsNaN(north) || math.IsNaN(west) || math.IsNaN(east) || math.IsNaN(resolution) {
		return nil, fmt.Errorf("parameters must not be NaN")
	}
	if south < -90 || south > 90 || north < -90 || north > 90 {
		return nil, fmt.Errorf("latitude must be between -90 and 90")
	}
	if west < -180 || west > 180 || east < -180 || east > 180 {
		return nil, fmt.Errorf("longitude must be between -180 and 180")
	}
	if south >= north {
		return nil, fmt.Errorf("south must be less than north")
	}
	if west >= east {
		return nil, fmt.Errorf("west must be less than east")
	}
	if resolution < heatmapMinResolution || resolution > heatmapMaxResolution {
		return nil, fmt.Errorf("resolution must be between %.1f and %.1f degrees", heatmapMinResolution, heatmapMaxResolution)
	}

	rows, cols := heatmapGridDimensions(south, north, west, east, resolution)
	if rows*cols > heatmapMaxGridCells {
		return nil, fmt.Errorf("grid too large: %d cells exceeds maximum %d", rows*cols, heatmapMaxGridCells)
	}

	// Parse optional stride parameter (default 1 = all 48 weeks)
	stride := 1
	if strideStr := ctx.QueryParam("stride"); strideStr != "" {
		s, err := strconv.Atoi(strideStr)
		if err != nil || !validStrides[s] {
			return nil, fmt.Errorf("invalid stride: must be a divisor of 48 (1,2,4,6,8,12,16,24,48)")
		}
		stride = s
	}

	return &heatmapParams{
		species:    species,
		south:      south,
		north:      north,
		west:       west,
		east:       east,
		resolution: resolution,
		rows:       rows,
		cols:       cols,
		stride:     stride,
	}, nil
}

// computeHeatmapGrid generates the heatmap data by running batch geomodel inference
// for each week across all grid points, then extracting the target species scores.
// Returns float32[weeks][rows][cols] in flat layout.
func (c *Handler) computeHeatmapGrid(birdnet *classifier.Orchestrator, params *heatmapParams, speciesIdx, numGeoSpecies int) ([]float32, error) {
	totalCells := params.rows * params.cols
	result := make([]float32, bnhmWeeks*totalCells)

	// Pre-allocate input triples once; only the week value changes per iteration.
	inputs := make([]float32, totalCells*3)
	for row := range params.rows {
		lat := float32(params.south + (float64(row)+0.5)*params.resolution)
		for col := range params.cols {
			lon := float32(params.west + (float64(col)+0.5)*params.resolution)
			idx := (row*params.cols + col) * 3
			inputs[idx] = lat
			inputs[idx+1] = lon
		}
	}

	for week := 1; week <= bnhmWeeks; week++ {
		weekOffset := (week - 1) * totalCells

		// Update week value for all grid points in-place
		weekF := float32(week)
		for i := 2; i < len(inputs); i += 3 {
			inputs[i] = weekF
		}

		// Process in chunks of heatmapBatchSize to release the lock between calls
		for chunkStart := 0; chunkStart < totalCells; chunkStart += heatmapBatchSize {
			chunkEnd := chunkStart + heatmapBatchSize
			if chunkEnd > totalCells {
				chunkEnd = totalCells
			}
			chunkSize := chunkEnd - chunkStart

			chunkInputs := inputs[chunkStart*3 : chunkEnd*3]
			scores, err := birdnet.BatchRangeFilterInference(chunkInputs, chunkSize)
			if err != nil {
				return nil, fmt.Errorf("batch inference failed at week %d chunk %d: %w", week, chunkStart, err)
			}

			expectedLen := chunkSize * numGeoSpecies
			if len(scores) != expectedLen {
				return nil, fmt.Errorf("batch inference size mismatch at week %d chunk %d: got %d scores, want %d",
					week, chunkStart, len(scores), expectedLen)
			}

			// Extract the target species score from each point's output
			for i := range chunkSize {
				result[weekOffset+chunkStart+i] = scores[i*numGeoSpecies+speciesIdx]
			}
		}
	}

	return result, nil
}

// computeHeatmapGridOptimized generates heatmap data using the dedicated
// HeatmapInferenceService with IoBinding for tensor reuse across all weeks.
func (c *Handler) computeHeatmapGridOptimized(ctx context.Context, service *classifier.HeatmapInferenceService, params *heatmapParams) ([]float32, error) {
	totalCells := params.rows * params.cols
	weeksToCompute := (bnhmWeeks + params.stride - 1) / params.stride
	result := make([]float32, weeksToCompute*totalCells)

	coords := make([]float32, totalCells*2)
	for row := range params.rows {
		lat := float32(params.south + (float64(row)+0.5)*params.resolution)
		for col := range params.cols {
			lon := float32(params.west + (float64(col)+0.5)*params.resolution)
			idx := (row*params.cols + col) * 2
			coords[idx] = lat
			coords[idx+1] = lon
		}
	}

	if err := service.ComputeGridWithBinding(ctx, coords, totalCells, params.species,
		params.stride, bnhmWeeks, heatmapOptBatchSize, result); err != nil {
		return nil, fmt.Errorf("heatmap grid computation failed: %w", err)
	}

	return result, nil
}

// heatmapCache is the module-level LRU cache for heatmap responses.
// Initialized lazily on first use via initHeatmapCache.
var (
	heatmapCacheInstance *heatmapLRU
	heatmapCacheOnce     sync.Once
)

func getHeatmapCache() *heatmapLRU {
	heatmapCacheOnce.Do(func() {
		heatmapCacheInstance = newHeatmapLRU(64)
	})
	return heatmapCacheInstance
}

// InvalidateHeatmapCache bumps the generation counter, causing all cached
// entries to be treated as stale on next access.
func InvalidateHeatmapCache() {
	getHeatmapCache().invalidate()
}

// GetHeatmapGrid returns a compact binary grid of species probability across
// a map viewport for BirdNET weeks (stride-configurable).
func (c *Handler) GetHeatmapGrid(ctx echo.Context) error {
	params, err := c.validateHeatmapParams(ctx)
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}

	// Check cache first (includes stride in key)
	cache := getHeatmapCache()
	cacheKey := heatmapCacheKey(params.species, params.south, params.north, params.west, params.east, params.resolution)
	if params.stride > 1 {
		cacheKey = fmt.Sprintf("%s|s%d", cacheKey, params.stride)
	}

	cached, _, hit := cache.get(cacheKey)
	if hit {
		c.LogAPIRequest(ctx, logger.LogLevelDebug, "Heatmap cache hit", logger.String("species", params.species))
		return ctx.Blob(http.StatusOK, "application/octet-stream", cached)
	}

	// Try the dedicated heatmap service (optimized path)
	birdnet, err := c.GetBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	service := birdnet.GetHeatmapService()
	if service != nil {
		return c.getHeatmapOptimized(ctx, service, params, cache, cacheKey)
	}

	// Fallback: use shared BatchRangeFilterInference (original path, stride=1 only)
	if params.stride > 1 {
		return c.HandleError(ctx, nil, "Stride parameter requires dedicated heatmap service (not available)", http.StatusServiceUnavailable)
	}
	return c.getHeatmapFallback(ctx, birdnet, params, cache, cacheKey)
}

// getHeatmapOptimized uses the dedicated HeatmapInferenceService with
// singleflight deduplication and concurrency limiting.
func (c *Handler) getHeatmapOptimized(ctx echo.Context, service *classifier.HeatmapInferenceService, params *heatmapParams, cache *heatmapLRU, cacheKey string) error {
	reqCtx := ctx.Request().Context()

	// Use singleflight to deduplicate concurrent identical requests.
	// The shared computation uses context.Background() throughout so one
	// caller's cancellation doesn't kill the computation for others.
	ch := heatmapFlight.DoChan(cacheKey, func() (any, error) {
		// Acquire concurrency semaphore (blocking; individual callers bail
		// via the outer select on reqCtx.Done())
		heatmapSem <- struct{}{}
		defer func() { <-heatmapSem }()

		// Snapshot cache generation for poisoning prevention
		_, missGen, _ := cache.get(cacheKey)

		data, err := c.computeHeatmapGridOptimized(context.Background(), service, params)
		if err != nil {
			return nil, err
		}

		weeksComputed := (bnhmWeeks + params.stride - 1) / params.stride
		encoded := encodeBNHMWithWeeks(params.cols, params.rows, weeksComputed,
			float32(params.south), float32(params.west), float32(params.resolution), data)

		cache.put(cacheKey, encoded, missGen)
		return encoded, nil
	})

	select {
	case result := <-ch:
		if result.Err != nil {
			return c.HandleError(ctx, result.Err, "Failed to compute heatmap grid", http.StatusInternalServerError)
		}
		c.LogAPIRequest(ctx, logger.LogLevelInfo, "Heatmap grid computed (optimized)",
			logger.String("species", params.species),
			logger.Int("rows", params.rows),
			logger.Int("cols", params.cols),
			logger.Int("stride", params.stride))
		return ctx.Blob(http.StatusOK, "application/octet-stream", result.Val.([]byte))
	case <-reqCtx.Done():
		return reqCtx.Err()
	}
}

// getHeatmapFallback uses the original shared BatchRangeFilterInference path.
func (c *Handler) getHeatmapFallback(ctx echo.Context, birdnet *classifier.Orchestrator, params *heatmapParams, cache *heatmapLRU, cacheKey string) error {
	speciesIdx, numGeoSpecies, found := birdnet.GeomodelSpeciesInfo(params.species)
	if !found {
		return c.HandleError(ctx, nil, "Species not found in geomodel", http.StatusBadRequest)
	}
	if numGeoSpecies == 0 {
		return c.HandleError(ctx, nil, "Geomodel labels not available", http.StatusInternalServerError)
	}

	_, missGen, _ := cache.get(cacheKey)

	data, err := c.computeHeatmapGrid(birdnet, params, speciesIdx, numGeoSpecies)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to compute heatmap grid", http.StatusInternalServerError)
	}

	encoded := encodeBNHM(params.cols, params.rows, float32(params.south), float32(params.west), float32(params.resolution), data)
	cache.put(cacheKey, encoded, missGen)

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Heatmap grid computed (fallback)",
		logger.String("species", params.species),
		logger.Int("rows", params.rows),
		logger.Int("cols", params.cols))

	return ctx.Blob(http.StatusOK, "application/octet-stream", encoded)
}

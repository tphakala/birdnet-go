package api

import (
	"container/list"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// BNHM binary format constants
const (
	bnhmMagic      = "BNHM"
	bnhmVersion    = 1
	bnhmHeaderSize = 40
	bnhmWeeks      = 48
)

// Heatmap validation constraints
const (
	heatmapMinResolution = 0.1
	heatmapMaxResolution = 5.0
	heatmapMaxGridCells  = 50000
	heatmapBatchSize     = 512
)

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

func (c *heatmapLRU) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*heatmapCacheEntry)
	if entry.generation != c.generation.Load() {
		c.order.Remove(elem)
		delete(c.items, key)
		return nil, false
	}

	c.order.MoveToFront(elem)
	return entry.value, true
}

func (c *heatmapLRU) put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		entry := elem.Value.(*heatmapCacheEntry)
		entry.value = value
		entry.generation = c.generation.Load()
		return
	}

	entry := &heatmapCacheEntry{
		key:        key,
		value:      value,
		generation: c.generation.Load(),
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
	size := bnhmHeaderSize + len(data)*4
	buf := make([]byte, size)

	copy(buf[0:4], bnhmMagic)
	binary.LittleEndian.PutUint32(buf[4:8], bnhmVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(cols))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(rows))
	binary.LittleEndian.PutUint32(buf[16:20], bnhmWeeks)
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
}

// validateHeatmapParams parses and validates heatmap query parameters.
func (c *Controller) validateHeatmapParams(ctx echo.Context) (*heatmapParams, error) {
	species := ctx.QueryParam("species")
	if species == "" {
		return nil, fmt.Errorf("species parameter is required")
	}

	south, err := parseFloat64(ctx.QueryParam("south"))
	if err != nil {
		return nil, fmt.Errorf("invalid south parameter: %w", err)
	}
	north, err := parseFloat64(ctx.QueryParam("north"))
	if err != nil {
		return nil, fmt.Errorf("invalid north parameter: %w", err)
	}
	west, err := parseFloat64(ctx.QueryParam("west"))
	if err != nil {
		return nil, fmt.Errorf("invalid west parameter: %w", err)
	}
	east, err := parseFloat64(ctx.QueryParam("east"))
	if err != nil {
		return nil, fmt.Errorf("invalid east parameter: %w", err)
	}
	resolution, err := parseFloat64(ctx.QueryParam("resolution"))
	if err != nil {
		return nil, fmt.Errorf("invalid resolution parameter: %w", err)
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

	return &heatmapParams{
		species:    species,
		south:      south,
		north:      north,
		west:       west,
		east:       east,
		resolution: resolution,
		rows:       rows,
		cols:       cols,
	}, nil
}

// computeHeatmapGrid generates the heatmap data by running batch geomodel inference
// for each week across all grid points, then extracting the target species scores.
// Returns float32[weeks][rows][cols] in flat layout.
func (c *Controller) computeHeatmapGrid(params *heatmapParams, speciesIdx, numGeoSpecies int) ([]float32, error) {
	totalCells := params.rows * params.cols
	result := make([]float32, bnhmWeeks*totalCells)

	birdnet, err := c.getBirdNETInstance()
	if err != nil {
		return nil, err
	}

	for week := 1; week <= bnhmWeeks; week++ {
		weekOffset := (week - 1) * totalCells

		// Build input triples for all grid points in this week
		inputs := make([]float32, 0, totalCells*3)
		for row := range params.rows {
			lat := params.south + (float64(row)+0.5)*params.resolution
			for col := range params.cols {
				lon := params.west + (float64(col)+0.5)*params.resolution
				inputs = append(inputs, float32(lat), float32(lon), float32(week))
			}
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

			// Extract the target species score from each point's output
			for i := range chunkSize {
				scoreIdx := i*numGeoSpecies + speciesIdx
				if scoreIdx < len(scores) {
					result[weekOffset+chunkStart+i] = scores[scoreIdx]
				}
			}
		}
	}

	return result, nil
}

// initHeatmapRoutes registers the heatmap grid endpoint and hooks up
// cache invalidation for range filter reloads.
func (c *Controller) initHeatmapRoutes() {
	c.Group.GET("/range/heatmap", c.GetHeatmapGrid)

	classifier.OnRangeFilterReload(func() {
		InvalidateHeatmapCache()
	})
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
// a map viewport for all 48 BirdNET weeks.
func (c *Controller) GetHeatmapGrid(ctx echo.Context) error {
	params, err := c.validateHeatmapParams(ctx)
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}

	// Verify species exists in geomodel
	birdnet, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	speciesIdx, found := birdnet.GeomodelSpeciesIndex(params.species)
	if !found {
		return c.HandleError(ctx, nil, "Species not found in geomodel", http.StatusBadRequest)
	}

	labels := birdnet.GeomodelLabels()
	if len(labels) == 0 {
		return c.HandleError(ctx, nil, "Geomodel labels not available", http.StatusInternalServerError)
	}
	numGeoSpecies := len(labels)

	// Check cache
	cache := getHeatmapCache()
	cacheKey := heatmapCacheKey(params.species, params.south, params.north, params.west, params.east, params.resolution)

	if cached, ok := cache.get(cacheKey); ok {
		c.logAPIRequest(ctx, logger.LogLevelDebug, "Heatmap cache hit", logger.String("species", params.species))
		return ctx.Blob(http.StatusOK, "application/octet-stream", cached)
	}

	// Compute grid
	data, err := c.computeHeatmapGrid(params, speciesIdx, numGeoSpecies)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to compute heatmap grid", http.StatusInternalServerError)
	}

	// Encode BNHM
	encoded := encodeBNHM(params.cols, params.rows, float32(params.south), float32(params.west), float32(params.resolution), data)

	// Cache the result
	cache.put(cacheKey, encoded)

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Heatmap grid computed",
		logger.String("species", params.species),
		logger.Int("rows", params.rows),
		logger.Int("cols", params.cols))

	return ctx.Blob(http.StatusOK, "application/octet-stream", encoded)
}

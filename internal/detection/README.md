# Detection Package

The `detection` package provides domain models for bird detection processing, independent of database schema concerns.

## Purpose

This package implements the **Repository Pattern** to decouple business logic from database persistence. This separation enables:

- **Database normalization** without breaking runtime code
- **Improved testability** through mock repositories
- **Clear separation of concerns** between domain and persistence layers
- **Flexible caching strategies** for performance optimization

## Architecture

```
Application Layer (processors, APIs)
         ↓
    Domain Layer (this package)
    - Detection models
    - Repository interfaces
    - Species cache
         ↓
Repository Implementation
    - Adapts datastore.Interface
    - Mapper: Domain ↔ Database
         ↓
  Persistence Layer (datastore)
    - Database entities (GORM)
    - SQL operations
```

## Key Components

### Domain Models

#### `Detection`
Runtime representation of a bird detection. Contains both persisted data and runtime-only metadata (like `AudioSource` and `Occurrence`).

#### `Prediction`
Represents one species prediction from BirdNET. A detection typically contains 5-10 predictions, with the top prediction becoming the actual `Detection.Species`.

#### `Species`
Normalized species information. Cached in memory for fast lookups (~300 species max).

#### `AudioSource`
Runtime-only metadata about the audio input source. **Not persisted** to database (only `SourceNode` is saved).

### Repository Pattern

#### `Repository` Interface
Defines all detection persistence operations. Implementations can:
- Mock for testing
- Use different backends (SQLite, PostgreSQL, in-memory)
- Apply caching strategies

#### `SpeciesRepository` Interface
Manages species lookup and caching. Species are cached in memory since they rarely change.

### Data Conversion

#### `Mapper`
Converts between:
- `Detection` ↔ `datastore.Note`
- `Prediction` ↔ `datastore.Results`

Handles differences between domain and persistence models:
- Runtime-only fields (Source, Occurrence)
- Virtual fields (Verified, Locked)
- Type conversions

#### `SpeciesCache`
In-memory cache for species data with multiple indexes:
- By ID (uint)
- By scientific name (string)
- By eBird code (string)

Thread-safe with read/write locks for concurrent access.

## Usage Examples

### Creating a Detection

```go
detection := detection.NewDetection(
    "node1",                    // sourceNode
    "2025-01-15", "14:30:00",  // date, time
    beginTime, endTime,         // time.Time
    audioSource,                // AudioSource (runtime metadata)
    "amecro",                   // species code
    "Corvus brachyrhynchos",    // scientific name
    "American Crow",            // common name
    0.95, 0.1, 1.2,            // confidence, threshold, sensitivity
    47.6062, -122.3321,         // latitude, longitude
    "/clips/detection_123.wav", // clip name
    50 * time.Millisecond,      // processing time
    0.85,                       // occurrence probability
)
```

### Using the Mapper

```go
mapper := detection.NewMapper(speciesCache)

// Domain → Database
note := mapper.ToDatastore(detection)
ds.Save(&note, results)

// Database → Domain
detection := mapper.FromDatastore(&note, audioSource)
```

### Using the Species Cache

```go
cache := detection.NewSpeciesCache(speciesRepo, 1*time.Hour)

// Lookup (cache-first, then database)
species, err := cache.GetByScientificName(ctx, "Corvus brachyrhynchos")

// Get statistics
stats := cache.Stats()
fmt.Printf("Cache size: %d species\n", stats.Size)
```

## Design Decisions

### Why Not Persist AudioSource?

`AudioSource` contains connection strings and display names that are:
- Runtime configuration (not detection data)
- Potentially sensitive (RTSP URLs with credentials)
- Reconstructable from `SourceNode` + config

Only `SourceNode` (device/node name) is persisted.

### Why Not Persist Occurrence?

`Occurrence` is a calculated probability based on:
- Species range maps
- Time of year
- Geographic location

It can be recalculated on demand, so no need to store.

### Why Pointer Receivers for Mapper?

While the current mapper has no state, it's designed to optionally hold a `SpeciesCache` reference for future optimizations.

### Why Multiple Species Indexes?

Different parts of the codebase lookup species by:
- ID (database joins)
- Scientific name (BirdNET results)
- eBird code (occurrence calculations)

Multiple indexes avoid repeated database queries.

## Performance Considerations

### Small Dataset Optimization

BirdNET-Go typically handles ~300 species maximum. This small dataset allows:
- Full species cache in memory (~10KB)
- Simple map-based indexes (no B-trees needed)
- Instant lookups without database queries

### Memory Usage

- **Detection**: ~500 bytes per instance
- **Species cache**: ~30 bytes × 300 = ~10KB total
- **Predictions**: ~50 bytes × 5 average = ~250 bytes per detection

Total memory for 1000 active detections: ~750KB (negligible)

## Future Enhancements

### Normalized Database Schema (Phase 2)

```go
// Future: Species stored in separate table
type DetectionEntity struct {
    ID        uint
    SpeciesID uint  // Foreign key instead of denormalized fields
    // ...
}
```

### Multilingual Species Names

```go
// Future: Support multiple languages
type SpeciesName struct {
    SpeciesID uint
    Language  string  // "en", "es", "fr", etc.
    Name      string
}
```

### Event Sourcing (Maybe)

```go
// Future: Track all detection changes
type DetectionEvent struct {
    DetectionID uint
    EventType   string  // "created", "verified", "locked"
    Timestamp   time.Time
}
```

## Testing

Unit tests focus on:
- Mapper round-trip conversions (no data loss)
- Species cache concurrent access
- Edge cases (empty species, nil pointers)

See `*_test.go` files for examples.

## Migration Plan

This package is part of a phased migration:

1. **Phase 1** (Current): Foundation - Domain models, mapper, cache
2. **Phase 2**: Repository implementation adapting existing datastore
3. **Phase 3**: Gradual code migration (observation → processor → API)
4. **Phase 4**: Database normalization
5. **Phase 5**: Cleanup and optimization

See `/plans/domain-model-separation-refactoring.local.md` for full plan.

## Related Issues

- #1227 - Domain/persistence separation proposal
- #874 - Database normalization for storage reduction

## References

- Repository Pattern: https://threedots.tech/post/repository-pattern-in-go/
- Domain-Driven Design: https://www.citerus.se/go-ddd/

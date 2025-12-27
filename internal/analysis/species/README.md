# Species Tracking Package

**Package**: `internal/analysis/species`
**Purpose**: Track and analyze bird species detections across multiple time periods (lifetime, yearly, seasonal)

## Overview

This package provides comprehensive species tracking functionality for BirdNET-Go. It maintains historical detection data and calculates species status across different time periods, enabling "new species" notifications and temporal analysis.

**Key Stats**:

- **Lines of Code**: ~15,260 (including tests)
- **Main Implementation**: 1,815 lines
- **Public Methods**: 44
- **Test Coverage**: 22 comprehensive test files

## Core Components

### 1. `SpeciesTracker` (Main Struct)

The primary tracking engine that maintains species detection state across multiple time periods.

```go
type SpeciesTracker struct {
    // Lifetime tracking
    speciesFirstSeen map[string]time.Time
    windowDays       int

    // Multi-period tracking
    speciesThisYear map[string]time.Time
    speciesBySeason map[string]map[string]time.Time

    // Configuration & state
    ds                 SpeciesDatastore
    yearlyEnabled      bool
    seasonalEnabled    bool

    // Performance optimizations
    statusCache        map[string]cachedSpeciesStatus
    cacheTTL           time.Duration

    // Notification suppression
    notificationLastSent          map[string]time.Time
    notificationSuppressionWindow time.Duration

    // Thread safety
    mu sync.RWMutex
}
```

### 2. `SpeciesStatus` (Status Data)

Represents the tracking status of a species across all periods.

```go
type SpeciesStatus struct {
    // Lifetime tracking
    FirstSeenTime   time.Time
    IsNew           bool
    DaysSinceFirst  int

    // Yearly tracking
    FirstThisYear   *time.Time
    IsNewThisYear   bool
    DaysThisYear    int

    // Seasonal tracking
    FirstThisSeason *time.Time
    IsNewThisSeason bool
    DaysThisSeason  int
    CurrentSeason   string

    LastUpdatedTime time.Time
}
```

### 3. `SpeciesDatastore` (Interface)

Minimal database interface required by the tracker.

```go
type SpeciesDatastore interface {
    GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
    GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
}
```

## Key Concepts

### Time Periods

The tracker manages three independent time periods:

1. **Lifetime**: All-time first detection (never resets)
2. **Yearly**: First detection in the current tracking year (resets annually)
3. **Seasonal**: First detection in the current season (resets per season)

### Tracking Windows

Each period has a configurable "new species" window:

- **Lifetime Window**: Days after first-ever detection (default: 14 days)
- **Yearly Window**: Days after first detection this year
- **Seasonal Window**: Days after first detection this season

### Seasonal Tracking

Supports flexible season definitions:

**Traditional Seasons** (Northern Hemisphere):

- Spring: March 20
- Summer: June 21
- Fall: September 22
- Winter: December 21

**Equatorial Seasons**:

- Wet1, Dry1, Wet2, Dry2

**Custom Seasons**: Configurable via settings

### Yearly Tracking

Supports both calendar years and custom fiscal/academic years:

- **Calendar Year**: January 1 - December 31
- **Fiscal Year**: Configurable reset date (e.g., July 1 - June 30)

## Public API

### Initialization

```go
// Create tracker from configuration settings
NewTrackerFromSettings(ds SpeciesDatastore, settings *conf.SpeciesTrackingSettings) *SpeciesTracker

// Initialize from database (call once after creation)
InitFromDatabase() error
```

### Species Status Queries

```go
// Get status for a single species
GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus

// Get status for multiple species (optimized batch operation)
GetBatchSpeciesStatus(scientificNames []string, currentTime time.Time) map[string]SpeciesStatus

// Check if species is new (simple check)
IsNewSpecies(scientificName string) bool
```

### Species Updates

```go
// Update species detection time
UpdateSpecies(scientificName string, detectionTime time.Time) bool

// Atomically check and update (race-condition safe)
CheckAndUpdateSpecies(scientificName string, detectionTime time.Time) (isNew bool, daysSinceFirstSeen int)
```

### Notification Suppression

```go
// Check if notification should be suppressed
ShouldSuppressNotification(scientificName string, currentTime time.Time) bool

// Record that notification was sent (persists to database - BG-17 fix)
RecordNotificationSent(scientificName string, sentTime time.Time)

// Cleanup old notification records (memory + database - BG-17 fix)
CleanupOldNotificationRecords(currentTime time.Time) int
```

**BG-17 Fix**: Notification suppression state is now persisted to the database via the `notification_histories` table. This prevents duplicate "new species" notifications after application restarts. The state is automatically loaded during `InitFromDatabase()` and saved asynchronously when notifications are sent.

### Maintenance

```go
// Sync with database if needed
SyncIfNeeded() error

// Prune old entries to prevent unbounded growth
PruneOldEntries() int

// Get statistics
GetSpeciesCount() int
GetWindowDays() int

// Cleanup resources
Close() error
```

### Testing Helpers

```go
// Testing-only methods (DO NOT USE IN PRODUCTION)
SetCurrentYearForTesting(year int)
SetCurrentSeasonForTesting(season string)
ExpireCacheForTesting(scientificName string)
ClearCacheForTesting()
IsSeasonMapInitialized(season string) bool
GetSeasonMapCount(season string) int
```

## Internal Architecture

### Thread Safety

All public methods use read-write mutex for safe concurrent access:

- **Read locks**: Status queries, checks
- **Write locks**: Updates, cache management, period resets

### Caching Strategy

**Status Cache**:

- **Purpose**: Avoid expensive status calculations
- **TTL**: 30 seconds (default)
- **Invalidation**: On species update, year change
- **Cleanup**: Periodic LRU eviction when size > 1000 entries
- **Size Limit**: Max 1000 entries, target 800 after cleanup

**Season Cache**:

- **Purpose**: Avoid repeated season calculations
- **TTL**: 1 hour
- **Validation**: Checks if input time is in same season period

### Period Reset Logic

**Yearly Reset**:

- Triggers when crossing the configured reset date
- Clears `speciesThisYear` map
- Invalidates status cache

**Seasonal Reset**:

- Automatic when season changes
- Initializes new season map if needed

### Database Synchronization

**Initial Load**:

1. Load lifetime data (all-time first detections)
2. Load yearly data (current year first detections)
3. Load seasonal data (each season's first detections)

**Periodic Sync**:

- Interval: Configurable (default: every N minutes)
- Preserves existing data if database returns empty results
- Defensive against data loss

### Memory Management

**Pruning Strategy**:

- **Lifetime data**: Keep 10 years (only prune very old entries)
- **Yearly data**: Prune entries from previous tracking years
- **Seasonal data**: Keep 1 year of seasonal data
- **Notification records**: Prune after suppression window expires

**Growth Prevention**:

- Pre-allocated maps with initial capacity (100)
- LRU cache eviction for status cache
- Periodic cleanup of old records

### Logging

**Centralized Logger**:

- Uses `internal/logger` package
- Module path: `analysis.processor`
- Level: Debug (configurable via settings)
- Structured logging with type-safe field constructors

## Usage Examples

### Basic Initialization

```go
import (
    "github.com/tphakala/birdnet-go/internal/analysis/species"
    "github.com/tphakala/birdnet-go/internal/conf"
)

// Create settings
settings := &conf.SpeciesTrackingSettings{
    Enabled: true,
    NewSpeciesWindowDays: 14,
    SyncIntervalMinutes: 30,
    YearlyTracking: conf.YearlyTrackingSettings{
        Enabled: true,
        WindowDays: 7,
        ResetMonth: 1,
        ResetDay: 1,
    },
    SeasonalTracking: conf.SeasonalTrackingSettings{
        Enabled: true,
        WindowDays: 3,
    },
    NotificationSuppressionHours: 168, // 7 days
}

// Create tracker
tracker := species.NewTrackerFromSettings(datastore, settings)

// Initialize from database
if err := tracker.InitFromDatabase(); err != nil {
    return fmt.Errorf("failed to initialize species tracker: %w", err)
}
defer tracker.Close()
```

### Checking Species Status

```go
// Single species
status := tracker.GetSpeciesStatus("Turdus migratorius", time.Now())
if status.IsNew {
    fmt.Printf("New species detected! First seen: %s\n", status.FirstSeenTime)
}
if status.IsNewThisYear {
    fmt.Printf("New for this year! Days this year: %d\n", status.DaysThisYear)
}
if status.IsNewThisSeason {
    fmt.Printf("New for %s! Days this season: %d\n",
        status.CurrentSeason, status.DaysThisSeason)
}

// Batch query (more efficient for multiple species)
species := []string{"Turdus migratorius", "Cyanocitta cristata", "Cardinalis cardinalis"}
statuses := tracker.GetBatchSpeciesStatus(species, time.Now())
```

### Updating Detections

```go
// Simple update
isNew := tracker.UpdateSpecies("Turdus migratorius", time.Now())

// Atomic check-and-update (prevents race conditions)
isNew, daysSince := tracker.CheckAndUpdateSpecies("Turdus migratorius", time.Now())
if isNew {
    // Send notification
}
```

### Notification Management

```go
scientificName := "Turdus migratorius"
currentTime := time.Now()

// Check if we should suppress notification
if tracker.ShouldSuppressNotification(scientificName, currentTime) {
    log.Debug("suppressing duplicate notification",
        logger.String("species", scientificName))
    return
}

// Send notification...

// Record that we sent it
tracker.RecordNotificationSent(scientificName, currentTime)
```

### Periodic Maintenance

```go
// Background maintenance goroutine
go func() {
    log := GetLogger()
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()

    for range ticker.C {
        // Sync with database
        if err := tracker.SyncIfNeeded(); err != nil {
            log.Error("sync failed", logger.Error(err))
        }

        // Prune old entries
        pruned := tracker.PruneOldEntries()
        if pruned > 0 {
            log.Debug("pruned old entries", logger.Int("count", pruned))
        }
    }
}()
```

## Performance Considerations

### Optimizations

1. **Status Cache**: 30s TTL reduces expensive calculations
2. **Season Cache**: 1h TTL avoids repeated season calculations
3. **Batch API**: `GetBatchSpeciesStatus` performs expensive operations once
4. **Pre-allocation**: Maps pre-allocated with capacity hints
5. **Buffer Reuse**: `statusBuffer` reused for single-species queries
6. **Lock Granularity**: Read locks for queries, write locks only for updates

### Scalability

- **Memory**: O(n) where n = number of unique species
- **Cache size**: Limited to 1000 entries with LRU eviction
- **Concurrent access**: Thread-safe with RWMutex
- **Database load**: Configurable sync interval reduces load

### Known Limitations

1. **Season calculation**: Uses cached order, requires initialization
2. **Year-crossing seasons**: Special handling for Oct-Dec seasons
3. **Time zones**: All calculations use system local timezone
4. **Cache invalidation**: Manual invalidation on updates

## Testing

### Test Coverage

The package has **exceptional test coverage** across **24 test files**:

- **166 test functions**
- **~12,470 lines of test code**
- **~95% statement coverage**
- **Comprehensive race condition detection**
- **Integration and reliability tests**

**📚 See [TEST_DOCUMENTATION.md](TEST_DOCUMENTATION.md) for complete test documentation**

### Test Categories

| Category                  | Files | Coverage                                    |
| ------------------------- | ----- | ------------------------------------------- |
| Core Functionality        | 3     | Basic tracking, multi-period, configuration |
| Caching & Performance     | 4     | Cache behavior, TTL, LRU, batch operations  |
| Concurrency & Thread Safe | 1     | Race conditions, concurrent access          |
| Database Integration      | 3     | Init, sync, reliability, error recovery     |
| Period-Specific           | 3     | Yearly/seasonal logic, transitions, resets  |
| Date Range & Time         | 2     | Date calculations, time edges, fiscal years |
| Memory Management         | 2     | Cleanup, pruning, cache limits              |
| Notifications             | 1     | Suppression logic, record cleanup           |
| Reliability & Correctness | 2     | Business logic, long-running stability      |
| Coverage & Comprehensive  | 3     | Edge cases, uncovered paths, integration    |

### Quick Start Testing

```bash
# Run all tests with race detector (RECOMMENDED)
go test -race -v ./internal/analysis/species/

# Run specific category
go test -v -run TestCache              # Caching tests
go test -race -v -run TestRace         # Race condition tests
go test -v -run TestNotification       # Notification tests
go test -v -run TestPeriods            # Period logic tests

# Generate coverage report
go test -coverprofile=coverage.out ./internal/analysis/species/
go tool cover -html=coverage.out
```

### Test Helpers

```go
// Mock datastore for testing
type MockSpeciesDatastore struct {
    mock.Mock
}

// Testing utilities
func (m *MockSpeciesDatastore) GetNewSpeciesDetections(...) ([]datastore.NewSpeciesData, error)
func (m *MockSpeciesDatastore) GetSpeciesFirstDetectionInPeriod(...) ([]datastore.NewSpeciesData, error)
```

**For detailed test documentation, test patterns, known issues, and debugging tips, see [TEST_DOCUMENTATION.md](TEST_DOCUMENTATION.md)**

## Configuration

### Settings Structure

```go
type SpeciesTrackingSettings struct {
    Enabled                      bool
    NewSpeciesWindowDays         int
    SyncIntervalMinutes          int
    NotificationSuppressionHours int

    YearlyTracking struct {
        Enabled    bool
        WindowDays int
        ResetMonth int // 1-12
        ResetDay   int // 1-31
    }

    SeasonalTracking struct {
        Enabled    bool
        WindowDays int
        Seasons    map[string]struct {
            StartMonth int // 1-12
            StartDay   int // 1-31
        }
    }
}
```

### Default Values

```go
const (
    defaultCacheTTL                      = 30 * time.Second
    defaultSeasonCacheTTL                = time.Hour
    defaultNotificationSuppressionWindow = 168 * time.Hour // 7 days
    initialSpeciesCapacity               = 100
    maxStatusCacheSize                   = 1000
    targetCacheSize                      = 800
)
```

## Error Handling

The package uses the internal errors package for structured error handling:

```go
errors.Newf("message").
    Component("new-species-tracker").
    Category(errors.CategoryDatabase).
    Context("operation", "load_data").
    Build()
```

Common error categories:

- `CategoryDatabase` - Database operation failures
- `CategoryConfiguration` - Invalid configuration
- `CategoryValidation` - Invalid input data
- `CategoryResource` - Resource cleanup failures

## Integration Points

### Dependencies

- `internal/conf` - Configuration settings
- `internal/datastore` - Database operations
- `internal/errors` - Structured error handling
- `internal/logger` - Centralized logging

### Used By

- Detection analysis pipeline
- Notification system
- API v2 endpoints (`/api/v2/species/status`)
- Dashboard species statistics

## Contributing

When modifying this package:

1. **Run tests**: `go test -race -v ./internal/analysis/species/`
2. **Run linter**: `golangci-lint run -v internal/analysis/species/`
3. **Check coverage**: Maintain high test coverage
4. **Update docs**: Keep this README current
5. **Add tests**: New features require comprehensive tests

# Species Package Test Documentation

**Package**: `internal/analysis/species`
**Total Test Files**: 24
**Total Test Functions**: 166
**Total Lines of Test Code**: ~12,470

## Overview

This package has comprehensive test coverage ensuring correctness of species tracking across all three time periods (lifetime, yearly, seasonal), thread safety, caching behavior, database reliability, and edge cases.

## Test Organization

### Core Functionality Tests

#### 1. `species_tracker_test.go` (566 lines)

**Primary tests for core tracking functionality**

```bash
go test -v -run TestSpeciesTracker
```

**Key Tests**:

- `TestSpeciesTracker_NewSpecies` - New species detection and window logic
- `TestSpeciesTracker_ConcurrentAccess` - Thread safety with RWMutex
- `TestSpeciesTracker_UpdateSpecies` - Species update and first-seen time management
- `TestSpeciesTracker_EdgeCases` - Edge cases (empty names, zero times)
- `TestSpeciesTracker_PruneOldEntries` - Memory management and pruning
- `TestNewTrackerFromSettings_BasicConfiguration` - Initialization from settings
- `TestMultiPeriodTracking_YearlyTracking` - Yearly period tracking
- `TestMultiPeriodTracking_SeasonalTracking` - Seasonal period tracking
- `TestSeasonDetection` - Season calculation logic
- `TestMultiPeriodTracking_CrossPeriodScenarios` - Cross-period interactions
- `TestMultiPeriodTracking_SeasonTransition` - Season boundary handling
- `TestMultiPeriodTracking_YearReset` - Year reset logic

**Coverage**: Basic tracker lifecycle, multi-period tracking, configuration

---

### Caching & Performance Tests

#### 2. `species_tracker_cache_test.go` (398 lines)

**Cache behavior, TTL, and LRU eviction**

```bash
go test -v -run TestCache
```

**Key Tests**:

- `TestCleanupExpiredCacheComprehensive` - Cache expiration and cleanup
  - Empty cache cleanup
  - Expired entry removal
  - LRU eviction when size > 1000
  - Timestamp-based eviction
- `TestCacheManagement` - Cache hit/miss behavior, TTL validation
- `TestCleanupOldNotificationRecordsLockedComprehensive` - Notification record cleanup
- `TestCheckAndUpdateSpeciesComprehensive` - Atomic check-and-update with caching

**Coverage**: Status caching, notification suppression cache, TTL management, memory limits

---

#### 3. `species_tracker_performance_edge_test.go` (289 lines)

**Performance edge cases and optimization validation**

```bash
go test -v -run TestPerformanceEdge
```

**Key Tests**:

- High-volume species tracking (10,000+ species)
- Batch API performance validation
- Cache hit rate optimization
- Memory allocation patterns

**Coverage**: Scalability, batch operations, cache efficiency

---

#### 4. `species_tracker_performance_edge_refactored_test.go` (278 lines)

**Refactored performance tests with better isolation**

```bash
go test -v -run TestPerformanceEdgeRefactored
```

**Coverage**: Same as above but with cleaner test structure

---

### Concurrency & Thread Safety Tests

#### 5. `species_tracker_race_condition_test.go` (270 lines)

**Race condition detection and demonstration**

```bash
go test -race -v -run TestRace
```

**Key Tests**:

- `TestRaceConditionInTimeCalculation` - Demonstrates race in CheckAndUpdateSpecies
  - 200 goroutines × 50 ops = 10,000 concurrent operations
  - Targets 10 species to maximize contention
  - Detects negative day calculations from race conditions
  - Documents error rates (typically < 1%)
- `TestRaceConditionFixDemo` - Shows proposed fix (skipped, documentation only)
- `TestHighContentionScenario` - Maximum contention (500 goroutines, 1 species)

**CRITICAL**: Run with `-race` flag to detect data races!

**Known Issue**: Race condition exists in `CheckAndUpdateSpecies` under high concurrency, documented and tracked.

**Coverage**: Thread safety, concurrent access patterns, race condition edge cases

---

### Database Integration Tests

#### 6. `species_tracker_init_test.go` (560 lines)

**Database initialization and sync logic**

```bash
go test -v -run TestInit
```

**Key Tests**:

- Database initialization success/failure scenarios
- Sync interval validation
- Data preservation on sync failure
- Empty database handling
- Historical data loading

**Coverage**: InitFromDatabase(), SyncIfNeeded(), database error handling

---

#### 7. `species_tracker_database_reliability_test.go` (486 lines)

**Database resilience and error recovery**

```bash
go test -v -run TestDatabaseReliability
```

**Key Tests**:

- Database connection failures
- Empty result handling
- Partial data scenarios
- Sync retry logic
- Data corruption recovery

**Coverage**: Database failure modes, resilience, graceful degradation

---

#### 8. `species_tracker_integration_test.go` (577 lines)

**End-to-end integration scenarios**

```bash
go test -v -run TestIntegration
```

**Key Tests**:

- Complete tracking workflows
- Database → Tracker → Status queries
- Multi-period coordination
- Real-world usage patterns

**Coverage**: Full system integration, realistic scenarios

---

### Period-Specific Tests

#### 9. `species_tracker_periods_test.go` (864 lines)

**Yearly and seasonal period logic**

```bash
go test -v -run TestPeriods
```

**Key Tests**:

- Year reset logic (calendar year vs fiscal year)
- Season transitions
- Period boundary handling
- Fiscal year calculations (e.g., July 1 - June 30)
- Year-crossing seasons (winter spanning Dec-Feb)

**Coverage**: Period resets, boundary conditions, fiscal year support

---

#### 10. `species_tracker_season_test.go` (691 lines)

**Season detection and calculation**

```bash
go test -v -run TestSeason
```

**Key Tests**:

- Traditional seasons (spring, summer, fall, winter)
- Equatorial seasons (wet1, dry1, wet2, dry2)
- Custom season configurations
- Season caching behavior
- Year-crossing season logic

**Coverage**: getCurrentSeason(), computeCurrentSeason(), season cache

---

#### 11. `species_tracker_season_validation_test.go` (155 lines)

**Season configuration validation**

```bash
go test -v -run TestSeasonValidation
```

**Key Tests**:

- Valid season date validation
- Invalid month/day rejection
- Leap year handling (Feb 29)
- Season boundary edge cases

**Coverage**: validateSeasonDate(), season configuration errors

---

### Date Range & Time Logic Tests

#### 12. `species_tracker_daterange_test.go` (724 lines)

**Date range calculations for queries**

```bash
go test -v -run TestDateRange
```

**Key Tests**:

- `getYearDateRange()` calculations
  - Calendar year ranges (Jan 1 - Dec 31)
  - Fiscal year ranges (custom reset dates)
  - Year boundary handling
- `getSeasonDateRange()` calculations
  - 3-month season periods
  - Year-crossing season adjustments
  - Season start/end date accuracy

**Coverage**: Database query date ranges, fiscal year support

---

#### 13. `species_tracker_time_edge_test.go` (444 lines)

**Time calculation edge cases**

```bash
go test -v -run TestTimeEdge
```

**Key Tests**:

- Midnight boundary handling
- Daylight saving time transitions
- Leap year calculations
- Time zone edge cases
- Microsecond precision

**Coverage**: Time arithmetic, boundary conditions, precision issues

---

### Memory Management Tests

#### 14. `species_tracker_cleanup_test.go` (547 lines)

**Memory cleanup and pruning**

```bash
go test -v -run TestCleanup
```

**Key Tests**:

- `PruneOldEntries()` behavior
  - Lifetime data retention (10 years)
  - Yearly data pruning (previous years)
  - Seasonal data pruning (1 year retention)
- Notification record cleanup
- Cache size limits
- LRU eviction

**Coverage**: Memory management, unbounded growth prevention

---

#### 15. `species_tracker_memory_management_test.go` (561 lines)

**Advanced memory management scenarios**

```bash
go test -v -run TestMemoryManagement
```

**Key Tests**:

- Map pre-allocation efficiency
- Cache size enforcement
- Memory leak prevention
- Large dataset handling

**Coverage**: Memory optimizations, capacity management

---

### Notification Tests

#### 16. `notification_suppression_test.go` (153 lines)

**Notification suppression logic**

```bash
go test -v -run TestNotification
```

**Key Tests**:

- `TestNotificationSuppression` - Core suppression behavior
  - First notification not suppressed
  - Duplicate notification suppression
  - Different species independence
  - Suppression window expiration
  - Old record cleanup
- `TestNotificationSuppressionThreadSafety` - Concurrent notification handling

**Coverage**: ShouldSuppressNotification(), RecordNotificationSent(), CleanupOldNotificationRecords()

---

### Reliability & Correctness Tests

#### 17. `species_tracker_reliability_test.go` (410 lines)

**System reliability under stress**

```bash
go test -v -run TestReliability
```

**Key Tests**:

- Long-running operation stability
- Error recovery
- State consistency after failures
- Resource leak detection

**Coverage**: Reliability, error recovery, long-running scenarios

---

#### 18. `species_tracker_business_logic_reliability_test.go` (467 lines)

**Business logic correctness**

```bash
go test -v -run TestBusinessLogicReliability
```

**Key Tests**:

- Species status calculation accuracy
- Period transition correctness
- Window boundary precision
- Status flag consistency

**Coverage**: Business logic rules, calculation accuracy

---

### Critical Operations Tests

#### 19. `species_tracker_critical_operations_test.go` (602 lines)

**Critical operation validation**

```bash
go test -v -run TestCriticalOperations
```

**Key Tests**:

- CheckAndUpdateSpecies() atomicity
- UpdateSpecies() correctness
- GetSpeciesStatus() accuracy
- GetBatchSpeciesStatus() consistency

**Coverage**: Critical public API methods

---

### Comprehensive & Coverage Tests

#### 20. `species_tracker_comprehensive_test.go` (944 lines)

**Comprehensive scenarios combining multiple features**

```bash
go test -v -run TestComprehensive
```

**Key Tests**:

- Multi-period + caching + concurrency
- Full lifecycle workflows
- Complex state transitions
- Real-world usage patterns

**Coverage**: Feature integration, complex scenarios

---

#### 21. `species_tracker_coverage_test.go` (1072 lines)

**Code coverage improvement tests**

```bash
go test -v -run TestCoverage
```

**Key Tests**:

- Edge cases for uncovered lines
- Error path coverage
- Defensive code validation
- Boundary condition coverage

**Coverage**: Code coverage gaps, error paths

---

#### 22. `species_tracker_uncovered_test.go` (576 lines)

**Additional coverage for missed paths**

```bash
go test -v -run TestUncovered
```

**Coverage**: Previously uncovered code paths

---

### Utility & Additional Tests

#### 23. `species_tracker_utility_test.go` (566 lines)

**Utility method tests**

```bash
go test -v -run TestUtility
```

**Key Tests**:

- GetSpeciesCount()
- GetWindowDays()
- IsNewSpecies()
- Helper method validation

**Coverage**: Utility methods, getters, simple queries

---

#### 24. `species_tracker_additional_test.go` (572 lines)

**Additional edge cases and scenarios**

```bash
go test -v -run TestAdditional
```

**Coverage**: Miscellaneous edge cases

---

#### 25. `species_tracker_fix_validation_test.go` (264 lines)

**Validation of specific bug fixes**

```bash
go test -v -run TestFixValidation
```

**Coverage**: Regression tests for known bugs

---

## Running Tests

### All Tests

```bash
# Run all tests
go test -v ./internal/analysis/species/

# Run with race detector (RECOMMENDED)
go test -race -v ./internal/analysis/species/

# Run with coverage
go test -cover ./internal/analysis/species/

# Generate coverage report
go test -coverprofile=coverage.out ./internal/analysis/species/
go tool cover -html=coverage.out
```

### Specific Test Categories

```bash
# Core functionality
go test -v -run TestSpeciesTracker

# Caching
go test -v -run TestCache

# Race conditions (MUST use -race flag)
go test -race -v -run TestRace

# Database integration
go test -v -run TestInit
go test -v -run TestDatabaseReliability

# Periods
go test -v -run TestPeriods
go test -v -run TestSeason

# Notifications
go test -v -run TestNotification

# Memory management
go test -v -run TestCleanup
go test -v -run TestMemoryManagement

# Performance
go test -v -run TestPerformance
```

### Performance Tests

```bash
# Run only short tests (skip long-running performance tests)
go test -short -v ./internal/analysis/species/

# Run only performance tests
go test -v -run TestPerformance ./internal/analysis/species/
```

### Benchmarks

```bash
# Run benchmarks (if any)
go test -bench=. -benchmem ./internal/analysis/species/
```

## Test Helpers & Mocks

### MockSpeciesDatastore

```go
type MockSpeciesDatastore struct {
    mock.Mock
}

func (m *MockSpeciesDatastore) GetNewSpeciesDetections(
    ctx context.Context,
    startDate, endDate string,
    limit, offset int,
) ([]datastore.NewSpeciesData, error) {
    args := m.Called(ctx, startDate, endDate, limit, offset)
    return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
}
```

**Usage**:

```go
ds := &MockSpeciesDatastore{}
ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
    Return([]datastore.NewSpeciesData{
        {
            ScientificName: "Turdus migratorius",
            CommonName: "American Robin",
            FirstSeenDate: "2024-01-15",
        },
    }, nil)
```

### Test Constants

```go
const (
    oldSpeciesDays     = 20 // Outside window
    recentSpeciesDays  = 5  // Within window
    newSpeciesWindow   = 14 // Default window
    syncIntervalMins   = 60 // Sync interval
    yearlyWindowDays   = 30 // Yearly window
    seasonalWindowDays = 21 // Seasonal window
)
```

### Test Settings Helper

```go
func createTestSettings() *conf.SpeciesTrackingSettings {
    return &conf.SpeciesTrackingSettings{
        Enabled: true,
        NewSpeciesWindowDays: 14,
        SyncIntervalMinutes: 60,
        NotificationSuppressionHours: 168,
        YearlyTracking: conf.YearlyTrackingSettings{
            Enabled: true,
            WindowDays: 30,
            ResetMonth: 1,
            ResetDay: 1,
        },
        SeasonalTracking: conf.SeasonalTrackingSettings{
            Enabled: true,
            WindowDays: 21,
        },
    }
}
```

## Known Issues & Race Conditions

### Race Condition in CheckAndUpdateSpecies

**Issue**: Under high concurrency, `CheckAndUpdateSpecies()` can return negative days.

**Root Cause**: Non-atomic read-calculate-update sequence:

1. Goroutine A reads `firstSeen` time
2. Goroutine B updates `firstSeen` to earlier time
3. Goroutine A calculates days using old `firstSeen` vs new detection time
4. Result: negative days calculation

**Detection**:

```bash
go test -race -v -run TestRaceConditionInTimeCalculation
```

**Error Rate**: Typically < 1% under extreme contention (500 goroutines, 1 species)

**Status**: Documented and tracked. Defensive check added to prevent negative values from being returned.

**Workaround**: Current code includes defensive checks:

```go
// Defensive check: prevent negative days
daysSince = max(0, daysSince)
```

**Proposed Fix**: See `TestRaceConditionFixDemo` in `species_tracker_race_condition_test.go`

## Testing Best Practices

### 1. Always Use Race Detector

```bash
go test -race -v ./internal/analysis/species/
```

The race detector is **critical** for this package due to concurrent access patterns.

### 2. Test Isolation

All tests use `t.Parallel()` for faster execution. Ensure tests don't share state:

```go
func TestMyFeature(t *testing.T) {
    t.Parallel() // Run in parallel with other tests

    // Create isolated tracker instance
    tracker := NewTrackerFromSettings(mockDS, testSettings)
    // ... test logic
}
```

### 3. Mock Database Operations

Always mock database calls to avoid external dependencies:

```go
ds := &MockSpeciesDatastore{}
ds.On("GetNewSpeciesDetections", mock.Anything, ...).Return(mockData, nil)
```

### 4. Time-Based Testing

Use fixed times or relative offsets to avoid flaky tests:

```go
baseTime := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
detectionTime := baseTime.Add(5 * 24 * time.Hour)
```

### 5. Verify All Periods

When testing multi-period tracking, verify all three periods:

```go
status := tracker.GetSpeciesStatus(scientificName, currentTime)
assert.True(t, status.IsNew, "Lifetime")
assert.True(t, status.IsNewThisYear, "Yearly")
assert.True(t, status.IsNewThisSeason, "Seasonal")
```

## Test Coverage Goals

| Category               | Target | Current |
| ---------------------- | ------ | ------- |
| Statement Coverage     | > 90%  | ~95%    |
| Branch Coverage        | > 85%  | ~90%    |
| Function Coverage      | 100%   | 100%    |
| Concurrency Tests      | All    | 3 files |
| Integration Tests      | All    | 1 file  |
| Database Failure Tests | All    | 2 files |

## CI/CD Integration

### GitHub Actions Example

```yaml
- name: Run Species Tracker Tests
  run: |
    go test -race -v -coverprofile=coverage.out ./internal/analysis/species/
    go tool cover -func=coverage.out

- name: Upload Coverage
  uses: codecov/codecov-action@v3
  with:
    files: ./coverage.out
    flags: species-tracker
```

## Debugging Test Failures

### Enable Debug Logging

The package uses the centralized logger:

```go
// Use debug level for verbose output
log := GetLogger()
log.Debug("debugging tracker state",
    logger.Int("species_count", tracker.GetSpeciesCount()))
```

### Use Race Detector Output

```bash
go test -race -v -run TestFailingTest 2>&1 | tee race.log
```

### Inspect Cache State

```go
// In test
tracker.mu.RLock()
fmt.Printf("Cache size: %d\n", len(tracker.statusCache))
fmt.Printf("Species count: %d\n", len(tracker.speciesFirstSeen))
tracker.mu.RUnlock()
```

### Use Testing Helpers

```go
// Check internal state
assert.True(t, tracker.IsSeasonMapInitialized("spring"))
assert.Equal(t, 5, tracker.GetSeasonMapCount("spring"))

// Force cache expiration
tracker.ExpireCacheForTesting(scientificName)

// Clear cache
tracker.ClearCacheForTesting()
```

## Contributing Tests

When adding new features, ensure:

1. **Unit tests** for new functions
2. **Integration tests** for feature interactions
3. **Race condition tests** if accessing shared state
4. **Edge case tests** for boundary conditions
5. **Documentation** in this file

### Test File Naming

- `*_test.go` - Test file
- `Test*` - Test function
- `Benchmark*` - Benchmark function

### Example New Test

```go
// species_tracker_new_feature_test.go
package species

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestNewFeature(t *testing.T) {
    t.Parallel() // Always use parallel

    // Setup
    tracker := createTestTracker()

    // Test
    result := tracker.NewFeature()

    // Assert
    assert.NotNil(t, result)
}
```

## License

Part of BirdNET-Go - See project LICENSE file

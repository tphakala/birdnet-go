# Exclude False Positives from Statistics

**Date:** 2026-02-14
**Issue:** #1951
**Status:** Design Complete - Reviewed and Updated
**Reviewer:** Kimi K2.5 (OpenCode)
**Review Date:** 2026-02-14

## Problem Statement

Detection statistics currently include all detections regardless of review status. When users mark detections as false positives (verified = 'false_positive'), these should be excluded from all statistics calculations to provide accurate reporting.

Users report that after marking detections as false positives, the statistics (counts, hourly distributions, species summaries) still include those detections, making the statistics inaccurate and undermining the utility of the review feature.

## Solution Overview

Add LEFT JOIN filtering to all analytics queries in both legacy and v2 datastores. The filter will exclude only detections marked as 'false_positive' while including:
- Unreviewed detections (no note_reviews record)
- Detections marked as 'correct'

### Design Principles
- **Default behavior:** False positives excluded from all statistics
- **No override option:** Always exclude false positives (users can query detections API with filters if needed)
- **No API changes:** Backend-only fix, transparent to API consumers
- **Consistent pattern:** Use same LEFT JOIN approach across all queries

## Affected Components

### Legacy Datastore (`internal/datastore/`)

1. **GetTopBirdsData** (interfaces.go:486) - Daily species summary counts
2. **GetHourlyOccurrences** (interfaces.go:648) - Hourly detection patterns for single species
3. **GetBatchHourlyOccurrences** (interfaces.go:683) - Hourly patterns for multiple species
4. **GetSpeciesSummaryData** (analytics.go:67) - Overall species statistics
5. **GetHourlyAnalyticsData** (analytics.go:261) - Hourly analytics endpoint
6. **GetDailyAnalyticsData** (analytics.go:295) - Daily trends
7. **GetSpeciesDiversityData** (analytics.go:335) - Unique species per day
8. **GetHourlyDistribution** (analytics.go:446) - Time-of-day distribution
9. **GetNewSpeciesDetections** (analytics.go:609) - First-time species detections

### V2 Datastore (`internal/datastore/v2only/datastore.go`)

1. **GetTopBirdsData** (line 694) - Daily species summary counts
2. **GetHourlyOccurrences** (line 770) - Hourly detection patterns for single species
3. **GetBatchHourlyOccurrences** (line 803) - Hourly patterns for multiple species
4. **GetDailyAnalyticsData** (line 1938) - Daily trends
5. **GetSpeciesDiversityData** (line 2097) - Unique species per day

**Note:** V2 uses `detection_reviews` table instead of `note_reviews`, and may use table prefixes (`v2_`). Must use repository helper methods like `r.reviewsTable()` instead of hardcoded table names.

## Technical Design

### SQL Filtering Pattern

All analytics queries will use this consistent pattern:

```sql
FROM notes
LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
WHERE ... [existing conditions] ...
  AND (note_reviews.verified IS NULL OR note_reviews.verified != 'false_positive')
```

### Why LEFT JOIN?

- Preserves all notes even without review records (unreviewed detections)
- More performant than NOT EXISTS subquery for SQLite/MySQL
- Consistent with existing search query patterns (see `SearchNotesAdvanced` in interfaces.go:2297)
- Existing index on `note_reviews.note_id` provides good join performance

### GORM Implementation Pattern

For queries using GORM query builder:

```go
query := ds.DB.Model(&Note{}).
    Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
    Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", "false_positive").
    // ... rest of query conditions
```

For raw SQL queries:

```go
queryStr := `
    SELECT ...
    FROM notes
    LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
    WHERE date >= ? AND date <= ?
      AND (note_reviews.verified IS NULL OR note_reviews.verified != 'false_positive')
    GROUP BY scientific_name
`
```

### Important Implementation Notes

1. **Column qualification:** When adding JOINs, qualify column names with table prefix (`notes.date`, `notes.common_name`) to avoid ambiguity
2. **Consistent WHERE clause:** Always use `(note_reviews.verified IS NULL OR note_reviews.verified != 'false_positive')`
3. **No new indexes needed:** Existing foreign key index on `note_reviews.note_id` is sufficient

## Query-by-Query Modifications

### Example: GetTopBirdsData

**Before:**
```go
query := ds.DB.Table("notes").
    Select("common_name, scientific_name, species_code, COUNT(*) as count, MAX(confidence) as confidence, date, MAX(time) as time").
    Where("date = ? AND confidence >= ?", selectedDate, minConfidenceNormalized).
    Group("common_name, scientific_name, species_code, date").
    Order("count DESC").
    Limit(reportCount)
```

**After:**
```go
query := ds.DB.Table("notes").
    Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
    Select("notes.common_name, notes.scientific_name, notes.species_code, COUNT(*) as count, MAX(notes.confidence) as confidence, notes.date, MAX(notes.time) as time").
    Where("notes.date = ? AND notes.confidence >= ?", selectedDate, minConfidenceNormalized).
    Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", "false_positive").
    Group("notes.common_name, notes.scientific_name, notes.species_code, notes.date").
    Order("count DESC").
    Limit(reportCount)
```

**Key changes:**
- Add `Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id")`
- Add `Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", "false_positive")`
- Qualify all column names with `notes.` prefix in SELECT, WHERE, and GROUP BY clauses

### Example: GetHourlyOccurrences

**Before:**
```go
err := ds.DB.Model(&Note{}).
    Select(fmt.Sprintf("%s as hour, COUNT(*) as count", hourFormat)).
    Where("date = ? AND common_name = ? AND confidence >= ?", date, commonName, minConfidenceNormalized).
    Group(hourFormat).
    Scan(&results).Error
```

**After:**
```go
err := ds.DB.Model(&Note{}).
    Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
    Select(fmt.Sprintf("%s as hour, COUNT(*) as count", hourFormat)).
    Where("notes.date = ? AND notes.common_name = ? AND notes.confidence >= ?", date, commonName, minConfidenceNormalized).
    Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", "false_positive").
    Group(hourFormat).
    Scan(&results).Error
```

### V2 Datastore Pattern

V2 datastore uses a repository pattern. Apply the same filtering logic:

```sql
LEFT JOIN detection_reviews ON detections.id = detection_reviews.detection_id
WHERE (detection_reviews.verified IS NULL OR detection_reviews.verified != 'false_positive')
```

Note: V2 may use different table names (`detections` vs `notes`). Verify during implementation.

## Testing Strategy

### Unit Tests Required

For each modified method, add tests that verify:

#### 1. False Positives Are Excluded

```go
func TestGetTopBirdsData_ExcludesFalsePositives(t *testing.T) {
    // Given: 3 detections for same species on same date
    //   - 1 unreviewed (no review record)
    //   - 1 marked as verified='correct'
    //   - 1 marked as verified='false_positive'
    // When: GetTopBirdsData is called for that date
    // Then: Count = 2 (excludes false_positive, includes unreviewed + correct)
}
```

#### 2. Unreviewed Detections Are Included

```go
func TestGetTopBirdsData_IncludesUnreviewed(t *testing.T) {
    // Given: Detection with no review record
    // When: GetTopBirdsData is called
    // Then: Detection is included in results
}
```

#### 3. Correct Detections Are Included

```go
func TestGetTopBirdsData_IncludesCorrect(t *testing.T) {
    // Given: Detection marked as verified='correct'
    // When: GetTopBirdsData is called
    // Then: Detection is included in results
}
```

### Test Organization

- **Legacy tests:** Add to `internal/datastore/analytics_test.go` or create new `analytics_filtering_test.go`
- **V2 tests:** Add to `internal/datastore/v2only/datastore_test.go`

### Test Data Setup Pattern

Use existing testify patterns:
```go
// Setup test database
ds := setupTestDB(t)
defer ds.Close()

// Create test notes
note1 := createTestNote(t, ds, "Parus major", "2026-02-14")
note2 := createTestNote(t, ds, "Parus major", "2026-02-14")
note3 := createTestNote(t, ds, "Parus major", "2026-02-14")

// Create reviews (note1 has no review record - unreviewed)
createReview(t, ds, note2.ID, "correct")
createReview(t, ds, note3.ID, "false_positive")

// Execute query and assert
results, err := ds.GetTopBirdsData("2026-02-14", 0.0, 0)
require.NoError(t, err)
assert.Len(t, results, 1) // Only one species
assert.Equal(t, 2, results[0].Count) // Excludes false_positive
```

## Implementation Checklist

### Phase 1: Legacy Datastore

- [ ] Update `GetTopBirdsData` (interfaces.go:486) + tests
- [ ] Update `GetHourlyOccurrences` (interfaces.go:648) + tests
- [ ] Update `GetBatchHourlyOccurrences` (interfaces.go:683) + tests
- [ ] Update `GetSpeciesSummaryData` (analytics.go:67) + tests
- [ ] Update `GetHourlyAnalyticsData` (analytics.go:261) + tests
- [ ] Update `GetDailyAnalyticsData` (analytics.go:295) + tests
- [ ] Update `GetSpeciesDiversityData` (analytics.go:335) + tests
- [ ] Update `GetHourlyDistribution` (analytics.go:446) + tests
- [ ] Update `GetNewSpeciesDetections` (analytics.go:609) + tests
- [ ] Add performance benchmark test

### Phase 2: V2 Datastore

- [ ] Update `GetTopBirdsData` (v2only/datastore.go:694) + tests
- [ ] Update `GetHourlyOccurrences` (v2only/datastore.go:770) + tests
- [ ] Update `GetBatchHourlyOccurrences` (v2only/datastore.go:803) + tests
- [ ] Update `GetDailyAnalyticsData` (v2only/datastore.go:1938) + tests
- [ ] Update `GetSpeciesDiversityData` (v2only/datastore.go:2097) + tests
- [ ] Verify table prefix handling (v2_detection_reviews)
- [ ] Add performance benchmark test for v2

### Phase 3: Validation

- [ ] Run full test suite: `go test ./... -v`
- [ ] Run linter: `golangci-lint run -v`
- [ ] Run benchmarks: Compare before/after performance
- [ ] Manual verification with test database containing false positives
- [ ] Verify no regressions in existing analytics endpoints
- [ ] Test zero-results case (all detections are false positives)

## Migration Considerations

### No Database Schema Changes
- Only query logic changes
- No migrations required
- Existing indexes are sufficient

### Backward Compatibility
- No API changes
- Existing API consumers unaffected
- Statistics become more accurate without breaking changes

### Performance Impact
- LEFT JOIN uses existing foreign key index
- Minimal performance impact expected
- No additional indexes needed

## Risks and Mitigations

### Risk: Breaking existing queries
**Mitigation:** Comprehensive unit tests for each modified method

### Risk: Performance degradation
**Mitigation:** Use LEFT JOIN with existing indexes, test with large datasets

### Risk: Different behavior in legacy vs v2
**Mitigation:** Apply identical filtering logic to both implementations, test both

## Success Criteria

1. All analytics endpoints exclude false positives from statistics
2. Unreviewed and correct detections still included
3. All existing tests pass
4. New tests verify filtering behavior
5. No performance degradation (< 5% slower acceptable)
6. Issue #1951 resolved - user can mark false positives and see accurate statistics

## References

- Issue: #1951
- Existing filtering pattern: `internal/datastore/interfaces.go:2297` (SearchNotesAdvanced)
- Review model: `internal/datastore/model.go:66-72`

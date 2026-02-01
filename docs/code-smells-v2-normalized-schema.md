# Code Smells - V2 Normalized Schema

Code review findings from `feature/v2-normalized-schema` branch that need to be addressed.

## High Priority

### 1. N+1 queries in `Save` method

**File:** `internal/datastore/v2only/datastore.go:259-268`

**Problem:** The `Save` method loops through all predictions and calls `ds.label.GetOrCreate` for each one individually.

**Fix:** Collect all species names from results and use `ds.label.BatchGetOrCreate` before the loop.

```go
// Current (inefficient)
for i, r := range results {
    predLabel, err := ds.label.GetOrCreate(ctx, r.Species, model.ID, ds.speciesLabelTypeID, ds.avesClassID)
    // ...
}

// Fixed (batch)
speciesNames := make([]string, len(results))
for i, r := range results {
    speciesNames[i] = r.Species
}
predLabels, err := ds.label.BatchGetOrCreate(ctx, speciesNames, model.ID, ds.speciesLabelTypeID, ds.avesClassID)
```

---

### 2. N+1 queries in migration workers

**File:** `internal/datastore/v2/migration/worker_auxiliary.go` (lines 196, 249, 307, 358)

**Problem:** Migration functions for ImageCache, Thresholds, ThresholdEvents, and Notifications perform individual database calls per item.

**Fix:** Gather all scientific names first, batch resolve labels, then batch insert entities.

---

### 3. N+1 in `GetImageCacheBatch`

**File:** `internal/datastore/v2/repository/image_cache_impl.go:128`

**Problem:** Iterates through `scientificNames` and executes a query for each name.

**Fix:** Add batch lookup capability to `LabelRepository` and use it here.

---

## Medium Priority

### 4. Large function: `Save` in v2only needs refactoring

**File:** `internal/datastore/v2only/datastore.go:238-334`

**Problem:** At 96 lines, this function is quite long with multiple responsibilities (model lookup, label resolution, timestamp parsing, transaction handling).

**Fix:** Extract into helper functions:
- `parseDetectionTimestamp(date, time string) int64`
- `preparePredictionLabels(ctx, results, modelID) ([]*entities.Label, error)`

---

### 5. Duplicate migration patterns need refactoring

**File:** `internal/datastore/v2/migration/worker_related.go:206-461`

**Problem:** `migrateReviews`, `migrateComments`, and `migrateLocks` share nearly identical structure:
- Fetch batch → filter existing IDs → convert entities → save batch

**Fix:** Consider a generic batched migration helper:

```go
type batchMigrator[T any, V any] struct {
    fetchBatch    func(lastID uint, size int) ([]T, error)
    convert       func(*T, map[uint]struct{}) *V
    saveBatch     func(ctx context.Context, items []*V) error
    getDetectionID func(*T) uint
    getID         func(*T) uint
}
```

---

### 6. Hardcoded limit in filter conversion

**File:** `internal/datastore/v2/repository/filter_conversion.go:333`

**Problem:** `ResolveSpeciesToLabelIDsWithCommonName` uses hardcoded limit of 100 which may exclude valid species from search results.

**Fix:** Increase limit to 1000 or implement exact-match prioritization for filter resolution.

---

## Low Priority

### 7. Update minimum prediction confidence threshold

**File:** `internal/datastore/v2/migration/worker_related.go:478`

**Current:**
```go
const minPredictionConfidence = 0.1
```

**Fix:**
```go
// minPredictionConfidence is the minimum confidence threshold for migrating predictions.
// Set to 0.2 (20%) to reduce table size by excluding predictions that are almost
// certainly incorrect. Predictions below this threshold provide no analytical value
// and would only increase storage requirements.
const minPredictionConfidence = 0.2
```

---

## Tracking

- [x] Fix N+1 in `Save` method (commit: fix code smells)
- [x] Fix N+1 in migration workers (commit: fix code smells)
- [x] Fix N+1 in `GetImageCacheBatch` (commit: fix code smells)
- [x] Refactor `Save` function (commit: fix code smells)
- [ ] Refactor duplicate migration patterns (DEFERRED - complexity outweighs benefit)
- [ ] Fix hardcoded limit in filter conversion (NO CHANGE - intentional design)
- [x] Update minPredictionConfidence to 0.2 (commit: fix code smells)

## Related Issues

- #1911 - Add manual mapping for non-species label types during migration

# Fix Plan: V2 Normalized Schema Code Smells

## Overview

This plan addresses the code smells identified in `docs/code-smells-v2-normalized-schema.md`.
Fixes are ordered by priority and dependency relationships.

## Pre-requisites

Before implementing:
- [ ] All existing tests pass (`go test ./...`)
- [ ] Linter passes (`golangci-lint run -v`)

---

## High Priority Fixes

### Fix 1: N+1 in `Save` method (v2only/datastore.go)

**Problem:** Lines 259-268 loop through predictions calling `GetOrCreate` individually.

**Current Code:**
```go
for i, r := range results {
    predLabel, err := ds.label.GetOrCreate(ctx, r.Species, model.ID, ds.speciesLabelTypeID, ds.avesClassID)
    // ...
}
```

**Solution:** Use existing `BatchGetOrCreate` method.

**Implementation:**
```go
if len(results) > 0 {
    // Collect unique species names
    speciesNames := make([]string, len(results))
    for i, r := range results {
        speciesNames[i] = r.Species
    }

    // Batch resolve all labels (returns map[scientificName]*Label)
    labelMap, err := ds.label.BatchGetOrCreate(ctx, speciesNames, model.ID, ds.speciesLabelTypeID, ds.avesClassID)
    if err != nil {
        return fmt.Errorf("failed to batch get/create prediction labels: %w", err)
    }

    // Build predLabels slice from map
    predLabels = make([]*entities.Label, len(results))
    for i, r := range results {
        label, ok := labelMap[r.Species]
        if !ok {
            return fmt.Errorf("label not found for species %s after batch creation", r.Species)
        }
        predLabels[i] = label
    }
}
```

**Files:** `internal/datastore/v2only/datastore.go`

**Tests:** Existing `Save` tests should pass; add benchmark test.

---

### Fix 2: N+1 in `GetImageCacheBatch` (image_cache_impl.go)

**Problem:** Lines 122-131 loop through `scientificNames` calling `GetByScientificName` individually.

**Current Code:**
```go
for _, sciName := range scientificNames {
    labels, err := r.labelRepo.GetByScientificName(ctx, sciName)
    // ...
}
```

**Solution:** Add `GetByScientificNames` (plural) method to LabelRepository interface.

**Implementation:**

1. Add interface method in `label.go`:
```go
// GetByScientificNames retrieves all labels matching any of the scientific names.
// Returns a map of scientificName -> []*Label for efficient lookup.
GetByScientificNames(ctx context.Context, names []string) (map[string][]*entities.Label, error)
```

2. Implement in `label_impl.go` (with chunking to avoid SQL parameter limits):
```go
// GetByScientificNames retrieves all labels matching any of the scientific names.
// Handles large name sets by chunking to avoid SQL parameter limits.
func (r *labelRepository) GetByScientificNames(ctx context.Context, names []string) (map[string][]*entities.Label, error) {
    if len(names) == 0 {
        return make(map[string][]*entities.Label), nil
    }

    result := make(map[string][]*entities.Label, len(names))

    // Chunk to avoid SQL parameter limits (batchQuerySize = 500)
    for i := 0; i < len(names); i += batchQuerySize {
        end := min(i+batchQuerySize, len(names))
        batchNames := names[i:end]

        var labels []*entities.Label
        err := r.db.WithContext(ctx).Table(r.tableName()).
            Where("scientific_name IN ?", batchNames).
            Find(&labels).Error
        if err != nil {
            return nil, fmt.Errorf("batch load labels: %w", err)
        }

        for _, label := range labels {
            result[label.ScientificName] = append(result[label.ScientificName], label)
        }
    }
    return result, nil
}
```

3. Update `GetImageCacheBatch`:
```go
// Batch fetch all labels for scientific names
labelsByName, err := r.labelRepo.GetByScientificNames(ctx, scientificNames)
if err != nil {
    return nil, err
}

var allLabelIDs []uint
labelIDToSciName := make(map[uint]string)
for sciName, labels := range labelsByName {
    for _, label := range labels {
        allLabelIDs = append(allLabelIDs, label.ID)
        labelIDToSciName[label.ID] = sciName
    }
}
```

**Files:**

- `internal/datastore/v2/repository/label.go`
- `internal/datastore/v2/repository/label_impl.go`
- `internal/datastore/v2/repository/image_cache_impl.go`
- `internal/datastore/v2/repository/filter_conversion_test.go` (mock update)

**Tests:** Add unit test for new method; manually update mock (no mockery generation used in this codebase - mocks are hand-written in test files).

---

### Fix 3: N+1 in Migration Workers (worker_auxiliary.go)

**Problem:** `migrateImageCaches` (line 207) calls `GetOrCreate` per cache item.

**Solution:** Batch resolve all labels before loop.

**Implementation:**
```go
func (m *AuxiliaryMigrator) migrateImageCaches(ctx context.Context, result *AuxiliaryMigrationResult) {
    // ... validation ...

    legacyCaches, err := m.legacyStore.GetAllImageCaches("wikimedia")
    // ... error handling ...

    // Collect unique species names
    speciesSet := make(map[string]struct{})
    for i := range legacyCaches {
        speciesSet[legacyCaches[i].ScientificName] = struct{}{}
    }
    speciesNames := make([]string, 0, len(speciesSet))
    for name := range speciesSet {
        speciesNames = append(speciesNames, name)
    }

    // Batch resolve all labels
    labelMap, err := m.labelRepo.BatchGetOrCreate(ctx, speciesNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
    if err != nil {
        m.logger.Warn("failed to batch resolve labels", logger.Error(err))
        result.ImageCaches.Error = err
        return
    }

    // Now iterate using pre-resolved labels
    for i := range legacyCaches {
        cache := &legacyCaches[i]
        label, ok := labelMap[cache.ScientificName]
        if !ok {
            result.ImageCaches.Skipped++
            continue
        }
        // ... create v2Cache with label.ID ...
    }
}
```

**Files:** `internal/datastore/v2/migration/worker_auxiliary.go`

**Tests:** Existing migration tests should pass.

---

## Medium Priority Fixes

### Fix 4: Refactor `Save` Function

**Problem:** 96 lines with multiple responsibilities.

**Solution:** Extract helper functions.

**Implementation:**
```go
// parseDetectionTimestamp converts date/time strings to Unix timestamp.
func parseDetectionTimestamp(date, time string, tz *time.Location) int64 {
    if date != "" && time != "" {
        dateTimeStr := date + " " + time
        if t, err := time.ParseInLocation("2006-01-02 15:04:05", dateTimeStr, tz); err == nil {
            return t.Unix()
        }
    } else if date != "" {
        if t, err := time.ParseInLocation("2006-01-02", date, tz); err == nil {
            return t.Unix()
        }
    }
    return time.Now().Unix()
}

// batchResolvePredictionLabels resolves species names to labels using batch operation.
func (ds *Datastore) batchResolvePredictionLabels(ctx context.Context, results []datastore.Results, modelID uint) ([]*entities.Label, error) {
    if len(results) == 0 {
        return nil, nil
    }

    speciesNames := make([]string, len(results))
    for i, r := range results {
        speciesNames[i] = r.Species
    }

    labelMap, err := ds.label.BatchGetOrCreate(ctx, speciesNames, modelID, ds.speciesLabelTypeID, ds.avesClassID)
    if err != nil {
        return nil, err
    }

    labels := make([]*entities.Label, len(results))
    for i, r := range results {
        label, ok := labelMap[r.Species]
        if !ok {
            return nil, fmt.Errorf("label not found for species %s", r.Species)
        }
        labels[i] = label
    }
    return labels, nil
}
```

**Files:** `internal/datastore/v2only/datastore.go`

**Tests:** Existing tests should pass.

---

### Fix 5: Duplicate Migration Patterns (DEFER)

**Status:** DEFERRED - The patterns are similar but have enough differences that a generic abstraction would be complex and reduce readability. The duplication is acceptable for maintainability.

**Reasoning:**
- Each migration function has specific entity types and conversion logic
- Generic solution would require type parameters and complex interfaces
- Current code is readable and easy to understand/debug
- Migration code runs once per database; performance not critical

---

### Fix 6: Hardcoded Limit (NO CHANGE)

**Status:** NO CHANGE - The limit of 100 is intentional per the existing comment.

**Current Comment (line 420-421):**
```go
// Limit of 100 is intentional for Simple Search API - a broader search term returning
// 100+ species indicates the user should refine their search.
```

**Reasoning:** This is a deliberate UX decision, not a bug.

---

## Low Priority Fixes

### Fix 7: Update minPredictionConfidence

**Problem:** Current threshold 0.1 (10%) is too low.

**Solution:** Increase to 0.2 (20%).

**Implementation:**
```go
// minPredictionConfidence is the minimum confidence threshold for migrating predictions.
// Set to 0.2 (20%) to reduce table size by excluding predictions that are almost
// certainly incorrect. Predictions below this threshold provide no analytical value
// and would only increase storage requirements.
const minPredictionConfidence = 0.2
```

**Files:** `internal/datastore/v2/migration/worker_related.go:478`

**Tests:** No test changes needed.

---

## Implementation Order

1. **Fix 7** - Trivial constant change (1 line)
2. **Fix 1** - N+1 in Save (uses existing BatchGetOrCreate)
3. **Fix 2** - N+1 in GetImageCacheBatch (new method + refactor)
4. **Fix 3** - N+1 in Migration Workers (uses BatchGetOrCreate)
5. **Fix 4** - Refactor Save function (extract helpers)

## Verification

After each fix:
1. Run tests: `go test ./internal/datastore/... -v`
2. Run linter: `golangci-lint run -v`
3. Manual smoke test if applicable

## Excluded Items

- **Fix 5** (Duplicate migration patterns): DEFERRED - complexity outweighs benefit
- **Fix 6** (Hardcoded limit): NO CHANGE - intentional design decision

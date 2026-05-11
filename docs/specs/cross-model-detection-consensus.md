# Cross-Model Detection Consensus

## Problem Statement

With multi-model support (BirdNET, Perch, BatNET), pending detections are keyed by
`sourceID:speciesName:modelID`, creating independent flush entries per model.  This
causes:

1. **No cross-model confirmation** - BirdNET 2/5 + Perch 2/5 = both discarded, even
   though 4 independent detections across 2 models is strong evidence.
2. **Duplicate database records** - the same bird event produces N detection rows (one
   per model that passes the threshold independently).
3. **No stored model attribution** - after flush, the single `ModelID` on a Detection
   record only captures which model "won", not the full picture.

## Design Goals

- Merge pending detections across models for the same `source:species`.
- Use combined hit count for threshold evaluation (cross-model consensus).
- Store per-model contribution data (hit count, max confidence) alongside each
  detection for observability and analysis.
- Preserve granular per-model logging throughout the pipeline.
- One Detection record per bird event, not per model.

## Changes Overview

### 1. PendingDetection Struct (processor.go)

**Current key**: `sourceID:speciesName:modelID`
**New key**: `sourceID:speciesName`

Add a `ModelContributions` map to track per-model data inside a single entry:

```go
type ModelContribution struct {
    HitCount      int       // Number of times this model detected the species
    MaxConfidence float64   // Highest confidence seen from this model
    LastHitAt     time.Time // When this model last detected the species
}

type PendingDetection struct {
    // Existing fields (ModelID removed, Confidence becomes best-across-models)
    Detection          Detections
    Confidence         float64   // Best confidence seen across all models
    Source             string
    FirstDetected      time.Time
    CreatedAt          time.Time
    AudioCapturedAt    time.Time
    LastUpdated        time.Time
    FlushDeadline      time.Time
    Count              int       // Total hits across all models
    ExtendedCapture    bool
    MaxDeadline        time.Time

    // New fields
    BestModelID        string                       // Model that produced the highest confidence
    ModelContributions map[string]ModelContribution  // Keyed by model ID string
}
```

The top-level `ModelID` field is replaced by `BestModelID` (the model with the
highest confidence detection).  `Count` becomes the sum of all model hit counts.

### 2. processDetections Update (processor.go ~line 611)

When a detection arrives:

1. Compute `mapKey = pendingDetectionKey(sourceID, speciesName)` (no modelID).
2. Look up existing entry.
3. **If exists**:
   - Log the per-model update (with model_id in the log entry).
   - Increment `ModelContributions[modelID].HitCount`.
   - Update `ModelContributions[modelID].MaxConfidence` if new confidence is higher.
   - Update `ModelContributions[modelID].LastHitAt`.
   - Increment `Count` (total).
   - If new confidence > `Confidence`, update `Confidence`, `BestModelID`, and
     replace `Detection` data (keeps the best audio/result for clip export).
   - Do NOT extend `FlushDeadline` (keep current behavior: fixed at creation time).
     Models analyze the same audio, so hits arrive within the same window naturally.
4. **If new**:
   - Create entry with `ModelContributions` initialized to a single-entry map.
   - Set `BestModelID = modelID`, `Count = 1`.

**Logging**: Every incoming detection still logs with its specific `model_id`,
`species`, `source`, `confidence`, and `operation` fields.  No change to log
granularity.

### 3. flushPendingDetections Update (processor.go ~line 1425)

The flush loop iterates entries keyed by `source:species` (fewer entries than before).

**Threshold check**: `shouldDiscardDetection` uses `item.Count` (total across all
models) vs `minDetections`.  No change to the function signature needed since
`item.Count` already represents the total.

**Discard logging**: Log the discard with per-model breakdown:

```go
GetLogger().Info("discarding detection",
    logger.String("species", speciesName),
    logger.String("source", displayName),
    logger.String("best_model_id", item.BestModelID),
    logger.Int("total_count", item.Count),
    logger.Int("model_count", len(item.ModelContributions)),
    logger.String("reason", reason),
    logger.String("operation", "discard_detection"))
```

**Flush logging**: Log the flush with per-model breakdown:

```go
GetLogger().Info("Flushing detection",
    logger.String("species", speciesName),
    logger.String("source", displayName),
    logger.String("best_model_id", item.BestModelID),
    logger.Int("total_count", item.Count),
    logger.Int("model_count", len(item.ModelContributions)),
    logger.String("operation", "flush_detection"))
```

### 4. processApprovedDetection Update (processor.go ~line 1271)

- Call `LearnFromApprovedDetection` once per contributing model (iterate
  `ModelContributions`), using each model's max confidence.
- The Detection data sent to the action queue uses `BestModelID` as the model
  identity (highest confidence detection data is already stored).
- Metrics increment happens once per flushed detection (not per model).

### 5. Dynamic Threshold Update (dynamic_threshold.go)

`dynamicThresholdKey` is already `modelID:speciesLowercase`.  No schema change
needed.  The only change is that `LearnFromApprovedDetection` is called for each
contributing model from `ModelContributions` instead of once.

`updateDynamicThreshold` in `processDetections` already receives the specific
`modelID` from the incoming detection.  No change needed.

### 6. SSE Pending Detection Broadcast (pending_broadcast.go)

Update `SSEPendingDetection` to include multi-model information:

```go
type SSEModelContribution struct {
    ModelID       string  `json:"modelID"`
    HitCount      int     `json:"hitCount"`
    MaxConfidence float64 `json:"maxConfidence"`
}

type SSEPendingDetection struct {
    Species            string                 `json:"species"`
    ScientificName     string                 `json:"scientificName"`
    Thumbnail          string                 `json:"thumbnail"`
    Status             PendingDetectionStatus `json:"status"`
    FirstDetected      int64                  `json:"firstDetected"`
    AudioCapturedAt    int64                  `json:"audioCapturedAt,omitempty"`
    LastUpdated        int64                  `json:"lastUpdated"`
    Source             string                 `json:"source"`
    SourceID           string                 `json:"sourceID"`
    BestModelID        string                 `json:"bestModelID,omitempty"`
    HitCount           int                    `json:"hitCount"`
    ModelContributions []SSEModelContribution `json:"modelContributions,omitempty"`
}
```

The frontend currently keys pending detections by `sourceID + species`.  With model
removed from the backend key, this naturally aligns: one SSE entry per
source+species.

### 7. Database Schema: DetectionModelContribution Entity

New entity in `internal/datastore/v2/entities/`:

```go
// DetectionModelContribution records one AI model's contribution to a detection event.
// A single Detection may have votes from multiple models (cross-model consensus).
type DetectionModelContribution struct {
    ID            uint    `gorm:"primaryKey"`
    DetectionID   uint    `gorm:"not null;index;uniqueIndex:idx_contrib_detection_model"`
    ModelID       uint    `gorm:"not null;uniqueIndex:idx_contrib_detection_model"`
    HitCount      int     `gorm:"not null"` // Number of inference hits from this model
    MaxConfidence float64 `gorm:"not null"` // Highest confidence from this model

    // Relationships
    Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
    Model     *AIModel   `gorm:"foreignKey:ModelID"`
}
```

The existing `Detection.ModelID` field is repurposed to reference the "best" model
(highest confidence contributor).  `Detection.Confidence` stores the best confidence
across all contributing models.

### 8. Detection Save Path

In the database action handler, after creating the Detection record:

1. Save the Detection (gets auto-generated ID).
2. For each entry in `ModelContributions`, resolve the model string to an `AIModel`
   ID and create a `DetectionModelContribution` row.

This requires passing `ModelContributions` through the action pipeline.  The
`Detections` struct (processor.go line 184) or the `Note` model needs a field to
carry this data from flush to database save.

**Option A**: Add `ModelContributions map[string]ModelContribution` to the
`Detections` struct (processor-internal, no schema impact).

**Option B**: Add a runtime-only field to `detection.Result` with `gorm:"-"`.
The `DatabaseAction` already receives `detection.Result`, so no new data path
is needed.  `DetectionRepository.Save` checks for non-nil votes and persists
them alongside the Detection record.

Recommendation: **Option B** - cleanest integration with the existing action
pipeline.  `detection.Result` is already the single carrier for all detection
data through actions.  The field is runtime-only (not persisted by GORM directly)
and `DetectionRepository.Save` handles the vote records explicitly.

### 9. Migration

- GORM AutoMigrate creates the `detection_model_contributions` table.
- Existing detections have no votes (acceptable; votes are additive metadata).
- No migration of historical data needed; the feature is forward-looking.

### 10. API Considerations

The v2 detections API should include model votes when returning detection details.
This can be a follow-up, not part of the initial implementation.  The endpoint would
preload `DetectionModelContribution` relationships and include them in the response.

## Scope Boundaries

**In scope**:
- Merge pending detections across models (key change)
- ModelContributions tracking in PendingDetection
- DetectionModelContribution database entity and save path
- Updated logging with per-model breakdown
- SSE broadcast with model contributions
- Dynamic threshold learning per contributing model

**Out of scope (follow-up)**:
- API v2 endpoint changes to expose model votes
- Frontend UI changes for model contribution display
- Weighted model confidence (all models treated equally for now)
- Model-specific threshold adjustments (beyond existing dynamic thresholds)

## Gemini Review Findings

### Filter strictness dilution (important)

The `minDetections` threshold is calibrated for a single model's inference cadence
over the detection window.  With two models processing the same audio concurrently,
`item.Count` grows 2x as fast, reaching the threshold in half the physical time.
This could weaken the temporal false-positive filter.

**Options**:
- (a) Count unique temporal windows (not raw inference hits) for threshold comparison.
- (b) Accept the faster confirmation as a feature: cross-model agreement in less
  time IS stronger evidence than one model over more time.
- (c) Scale `minDetections` by the number of active models (may over-complicate).

**Decision**: Option (b) - accept faster confirmation as a feature.  Cross-model
agreement is qualitatively stronger than repeated single-model hits.  If two
independent AI engines identify the same species, the combined count should pass
the false positive filter more easily.  This is the core value proposition of
running multiple models: fewer false positives through independent corroboration.

### Naming: renamed from DetectionModelVote

"Vote" implies binary consensus.  The entity stores granular metrics (hit count,
max confidence).  Renamed to `DetectionModelContribution` throughout.

### Domain model purity (Option B refinement)

`detection.Result` is a pure domain model with no GORM tags.  Do NOT add `gorm:"-"`
to the new field.  The `DetectionRepository` already maps `Result` to
`entities.Detection`; it can extract contributions and save them separately without
polluting the domain model.

### Model ID representation

The `ModelContributions` map key is currently a string (e.g., "BirdNET_V2.4").
Consider keying by `detection.ModelInfo` or storing `ModelInfo` inside
`ModelContribution` to make AIModel resolution easier at save time.

### Extended capture idempotency

`applyExtendedCapture` may be called multiple times for the same key when two models
detect the same species concurrently.  Verify it is idempotent (doesn't
double-extend or reset the capture buffer).

### Dynamic threshold learning guard

When calling `LearnFromApprovedDetection` for each contributing model, use that
specific model's `MaxConfidence`, not the combined best.  A model that hit at 10%
confidence should not trigger threshold learning.  Add a minimum confidence guard
(e.g., only learn if the model's max confidence exceeds the base threshold).

## Additional Code Paths Affected

These sites reference `PendingDetection.ModelID` and need updating to use
`BestModelID` or `ModelContributions`:

| File | Line | Usage | Change |
|------|------|-------|--------|
| processor.go | 615 | Log `item.ModelID` in `processResults` | Use incoming `item.ModelID` (pre-pending, no change) |
| processor.go | 650 | `pendingDetectionKey(..., item.ModelID)` | Remove modelID parameter |
| processor.go | 678 | Log `item.ModelID` in new pending creation | Use incoming `item.ModelID` (log context) |
| processor.go | 685 | Set `ModelID: item.ModelID` on new entry | Set `BestModelID`, init `ModelContributions` |
| processor.go | 703 | `updateDynamicThreshold(item.ModelID, ...)` | Use incoming `item.ModelID` (no change, per-hit) |
| processor.go | 759 | `shouldFilterDetection(..., item.ModelID)` | Use incoming `item.ModelID` (pre-pending, no change) |
| processor.go | 920 | `createDetectionResult(..., item.ModelID)` | No change (file-based detection path, not pending) |
| processor.go | 1282 | Log `item.ModelID` in approved detection | Use `item.BestModelID` |
| processor.go | 1291 | `LearnFromApprovedDetection(item.ModelID, ...)` | Iterate `ModelContributions` |
| processor.go | 1468 | Log `item.ModelID` in flush | Use `item.BestModelID` |
| processor.go | 1501 | SSE snapshot `ModelID: item.ModelID` | Use `item.BestModelID` + contributions |
| pending_broadcast.go | 121 | SSE active snapshot `ModelID` | Use `BestModelID` + contributions |
| pending_broadcast.go | 187 | `buildFlushNotification` `ModelID` | Use `BestModelID` + contributions |

Note: Lines 615, 678, 703, 759 use the incoming detection's `item.ModelID` (from
the analysis result), NOT the pending detection's field.  These don't change.

## Self-Review Notes

### FlushDeadline behavior

FlushDeadline stays fixed at creation time (matching current behavior).  Models
analyze the same audio concurrently, so hits arrive within the same detection
window.  No deadline extension needed.

### Data flow for model contributions to database

The `DatabaseAction` receives `detection.Result` and `[]detection.AdditionalResult`.
Neither type currently carries model contribution data.  Two options:

- **Option A (spec recommendation)**: Add `ModelContributions` to the `Detections`
  struct (processor-internal).  The `DatabaseAction` would need a new field or the
  `Detections` struct needs to be accessible from the action.  Currently only
  `Result` and `Results` are copied into `DatabaseAction` (lines 80-85 of
  actions_types.go).
- **Option B**: Add a runtime-only `ModelVotes []ModelContribution` field to
  `detection.Result` with `gorm:"-"`.  This is wider in scope but cleanest for the
  action handler since it already receives `*detection.Result`.

After review, **Option B is better**: the `DatabaseAction.Result` field is already
the carrier for all detection data.  Adding a runtime-only field avoids creating a
parallel data path.  The `DetectionRepository.Save` method can check for non-nil
votes and save them alongside the Detection record.

### Dynamic threshold learning scope

When a merged detection is approved (e.g. BirdNET 2/5 + Perch 3/5 = 5/5), should
we call `LearnFromApprovedDetection` for ALL contributing models?  BirdNET only saw
the species 2/5 times, which wouldn't have passed individually.  But the species IS
confirmed present by consensus.  Learning for all models is correct: the detection
was real, each model's observation was valid, and lowering future thresholds for
that model+species pair is appropriate.

### Species label consistency assumption

The design assumes all models use the same species common names (same label
taxonomy).  Production logs confirm BirdNET and Perch both use Finnish names
(sinitiainen, talitiainen, etc.) for the same species.  If a future model uses
different labels, the merge would not work.  This is acceptable for now; label
normalization can be added later if needed.

### Extended capture

`applyExtendedCapture(mapKey, ...)` uses the pending detection map key.  With the
key change (removing modelID), extended capture naturally applies once per
species/source rather than per model.  No code change needed beyond the key format.

## Risk Assessment

1. **Pending detection map concurrency** - The map is already protected by
   `pendingMutex`.  Changing the key format doesn't affect locking.

2. **Audio clip selection** - When multiple models contribute, the detection with the
   highest confidence provides the audio clip data.  This is correct because higher
   confidence correlates with clearer audio.

3. **Backward compatibility** - The `Detection.ModelID` field retains its meaning
   (best model).  Existing queries work unchanged.  The votes table is additive.

4. **Flush deadline management** - FlushDeadline stays fixed at creation time (no
   change from current behavior).  Models analyze the same audio concurrently, so
   all hits naturally arrive within the same window.

5. **Single-model deployments** - When only one model is active, behavior is identical
   to today.  `ModelContributions` has one entry, `Count` equals that model's hits.

6. **Species label divergence** - If a future model uses different common names for
   the same species, detections would not merge.  Acceptable for now; all current
   models share the same label set.

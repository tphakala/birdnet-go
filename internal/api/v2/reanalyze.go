// internal/api/v2/reanalyze.go
//
// Re-runs inference on a saved detection's audio clip against one or more
// classifier models. Lets users ask "what does every classifier I have think
// about this clip?" without persisting anything — the alternate predictions
// are returned in the response and not written to the datastore.
//
// By default, the endpoint runs every currently-loaded classifier that's
// compatible with the saved clip (i.e. non-ultrasonic models, since RTSP
// audio is recorded at standard rates). Callers can override this by passing
// an explicit modelIds list.
package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/sync/errgroup"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// reanalyzeMaxDurationSec caps the input duration fed to ffmpeg per
	// reanalysis request. 60s comfortably covers BirdNET-Go's default extended
	// capture buffer plus pre/post padding; longer clips are truncated. This
	// is a hard ceiling against runaway inference cost from oversized inputs.
	reanalyzeMaxDurationSec = 60

	// reanalyzeTopN is the maximum number of species predictions returned in
	// the response. Sorting is by max confidence observed across windows and
	// across all models that were run.
	reanalyzeTopN = 10
)

// ReanalyzeRequest is the JSON body of POST /api/v2/detections/:id/reanalyze.
type ReanalyzeRequest struct {
	// ModelIDs is an optional list of models to run inference with. Each
	// entry may be either the orchestrator registry ID (e.g. "Perch_V2",
	// "BirdNET_V2.4") or the user-facing config alias used elsewhere in the
	// API (e.g. "perch_v2", "birdnet"). When empty (the common case), every
	// currently-loaded classifier compatible with the saved clip is run.
	ModelIDs []string `json:"modelIds,omitempty"`
}

// ReanalyzeModelInfo describes a model that participated in the reanalysis.
type ReanalyzeModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SampleRate  int    `json:"sampleRate"`
	WindowCount int    `json:"windowCount"`
}

// ReanalyzePrediction is one species entry in the response. ByModel maps each
// participating model's registry ID to the max confidence that model
// produced for this species across all windows of the clip. Models absent
// from the map did not predict the species in any window.
type ReanalyzePrediction struct {
	ScientificName string             `json:"scientificName"`
	CommonName     string             `json:"commonName,omitempty"`
	ByModel        map[string]float32 `json:"byModel"`
}

// MaxConfidence returns the highest confidence any model produced for this
// species. Used for sorting the top-N list.
func (p ReanalyzePrediction) MaxConfidence() float32 {
	var best float32
	for _, c := range p.ByModel {
		if c > best {
			best = c
		}
	}
	return best
}

// ReanalyzeResponse is the JSON shape returned by the reanalysis endpoint.
type ReanalyzeResponse struct {
	DetectionID     uint                  `json:"detectionId"`
	ClipDurationSec float64               `json:"clipDurationSec"`
	ModelsRun       []ReanalyzeModelInfo  `json:"modelsRun"`
	Predictions     []ReanalyzePrediction `json:"predictions"`
}

// ReanalyzeDetection handles POST /api/v2/detections/:id/reanalyze.
// @Summary Reanalyze a saved detection clip with one or more models
// @Description Decodes the saved audio clip and runs it through the requested
// @Description classifier models (or every compatible loaded model when none
// @Description is specified). Returns the top-N species predictions with
// @Description per-model max confidence. Does not modify the detection record.
// @Tags detections
// @Accept json
// @Produce json
// @Param id path int true "Detection (note) ID"
// @Param request body ReanalyzeRequest false "Model selection (optional; defaults to all compatible)"
// @Success 200 {object} ReanalyzeResponse "Top-N predictions across the chosen models"
// @Failure 400 {object} ErrorResponse "Invalid request or unknown model"
// @Failure 404 {object} ErrorResponse "Detection or audio clip not found"
// @Failure 500 {object} ErrorResponse "Decode or inference failure"
// @Failure 503 {object} ErrorResponse "Classifier orchestrator unavailable"
// @Router /detections/{id}/reanalyze [post]
func (c *Controller) ReanalyzeDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")
	if _, err := strconv.ParseUint(idStr, 10, 64); err != nil {
		return c.HandleError(ctx, err, "Detection ID must be a numeric value", http.StatusBadRequest)
	}

	// Body is optional in shape — empty body means "run all compatible
	// loaded models" — but a malformed body (e.g., invalid JSON) should
	// fail loudly so a buggy client doesn't silently get the default
	// behaviour when it meant to restrict the model list.
	req := &ReanalyzeRequest{}
	if err := ctx.Bind(req); err != nil {
		// A truly empty body returns nil from Bind on Echo; only malformed
		// JSON / wrong content-type lands here.
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}

	note, err := c.DS.Get(idStr)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	clipPath, err := c.DS.GetNoteClipPath(idStr)
	if err != nil {
		if isClipNotFoundErr(err) {
			return c.HandleError(ctx, err,
				"No audio clip available for this detection", http.StatusNotFound)
		}
		return c.HandleError(ctx, err,
			"Failed to look up clip path", http.StatusInternalServerError)
	}
	if clipPath == "" {
		return c.HandleError(ctx,
			fmt.Errorf("clip path empty"),
			"No audio clip available for this detection", http.StatusNotFound)
	}

	bn, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err,
			"Classifier not available", http.StatusServiceUnavailable)
	}

	chosen, err := selectModelsForReanalysis(bn, req.ModelIDs)
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}
	if len(chosen) == 0 {
		return c.HandleError(ctx,
			fmt.Errorf("no compatible models loaded"),
			"No compatible classifier models are loaded for this clip",
			http.StatusBadRequest)
	}

	relClipPath, err := c.normalizeAndValidatePathWithLogger(clipPath, c.apiLogger)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid clip path", http.StatusBadRequest)
	}
	absClipPath := filepath.Join(c.SFS.BaseDir(), relClipPath)

	// Group models by sample rate so we decode once per unique rate, not
	// once per model. With BirdNET v2.4 (48k) + Perch v2 (32k) loaded, this
	// is two decodes total even when running both — vs N decodes naively.
	bySR := make(map[int][]loadedModel)
	for _, m := range chosen {
		bySR[m.spec.SampleRate] = append(bySR[m.spec.SampleRate], m)
	}

	ffmpegPath := c.Settings.Realtime.Audio.FfmpegPath

	// Accumulator: scientific-name labels (or raw label strings) -> per-model
	// max confidence. The label-string form survives until aggregation so
	// duplicates across windows of the same model merge correctly.
	byLabelModel := make(map[string]map[string]float32)
	modelInfos := make([]ReanalyzeModelInfo, 0, len(chosen))
	clipDurationSec := 0.0

	for sr, models := range bySR {
		samples, err := decodeClipMonoPCM16(
			ctx.Request().Context(), ffmpegPath, absClipPath, sr, reanalyzeMaxDurationSec)
		if err != nil {
			c.logAPIRequest(ctx, logger.LogLevelError, "Failed to decode clip for reanalysis",
				logger.String("detection_id", idStr),
				logger.String("clip_path", relClipPath),
				logger.Int("sample_rate", sr),
				logger.Error(err))
			return c.HandleError(ctx, err,
				"Failed to decode audio clip", http.StatusInternalServerError)
		}
		// Record the longest-duration view of the clip across decoded rates.
		// All decodes of the same source should produce the same duration in
		// seconds, modulo ffmpeg resampler edge effects; max is the safest.
		if d := float64(len(samples)) / float64(sr); d > clipDurationSec {
			clipDurationSec = d
		}

		// Dispatch each model in this sample-rate group concurrently.
		// Distinct model IDs hit distinct orchestrator per-model mutexes, so
		// goroutines genuinely run in parallel; the orchestrator itself
		// serializes calls into the same model. errgroup gives us first-
		// error short-circuit (one model failing aborts the whole request)
		// and gctx propagation so the canceled request also cancels in-
		// flight ffmpeg/inference work.
		g, gctx := errgroup.WithContext(ctx.Request().Context())
		var accMu sync.Mutex
		for _, m := range models {
			g.Go(func() error {
				scores, windowCount, err := reanalyzeSamples(
					gctx, bn.PredictModel, m.id, m.spec, samples)
				if err != nil {
					return fmt.Errorf("inference for %s: %w", m.id, err)
				}
				accMu.Lock()
				defer accMu.Unlock()
				modelInfos = append(modelInfos, ReanalyzeModelInfo{
					ID:          m.id,
					Name:        m.name,
					SampleRate:  m.spec.SampleRate,
					WindowCount: windowCount,
				})
				for _, s := range scores {
					label := s.ScientificName
					if label == "" {
						// SplitSpeciesName fallback: the raw label couldn't
						// be parsed into scientific form. Key by CommonName
						// so we still merge identical raw labels across
						// windows.
						label = s.CommonName
					}
					if _, ok := byLabelModel[label]; !ok {
						byLabelModel[label] = make(map[string]float32)
					}
					if existing, ok := byLabelModel[label][m.id]; !ok || s.Confidence > existing {
						byLabelModel[label][m.id] = s.Confidence
					}
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			c.logAPIRequest(ctx, logger.LogLevelError, "Reanalysis inference failed",
				logger.String("detection_id", idStr),
				logger.Error(err))
			return c.HandleError(ctx, err,
				"Inference failed", http.StatusInternalServerError)
		}
	}

	// Stabilize ordering of ModelsRun by ID so the response is deterministic.
	sort.Slice(modelInfos, func(i, j int) bool { return modelInfos[i].ID < modelInfos[j].ID })

	// Build the top-N prediction list. SplitSpeciesName is reapplied so the
	// frontend can show common name primary + scientific secondary, and the
	// resolver fills in locale-specific common names for bare-scientific
	// labels (Perch v2's output shape).
	predictions := make([]ReanalyzePrediction, 0, len(byLabelModel))
	for label, perModel := range byLabelModel {
		scientific, common := classifier.SplitSpeciesName(label)
		predictions = append(predictions, ReanalyzePrediction{
			ScientificName: scientific,
			CommonName:     common,
			ByModel:        perModel,
		})
	}
	applyLocalizedCommonNamesV2(bn, predictions, c.Settings.BirdNET.Locale)

	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].MaxConfidence() > predictions[j].MaxConfidence()
	})
	if len(predictions) > reanalyzeTopN {
		predictions = predictions[:reanalyzeTopN]
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Reanalysis complete",
		logger.String("detection_id", idStr),
		logger.Int("model_count", len(modelInfos)),
		logger.Int("prediction_count", len(predictions)))

	return ctx.JSON(http.StatusOK, ReanalyzeResponse{
		DetectionID:     note.ID,
		ClipDurationSec: clipDurationSec,
		ModelsRun:       modelInfos,
		Predictions:     predictions,
	})
}

// loadedModel is an internal shorthand carrying the registry ID, display
// name, and spec of a model that's currently in the orchestrator.
type loadedModel struct {
	id   string
	name string
	spec classifier.ModelSpec
}

// selectModelsForReanalysis resolves the request's requested model list (or
// the default set when empty) into the concrete set of loaded models to run.
//
// Default policy: every loaded model whose Spec.RawSampleRate is zero, i.e.
// every standard-audio model. Bat models (RawSampleRate=256000) and other
// special-rate models are excluded because RTSP clips don't carry the
// required ultrasonic content.
//
// Explicit policy: when ModelIDs is set, each entry is resolved (config
// alias or registry ID accepted) and validated to be loaded. An unknown or
// unloaded ID surfaces as an error so the caller knows their request was
// silently rejected.
func selectModelsForReanalysis(bn *classifier.Orchestrator, requestedIDs []string) ([]loadedModel, error) {
	if len(requestedIDs) > 0 {
		// Dedupe after registry-ID resolution so a request like
		// {"modelIds":["birdnet","BirdNET_V2.4"]} doesn't schedule the same
		// model twice (both forms resolve to "BirdNET_V2.4"). Without this
		// the response carries duplicate modelsRun entries and we burn
		// inference cost re-running an identical model.
		out := make([]loadedModel, 0, len(requestedIDs))
		seen := make(map[string]struct{}, len(requestedIDs))
		for _, raw := range requestedIDs {
			resolvedID := raw
			if registryID, ok := classifier.ResolveConfigModelID(raw); ok {
				resolvedID = registryID
			}
			if _, dup := seen[resolvedID]; dup {
				continue
			}
			spec, name, ok := lookupLoadedModel(bn, resolvedID)
			if !ok {
				return nil, fmt.Errorf("model %q is not loaded; enable it in Settings -> Models first", raw)
			}
			seen[resolvedID] = struct{}{}
			out = append(out, loadedModel{id: resolvedID, name: name, spec: spec})
		}
		return out, nil
	}

	// Default: all standard-audio loaded models. Index-based loop so we
	// don't copy classifier.ModelInfo (~224 bytes) per iteration.
	var out []loadedModel
	infos := bn.ModelInfos()
	for i := range infos {
		if infos[i].Spec.RawSampleRate != 0 {
			// Skip ultrasonic-only models (bat). They expect raw audio at
			// 256kHz that our saved RTSP clips don't contain — running them
			// would produce nonsense scores.
			continue
		}
		if infos[i].Spec.SampleRate <= 0 {
			continue
		}
		out = append(out, loadedModel{id: infos[i].ID, name: infos[i].Name, spec: infos[i].Spec})
	}
	return out, nil
}

// lookupLoadedModel returns the spec and display name of the loaded model
// with the given ID, or (zero, "", false) when the model is not currently
// loaded by the orchestrator.
func lookupLoadedModel(bn *classifier.Orchestrator, modelID string) (classifier.ModelSpec, string, bool) {
	infos := bn.ModelInfos()
	for i := range infos {
		if infos[i].ID == modelID {
			return infos[i].Spec, infos[i].Name, true
		}
	}
	return classifier.ModelSpec{}, "", false
}

// predictModelFn is the subset of *classifier.Orchestrator that reanalyzeSamples
// depends on. Factoring it out lets tests pass a stub without instantiating
// a real orchestrator (which would require loading an actual model on disk).
type predictModelFn func(ctx context.Context, modelID string, sample [][]float32) ([]datastore.Results, error)

// reanalyzeSampleScore is a window-aggregated score for one species from one
// model. Internal to the multi-model handler.
type reanalyzeSampleScore struct {
	ScientificName string
	CommonName     string
	Confidence     float32
}

// reanalyzeSamples slides a clip-length window across the decoded audio at
// 50% overlap (matching the realtime pipeline) and dispatches each window to
// PredictModel. Per-species the maximum confidence observed across all
// windows is retained.
//
// Returns (scores keyed by species, total window count, error). Sorting and
// top-N truncation happen in the multi-model aggregator (this function may
// be called once per model on the same clip).
func reanalyzeSamples(
	ctx context.Context,
	predict predictModelFn,
	modelID string,
	spec classifier.ModelSpec,
	samples []float32,
) ([]reanalyzeSampleScore, int, error) {
	if len(samples) == 0 {
		return nil, 0, errors.Newf("no audio samples to analyze").
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Build()
	}

	// Compute clip length in samples via nanosecond math rather than
	// rounding-prone Seconds() * SampleRate. The current model registry only
	// ships integer-second windows (3s BirdNET, 5s Perch) so this is purely
	// future-proofing for any fractional-window model someone might add,
	// but the math costs nothing and avoids a silent off-by-N-samples bug
	// in that case.
	clipLen := int(spec.ClipLength.Nanoseconds() * int64(spec.SampleRate) / int64(time.Second))
	if clipLen <= 0 {
		return nil, 0, errors.Newf("model %q has invalid clip length", modelID).
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Context("model_id", modelID).
			Context("clip_length_sec", spec.ClipLength.Seconds()).
			Context("sample_rate", spec.SampleRate).
			Build()
	}

	if len(samples) < clipLen {
		padded := make([]float32, clipLen)
		copy(padded, samples)
		samples = padded
	}

	stride := clipLen / 2
	if stride <= 0 {
		stride = clipLen
	}

	best := make(map[string]float32)
	windowCount := 0
	for offset := 0; offset+clipLen <= len(samples); offset += stride {
		window := samples[offset : offset+clipLen]
		results, err := predict(ctx, modelID, [][]float32{window})
		if err != nil {
			return nil, windowCount, err
		}
		windowCount++
		for _, r := range results {
			if existing, ok := best[r.Species]; !ok || r.Confidence > existing {
				best[r.Species] = r.Confidence
			}
		}
	}

	scores := make([]reanalyzeSampleScore, 0, len(best))
	for label, conf := range best {
		scientific, common := classifier.SplitSpeciesName(label)
		scores = append(scores, reanalyzeSampleScore{
			ScientificName: scientific,
			CommonName:     common,
			Confidence:     conf,
		})
	}
	// Sort descending by confidence so single-model callers see a usable
	// ranked list directly; the multi-model aggregator re-sorts the merged
	// output later, but the per-model ordering aids debugging and tests.
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Confidence > scores[j].Confidence
	})
	return scores, windowCount, nil
}

// applyLocalizedCommonNamesV2 fills in CommonName on each prediction via the
// orchestrator's resolver chain in the user's configured locale. Predictions
// that already carry a model-supplied common name keep it; only the ones
// derived from bare scientific labels (Perch v2's shape) get resolved.
func applyLocalizedCommonNamesV2(bn *classifier.Orchestrator, preds []ReanalyzePrediction, locale string) {
	if bn == nil {
		return
	}
	for i := range preds {
		if preds[i].CommonName != "" {
			continue
		}
		if preds[i].ScientificName == "" {
			continue
		}
		if name := bn.ResolveName(preds[i].ScientificName, locale); name != "" {
			preds[i].CommonName = name
		}
	}
}

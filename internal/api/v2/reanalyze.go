// internal/api/v2/reanalyze.go
//
// Re-runs inference on a saved detection's audio clip against a chosen
// classifier model. Lets users ask "what would Perch v2 have called this
// BirdNET detection?" without persisting anything — the alternate prediction
// is returned in the response and not written to the datastore.
package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/labstack/echo/v4"

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
	// the response. Sorting is by max confidence observed across windows.
	reanalyzeTopN = 10
)

// ReanalyzeRequest is the JSON body of POST /api/v2/detections/:id/reanalyze.
type ReanalyzeRequest struct {
	// ModelID accepts either the orchestrator registry ID (e.g.
	// "Perch_V2", "BirdNET_V2.4") or the user-facing config alias used
	// elsewhere in the API (e.g. "perch_v2", "birdnet"). The handler
	// resolves the alias internally via classifier.ResolveConfigModelID,
	// so frontend callers can pass whichever they already have on hand.
	// Required.
	ModelID string `json:"modelId"`
}

// ReanalyzePrediction is one entry in the top-N predictions returned by
// reanalysis. Confidence is the maximum the model produced for this species
// across all windows of the clip.
type ReanalyzePrediction struct {
	Species    string  `json:"species"`
	Confidence float32 `json:"confidence"`
}

// ReanalyzeResponse is the JSON shape returned by the reanalysis endpoint.
type ReanalyzeResponse struct {
	DetectionID     uint                  `json:"detectionId"`
	ModelID         string                `json:"modelId"`
	ModelName       string                `json:"modelName"`
	SampleRate      int                   `json:"sampleRate"`
	ClipDurationSec float64               `json:"clipDurationSec"`
	WindowCount     int                   `json:"windowCount"`
	Predictions     []ReanalyzePrediction `json:"predictions"`
}

// ReanalyzeDetection handles POST /api/v2/detections/:id/reanalyze.
// @Summary Reanalyze a saved detection clip with a chosen model
// @Description Decodes the saved audio clip for the given detection and runs
// @Description it through the specified classifier model, returning the top-N
// @Description species predictions. The original detection record is not
// @Description modified — this is a read-only "second-opinion" inference.
// @Tags detections
// @Accept json
// @Produce json
// @Param id path int true "Detection (note) ID"
// @Param request body ReanalyzeRequest true "Model selection"
// @Success 200 {object} ReanalyzeResponse "Top-N predictions for the chosen model"
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

	req := &ReanalyzeRequest{}
	if err := ctx.Bind(req); err != nil {
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}
	if req.ModelID == "" {
		return c.HandleError(ctx,
			fmt.Errorf("modelId is required"),
			"modelId is required", http.StatusBadRequest)
	}

	// Confirm the detection exists before doing any heavy work.
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

	// Resolve the model spec from the orchestrator's currently-loaded models.
	// Reanalysis is restricted to loaded models so we never trigger an on-demand
	// model load from a user-facing handler (load can take seconds and holds a
	// write lock; not appropriate for an interactive request). Accept either
	// the registry ID directly or the user-facing config alias.
	resolvedID := req.ModelID
	if registryID, ok := classifier.ResolveConfigModelID(req.ModelID); ok {
		resolvedID = registryID
	}
	spec, modelName, ok := lookupLoadedModel(bn, resolvedID)
	if !ok {
		return c.HandleError(ctx,
			fmt.Errorf("model %q is not loaded", req.ModelID),
			"Specified model is not loaded; enable it in Settings -> Models first",
			http.StatusBadRequest)
	}

	// Resolve the clip path inside the SecureFS sandbox. normalizeAndValidate
	// strips any clips-prefix the DB row may include and rejects traversal
	// attempts; joining onto BaseDir() then yields an absolute path that
	// ffmpeg can open directly.
	relClipPath, err := c.normalizeAndValidatePathWithLogger(clipPath, c.apiLogger)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid clip path", http.StatusBadRequest)
	}
	absClipPath := filepath.Join(c.SFS.BaseDir(), relClipPath)

	// Decode the entire clip up front. Reading once and analyzing many windows
	// is cheaper than spawning ffmpeg per window, and keeps wall-clock latency
	// at one ffmpeg invocation regardless of clip length.
	samples, err := decodeClipMonoPCM16(
		ctx.Request().Context(),
		c.Settings.Realtime.Audio.FfmpegPath,
		absClipPath,
		spec.SampleRate,
		reanalyzeMaxDurationSec,
	)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to decode clip for reanalysis",
			logger.String("detection_id", idStr),
			logger.String("clip_path", relClipPath),
			logger.String("model_id", req.ModelID),
			logger.Error(err))
		return c.HandleError(ctx, err,
			"Failed to decode audio clip", http.StatusInternalServerError)
	}

	predictions, windowCount, err := reanalyzeSamples(
		ctx.Request().Context(), bn.PredictModel, resolvedID, spec, samples)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Reanalysis inference failed",
			logger.String("detection_id", idStr),
			logger.String("model_id", req.ModelID),
			logger.Error(err))
		return c.HandleError(ctx, err,
			"Inference failed", http.StatusInternalServerError)
	}

	clipDurationSec := 0.0
	if spec.SampleRate > 0 {
		clipDurationSec = float64(len(samples)) / float64(spec.SampleRate)
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Reanalysis complete",
		logger.String("detection_id", idStr),
		logger.String("model_id", req.ModelID),
		logger.Int("window_count", windowCount),
		logger.Int("prediction_count", len(predictions)))

	return ctx.JSON(http.StatusOK, ReanalyzeResponse{
		DetectionID:     note.ID,
		ModelID:         resolvedID,
		ModelName:       modelName,
		SampleRate:      spec.SampleRate,
		ClipDurationSec: clipDurationSec,
		WindowCount:     windowCount,
		Predictions:     predictions,
	})
}

// lookupLoadedModel returns the spec and display name of the loaded model
// with the given ID, or (zero, "", false) when the model is not currently
// loaded by the orchestrator.
func lookupLoadedModel(bn *classifier.Orchestrator, modelID string) (classifier.ModelSpec, string, bool) {
	for _, info := range bn.ModelInfos() {
		if info.ID == modelID {
			return info.Spec, info.Name, true
		}
	}
	return classifier.ModelSpec{}, "", false
}

// predictModelFn is the subset of *classifier.Orchestrator that reanalyzeSamples
// depends on. Factoring it out lets tests pass a stub without instantiating
// a real orchestrator (which would require loading an actual model on disk).
type predictModelFn func(ctx context.Context, modelID string, sample [][]float32) ([]datastore.Results, error)

// reanalyzeSamples slides a clip-length window across the decoded audio at
// 50% overlap (matching the realtime pipeline) and dispatches each window to
// PredictModel. Per-species the maximum confidence observed across all
// windows is retained, then the result is sorted descending and truncated to
// reanalyzeTopN.
//
// Returns (top-N predictions, total window count, error).
func reanalyzeSamples(
	ctx context.Context,
	predict predictModelFn,
	modelID string,
	spec classifier.ModelSpec,
	samples []float32,
) ([]ReanalyzePrediction, int, error) {
	if len(samples) == 0 {
		return nil, 0, errors.Newf("no audio samples to analyze").
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Build()
	}

	clipLen := int(spec.ClipLength.Seconds()) * spec.SampleRate
	if clipLen <= 0 {
		return nil, 0, errors.Newf("model %q has invalid clip length", modelID).
			Component("api/v2/reanalyze").
			Category(errors.CategoryValidation).
			Context("model_id", modelID).
			Context("clip_length_sec", spec.ClipLength.Seconds()).
			Context("sample_rate", spec.SampleRate).
			Build()
	}

	// Pad short clips up to one full window so we always have at least one
	// inference call. Real saved clips are typically longer than the model's
	// window (~3s for BirdNET, ~5s for Perch), but extended-capture configs
	// can in principle produce shorter ones.
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

	predictions := make([]ReanalyzePrediction, 0, len(best))
	for species, conf := range best {
		predictions = append(predictions, ReanalyzePrediction{
			Species:    species,
			Confidence: conf,
		})
	}
	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].Confidence > predictions[j].Confidence
	})
	if len(predictions) > reanalyzeTopN {
		predictions = predictions[:reanalyzeTopN]
	}
	return predictions, windowCount, nil
}

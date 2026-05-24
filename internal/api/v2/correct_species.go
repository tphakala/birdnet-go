// internal/api/v2/correct_species.go
//
// Operator-driven correction of a detection's species. After running the
// reanalyze endpoint and seeing what other models think, the user can pick
// "this is the right one" — this endpoint updates the detection record in
// place to reflect that choice, and marks it as verified='correct' in the
// same step.
//
// Implementation note: all persistence goes through c.Repo.CorrectSpecies
// (datastore.DetectionRepository). The legacy v1 path lands in the notes
// table; the v2only adapter routes through v2 repositories; any future
// dual-write composition mirrors the write across both. Keeping the
// schema-specific SQL out of the handler is the whole point — we don't
// want this endpoint to drift when upstream finishes the v1→v2 cutover.
package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	bnerrors "github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// CorrectSpeciesRequest is the JSON body of
// POST /api/v2/detections/:id/correct-species.
type CorrectSpeciesRequest struct {
	// ScientificName is the binomial Latin name the user is asserting (e.g.
	// "Ficedula hypoleuca"). The chosen ModelID must be loaded by the
	// running server, which is the gate against arbitrary species names —
	// the UI only offers species the model actually predicted.
	ScientificName string `json:"scientificName"`

	// ModelID is the orchestrator registry ID (e.g. "BirdNET_V2.4",
	// "Perch_V2") or the user-facing config alias (e.g. "birdnet"). Used
	// to validate that the correction comes from a known model and to
	// route v2 writes to the matching ai_models row. Required.
	ModelID string `json:"modelId"`

	// Confidence is the confidence value to record on the corrected
	// detection. Typically the max confidence the chosen model produced
	// for this species in the reanalysis pass. Range [0, 1].
	Confidence float64 `json:"confidence"`
}

// CorrectSpeciesResponse describes the corrected detection.
type CorrectSpeciesResponse struct {
	DetectionID    uint    `json:"detectionId"`
	ScientificName string  `json:"scientificName"`
	CommonName     string  `json:"commonName"`
	ModelID        string  `json:"modelId"`
	ModelName      string  `json:"modelName"`
	Confidence     float64 `json:"confidence"`
	Verified       string  `json:"verified"`
}

// CorrectDetectionSpecies handles POST /api/v2/detections/:id/correct-species.
// @Summary Correct a detection's species and mark it verified
// @Description Replaces the detection's species/confidence with the chosen
// @Description prediction (typically from a reanalyze response), and records
// @Description a 'correct' review in one operation. The detection must be
// @Description unlocked.
// @Tags detections
// @Accept json
// @Produce json
// @Param id path int true "Detection ID"
// @Param request body CorrectSpeciesRequest true "Correction payload"
// @Success 200 {object} CorrectSpeciesResponse "Updated detection state"
// @Failure 400 {object} ErrorResponse "Invalid request or unknown model"
// @Failure 404 {object} ErrorResponse "Detection not found"
// @Failure 409 {object} ErrorResponse "Detection is locked"
// @Failure 500 {object} ErrorResponse "Database failure"
// @Router /detections/{id}/correct-species [post]
func (c *Controller) CorrectDetectionSpecies(ctx echo.Context) error {
	idStr := ctx.Param("id")
	if _, err := strconv.ParseUint(idStr, 10, 64); err != nil {
		return c.HandleError(ctx, err,
			"Detection ID must be a numeric value", http.StatusBadRequest)
	}

	req := &CorrectSpeciesRequest{}
	if err := ctx.Bind(req); err != nil {
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}
	if req.ScientificName == "" {
		return c.HandleError(ctx,
			fmt.Errorf("scientificName is required"),
			"scientificName is required", http.StatusBadRequest)
	}
	if req.ModelID == "" {
		return c.HandleError(ctx,
			fmt.Errorf("modelId is required"),
			"modelId is required", http.StatusBadRequest)
	}
	if req.Confidence < 0 || req.Confidence > 1 {
		return c.HandleError(ctx,
			fmt.Errorf("confidence %.4f out of range", req.Confidence),
			"confidence must be in [0, 1]", http.StatusBadRequest)
	}

	// Capture current state for the structured log diff at the end. The
	// repository performs its own atomic lock check, so c.DS.Get here is
	// pure read-side context — the canonical lock guard runs inside the
	// CorrectSpecies write.
	//
	// Discriminate between "detection does not exist" and "fetch failed for
	// some other reason" (transient DB error, schema issue, etc.). The
	// legacy datastore.Get returns a CategoryNotFound enhanced error; the
	// v2only path returns repository.ErrDetectionNotFound. Anything else
	// is an infrastructure problem and deserves a 500, not a misleading
	// 404 that tells the operator the detection is gone.
	existing, err := c.DS.Get(idStr)
	if err != nil {
		if errors.Is(err, repository.ErrDetectionNotFound) || bnerrors.IsNotFound(err) {
			return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to fetch detection", http.StatusInternalServerError)
	}
	if existing.Locked {
		return c.HandleError(ctx,
			fmt.Errorf("detection is locked"),
			"Detection is locked and cannot be corrected; unlock it first",
			http.StatusConflict)
	}

	// Resolve the registry ID through the alias chain, confirm the model
	// is loaded, and capture the (name, version, variant) tuple v2-aware
	// implementations need to find the matching v2_ai_models row.
	bn, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err,
			"Classifier not available", http.StatusServiceUnavailable)
	}
	resolvedID := req.ModelID
	if registryID, ok := classifier.ResolveConfigModelID(req.ModelID); ok {
		resolvedID = registryID
	}
	// Index-based loop so we don't copy classifier.ModelInfo (~224 bytes)
	// every iteration just to read .ID. The chosen entry is copied once
	// when we record loadedInfo for the response payload.
	var (
		loadedInfo       classifier.ModelInfo
		modelDisplayName string
		modelFound       bool
	)
	infos := bn.ModelInfos()
	for i := range infos {
		if infos[i].ID == resolvedID {
			loadedInfo = infos[i]
			modelDisplayName = infos[i].Name
			modelFound = true
			break
		}
	}
	if !modelFound || loadedInfo.DetectionName == "" || loadedInfo.DetectionVersion == "" {
		return c.HandleError(ctx,
			fmt.Errorf("model %q is not loaded", req.ModelID),
			"Specified model is not loaded; cannot apply correction",
			http.StatusBadRequest)
	}
	// Reject ultrasonic-only models (e.g. the Bat classifier). They expect
	// raw audio at rates our saved RTSP clips don't contain, so a
	// correction attributed to one is meaningless — the reanalyze endpoint
	// filters them out for the same reason (RawSampleRate != 0).
	if loadedInfo.Spec.RawSampleRate != 0 {
		return c.HandleError(ctx,
			fmt.Errorf("model %q is ultrasonic-only", req.ModelID),
			"Specified model is not applicable to this detection's audio; cannot apply correction",
			http.StatusBadRequest)
	}

	// Resolve the locale-appropriate common name. The resolver chain on
	// the orchestrator covers BirdNET's labels and the v3 geomodel
	// taxonomy companion (PR #3042 upstream); it falls back to the
	// scientific name if the species is outside both vocabularies, which
	// keeps the UI readable even for an exotic correction.
	commonName := bn.ResolveName(req.ScientificName, c.Settings.BirdNET.Locale)

	if err := c.Repo.CorrectSpecies(ctx.Request().Context(), idStr, &datastore.CorrectSpeciesParams{
		ScientificName: req.ScientificName,
		CommonName:     commonName,
		Confidence:     req.Confidence,
		Model:          loadedInfo.ToDetectionModelInfo(),
	}); err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Species correction failed",
			logger.String("detection_id", idStr),
			logger.String("model_id", req.ModelID),
			logger.String("scientific_name", req.ScientificName),
			logger.Error(err))

		// Map known sentinels to precise statuses. Anything else stays
		// 500 with a generic client message so DB/schema internals don't
		// leak through the wrap; the real error is in the structured
		// log above.
		status := http.StatusInternalServerError
		clientMsg := "Internal error while correcting species"
		if errors.Is(err, datastore.ErrDetectionLocked) {
			status = http.StatusConflict
			clientMsg = "Failed to correct species: detection is locked"
		}
		return c.HandleError(ctx, err, clientMsg, status)
	}

	// Invalidate the detection-list cache so dashboards/species pages
	// reflect the new label immediately. Without this, the 5-minute
	// species-detection cache continues serving the pre-correction
	// species name (and stale confidence/verified state) for any list
	// query that included this detection — the operator sees the
	// correction stick on the detail page but the parent list still
	// shows the old label for up to 5 minutes. Same pattern Delete,
	// Review, and Lock handlers use.
	c.invalidateDetectionCache()

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Species correction applied",
		logger.String("detection_id", idStr),
		logger.String("model_id", resolvedID),
		logger.String("from_species", existing.ScientificName),
		logger.String("to_species", req.ScientificName),
		logger.Float64("from_confidence", existing.Confidence),
		logger.Float64("to_confidence", req.Confidence))

	return ctx.JSON(http.StatusOK, CorrectSpeciesResponse{
		DetectionID:    existing.ID,
		ScientificName: req.ScientificName,
		CommonName:     commonName,
		ModelID:        resolvedID,
		ModelName:      modelDisplayName,
		Confidence:     req.Confidence,
		Verified:       "correct",
	})
}

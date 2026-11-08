// internal/api/v2/detections/predictions.go
package detections

import (
	"cmp"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	detectionPkg "github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/vocalization"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// Detection predictions messages and log text (file-local).
const (
	predictionsV2RequiredMessage = "Detection predictions require the enhanced (v2) database"
	invalidDetectionIDMessage    = "Invalid detection ID"
	predictionsLoadFailedMessage = "Failed to load detection predictions"
	predictionsRequestEndedLog   = "detection predictions request ended before the read finished"
	predictionsV2RequiredLog     = "detection predictions requested without the enhanced database"

	// detectionIDBits is the width an id must fit in. Detection ids are Go uint
	// values, 32 bits on a 32-bit build, so a wider id would truncate to another
	// detection there.
	detectionIDBits = 32
)

// DetectionPredictionsResponse is the response of GET /detections/:id/predictions.
// ModelPredictions holds one set per model that has predictions to show (today at
// most one, the detecting model's) and is empty when nothing remains after
// filtering.
type DetectionPredictionsResponse struct {
	DetectionID      uint                         `json:"detectionId"`
	ModelPredictions []ModelPredictionSetResponse `json:"modelPredictions"`
}

// ModelPredictionSetResponse is the alternate species one model scored for a
// detection. Predictions is never empty in a response.
type ModelPredictionSetResponse struct {
	ModelName    string               `json:"modelName"`
	ModelVersion string               `json:"modelVersion"`
	ModelVariant string               `json:"modelVariant"`
	ModelType    string               `json:"modelType"`
	Predictions  []PredictionResponse `json:"predictions"`
}

// PredictionResponse is one alternate species with the confidence the model gave it.
type PredictionResponse struct {
	ScientificName string  `json:"scientificName"`
	CommonName     string  `json:"commonName"`
	SpeciesCode    string  `json:"speciesCode"`
	Confidence     float64 `json:"confidence"`
}

// GetDetectionPredictions handles GET /api/v2/detections/:id/predictions. It returns
// the alternate species the detecting model scored for one detection, without the
// detected species, human sound classes and a dog class that only the filter
// preserved. It answers 409 unless the enhanced (v2) database is in use, and 400
// for an id that is not a positive integer that fits in 32 bits.
func (c *Handler) GetDetectionPredictions(ctx echo.Context) error {
	if c.V2Manager == nil || !datastoreV2.IsEnhancedDatabase() {
		// Answered without an error log line: the route is public and a legacy
		// install would otherwise fill the health error buffer with normal use.
		resp := c.NewErrorResponse(nil, predictionsV2RequiredMessage, http.StatusConflict)
		c.LogDebugIfEnabled(predictionsV2RequiredLog,
			logger.String("correlation_id", resp.CorrelationID),
			logger.String("path", apicore.RoutePattern(ctx)),
		)
		return ctx.JSON(http.StatusConflict, resp)
	}

	n, err := strconv.ParseUint(ctx.Param("id"), 10, detectionIDBits)
	if err != nil || n == 0 {
		return c.HandleError(ctx, err, invalidDetectionIDMessage, http.StatusBadRequest)
	}
	// Read the validated number, so the id that was checked is the id that is read.
	id := strconv.FormatUint(n, 10)

	reqCtx := ctx.Request().Context()
	r, err := c.Repo.Get(reqCtx, id)
	if err != nil {
		if handled, respErr := c.respondIfRequestEnded(ctx, err, predictionsRequestEndedLog); handled {
			return respErr
		}
		if errors.Is(err, repository.ErrDetectionNotFound) {
			return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
		}
		return c.HandleError(ctx, err, predictionsLoadFailedMessage, http.StatusInternalServerError)
	}

	rows, err := c.Repo.GetAdditionalResults(reqCtx, id)
	if err != nil {
		if handled, respErr := c.respondIfRequestEnded(ctx, err, predictionsRequestEndedLog); handled {
			return respErr
		}
		return c.HandleError(ctx, err, predictionsLoadFailedMessage, http.StatusInternalServerError)
	}

	resp := DetectionPredictionsResponse{
		DetectionID:      uint(n),
		ModelPredictions: make([]ModelPredictionSetResponse, 0, 1),
	}
	if preds := buildPredictions(r.Species.ScientificName, rows); len(preds) > 0 {
		modelType := r.Model.ModelType
		if modelType == "" {
			modelType = defaultModelType
		}
		resp.ModelPredictions = append(resp.ModelPredictions, ModelPredictionSetResponse{
			ModelName:    r.Model.Name,
			ModelVersion: r.Model.Version,
			ModelVariant: r.Model.Variant,
			ModelType:    modelType,
			Predictions:  preds,
		})
	}
	return ctx.JSON(http.StatusOK, resp)
}

// predictionCandidate is one stored row after trimming, ready to rank.
type predictionCandidate struct {
	name       string // trimmed stored scientific name
	key        string // lowercased canonical name, the identity of a species
	commonName string
	code       string
	confidence float64
	dog        bool
}

// comparePredictionCandidates orders candidates best first: higher confidence, then
// species key, then trimmed stored name, so the first row of a species is its best.
func comparePredictionCandidates(a, b *predictionCandidate) int {
	if c := cmp.Compare(b.confidence, a.confidence); c != 0 {
		return c
	}
	if c := cmp.Compare(a.key, b.key); c != 0 {
		return c
	}
	return cmp.Compare(a.name, b.name)
}

// buildPredictions turns the stored alternates of a detection into the rows to show.
// It drops the detected species (compared by canonical name, ignoring case), human
// sound classes, blank names and non-finite confidences, merges rows of one species
// (alias and case variants) into the best-ranked one, drops a dog class that scores
// below every non-dog row (the classifier appends one past its top results to keep
// it visible, so a low dog score is usually that artifact; a genuine low-scoring dog
// result is dropped with it), and sorts by confidence descending then species key.
// The result is the same for any input order.
func buildPredictions(primarySci string, rows []detectionPkg.AdditionalResult) []PredictionResponse {
	primaryKey := speciesindex.CanonicalKey(primarySci)

	candidates := make([]*predictionCandidate, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Species.ScientificName)
		if name == "" || math.IsNaN(row.Confidence) || math.IsInf(row.Confidence, 0) {
			continue
		}
		key := speciesindex.CanonicalKey(name)
		if key == primaryKey || vocalization.IsHumanSound(name) {
			continue
		}
		candidates = append(candidates, &predictionCandidate{
			name: name, key: key, commonName: row.Species.CommonName, code: row.Species.Code,
			confidence: row.Confidence, dog: vocalization.IsDog(name),
		})
	}
	slices.SortFunc(candidates, comparePredictionCandidates)

	// Candidates are ranked, so the first of a key is the winner of its species and
	// the first non-empty code among its rows is the one kept.
	entries := make([]*predictionCandidate, 0, len(candidates))
	indexByKey := make(map[string]int, len(candidates))
	for _, cand := range candidates {
		i, seen := indexByKey[cand.key]
		if !seen {
			indexByKey[cand.key] = len(entries)
			entries = append(entries, cand)
			continue
		}
		if entries[i].code == "" {
			entries[i].code = cand.code
		}
	}

	// Dog rule: compare against the non-dog entries only, so the outcome does not
	// depend on how many dog rows there are or on their order.
	minOther, haveOther := 0.0, false
	for _, e := range entries {
		if !e.dog && (!haveOther || e.confidence < minOther) {
			minOther, haveOther = e.confidence, true
		}
	}
	if haveOther {
		entries = slices.DeleteFunc(entries, func(e *predictionCandidate) bool {
			return e.dog && e.confidence < minOther
		})
	}

	out := make([]PredictionResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, PredictionResponse{
			ScientificName: openfauna.CanonicalName(e.name),
			CommonName:     e.commonName,
			SpeciesCode:    e.code,
			Confidence:     e.confidence,
		})
	}
	return out
}

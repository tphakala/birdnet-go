// internal/api/v2/detections/predictions.go
package detections

import (
	"cmp"
	"maps"
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
)

// Detection predictions messages and log text (file-local).
const (
	predictionsV2RequiredMessage = "Detection predictions require the enhanced (v2) database"
	invalidDetectionIDMessage    = "Invalid detection ID"
	predictionsLoadFailedMessage = "Failed to load detection predictions"
	predictionsRequestEndedLog   = "detection predictions request ended before the read finished"
	predictionsV2RequiredLog     = "detection predictions requested without the enhanced database"
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
// preserved. It answers 409 unless the enhanced (v2) database is in use.
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

	id := ctx.Param("id")
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return c.HandleError(ctx, err, invalidDetectionIDMessage, http.StatusBadRequest)
	}

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
}

// betterCandidate orders candidates best first: higher confidence, then smaller
// trimmed stored name.
func betterCandidate(a, b predictionCandidate) int {
	if c := cmp.Compare(b.confidence, a.confidence); c != 0 {
		return c
	}
	return cmp.Compare(a.name, b.name)
}

// buildPredictions turns the stored alternates of a detection into the rows to show.
// It drops the detected species (compared by canonical name, ignoring case), human
// sound classes, blank names and non-finite confidences, merges rows of one species
// (alias and case variants) into the best-ranked one, drops a dog class that scores
// below every non-dog row (the classifier appends one past its top results to keep
// it visible, so a low dog score says nothing about the audio), and sorts by
// confidence descending then species key. The result is the same for any input
// order, and nil when nothing remains.
func buildPredictions(primarySci string, rows []detectionPkg.AdditionalResult) []PredictionResponse {
	key := func(name string) string { return strings.ToLower(openfauna.CanonicalName(name)) }
	primaryKey := key(primarySci)

	candidates := make([]predictionCandidate, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Species.ScientificName)
		if name == "" || math.IsNaN(row.Confidence) || math.IsInf(row.Confidence, 0) {
			continue
		}
		k := key(name)
		if k == primaryKey || vocalization.IsHumanSound(name) {
			continue
		}
		candidates = append(candidates, predictionCandidate{
			name: name, key: k, commonName: row.Species.CommonName, code: row.Species.Code, confidence: row.Confidence,
		})
	}
	slices.SortFunc(candidates, betterCandidate)

	// Candidates are ranked, so the first of a key wins and the first with a code
	// supplies the code when the winner has none.
	type merged struct {
		best predictionCandidate
		code string
	}
	groups := make(map[string]*merged, len(candidates))
	for _, cand := range candidates {
		g, ok := groups[cand.key]
		if !ok {
			g = &merged{best: cand}
			groups[cand.key] = g
		}
		if g.code == "" {
			g.code = cand.code
		}
	}

	entries := make([]predictionCandidate, 0, len(groups))
	for _, g := range slices.Collect(maps.Values(groups)) {
		e := g.best
		e.code = g.code
		entries = append(entries, e)
	}

	// Dog rule: compare against the non-dog entries only, so the outcome does not
	// depend on how many dog rows there are or on their order.
	minOther, haveOther := 0.0, false
	for _, e := range entries {
		if vocalization.IsDog(e.name) {
			continue
		}
		if !haveOther || e.confidence < minOther {
			minOther, haveOther = e.confidence, true
		}
	}
	if haveOther {
		entries = slices.DeleteFunc(entries, func(e predictionCandidate) bool {
			return vocalization.IsDog(e.name) && e.confidence < minOther
		})
	}

	slices.SortFunc(entries, func(a, b predictionCandidate) int {
		if c := cmp.Compare(b.confidence, a.confidence); c != 0 {
			return c
		}
		return cmp.Compare(a.key, b.key)
	})

	if len(entries) == 0 {
		return nil
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

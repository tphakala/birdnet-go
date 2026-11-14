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
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	detectionPkg "github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/vocalization"
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
// Original marks the model's own pick of a detection whose species a reviewer
// changed; its confidence is the detection's.
type PredictionResponse struct {
	ScientificName string  `json:"scientificName"`
	CommonName     string  `json:"commonName"`
	SpeciesCode    string  `json:"speciesCode"`
	Confidence     float64 `json:"confidence"`
	Original       bool    `json:"original,omitempty"`
}

// GetDetectionPredictions handles GET /api/v2/detections/:id/predictions. It returns
// the alternate species the detecting model scored for one detection, without the
// detected species, human sound classes and a dog class that only the filter
// preserved. When a reviewer changed the species, the model's own pick is listed
// too, marked original, whatever those rules say. It answers 409 unless the enhanced
// (v2) database is in use, and 400 for an id that is not a positive integer that fits
// in 32 bits.
func (c *Handler) GetDetectionPredictions(ctx echo.Context) error {
	if c.V2Manager == nil || !datastoreV2.IsEnhancedDatabase() {
		return c.respondEnhancedDatabaseRequired(ctx, predictionsV2RequiredMessage, predictionsV2RequiredLog)
	}

	n, err := parseDetectionID(ctx)
	if err != nil {
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
	if preds := buildPredictions(r, rows); len(preds) > 0 {
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
//
// When a reviewer changed the species, r.OriginalSpecies is the model's own pick and
// it is listed as an extra entry marked original, with the detection's confidence
// (see originalPickCandidate for when there is none). The entry is exempt from the
// human and dog filters and does not take part in the dog comparison, so it never
// changes how the other rows are handled. Stored rows of its species only fill its
// missing code and its unresolved common name (empty, or the scientific name the
// datastore falls back to) when the row's own common name is resolved. A stand-in
// common name that no row replaces follows the entry's canonical scientific name.
//
// The result is the same for any input order.
func buildPredictions(r *detectionPkg.Result, rows []detectionPkg.AdditionalResult) []PredictionResponse {
	primaryKey := speciesindex.CanonicalKey(r.Species.ScientificName)
	original := originalPickCandidate(r, primaryKey)

	candidates := make([]*predictionCandidate, 0, len(rows))
	var originalRows []*predictionCandidate
	for _, row := range rows {
		name := strings.TrimSpace(row.Species.ScientificName)
		if name == "" || !isFiniteConfidence(row.Confidence) {
			continue
		}
		key := speciesindex.CanonicalKey(name)
		if key == primaryKey {
			continue
		}
		cand := &predictionCandidate{
			name: name, key: key, commonName: row.Species.CommonName, code: row.Species.Code,
			confidence: row.Confidence, dog: vocalization.IsDog(name),
		}
		// Checked before the human rule: the original is listed whatever its class.
		if original != nil && key == original.key {
			originalRows = append(originalRows, cand)
			continue
		}
		if vocalization.IsHumanSound(name) {
			continue
		}
		candidates = append(candidates, cand)
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

	if original != nil {
		original.fillFrom(originalRows)
		entries = append(entries, original)
		slices.SortFunc(entries, comparePredictionCandidates)
	}

	out := make([]PredictionResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, PredictionResponse{
			ScientificName: openfauna.CanonicalName(e.name),
			CommonName:     e.commonName,
			SpeciesCode:    e.code,
			Confidence:     e.confidence,
			Original:       e == original,
		})
	}
	return out
}

// originalPickCandidate returns the entry for the model's own pick of a detection
// whose species a reviewer changed, or nil when there is none to show: the species
// was never changed, the original has no species part in its name, it is the current
// species (primaryKey), or the detection's confidence is not finite.
func originalPickCandidate(r *detectionPkg.Result, primaryKey string) *predictionCandidate {
	name := strings.TrimSpace(r.OriginalSpecies.ScientificName)
	key := speciesindex.CanonicalKey(name)
	if name == "" || key == "" || key == primaryKey || !isFiniteConfidence(r.Confidence) {
		return nil
	}
	return &predictionCandidate{
		name: name, key: key, commonName: r.OriginalSpecies.CommonName, code: r.OriginalSpecies.Code,
		confidence: r.Confidence,
	}
}

// commonNameUnresolved reports whether a common name is missing or is only the
// scientific name the datastore falls back to for a species it cannot name.
func commonNameUnresolved(commonName, scientificName string) bool {
	commonName = strings.TrimSpace(commonName)
	return commonName == "" || strings.EqualFold(commonName, strings.TrimSpace(scientificName))
}

// isFiniteConfidence reports whether a confidence is a usable number: neither NaN
// nor infinite, which would also make the JSON encoding fail.
func isFiniteConfidence(confidence float64) bool {
	return !math.IsNaN(confidence) && !math.IsInf(confidence, 0)
}

// parseDetectionID reads the :id parameter of a detection route. It accepts only a
// positive integer that fits in detectionIDBits bits; any other value is an error.
func parseDetectionID(ctx echo.Context) (uint64, error) {
	n, err := strconv.ParseUint(ctx.Param("id"), 10, detectionIDBits)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errors.NewStd("detection id must be positive")
	}
	return n, nil
}

// fillFrom completes the original entry from stored rows of its species, ranked so
// the result does not depend on input order: the first resolved common name and the
// first non-empty code win. A stand-in common name that no row replaces follows the
// entry's canonical scientific name, under which it is shown.
func (c *predictionCandidate) fillFrom(rows []*predictionCandidate) {
	slices.SortFunc(rows, comparePredictionCandidates)
	for _, row := range rows {
		if commonNameUnresolved(c.commonName, c.name) && !commonNameUnresolved(row.commonName, row.name) {
			c.commonName = row.commonName
		}
		if c.code == "" {
			c.code = row.code
		}
	}
	if c.commonName != "" && commonNameUnresolved(c.commonName, c.name) {
		c.commonName = openfauna.CanonicalName(c.name)
	}
}

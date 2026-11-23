package models

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

const (
	// speciesQueryParam is the repeatable query parameter that carries the species names.
	speciesQueryParam = "species"
	// maxCoverageSpecies is the most species names one request may carry, counted
	// after trimming and dropping blanks.
	maxCoverageSpecies = 500
)

// speciesKnowledge is the part of classifier.ModelSpecies the coverage endpoint
// reads. *classifier.ModelSpecies satisfies it; tests inject a fixed fake because
// the snapshot has no exported constructor.
type speciesKnowledge interface {
	// HasModel reports whether the model is loaded with a live instance.
	HasModel(modelID string) bool
	// Knows reports whether the model can classify the species with the given
	// canonical key (speciesindex.CanonicalKey).
	Knows(modelID, key string) bool
}

// SpeciesCoverageModel is one loaded, enabled model that can classify a species.
type SpeciesCoverageModel struct {
	// ID is the canonical config alias, the form models.enabled uses after load.
	ID string `json:"id"`
	// RegistryID is the classifier registry ID the alias resolves to.
	RegistryID string `json:"registryId"`
	// Name is the display name of the model.
	Name string `json:"name"`
	// TaxonGroup is the model's taxon group ("" when ungrouped).
	TaxonGroup string `json:"taxonGroup"`
	// RangeFilterPass is true when the range filter does not apply to the model
	// (no location configured, or the model never participates) and otherwise true
	// only when the species is on the included list. It is false before the list is
	// first built.
	RangeFilterPass bool `json:"rangeFilterPass"`
}

// SpeciesCoverageEntry lists the models that know one requested species.
type SpeciesCoverageEntry struct {
	// Species is the requested name, trimmed.
	Species string `json:"species"`
	// Models is never null; it is empty when no loaded, enabled model knows the species.
	Models []SpeciesCoverageModel `json:"models"`
}

// SpeciesCoverageResponse is the body of GET /api/v2/models/species-coverage.
type SpeciesCoverageResponse struct {
	// Species has one entry per requested name, in request order.
	Species []SpeciesCoverageEntry `json:"species"`
}

// liveSpeciesKnowledge returns the live orchestrator's per-model species snapshot.
func (c *Handler) liveSpeciesKnowledge() (speciesKnowledge, error) {
	orch, err := c.GetBirdNETInstance()
	if err != nil {
		return nil, err
	}
	return orch.ModelSpecies(), nil
}

// GetSpeciesCoverage reports, for each requested species, the loaded and enabled
// models that know it. It uses the predicates runtime consensus eligibility uses:
// the per-model species snapshot, the model's taxon group, and the range filter
// (classifier.PassesRangeFilter). A client computes the "undetectable species"
// guard for a consensus rule per taxon group, because a detection is only compared
// with models of its own group.
//
// At most maxCoverageSpecies names are accepted per request, counted after
// trimming and dropping blanks; more is a 400. Names are matched as scientific
// names, as consensus rules store them after a save; common names are not
// resolved. It is auth-gated because rangeFilterPass is derived from the
// configured location. The first call after a model topology
// change can wait for the snapshot to be rebuilt.
//
// @Summary Get per-species model coverage
// @Description Lists the loaded, enabled models that know each requested species
// @Tags models
// @Produce json
// @Param species query []string true "Scientific species name (repeatable)"
// @Success 200 {object} SpeciesCoverageResponse
// @Router /api/v2/models/species-coverage [get]
func (c *Handler) GetSpeciesCoverage(ctx echo.Context) error {
	raws := ctx.QueryParams()[speciesQueryParam]
	names := make([]string, 0, len(raws))
	for _, raw := range raws {
		if name := strings.TrimSpace(raw); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return c.HandleError(ctx, nil, "at least one species is required", http.StatusBadRequest)
	}
	if len(names) > maxCoverageSpecies {
		return c.HandleError(ctx, nil, "at most "+strconv.Itoa(maxCoverageSpecies)+" species are accepted per request", http.StatusBadRequest)
	}

	source := c.speciesKnowledge
	if source == nil {
		source = c.liveSpeciesKnowledge
	}
	know, err := source()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	settings := c.CurrentSettings()
	// The model-invariant fields are built once per enabled model, sorted by alias.
	seen := make(map[string]struct{}, len(settings.Models.Enabled))
	candidates := make([]SpeciesCoverageModel, 0, len(settings.Models.Enabled))
	for _, alias := range settings.Models.Enabled {
		id, ok := classifier.ResolveConfigModelID(alias)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		candidates = append(candidates, SpeciesCoverageModel{
			ID:         classifier.ConfigAliasForRegistry(id),
			RegistryID: id,
			Name:       classifier.ModelRegistry[id].Name,
			TaxonGroup: string(classifier.TaxonGroupFor(id)),
		})
	}
	slices.SortFunc(candidates, func(a, b SpeciesCoverageModel) int { return strings.Compare(a.ID, b.ID) })

	resp := SpeciesCoverageResponse{Species: make([]SpeciesCoverageEntry, 0, len(names))}
	for _, name := range names {
		key := speciesindex.CanonicalKey(name)
		models := make([]SpeciesCoverageModel, 0, len(candidates))
		for _, m := range candidates {
			if !know.HasModel(m.RegistryID) || !know.Knows(m.RegistryID, key) {
				continue
			}
			m.RangeFilterPass = classifier.PassesRangeFilter(m.RegistryID, settings, name)
			models = append(models, m)
		}
		resp.Species = append(resp.Species, SpeciesCoverageEntry{Species: name, Models: models})
	}
	return ctx.JSON(http.StatusOK, resp)
}

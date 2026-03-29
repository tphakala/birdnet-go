// orchestrator.go is the primary entry point for model management and inference.
// In Phase 3b it wraps a single BirdNET instance with no behavior change.
// Future phases add multi-model support behind the same API.
package classifier

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// modelEntry holds a model instance with its own lock for concurrent access.
type modelEntry struct {
	instance ModelInstance
	mu       sync.Mutex // per-model lock; prevents inference on one model from blocking another
}

// Orchestrator manages classifier model instances and provides the primary
// inference API. It replaces direct *BirdNET usage at all call sites.
//
// Phase 3b: wraps a single BirdNET instance. All methods are passthroughs.
// Phase 3c+: manages multiple models with per-model locking and name resolution.
type Orchestrator struct {
	// Public fields — same layout as BirdNET for drop-in caller migration.
	Settings        *conf.Settings
	ModelInfo       ModelInfo
	TaxonomyMap     TaxonomyMap
	TaxonomyPath    string
	ScientificIndex ScientificNameIndex

	// Model management.
	// NOTE: models map is keyed by ModelInfo.ID at construction time. If ReloadModel
	// changes the model ID, the key goes stale. Delete() iterates values so cleanup
	// is unaffected. Phase 3c should re-key models on reload.
	models  map[string]*modelEntry
	primary *BirdNET // Phase 3b: direct access to the single model
}

// NewOrchestrator creates a new Orchestrator wrapping a single BirdNET model.
// This is the primary constructor — callers should use this instead of NewBirdNET.
func NewOrchestrator(settings *conf.Settings) (*Orchestrator, error) {
	bn, err := NewBirdNET(settings)
	if err != nil {
		return nil, err
	}

	o := &Orchestrator{
		Settings:        settings,
		ModelInfo:       bn.ModelInfo,
		TaxonomyMap:     bn.TaxonomyMap,
		TaxonomyPath:    bn.TaxonomyPath,
		ScientificIndex: bn.ScientificIndex,
		models: map[string]*modelEntry{
			bn.ModelInfo.ID: {instance: bn},
		},
		primary: bn,
	}
	return o, nil
}

// Predict runs inference using the primary model.
// Phase 3b: relies on BirdNET's internal locking.
// Phase 3c+ will use modelEntry.mu for per-model serialization.
func (o *Orchestrator) Predict(ctx context.Context, sample [][]float32) ([]datastore.Results, error) {
	return o.primary.Predict(ctx, sample)
}

// GetProbableSpecies returns species scores from the range filter.
func (o *Orchestrator) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	return o.primary.GetProbableSpecies(date, week)
}

// GetSpeciesOccurrence returns the occurrence probability for a species at the current time.
func (o *Orchestrator) GetSpeciesOccurrence(species string) float64 {
	return o.primary.GetSpeciesOccurrence(species)
}

// GetSpeciesOccurrenceAtTime returns the occurrence probability for a species at a specific time.
func (o *Orchestrator) GetSpeciesOccurrenceAtTime(species string, detectionTime time.Time) float64 {
	return o.primary.GetSpeciesOccurrenceAtTime(species, detectionTime)
}

// GetSpeciesCode returns the eBird species code for a given label.
func (o *Orchestrator) GetSpeciesCode(label string) (string, bool) {
	return o.primary.GetSpeciesCode(label)
}

// GetSpeciesWithScientificAndCommonName returns the scientific and common name for a label.
func (o *Orchestrator) GetSpeciesWithScientificAndCommonName(label string) (scientific, common string) {
	return o.primary.GetSpeciesWithScientificAndCommonName(label)
}

// EnrichResultWithTaxonomy adds taxonomy information to a detection result.
func (o *Orchestrator) EnrichResultWithTaxonomy(speciesLabel string) (scientific, common, code string) {
	return o.primary.EnrichResultWithTaxonomy(speciesLabel)
}

// RunFilterProcess executes the filter process on demand and prints results.
func (o *Orchestrator) RunFilterProcess(dateStr string, week float32) {
	o.primary.RunFilterProcess(dateStr, week)
}

// ReloadModel reloads the primary model and re-syncs shared state.
func (o *Orchestrator) ReloadModel() error {
	if err := o.primary.ReloadModel(); err != nil {
		return err
	}
	// Re-sync shared state after reload
	o.ModelInfo = o.primary.ModelInfo
	o.TaxonomyMap = o.primary.TaxonomyMap
	o.TaxonomyPath = o.primary.TaxonomyPath
	o.ScientificIndex = o.primary.ScientificIndex
	return nil
}

// Delete releases all resources held by the Orchestrator and its models.
// After calling Delete, the Orchestrator must not be used.
func (o *Orchestrator) Delete() {
	for _, entry := range o.models {
		entry.mu.Lock()
		if err := entry.instance.Close(); err != nil {
			GetLogger().Warn("failed to close model instance",
				logger.String("model_id", entry.instance.ModelID()),
				logger.Error(err))
		}
		entry.mu.Unlock()
	}
	// Nil out references to fail fast on use-after-delete.
	o.primary = nil
	o.models = nil
}

// Debug prints debug messages if debug mode is enabled.
func (o *Orchestrator) Debug(format string, v ...any) {
	o.primary.Debug(format, v...)
}

package labels

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// Resolver handles lazy label creation and caching.
// It uses repository interfaces for database access and caches
// lookup table IDs for efficient label resolution.
type Resolver struct {
	labelRepo    repository.LabelRepository
	labelTypeRepo repository.LabelTypeRepository
	taxClassRepo  repository.TaxonomicClassRepository
	modelRepo     repository.ModelRepository

	// Cached lookup table IDs (loaded at initialization)
	labelTypeIDs   map[string]uint // "species" -> ID, "noise" -> ID, etc.
	taxClassIDs    map[string]uint // "Aves" -> ID, "Chiroptera" -> ID

	// Label cache: (modelID, rawLabel) -> *entities.Label
	cache sync.Map
}

type cacheKey struct {
	modelID  uint
	rawLabel string
}

// NewResolver creates a new label resolver with repository dependencies.
func NewResolver(
	labelRepo repository.LabelRepository,
	labelTypeRepo repository.LabelTypeRepository,
	taxClassRepo repository.TaxonomicClassRepository,
	modelRepo repository.ModelRepository,
) *Resolver {
	return &Resolver{
		labelRepo:     labelRepo,
		labelTypeRepo: labelTypeRepo,
		taxClassRepo:  taxClassRepo,
		modelRepo:     modelRepo,
		labelTypeIDs:  make(map[string]uint),
		taxClassIDs:   make(map[string]uint),
	}
}

// Initialize loads and caches all lookup table IDs.
// This should be called once at application startup.
func (r *Resolver) Initialize(ctx context.Context) error {
	// Load label types
	labelTypes, err := r.labelTypeRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to load label types: %w", err)
	}
	for _, lt := range labelTypes {
		r.labelTypeIDs[lt.Name] = lt.ID
	}

	// Ensure required label types exist
	requiredTypes := []string{LabelTypeSpecies, LabelTypeNoise, LabelTypeEnvironment, LabelTypeDevice}
	for _, name := range requiredTypes {
		if _, exists := r.labelTypeIDs[name]; !exists {
			lt, err := r.labelTypeRepo.GetOrCreate(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to create label type %s: %w", name, err)
			}
			r.labelTypeIDs[lt.Name] = lt.ID
		}
	}

	// Load taxonomic classes
	taxClasses, err := r.taxClassRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to load taxonomic classes: %w", err)
	}
	for _, tc := range taxClasses {
		r.taxClassIDs[tc.Name] = tc.ID
	}

	// Ensure required taxonomic classes exist
	requiredClasses := []string{"Aves", "Chiroptera"}
	for _, name := range requiredClasses {
		if _, exists := r.taxClassIDs[name]; !exists {
			tc, err := r.taxClassRepo.GetOrCreate(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to create taxonomic class %s: %w", name, err)
			}
			r.taxClassIDs[tc.Name] = tc.ID
		}
	}

	return nil
}

// Resolve returns the label for a model's raw label, creating entries as needed.
// This is the main entry point for lazy label resolution.
func (r *Resolver) Resolve(ctx context.Context, model *entities.AIModel, rawLabel string) (*entities.Label, error) {
	if model == nil {
		return nil, errors.New("model cannot be nil")
	}

	key := cacheKey{modelID: model.ID, rawLabel: rawLabel}

	// Check cache first
	if cached, ok := r.cache.Load(key); ok {
		return cached.(*entities.Label), nil
	}

	// Parse the raw label
	parsed := ParseRawLabel(rawLabel, model.ModelType)

	// Get label type ID
	labelTypeID, ok := r.labelTypeIDs[parsed.LabelType]
	if !ok {
		return nil, fmt.Errorf("unknown label type: %s", parsed.LabelType)
	}

	// Get taxonomic class ID (optional)
	var taxonomicClassID *uint
	if parsed.TaxonomicClass != "" {
		if id, ok := r.taxClassIDs[parsed.TaxonomicClass]; ok {
			taxonomicClassID = &id
		}
	}

	// Get or create the label via repository
	label, err := r.labelRepo.GetOrCreate(ctx, parsed.ScientificName, model.ID, labelTypeID, taxonomicClassID)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create label: %w", err)
	}

	r.cache.Store(key, label)
	return label, nil
}

// ResolveBatch resolves multiple raw labels for a model in an optimized batch.
// Returns a map of rawLabel -> *entities.Label.
func (r *Resolver) ResolveBatch(ctx context.Context, model *entities.AIModel, rawLabels []string) (map[string]*entities.Label, error) {
	if model == nil {
		return nil, errors.New("model cannot be nil")
	}

	result := make(map[string]*entities.Label, len(rawLabels))
	var uncached []string
	var uncachedParsed []ParsedLabel

	// Check cache for each label
	for _, rawLabel := range rawLabels {
		key := cacheKey{modelID: model.ID, rawLabel: rawLabel}
		if cached, ok := r.cache.Load(key); ok {
			result[rawLabel] = cached.(*entities.Label)
		} else {
			uncached = append(uncached, rawLabel)
			uncachedParsed = append(uncachedParsed, ParseRawLabel(rawLabel, model.ModelType))
		}
	}

	if len(uncached) == 0 {
		return result, nil
	}

	// Get default label type ID for species (most common case for batch)
	speciesTypeID, ok := r.labelTypeIDs[LabelTypeSpecies]
	if !ok {
		return nil, errors.New("species label type not initialized")
	}

	// Get default taxonomic class ID based on model type
	var defaultTaxClassID *uint
	switch model.ModelType {
	case entities.ModelTypeBird:
		if id, ok := r.taxClassIDs["Aves"]; ok {
			defaultTaxClassID = &id
		}
	case entities.ModelTypeBat:
		if id, ok := r.taxClassIDs["Chiroptera"]; ok {
			defaultTaxClassID = &id
		}
	case entities.ModelTypeMulti:
		// Multi-type models can detect multiple taxonomic classes; no default
	}

	// Collect scientific names for batch operation
	scientificNames := make([]string, len(uncachedParsed))
	for i, parsed := range uncachedParsed {
		scientificNames[i] = parsed.ScientificName
	}

	// Batch get or create labels
	labels, err := r.labelRepo.BatchGetOrCreate(ctx, scientificNames, model.ID, speciesTypeID, defaultTaxClassID)
	if err != nil {
		return nil, fmt.Errorf("batch resolve labels: %w", err)
	}

	// Map results back and cache them
	for i, rawLabel := range uncached {
		parsed := uncachedParsed[i]
		if label, found := labels[parsed.ScientificName]; found {
			result[rawLabel] = label
			key := cacheKey{modelID: model.ID, rawLabel: rawLabel}
			r.cache.Store(key, label)
		}
	}

	return result, nil
}

// GetModel retrieves or creates an AI model by name, version, and variant.
func (r *Resolver) GetModel(ctx context.Context, name, version, variant string, modelType entities.ModelType, classifierPath *string) (*entities.AIModel, error) {
	return r.modelRepo.GetOrCreate(ctx, name, version, variant, modelType, classifierPath)
}

// ClearCache clears the resolver cache.
func (r *Resolver) ClearCache() {
	r.cache = sync.Map{}
}

// GetLabelTypeID returns the cached ID for a label type name.
func (r *Resolver) GetLabelTypeID(name string) (uint, bool) {
	id, ok := r.labelTypeIDs[name]
	return id, ok
}

// GetTaxonomicClassID returns the cached ID for a taxonomic class name.
func (r *Resolver) GetTaxonomicClassID(name string) (uint, bool) {
	id, ok := r.taxClassIDs[name]
	return id, ok
}

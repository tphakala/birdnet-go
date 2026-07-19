// Package v2only provides a datastore implementation using only the v2 schema.
// This is used after migration completes when the legacy database is no longer needed.
//
// # Data Model Architecture
//
// The v2 schema separates primary and secondary predictions:
//
//   - detections table: Contains the primary prediction (label_id, confidence).
//     Use this for ALL queries: daily grids, summaries, aggregations, search, etc.
//
//   - detection_predictions table: Contains ONLY secondary/alternative predictions.
//     Use ONLY for per-detection detail views showing "what else could this be?".
//
// IMPORTANT: Never join detection_predictions in aggregate queries. Doing so excludes
// detections that have no secondary predictions, causing species to randomly disappear
// from grids and summaries.
package v2only

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/dbstats"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
	"github.com/tphakala/birdnet-go/internal/logger"
	obmetrics "github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

// Sentinel errors for operations not supported in v2-only mode.
var (
	// ErrOperationNotSupported indicates an operation is not available in v2-only mode.
	ErrOperationNotSupported = errors.NewStd("operation not supported in v2-only mode")
	// ErrNotImplemented indicates a feature requires implementation.
	ErrNotImplemented = errors.NewStd("not implemented in v2-only datastore")
	// ErrInvalidHour is returned when an hour string is not a valid integer in the range 0-23.
	ErrInvalidHour = errors.NewStd("invalid hour: must be an integer between 0 and 23")
)

const (
	// minHour is the minimum valid hour value (midnight).
	minHour = 0
	// maxHour is the maximum valid hour value (11 PM).
	maxHour = 23
	// saveTransactionTimeout is the maximum duration for a Save transaction.
	// This prevents indefinite lock holding during slow I/O operations.
	saveTransactionTimeout = 30 * time.Second
)

// parseHour validates and parses an hour string to an integer.
// Returns ErrInvalidHour if the string is not a valid integer or is outside 0-23.
func parseHour(hour string) (int, error) {
	h, err := strconv.Atoi(hour)
	if err != nil {
		return 0, ErrInvalidHour
	}
	if h < minHour || h > maxHour {
		return 0, ErrInvalidHour
	}
	return h, nil
}

// parseID converts a string ID to uint.
func parseID(id string) (uint, error) {
	parsed, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %q: %w", id, err)
	}
	return uint(parsed), nil
}

// parseDetectionTimestamp converts date and time strings to Unix timestamp.
// Falls back to current time if parsing fails.
func parseDetectionTimestamp(date, timeStr string, tz *time.Location) int64 {
	if date != "" && timeStr != "" {
		dateTimeStr := date + " " + timeStr
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", dateTimeStr, tz); err == nil {
			return t.Unix()
		}
	} else if date != "" {
		if t, err := time.ParseInLocation("2006-01-02", date, tz); err == nil {
			return t.Unix()
		}
	}
	return time.Now().Unix()
}

// nameMaps holds the species name lookup maps. Stored behind an atomic.Pointer
// so readers are lock-free and UpdateNameMaps can swap atomically.
type nameMaps struct {
	// common maps scientific name → common name (display lookup).
	common map[string]string
	// commonFolded maps scientific name → lower-cased NFC-normalized common name. Precomputed
	// once here so common-name search (ResolveCommonNameToLabelIDs) does not normalize every map
	// value on every query.
	commonFolded map[string]string
	// species maps lowercase common name → scientific name (reverse lookup).
	species map[string]string
}

// Datastore implements datastore.Interface using only v2 repositories.
type Datastore struct {
	manager      v2.Manager
	detection    repository.DetectionRepository
	label        repository.LabelRepository
	model        repository.ModelRepository
	source       repository.AudioSourceRepository
	weather      repository.WeatherRepository
	imageCache   repository.ImageCacheRepository
	threshold    repository.DynamicThresholdRepository
	notification repository.NotificationHistoryRepository
	appEvent     repository.AppEventRepository
	log          logger.Logger
	metrics      *datastore.Metrics
	timezone     *time.Location
	suncalc      *suncalc.SunCalc

	// Cached lookup table IDs for label creation
	defaultModelID     uint  // Model ID to use for new labels
	speciesLabelTypeID uint  // "species" label type ID
	avesClassID        *uint // "Aves" taxonomic class ID (optional)
	chiropteraClassID  *uint // "Chiroptera" taxonomic class ID (optional)

	// nonBirdLabelTypeIDs maps each non-bird sound category to its label_type_id.
	// Set once in the constructor (read-only afterwards) so concurrent Save calls can
	// classify Perch v2 (FSD50K) sound classes without a data race.
	nonBirdLabelTypeIDs map[nonbird.Category]uint

	// names holds the species name lookup maps behind an atomic.Pointer
	// for lock-free reads and atomic swaps when locale changes.
	names atomic.Pointer[nameMaps]

	// nameResolver, when set, is the authoritative localized name source shared
	// with the classifier orchestrator. It overrides the label-derived maps and
	// resolves historic out-of-working-set species via on-demand lookup.
	nameResolver atomic.Pointer[datastore.SpeciesNameResolver]

	// speciesCodeMap provides O(1) lookup from scientific name to eBird species code.
	// Populated from the eBird taxonomy data passed via Config.SpeciesCodeMap.
	speciesCodeMap map[string]string

	// loggedMissingNames tracks scientific names already warned about for
	// missing common name mappings, preventing log spam.
	loggedMissingNames sync.Map

	// dbstatAvailable caches whether the dbstat virtual table exists.
	// 0 = unchecked, 1 = available, -1 = not available.
	dbstatAvailable int32

	// dbCounters tracks atomic query latency counters for metrics collection.
	dbCounters *dbstats.Counters
}

// Config configures the Datastore.
type Config struct {
	Manager      v2.Manager
	Detection    repository.DetectionRepository
	Label        repository.LabelRepository
	Model        repository.ModelRepository
	Source       repository.AudioSourceRepository
	Weather      repository.WeatherRepository
	ImageCache   repository.ImageCacheRepository
	Threshold    repository.DynamicThresholdRepository
	Notification repository.NotificationHistoryRepository
	AppEvent     repository.AppEventRepository
	Logger       logger.Logger
	Timezone     *time.Location
	SunCalc      *suncalc.SunCalc

	// Required: Cached lookup table IDs
	DefaultModelID     uint  // Model ID to use for new labels (typically default BirdNET)
	SpeciesLabelTypeID uint  // "species" label type ID
	AvesClassID        *uint // "Aves" taxonomic class ID (optional)
	ChiropteraClassID  *uint // "Chiroptera" taxonomic class ID (optional)

	// Labels provides species label mappings in "ScientificName_CommonName" format.
	// Used to build speciesMap for GetThresholdEvents workaround. See issue #1907.
	Labels []string

	// SpeciesCodeMap maps scientific names to eBird species codes.
	// Built from taxonomy data (e.g., birdnet.CreateScientificNameIndex).
	SpeciesCodeMap map[string]string
}

// getOrCreateLabelTypeID returns the id of the label type named name, creating
// the row if absent. It returns an error if the resolved id is 0 (a zero
// label_type_id is a silent FK orphan that would corrupt label rows).
func getOrCreateLabelTypeID(db *gorm.DB, name string) (uint, error) {
	var lt entities.LabelType
	if err := db.Where("name = ?", name).FirstOrCreate(&lt, entities.LabelType{Name: name}).Error; err != nil {
		return 0, fmt.Errorf("resolve label type %q: %w", name, err)
	}
	if lt.ID == 0 {
		return 0, fmt.Errorf("label type %q resolved to id 0", name)
	}
	return lt.ID, nil
}

// New creates a new V2-only Datastore.
func New(cfg *Config) (*Datastore, error) {
	if cfg.Manager == nil {
		return nil, fmt.Errorf("manager is required")
	}
	if cfg.Detection == nil {
		return nil, fmt.Errorf("detection repository is required")
	}
	if cfg.Label == nil {
		return nil, fmt.Errorf("label repository is required")
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("model repository is required")
	}

	// Self-initialize lookup table IDs if not provided.
	// These are seeded during Manager.Initialize(), so we look them up.
	db := cfg.Manager.DB()

	// Register GORM callbacks for query latency tracking.
	dbCounters := &dbstats.Counters{}
	dbstats.RegisterCallbacks(db, dbCounters)

	// Get or verify species label type ID.
	// Uses the helper so a zero id (FK orphan) causes construction to fail early.
	speciesLabelTypeID := cfg.SpeciesLabelTypeID
	if speciesLabelTypeID == 0 {
		var err error
		speciesLabelTypeID, err = getOrCreateLabelTypeID(db, entities.LabelTypeSpecies)
		if err != nil {
			return nil, fmt.Errorf("failed to get species label type: %w", err)
		}
	}

	// Build cached map of non-bird category -> label_type_id.
	// All seven IDs are resolved once here (read-only after construction) so
	// concurrent Save calls can classify non-bird sounds without a data race.
	nonBirdLabelTypeIDs := make(map[nonbird.Category]uint, len(nonbird.Categories()))
	for _, cat := range nonbird.Categories() {
		id, err := getOrCreateLabelTypeID(db, string(cat))
		if err != nil {
			return nil, fmt.Errorf("resolve non-bird label type for category %q: %w", cat, err)
		}
		nonBirdLabelTypeIDs[cat] = id
	}

	// Get or verify default model ID (BirdNET)
	defaultModelID := cfg.DefaultModelID
	if defaultModelID == 0 {
		var model entities.AIModel
		if err := db.Where("name = ? AND version = ? AND variant = ?",
			detection.DefaultModelName, detection.DefaultModelVersion, detection.DefaultModelVariant).
			FirstOrCreate(&model, entities.AIModel{
				Name:      detection.DefaultModelName,
				Version:   detection.DefaultModelVersion,
				Variant:   detection.DefaultModelVariant,
				ModelType: entities.ModelTypeBird,
			}).Error; err != nil {
			return nil, fmt.Errorf("failed to get default model: %w", err)
		}
		defaultModelID = model.ID
	}

	// Get or verify Aves taxonomic class ID (optional, for birds)
	avesClassID := cfg.AvesClassID
	if avesClassID == nil {
		var avesClass entities.TaxonomicClass
		if err := db.Where("name = ?", "Aves").FirstOrCreate(&avesClass, entities.TaxonomicClass{Name: "Aves"}).Error; err != nil {
			return nil, fmt.Errorf("failed to get Aves taxonomic class: %w", err)
		}
		avesClassID = &avesClass.ID
	}

	// Get or verify Chiroptera taxonomic class ID (optional, for bats)
	chiropteraClassID := cfg.ChiropteraClassID
	if chiropteraClassID == nil {
		var chiropteraClass entities.TaxonomicClass
		if err := db.Where("name = ?", "Chiroptera").FirstOrCreate(&chiropteraClass, entities.TaxonomicClass{Name: "Chiroptera"}).Error; err != nil {
			return nil, fmt.Errorf("failed to get Chiroptera taxonomic class: %w", err)
		}
		chiropteraClassID = &chiropteraClass.ID
	}

	tz := cfg.Timezone
	if tz == nil {
		tz = time.Local
	}

	// Build species name maps from labels. The OpenFauna resolver is injected
	// later via SetNameResolver (it is owned by the orchestrator, constructed
	// separately), so the maps are localized on the first post-wiring rebuild.
	nm := buildNameMaps(cfg.Labels, nil)

	// Use species code map from taxonomy data (injected via config).
	speciesCodeMap := cfg.SpeciesCodeMap
	if speciesCodeMap == nil {
		speciesCodeMap = make(map[string]string)
	}

	ds := &Datastore{
		manager:             cfg.Manager,
		detection:           cfg.Detection,
		label:               cfg.Label,
		model:               cfg.Model,
		source:              cfg.Source,
		weather:             cfg.Weather,
		imageCache:          cfg.ImageCache,
		threshold:           cfg.Threshold,
		notification:        cfg.Notification,
		appEvent:            cfg.AppEvent,
		log:                 cfg.Logger,
		timezone:            tz,
		suncalc:             cfg.SunCalc,
		defaultModelID:      defaultModelID,
		speciesLabelTypeID:  speciesLabelTypeID,
		avesClassID:         avesClassID,
		chiropteraClassID:   chiropteraClassID,
		nonBirdLabelTypeIDs: nonBirdLabelTypeIDs,
		speciesCodeMap:      speciesCodeMap,
		dbCounters:          dbCounters,
	}
	ds.names.Store(nm)

	// Start periodic WAL checkpoint for SQLite to prevent unbounded WAL growth.
	// The auto-checkpoint mechanism may not fire reliably with connection pooling.
	if sqliteMgr, ok := cfg.Manager.(*v2.SQLiteManager); ok {
		sqliteMgr.StartPeriodicCheckpoint()
	}

	return ds, nil
}

// buildNameMaps parses BirdNET labels ("ScientificName_CommonName" format)
// into lookup maps for common name resolution.
// See issue #1907 for context on species map usage.
// When resolver is non-nil, each label's common name is overridden by the
// resolver (authoritative/localized); labels the resolver does not cover keep
// their embedded common name. This keeps the reverse (search) maps consistent
// with what resolveCommonName displays.
func buildNameMaps(labels []string, resolver datastore.SpeciesNameResolver) *nameMaps {
	speciesMap := make(map[string]string, len(labels))
	commonMap := make(map[string]string, len(labels))
	commonFoldedMap := make(map[string]string, len(labels))
	// Ambiguous reverse keys are deleted, not last-writer-wins: an ambiguous common
	// name must fall through to substring search (which returns all matches) rather
	// than route to an arbitrary species.
	ambiguous := make(map[string]struct{})
	for _, sn := range datastore.ResolveLabelNames(labels, resolver) {
		commonMap[sn.Scientific] = sn.Common
		folded := strings.ToLower(norm.NFC.String(sn.Common))
		commonFoldedMap[sn.Scientific] = folded

		if _, seen := ambiguous[folded]; seen {
			continue
		}
		if existing, exists := speciesMap[folded]; exists && existing != sn.Scientific {
			ambiguous[folded] = struct{}{}
			delete(speciesMap, folded)
			continue
		}
		speciesMap[folded] = sn.Scientific
	}
	return &nameMaps{common: commonMap, commonFolded: commonFoldedMap, species: speciesMap}
}

// UpdateNameMaps rebuilds species name lookup maps from updated BirdNET labels.
// Called after locale or model changes to keep common name resolution current.
// The new maps are built first, then atomically swapped in - readers are never blocked.
// Also resets the missing-name warning deduplication so new mismatches are logged.
func (ds *Datastore) UpdateNameMaps(labels []string) {
	ds.names.Store(buildNameMaps(labels, ds.loadNameResolver()))
	ds.loggedMissingNames.Clear()
}

// SetNameResolver installs the authoritative localized name resolver, shared with
// the classifier orchestrator. Safe to call concurrently with reads; a nil
// resolver is ignored.
func (ds *Datastore) SetNameResolver(r datastore.SpeciesNameResolver) {
	if datastore.IsNilResolver(r) {
		return
	}
	ds.nameResolver.Store(&r)
}

// loadNameResolver returns the installed resolver, or nil if none has been set.
func (ds *Datastore) loadNameResolver() datastore.SpeciesNameResolver {
	if p := ds.nameResolver.Load(); p != nil {
		return *p
	}
	return nil
}

// Open is a no-op since the manager is already open.
func (ds *Datastore) Open() error {
	return nil
}

// loadNameMaps returns the current name maps. Always returns a non-nil value.
func (ds *Datastore) loadNameMaps() *nameMaps {
	if m := ds.names.Load(); m != nil {
		return m
	}
	return &nameMaps{
		common:       make(map[string]string),
		commonFolded: make(map[string]string),
		species:      make(map[string]string),
	}
}

// filterLookupDeps builds the dependency set used by repository filter resolution (species and
// device lookups, plus common-name search via the active-locale name maps).
func (ds *Datastore) filterLookupDeps() *repository.FilterLookupDeps {
	nm := ds.loadNameMaps()
	return &repository.FilterLookupDeps{
		LabelRepo:         ds.label,
		SourceRepo:        ds.source,
		SciToCommon:       nm.common,
		SciToCommonFolded: nm.commonFolded,
	}
}

// GetDBCounters returns the atomic counters for database query latency tracking.
func (ds *Datastore) GetDBCounters() *dbstats.Counters {
	return ds.dbCounters
}

// Close closes the datastore.
func (ds *Datastore) Close() error {
	if ds.manager != nil {
		log := logger.Global().Module("datastore")

		// Stop periodic WAL checkpoint before the final TRUNCATE checkpoint.
		if sqliteMgr, ok := ds.manager.(*v2.SQLiteManager); ok {
			sqliteMgr.StopPeriodicCheckpoint()
		}
		if !ds.manager.IsMySQL() {
			log.Info("performing SQLite WAL checkpoint",
				logger.String("operation", "wal_checkpoint_before_shutdown"),
				logger.String("mode", "v2only"))
			if err := ds.manager.CheckpointWAL(); err != nil {
				log.Warn("WAL checkpoint failed",
					logger.Error(err),
					logger.String("operation", "wal_checkpoint"),
					logger.Bool("continuing_shutdown", true))
			}
		}
		return ds.manager.Close()
	}
	return nil
}

// SetMetrics sets the metrics instance.
func (ds *Datastore) SetMetrics(metrics *datastore.Metrics) {
	ds.metrics = metrics
}

// Manager returns the underlying database manager.
// This allows access to the manager for API endpoints that need it.
func (ds *Datastore) Manager() v2.Manager {
	return ds.manager
}

// SetSunCalcMetrics sets the SunCalc metrics instance for observability.
func (ds *Datastore) SetSunCalcMetrics(suncalcMetrics any) {
	if ds.suncalc != nil && suncalcMetrics != nil {
		if m, ok := suncalcMetrics.(*obmetrics.SunCalcMetrics); ok {
			ds.suncalc.SetMetrics(m)
		}
	}
}

// Optimize performs database optimization.
func (ds *Datastore) Optimize(ctx context.Context) error {
	if !ds.manager.IsMySQL() {
		db := ds.manager.DB()
		if err := db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
			return fmt.Errorf("VACUUM failed: %w", err)
		}
		return db.WithContext(ctx).Exec("ANALYZE").Error
	}
	return nil
}

// Transaction runs a function within a database transaction.
func (ds *Datastore) Transaction(fc func(tx *gorm.DB) error) error {
	return ds.manager.DB().Transaction(fc)
}

// SchemaVersion returns the datastore schema version.
func (ds *Datastore) SchemaVersion() string {
	return datastore.SchemaVersionV2
}

// PingWithLatency executes SELECT 1 and returns the round-trip time.
func (ds *Datastore) PingWithLatency(ctx context.Context) (time.Duration, error) {
	db := ds.manager.DB()
	if db == nil {
		return 0, datastore.ErrDBNotConnected
	}
	start := time.Now()
	var result int
	if err := db.WithContext(ctx).Raw("SELECT 1").Scan(&result).Error; err != nil {
		return 0, fmt.Errorf("database ping failed: %w", err)
	}
	return time.Since(start), nil
}

// CountDetectionsSince returns the number of detections recorded since the given time.
func (ds *Datastore) CountDetectionsSince(ctx context.Context, since time.Time) (int, error) {
	db := ds.manager.DB()
	if db == nil {
		return 0, datastore.ErrDBNotConnected
	}
	var count int64
	if err := db.WithContext(ctx).Model(&entities.Detection{}).Where("detected_at >= ?", since.Unix()).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count detections failed: %w", err)
	}
	return int(count), nil
}

// GetDatabaseStats returns database statistics.
func (ds *Datastore) GetDatabaseStats(ctx context.Context) (*datastore.DatabaseStats, error) {
	count, err := ds.detection.CountAll(ctx)
	if err != nil {
		return nil, err
	}

	dbType := "sqlite"
	if ds.manager.IsMySQL() {
		dbType = "mysql"
	}

	stats := &datastore.DatabaseStats{
		Type:            dbType,
		TotalDetections: count,
		Connected:       true,
		Location:        ds.manager.Path(),
	}

	// Get database size (best-effort); guard against nil DB after concurrent Close()
	if db := ds.manager.DB(); db != nil {
		if !ds.manager.IsMySQL() {
			_ = db.WithContext(ctx).Raw("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&stats.SizeBytes).Error
		} else {
			_ = db.WithContext(ctx).Raw(`
				SELECT SUM(DATA_LENGTH + INDEX_LENGTH)
				FROM information_schema.TABLES
				WHERE TABLE_SCHEMA = DATABASE()
			`).Scan(&stats.SizeBytes).Error
		}
	}

	return stats, nil
}

// labelTypeForRawLabel resolves the label_type_id and taxonomic_class_id for a label given its
// full raw classifier label. A Perch v2 (FSD50K) non-bird sound class (recognized by
// nonbird.CategoryOf on the full raw label) gets its category's label type and a nil taxonomic
// class; everything else (birds, and any label not recognized as non-bird, including an empty
// rawLabel) gets the species label type and the model's taxonomic class. The stored scientific
// name is unchanged by this function - the caller still stores the extracted scientific name.
// isNonBird reports whether the non-bird branch was taken (used to gate first-writer-wins relabel).
func (ds *Datastore) labelTypeForRawLabel(rawLabel string, speciesTaxClassID *uint) (labelTypeID uint, taxClassID *uint, isNonBird bool) {
	if cat, ok := nonbird.CategoryOf(rawLabel); ok {
		return ds.nonBirdLabelTypeIDs[cat], nil, true
	}
	return ds.speciesLabelTypeID, speciesTaxClassID, false
}

// taxonomicClassForModel returns the appropriate taxonomic class ID for label
// creation based on the model type. Bird models use Aves, bat models use
// Chiroptera, and multi-taxa models use nil (no default taxonomic class).
func (ds *Datastore) taxonomicClassForModel(modelType entities.ModelType) *uint {
	switch modelType {
	case entities.ModelTypeBat:
		return ds.chiropteraClassID
	case entities.ModelTypeMulti:
		return nil
	default:
		return ds.avesClassID
	}
}

// EnsureModelRegistered creates the model entry in ai_models if it doesn't exist.
func (ds *Datastore) EnsureModelRegistered(info detection.ModelInfo) error {
	if info.Name == "" {
		info = detection.DefaultModelInfo()
	}
	ctx := context.Background()
	_, err := ds.model.GetOrCreate(ctx, info.Name, info.Version, info.Variant, detection.ResolveModelType(info.Name, info.Version), info.ClassifierPath)
	return err
}

// resolvePredictionLabels classifies and batch-resolves labels for all prediction results.
// It groups predictions by their (labelTypeID, taxClassID), calls BatchGetOrCreate per group,
// relabels any non-bird groups that were previously stored with the wrong (species) type, and
// returns a predLabels slice in the same order as results. Returns nil if results is empty.
func (ds *Datastore) resolvePredictionLabels(ctx context.Context, results []datastore.Results, modelID uint, taxonomicClassID *uint) ([]*entities.Label, error) {
	if len(results) == 0 {
		return nil, nil
	}

	// Collect species names and classify each prediction.
	// Results.Species may contain concatenated "ScientificName_CommonName" format
	// from legacy code (see AdditionalResultsToDatastoreResults). Extract only
	// the scientific name portion for v2 label storage.
	speciesNames := make([]string, len(results))
	predTypeIDs := make([]uint, len(results))
	predTaxIDs := make([]*uint, len(results))
	for i, r := range results {
		speciesNames[i] = detection.ExtractScientificName(r.Species)
		predTypeIDs[i], predTaxIDs[i], _ = ds.labelTypeForRawLabel(r.RawLabel, taxonomicClassID)
	}

	// Group prediction names by (labelTypeID, taxClassID). taxClassID nil is represented
	// by 0 in the key (real taxonomic-class IDs are never 0); groupTax preserves the
	// actual *uint to pass to BatchGetOrCreate.
	type predGroupKey struct{ typeID, taxID uint }
	groupNames := make(map[predGroupKey][]string)
	groupTax := make(map[predGroupKey]*uint)
	for i := range results {
		var taxKey uint
		if predTaxIDs[i] != nil {
			taxKey = *predTaxIDs[i]
		}
		k := predGroupKey{predTypeIDs[i], taxKey}
		groupNames[k] = append(groupNames[k], speciesNames[i])
		groupTax[k] = predTaxIDs[i]
	}

	// Batch resolve each group and merge into a single name->label map. A given scientific name
	// maps to exactly one label row per model (unique on (scientific_name, model_id)), so even if
	// the same name were classified into two groups, both BatchGetOrCreate calls return the same
	// underlying label (same ID). Downstream uses only the label ID, so the merge is safe
	// regardless of group iteration order.
	merged := make(map[string]*entities.Label, len(results))
	for k, names := range groupNames {
		m, err := ds.label.BatchGetOrCreate(ctx, names, modelID, k.typeID, groupTax[k])
		if err != nil {
			return nil, fmt.Errorf("failed to batch get/create prediction labels: %w", err)
		}
		for name, lbl := range m {
			// First-writer-wins relabel for non-bird groups (k.typeID is not the species type).
			if k.typeID != ds.speciesLabelTypeID && lbl.LabelTypeID != k.typeID {
				if err := ds.label.UpdateLabelType(ctx, lbl.ID, k.typeID); err != nil {
					return nil, fmt.Errorf("failed to relabel non-bird prediction label %q: %w", name, err)
				}
				lbl.LabelTypeID = k.typeID
				lbl.TaxonomicClassID = nil
			}
			merged[name] = lbl
		}
	}

	// Resolve predLabels in original order.
	predLabels := make([]*entities.Label, len(results))
	for i := range results {
		sciName := speciesNames[i]
		lbl, ok := merged[sciName]
		if !ok {
			return nil, fmt.Errorf("label not found for species %s after batch creation", results[i].Species)
		}
		predLabels[i] = lbl
	}

	return predLabels, nil
}

// Save saves a note with its results atomically.
// The detection and its predictions are saved in a single transaction to prevent
// partial writes (e.g., detection saved but predictions failed).
func (ds *Datastore) Save(note *datastore.Note, results []datastore.Results) error {
	ctx := context.Background()

	// Use the model info from the note if available, otherwise fall back to the default.
	// This allows multi-model detections to be attributed to the correct model.
	modelInfo := note.Model
	if modelInfo.Name == "" {
		modelInfo = detection.DefaultModelInfo()
	}
	model, err := ds.model.GetOrCreate(ctx, modelInfo.Name, modelInfo.Version, modelInfo.Variant, detection.ResolveModelType(modelInfo.Name, modelInfo.Version), modelInfo.ClassifierPath)
	if err != nil {
		return fmt.Errorf("failed to get/create model: %w", err)
	}

	// Resolve taxonomic class based on model type
	taxonomicClassID := ds.taxonomicClassForModel(model.ModelType)

	// NOTE: Label GetOrCreate calls are outside the transaction.
	// If the detection save fails, orphaned reference data may persist.
	// This is acceptable as they will be reused on subsequent saves.
	// Extract scientific name in case it contains concatenated "ScientificName_CommonName" format.
	// Classify the primary label: non-bird Perch sound classes get their category's label type
	// and a nil taxonomic class; birds and unrecognized labels (including empty RawLabel) keep
	// the species label type and the model's taxonomic class.
	primaryTypeID, primaryTaxID, primaryNonBird := ds.labelTypeForRawLabel(note.RawLabel, taxonomicClassID)
	label, err := ds.label.GetOrCreate(ctx, detection.ExtractScientificName(note.ScientificName), model.ID, primaryTypeID, primaryTaxID)
	if err != nil {
		return fmt.Errorf("failed to get/create label: %w", err)
	}
	// First-writer-wins relabel: if this non-bird class was previously created as species, correct its type.
	if primaryNonBird && label.LabelTypeID != primaryTypeID {
		if err := ds.label.UpdateLabelType(ctx, label.ID, primaryTypeID); err != nil {
			return fmt.Errorf("failed to relabel non-bird label %q: %w", label.ScientificName, err)
		}
		label.LabelTypeID = primaryTypeID
		label.TaxonomicClassID = nil
	}

	// Pre-resolve all prediction labels before starting transaction.
	// Uses batch operation to avoid N+1 queries. Predictions are grouped by their
	// classified (labelTypeID, taxClassID) so BatchGetOrCreate can be called once per
	// group. Non-bird groups are relabeled if they were previously stored as species.
	predLabels, err := ds.resolvePredictionLabels(ctx, results, model.ID, taxonomicClassID)
	if err != nil {
		return err
	}

	// Parse the date string and time string to get Unix timestamp
	detectedAt := parseDetectionTimestamp(note.Date, note.Time, ds.timezone)

	det := &entities.Detection{
		LabelID:    label.ID,
		ModelID:    model.ID,
		DetectedAt: detectedAt,
		Confidence: note.Confidence,
		Unlikely:   note.Unlikely,
	}

	if note.Latitude != 0 {
		det.Latitude = &note.Latitude
	}
	if note.Longitude != 0 {
		det.Longitude = &note.Longitude
	}
	if note.ClipName != "" {
		det.ClipName = &note.ClipName
	}
	if !note.BeginTime.IsZero() {
		bt := note.BeginTime.UnixMilli()
		det.BeginTime = &bt
	}
	if !note.EndTime.IsZero() {
		et := note.EndTime.UnixMilli()
		det.EndTime = &et
	}
	if note.ProcessingTime > 0 {
		pt := note.ProcessingTime.Milliseconds()
		det.ProcessingTimeMs = &pt
	}

	// Resolve audio source if provided (follows same pattern as conversion.go)
	if note.Source.SafeString != "" && ds.source != nil {
		nodeName := note.SourceNode
		if nodeName == "" {
			nodeName = "default"
		}
		var displayName *string
		if note.Source.DisplayName != "" {
			displayName = &note.Source.DisplayName
		}
		source, sourceErr := ds.source.GetOrCreate(ctx,
			note.Source.SafeString,
			nodeName,
			displayName,
			entities.SourceType(""))
		if sourceErr != nil {
			if ds.log != nil {
				ds.log.Warn("audio source resolution failed during save", logger.Error(sourceErr))
			}
			// Continue without source - not fatal
		} else {
			det.SourceID = &source.ID
		}
	} else if note.Source.SafeString != "" && ds.source == nil {
		if ds.log != nil {
			ds.log.Debug("audio source provided but source repository is nil, skipping resolution")
		}
	}

	// Use timeout context for transaction to prevent indefinite lock holding.
	txCtx, cancel := context.WithTimeout(ctx, saveTransactionTimeout)
	defer cancel()

	// Wrap detection and predictions in a transaction for atomicity.
	// DIRECT DB WRITE: We use tx.Create directly instead of ds.detection.Save()
	// to ensure both detection and predictions are in the same transaction.
	// The repository doesn't currently support transaction injection.
	if err := ds.manager.DB().WithContext(txCtx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(det).Error; err != nil {
			return fmt.Errorf("failed to save detection: %w", err)
		}

		if len(results) > 0 && len(predLabels) > 0 {
			preds := buildDedupedPredictions(det.ID, results, predLabels)
			if len(preds) > 0 {
				if err := tx.Create(&preds).Error; err != nil {
					return fmt.Errorf("failed to save predictions: %w", err)
				}
			}
		}

		return nil
	}); err != nil {
		return err
	}

	// Propagate database-assigned ID back to the caller's Note.
	// Without this, detection_repository.Save() reads note.ID=0 and
	// downstream actions (MQTT, SSE) publish detectionId=0 (GitHub #2453).
	note.ID = det.ID

	return nil
}

// buildDedupedPredictions deduplicates predictions by label_id, keeping the highest confidence
// for each label. This is defense-in-depth against UNIQUE constraint violations when custom
// BirdNET classifiers have the same species at multiple positions in the label file.
func buildDedupedPredictions(detectionID uint, results []datastore.Results, predLabels []*entities.Label) []*entities.DetectionPrediction {
	seen := make(map[uint]int, len(results))
	preds := make([]*entities.DetectionPrediction, 0, len(results))
	for i, r := range results {
		labelID := predLabels[i].ID
		if idx, exists := seen[labelID]; exists {
			if float64(r.Confidence) > preds[idx].Confidence {
				preds[idx].Confidence = float64(r.Confidence)
			}
			continue
		}
		seen[labelID] = len(preds)
		preds = append(preds, &entities.DetectionPrediction{
			DetectionID: detectionID,
			LabelID:     labelID,
			Confidence:  float64(r.Confidence),
		})
	}
	return preds
}

// Delete deletes a note by ID.
func (ds *Datastore) Delete(id string) error {
	ctx := context.Background()
	noteID, err := parseID(id)
	if err != nil {
		return err
	}
	return ds.detection.Delete(ctx, noteID)
}

// Get retrieves a note by ID.
func (ds *Datastore) Get(id string) (datastore.Note, error) {
	ctx := context.Background()
	noteID, err := parseID(id)
	if err != nil {
		return datastore.Note{}, err
	}

	det, err := ds.detection.GetWithRelations(ctx, noteID)
	if err != nil {
		return datastore.Note{}, err
	}

	return ds.detectionToNote(det), nil
}

// detectionToNote converts a v2 Detection to a legacy Note.
// Common name is looked up from the name maps which are built at startup.
func (ds *Datastore) detectionToNote(det *entities.Detection) datastore.Note {
	// Guard against nil detection to prevent panics
	if det == nil {
		return datastore.Note{}
	}

	scientificName := ""
	// Try to get scientific name from preloaded Label first.
	// Labels may contain legacy concatenated "ScientificName_CommonName" format,
	// so extract only the scientific name portion.
	if det.Label != nil && det.Label.ScientificName != "" {
		scientificName = detection.ExtractScientificName(det.Label.ScientificName)
	} else if det.LabelID > 0 && ds.label != nil {
		// Label not preloaded, fetch it from the repository
		ctx := context.Background()
		if label, err := ds.label.GetByID(ctx, det.LabelID); err == nil && label != nil {
			scientificName = detection.ExtractScientificName(label.ScientificName)
		}
	}

	// Look up common name from pre-built map, fallback to scientific name
	commonName := ds.resolveCommonName(scientificName)

	clipName := ""
	if det.ClipName != nil {
		clipName = *det.ClipName
	}

	lat := 0.0
	if det.Latitude != nil {
		lat = *det.Latitude
	}
	lon := 0.0
	if det.Longitude != nil {
		lon = *det.Longitude
	}

	// Convert Unix timestamp to date and time strings
	t := time.Unix(det.DetectedAt, 0).In(ds.timezone)
	dateStr := t.Format(time.DateOnly)
	timeStr := t.Format(time.TimeOnly)

	// Populate virtual Verified field from Review
	verified := ""
	if det.Review != nil {
		verified = string(det.Review.Verified)
	}

	// Populate virtual Locked field from Lock presence
	locked := det.Lock != nil

	// Convert BeginTime/EndTime from Unix millis to time.Time
	var beginTime, endTime time.Time
	if det.BeginTime != nil {
		beginTime = time.UnixMilli(*det.BeginTime).In(ds.timezone)
	}
	if det.EndTime != nil {
		endTime = time.UnixMilli(*det.EndTime).In(ds.timezone)
	}

	// Convert ProcessingTimeMs to time.Duration
	var processingTime time.Duration
	if det.ProcessingTimeMs != nil {
		processingTime = time.Duration(*det.ProcessingTimeMs) * time.Millisecond
	}

	// Map Source entity to datastore.AudioSource
	var source datastore.AudioSource
	if det.Source != nil {
		displayName := ""
		if det.Source.DisplayName != nil {
			displayName = *det.Source.DisplayName
		}
		source = datastore.AudioSource{
			ID:          det.Source.SourceURI,
			SafeString:  det.Source.SourceURI,
			DisplayName: displayName,
		}
	}

	// Map Comments entities to datastore.NoteComment
	var comments []datastore.NoteComment
	if len(det.Comments) > 0 {
		comments = make([]datastore.NoteComment, len(det.Comments))
		for i, c := range det.Comments {
			comments[i] = datastore.NoteComment{
				ID:        c.ID,
				NoteID:    c.DetectionID,
				Entry:     c.Entry,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			}
		}
	}

	note := datastore.Note{
		ID:             det.ID,
		Date:           dateStr,
		Time:           timeStr,
		ScientificName: scientificName,
		CommonName:     commonName,
		SpeciesCode:    ds.speciesCodeMap[scientificName],
		Confidence:     det.Confidence,
		Latitude:       lat,
		Longitude:      lon,
		ClipName:       clipName,
		BeginTime:      beginTime,
		EndTime:        endTime,
		ProcessingTime: processingTime,
		Source:         source,
		Comments:       comments,
		Unlikely:       det.Unlikely,
		Verified:       verified,
		Locked:         locked,
	}

	// Populate model info from preloaded Model entity. ModelType is carried here
	// (from the batch-loaded ai_models relation) so API handlers can read it
	// directly instead of issuing a per-detection lookup (avoids N+1 on lists).
	if det.Model != nil {
		note.Model = detection.ModelInfo{
			Name:           det.Model.Name,
			Version:        det.Model.Version,
			Variant:        det.Model.Variant,
			ClassifierPath: det.Model.ClassifierPath,
			ModelType:      string(det.Model.ModelType),
		}
	}

	return note
}

// detectionsToNotes converts multiple detections to notes.
// Note: Common names are currently not stored in the normalized schema.
// They default to scientific names until a species lookup table is added.
func (ds *Datastore) detectionsToNotes(dets []*entities.Detection) []datastore.Note {
	if len(dets) == 0 {
		return []datastore.Note{}
	}

	// Convert detections to notes
	// Raw labels map is nil - common names will default to scientific names
	notes := make([]datastore.Note, 0, len(dets))
	for _, det := range dets {
		notes = append(notes, ds.detectionToNote(det))
	}
	return notes
}

// detectionToRecord converts a v2 Detection to a DetectionRecord.
func (ds *Datastore) detectionToRecord(det *entities.Detection) datastore.DetectionRecord {
	// Scientific name from Label.
	// Labels may contain legacy concatenated "ScientificName_CommonName" format,
	// so extract only the scientific name portion.
	scientificName := ""
	if det.Label != nil && det.Label.ScientificName != "" {
		scientificName = detection.ExtractScientificName(det.Label.ScientificName)
	}

	// Look up common name from pre-built map, fallback to scientific name
	commonName := ds.resolveCommonName(scientificName)

	// Timestamp conversion
	timestamp := time.Unix(det.DetectedAt, 0).In(ds.timezone)

	// Coordinates (nil-safe)
	lat := 0.0
	lon := 0.0
	if det.Latitude != nil {
		lat = *det.Latitude
	}
	if det.Longitude != nil {
		lon = *det.Longitude
	}

	// Week number from timestamp
	_, week := timestamp.ISOWeek()

	// Audio file path (nil-safe)
	audioFilePath := ""
	hasAudio := false
	if det.ClipName != nil && *det.ClipName != "" {
		audioFilePath = *det.ClipName
		hasAudio = true
	}

	// Verification status from Review.
	// Emit the same vocabulary the rest of the API speaks ("correct",
	// "false_positive", "unverified"), matching api.VerificationStatus*
	// constants and the /api/v2/detections response. Issue #2769 was caused by
	// this path emitting the literal "verified", which the frontend's
	// result.verified === 'correct' check did not recognize.
	verified := "unverified"
	if det.Review != nil {
		switch det.Review.Verified {
		case entities.VerificationCorrect:
			verified = string(entities.VerificationCorrect)
		case entities.VerificationFalsePositive:
			verified = string(entities.VerificationFalsePositive)
		}
	}

	// Lock status
	locked := det.Lock != nil

	// Device and Source from Source (the preloaded AudioSource)
	device := ""
	source := ""
	if det.Source != nil {
		device = det.Source.NodeName
		// Prefer DisplayName for human-readable source identification;
		// fall back to SourceType if no display name is configured.
		if det.Source.DisplayName != nil && *det.Source.DisplayName != "" {
			source = *det.Source.DisplayName
		} else {
			source = string(det.Source.SourceType)
		}
	}

	// TimeOfDay calculation
	timeOfDay := ds.calculateTimeOfDay(timestamp, lat, lon)

	// Model type from the preloaded Model entity (batch-loaded via
	// loadDetectionRelations), so the search UI can pick the correct spectrogram
	// frequency axis (bat vs bird) without a per-result lookup.
	modelType := ""
	if det.Model != nil {
		modelType = string(det.Model.ModelType)
	}

	return datastore.DetectionRecord{
		ID:             strconv.FormatUint(uint64(det.ID), 10),
		Timestamp:      timestamp,
		ScientificName: scientificName,
		CommonName:     commonName,
		Confidence:     det.Confidence,
		Latitude:       lat,
		Longitude:      lon,
		Week:           week,
		AudioFilePath:  audioFilePath,
		Verified:       verified,
		Locked:         locked,
		Unlikely:       det.Unlikely,
		HasAudio:       hasAudio,
		Device:         device,
		Source:         source,
		TimeOfDay:      timeOfDay,
		ModelType:      modelType,
	}
}

// detectionsToRecords converts multiple detections to DetectionRecords.
// Uses batch fetching of raw_labels to avoid N+1 query problem.
func (ds *Datastore) detectionsToRecords(dets []*entities.Detection) []datastore.DetectionRecord {
	if len(dets) == 0 {
		return []datastore.DetectionRecord{}
	}

	records := make([]datastore.DetectionRecord, 0, len(dets))
	for _, det := range dets {
		records = append(records, ds.detectionToRecord(det))
	}
	return records
}

// calculateTimeOfDay determines the time of day category for a detection.
// Uses the configured SunCalc instance with its observer location.
// The lat/lon parameters are used only to check if valid coordinates exist.
func (ds *Datastore) calculateTimeOfDay(timestamp time.Time, lat, lon float64) string {
	// If no valid coordinates on the detection, we can't determine time of day
	// Note: We use the global SunCalc's observer location for actual calculation
	if lat == 0 && lon == 0 {
		return datastore.TimeOfDayAny
	}

	// If no SunCalc available, return "any"
	if ds.suncalc == nil {
		return datastore.TimeOfDayAny
	}

	// Get sun events for the detection date using the configured observer location
	sunEvents, err := ds.suncalc.GetSunEventTimes(timestamp)
	if err != nil {
		return datastore.TimeOfDayAny
	}

	// Define 30-minute window around sunrise/sunset
	window := 30 * time.Minute

	// Get detection time as string for comparison (format: "15:04:05")
	detTime := timestamp.Format(time.TimeOnly)

	// Calculate window boundaries
	sunriseStart := sunEvents.Sunrise.Add(-window).Format(time.TimeOnly)
	sunriseEnd := sunEvents.Sunrise.Add(window).Format(time.TimeOnly)
	sunsetStart := sunEvents.Sunset.Add(-window).Format(time.TimeOnly)
	sunsetEnd := sunEvents.Sunset.Add(window).Format(time.TimeOnly)
	sunriseTime := sunEvents.Sunrise.Format(time.TimeOnly)
	sunsetTime := sunEvents.Sunset.Format(time.TimeOnly)

	// Determine time of day
	switch {
	case detTime >= sunriseStart && detTime <= sunriseEnd:
		return datastore.TimeOfDaySunrise
	case detTime >= sunsetStart && detTime <= sunsetEnd:
		return datastore.TimeOfDaySunset
	case detTime >= sunriseTime && detTime < sunsetTime:
		return datastore.TimeOfDayDay
	default:
		return datastore.TimeOfDayNight
	}
}

// GetAllNotes retrieves all notes.
func (ds *Datastore) GetAllNotes() ([]datastore.Note, error) {
	ctx := context.Background()
	filters := &repository.SearchFilters{
		Limit:    10000,
		SortBy:   "detected_at",
		SortDesc: true,
	}

	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	// Load relations (review, lock, label, source) for accurate virtual fields
	if err := ds.loadDetectionRelations(ctx, dets); err != nil {
		if ds.log != nil {
			ds.log.Debug("failed to load some detection relations", logger.Error(err))
		}
	}

	return ds.detectionsToNotes(dets), nil
}

// GetTopBirdsData retrieves top birds data for a date.
func (ds *Datastore) GetTopBirdsData(ctx context.Context, selectedDate string, minConfidenceNormalized float64, limit int) ([]datastore.Note, error) {
	t, err := time.ParseInLocation("2006-01-02", selectedDate, ds.timezone)
	if err != nil {
		return nil, err
	}
	startTime := t.Unix()
	endTime := t.AddDate(0, 0, 1).Unix()

	// Use provided limit or fall back to config value
	reportCount := limit
	if reportCount <= 0 {
		reportCount = conf.Setting().GetEffectiveSummaryLimit()
	}

	// Struct to hold aggregated results per species
	type speciesAggregate struct {
		ScientificName string  `gorm:"column:scientific_name"`
		Count          int     `gorm:"column:count"`
		MaxConfidence  float64 `gorm:"column:max_confidence"`
		LatestTime     int64   `gorm:"column:latest_time"`
	}

	var results []speciesAggregate

	// Query groups detections by species, counting occurrences and getting max confidence.
	// Uses Detection.LabelID/Confidence directly (primary prediction) rather than
	// detection_predictions table (which only stores secondary predictions).
	// Secondary sort by scientific_name ensures deterministic results when counts are equal.
	// Excludes detections marked as false_positive.
	prefix := ds.manager.TablePrefix()
	db := ds.manager.DB()
	err = db.WithContext(ctx).Table(prefix+"detections d").
		Select(`
			l.scientific_name,
			COUNT(d.id) as count,
			MAX(d.confidence) as max_confidence,
			MAX(d.detected_at) as latest_time
		`).
		Joins(fmt.Sprintf("JOIN %slabels l ON d.label_id = l.id", prefix)).
		Joins(fmt.Sprintf("LEFT JOIN %sdetection_reviews dr ON d.id = dr.detection_id", prefix)).
		Where("d.detected_at >= ? AND d.detected_at < ?", startTime, endTime).
		Where("d.confidence >= ?", minConfidenceNormalized).
		Where("(dr.verified IS NULL OR dr.verified != ?)", string(entities.VerificationFalsePositive)).
		Group("l.scientific_name").
		Order("count DESC, l.scientific_name ASC").
		Limit(reportCount).
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// Convert aggregated results to Notes
	notes := make([]datastore.Note, 0, len(results))
	for _, r := range results {
		// Format the latest time as HH:MM:SS
		latestTime := time.Unix(r.LatestTime, 0).In(ds.timezone)

		// Labels may contain legacy concatenated "ScientificName_CommonName" format,
		// so extract only the scientific name portion.
		sciName := detection.ExtractScientificName(r.ScientificName)

		// Look up common name from the cached map
		commonName := ds.resolveCommonName(sciName)

		note := datastore.Note{
			ScientificName: sciName,
			CommonName:     commonName,
			SpeciesCode:    ds.speciesCodeMap[sciName],
			Confidence:     r.MaxConfidence,
			Date:           selectedDate,
			Time:           latestTime.Format(time.TimeOnly),
		}
		notes = append(notes, note)
	}

	return notes, nil
}

// GetBatchHourlyOccurrences retrieves hourly detection counts for multiple species on a given date.
// The species parameter holds scientific names. Scientific names map directly to
// label IDs for every model, so no localized common-name round-trip is performed
// (that round-trip dropped non-primary-model species such as bats from the daily
// summary). The returned map is keyed by the same scientific names that were passed in.
//
// Label IDs are resolved in a single batched query (no per-species N+1) and the hourly
// counts are fetched in a single batched query, so this is two queries total regardless
// of the number of species. A failure in either query is returned to the caller rather
// than silently zeroing a species, so a cancelled context aborts the request instead of
// producing partial counts.
func (ds *Datastore) GetBatchHourlyOccurrences(ctx context.Context, startDate, endDate string, species []string, minConfidence float64) (map[string][24]int, error) {
	if len(species) == 0 {
		return make(map[string][24]int), nil
	}

	// Parse the inclusive range bounds.
	firstDate, err := time.ParseInLocation(time.DateOnly, startDate, ds.timezone)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_batch_hourly_occurrences").
			Context("start_date", startDate).
			Build()
	}
	lastDate, err := time.ParseInLocation(time.DateOnly, endDate, ds.timezone)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_batch_hourly_occurrences").
			Context("end_date", endDate).
			Build()
	}

	// Calculate the Unix timestamp range. endDate is inclusive, so the exclusive upper bound is
	// the start of the day *after* it. Calendar-based arithmetic handles DST transitions correctly.
	startOfDay := firstDate.Unix()
	endOfDay := lastDate.AddDate(0, 0, 1).Unix()

	// Resolve all scientific names to label IDs in one batched query (avoids the
	// per-species N+1 round-trip). The returned map is keyed by the stored scientific
	// name; results are re-keyed by the caller's input names below.
	labelsByName, err := ds.label.GetByScientificNames(ctx, species)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_batch_hourly_occurrences_labels").
			Build()
	}

	// Flatten label IDs across all requested species and build a reverse map from label
	// ID back to the caller's input scientific name. Keying by the input name (not the
	// stored label.ScientificName) preserves the exact map contract the caller relies on
	// (it looks up results by note.ScientificName).
	flatLabelIDs := make([]uint, 0, len(species))
	labelToScientificName := make(map[uint]string) // labelID -> input scientific name
	for _, scientificName := range species {
		for _, label := range labelsByName[scientificName] {
			flatLabelIDs = append(flatLabelIDs, label.ID)
			labelToScientificName[label.ID] = scientificName
		}
	}

	// Initialize all requested species with zero counts so callers always get an entry.
	resultMap := make(map[string][24]int, len(species))
	for _, scientificName := range species {
		resultMap[scientificName] = [24]int{}
	}

	// No matching labels: every requested species has zero detections.
	if len(flatLabelIDs) == 0 {
		return resultMap, nil
	}

	// Fetch per-label hourly counts (each query is chunked internally). The hour bucket is computed
	// from a single fixed UTC offset, so the range is split at every zone transition and each part
	// queried with its own offset: a range spanning a DST change would otherwise bucket everything
	// after the transition an hour out. Most ranges yield exactly one segment.
	hourlyByLabel := make(map[uint][24]int, len(flatLabelIDs))
	for _, segment := range ds.splitByZoneOffset(startOfDay, endOfDay) {
		part, err := ds.detection.GetBatchHourlyOccurrences(ctx, flatLabelIDs, segment.start, segment.end, segment.offset, minConfidence)
		if err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_batch_hourly_occurrences").
				Build()
		}
		// Range over keys only to avoid copying the 192-byte [24]int value on every iteration.
		for labelID := range part {
			hours := part[labelID]
			acc := hourlyByLabel[labelID]
			for h := range hoursPerDay {
				acc[h] += hours[h]
			}
			hourlyByLabel[labelID] = acc
		}
	}

	// Aggregate per-label counts into per-species counts, keyed by the input scientific
	// name. Multiple label IDs (one per model) can map to the same species. Range over
	// keys only to avoid copying the 192-byte [24]int value on every iteration.
	for labelID := range hourlyByLabel {
		scientificName, ok := labelToScientificName[labelID]
		if !ok {
			continue
		}
		hours := hourlyByLabel[labelID]
		hourlyData := resultMap[scientificName]
		for h := range 24 {
			hourlyData[h] += hours[h]
		}
		resultMap[scientificName] = hourlyData
	}

	return resultMap, nil
}

// SpeciesDetections retrieves detections for a species.
// The species parameter is expected to be a scientific name.
func (ds *Datastore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	ctx := context.Background()

	var startTime, endTime *int64
	if date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
		if err == nil {
			if hour != "" {
				// Specific hour requested - apply hour+duration filter
				h, err := parseHour(hour)
				if err != nil {
					return nil, err
				}
				t = t.Add(time.Duration(h) * time.Hour)
				start := t.Unix()
				end := t.Add(time.Duration(duration) * time.Hour).Unix()
				if duration == 0 {
					end = t.Add(1 * time.Hour).Unix()
				}
				startTime = &start
				endTime = &end
			} else {
				// No hour specified - search the full day (matches legacy behavior)
				start := t.Unix()
				end := t.AddDate(0, 0, 1).Unix()
				startTime = &start
				endTime = &end
			}
		}
	}

	var labelIDs []uint
	if species != "" {
		// Species is now always scientific name - query directly
		ids, err := ds.label.GetLabelIDsByScientificName(ctx, species)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			// Species not found - return empty results instead of all detections
			return []datastore.Note{}, nil
		}
		labelIDs = ids
	}

	filters := &repository.SearchFilters{
		LabelIDs:  labelIDs,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     limit,
		Offset:    offset,
		SortBy:    "detected_at",
		SortDesc:  !sortAscending,
	}

	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	// Load relations (label, source, review, lock, comments) for proper Note conversion
	if err := ds.loadDetectionRelations(ctx, dets); err != nil {
		if ds.log != nil {
			ds.log.Debug("failed to load some detection relations", logger.Error(err))
		}
	}

	return ds.detectionsToNotes(dets), nil
}

// GetLastDetections retrieves the last N detections.
func (ds *Datastore) GetLastDetections(numDetections int) ([]datastore.Note, error) {
	ctx := context.Background()
	dets, err := ds.detection.GetRecent(ctx, numDetections)
	if err != nil {
		return nil, err
	}
	return ds.detectionsToNotes(dets), nil
}

// GetAllDetectedSpecies retrieves all detected species.
func (ds *Datastore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	ctx := context.Background()
	labelIDs, err := ds.detection.GetAllDetectedLabels(ctx)
	if err != nil {
		return nil, err
	}
	if len(labelIDs) == 0 {
		return []datastore.Note{}, nil
	}

	labels, err := ds.label.GetByIDs(ctx, labelIDs)
	if err != nil {
		return nil, err
	}

	// Use a map to deduplicate by scientific name (since labels are per-model).
	// Labels may contain legacy concatenated "ScientificName_CommonName" format,
	// so extract only the scientific name portion.
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if label == nil {
			continue
		}
		if label.LabelTypeID != ds.speciesLabelTypeID {
			continue
		}
		sciName := detection.ExtractScientificName(label.ScientificName)
		if sciName != "" {
			seen[sciName] = struct{}{}
		}
	}

	names := slices.Collect(maps.Keys(seen))
	slices.Sort(names)

	notes := make([]datastore.Note, 0, len(names))
	for _, sciName := range names {
		notes = append(notes, datastore.Note{
			ScientificName: sciName,
		})
	}
	return notes, nil
}

// SearchNotes searches notes by query string.
// Returns the matching notes, the total count of matching records (before pagination), and any error.
func (ds *Datastore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]datastore.Note, int64, error) {
	ctx := context.Background()
	filters := &repository.SearchFilters{
		Query:    query,
		Limit:    limit,
		Offset:   offset,
		SortBy:   "detected_at",
		SortDesc: !sortAscending,
	}

	// Resolve common names (active locale) so the free-text search matches both scientific and
	// common names. Scientific names stay on the unbounded LIKE via filters.Query; common-name
	// matches are OR-ed in via CommonLabelIDs. See issue #3378.
	commonIDs, err := repository.ResolveCommonNameToLabelIDs(ctx, ds.filterLookupDeps(), query)
	if err != nil {
		return nil, 0, err
	}
	filters.CommonLabelIDs = commonIDs

	dets, total, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, 0, err
	}

	// Load relations (review, lock, label, source) for accurate virtual fields
	if err := ds.loadDetectionRelations(ctx, dets); err != nil {
		if ds.log != nil {
			ds.log.Debug("failed to load some detection relations", logger.Error(err))
		}
	}

	return ds.detectionsToNotes(dets), total, nil
}

// SearchNotesAdvanced performs advanced search with filters.
// Converts all AdvancedSearchFilters fields to repository SearchFilters.
func (ds *Datastore) SearchNotesAdvanced(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	ctx := context.Background()

	// Set up dependencies for entity lookups. The name maps enable common-name resolution for the
	// free-text query (active locale), matching the dashboard search behavior. See issue #3378.
	deps := ds.filterLookupDeps()

	// Convert API-level filters to repository filters
	repoFilters, err := repository.ConvertAdvancedFilters(ctx, filters, deps, ds.timezone)
	if err != nil {
		return nil, 0, err
	}

	// Execute search
	dets, total, err := ds.detection.Search(ctx, repoFilters)
	if err != nil {
		return nil, 0, err
	}

	// Load relations (review, lock, label, source) for accurate virtual fields
	if err := ds.loadDetectionRelations(ctx, dets); err != nil {
		if ds.log != nil {
			ds.log.Debug("failed to load some detection relations", logger.Error(err))
		}
	}

	return ds.detectionsToNotes(dets), total, nil
}

// GetNoteClipPath retrieves the clip path for a note.
func (ds *Datastore) GetNoteClipPath(noteID string) (string, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return "", err
	}
	return ds.detection.GetClipPath(ctx, id)
}

// GetNoteModelType returns the AI model type for a detection by note ID.
// It JOINs detections with ai_models to retrieve the model_type field.
// Returns "bird" as default if the model type cannot be determined.
func (ds *Datastore) GetNoteModelType(noteID string) (string, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return "", fmt.Errorf("invalid note ID for model type lookup: %w", err)
	}
	return ds.detection.GetModelType(ctx, id)
}

// DeleteNoteClipPath deletes the clip path for a note.
func (ds *Datastore) DeleteNoteClipPath(noteID string) error {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return err
	}
	return ds.detection.Update(ctx, id, map[string]any{"clip_name": nil})
}

// GetNoteReview retrieves the review for a note.
func (ds *Datastore) GetNoteReview(noteID string) (*datastore.NoteReview, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	review, err := ds.detection.GetReview(ctx, id)
	if err != nil {
		return nil, err
	}

	return &datastore.NoteReview{
		ID:        review.ID,
		NoteID:    id,
		Verified:  string(review.Verified),
		CreatedAt: review.CreatedAt,
		UpdatedAt: review.UpdatedAt,
	}, nil
}

// SaveNoteReview saves a review for a note.
func (ds *Datastore) SaveNoteReview(review *datastore.NoteReview) error {
	ctx := context.Background()

	v2Review := &entities.DetectionReview{
		DetectionID: review.NoteID,
		Verified:    entities.VerificationStatus(review.Verified),
	}

	return ds.detection.SaveReview(ctx, v2Review)
}

// GetNoteComments retrieves comments for a note.
func (ds *Datastore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	comments, err := ds.detection.GetComments(ctx, id)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.NoteComment, 0, len(comments))
	for _, c := range comments {
		result = append(result, datastore.NoteComment{
			ID:        c.ID,
			NoteID:    id,
			Entry:     c.Entry,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	return result, nil
}

// GetNoteResults retrieves additional predictions for a note.
func (ds *Datastore) GetNoteResults(noteID string) ([]datastore.Results, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	preds, err := ds.detection.GetPredictions(ctx, id)
	if err != nil {
		return nil, err
	}

	// Batch-load all labels for predictions to avoid N+1 queries
	labelIDSet := make(map[uint]struct{}, len(preds))
	for _, pred := range preds {
		labelIDSet[pred.LabelID] = struct{}{}
	}
	labelIDs := slices.Collect(maps.Keys(labelIDSet))

	labelMap, labelErr := ds.label.GetByIDs(ctx, labelIDs)
	if labelErr != nil {
		if ds.log != nil {
			ds.log.Warn("failed to batch-load labels for predictions", logger.Error(labelErr))
		}
		labelMap = make(map[uint]*entities.Label) // fallback to empty map
	}

	results := make([]datastore.Results, 0, len(preds))
	for _, pred := range preds {
		scientificName := ""
		if label, ok := labelMap[pred.LabelID]; ok && label.ScientificName != "" {
			scientificName = detection.ExtractScientificName(label.ScientificName)
		}

		results = append(results, datastore.Results{
			ID:         pred.ID,
			Species:    scientificName,
			Confidence: float32(pred.Confidence),
		})
	}

	return results, nil
}

// GetAllReviews returns all reviews (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetAllReviews() ([]datastore.NoteReview, error) {
	return nil, nil
}

// GetAllComments returns all comments (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetAllComments() ([]datastore.NoteComment, error) {
	return nil, nil
}

// GetAllLocks returns all locks (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetAllLocks() ([]datastore.NoteLock, error) {
	return nil, nil
}

// GetAllResults returns all results (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetAllResults() ([]datastore.Results, error) {
	return nil, nil
}

// GetReviewsBatch returns a batch of reviews (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetReviewsBatch(afterID uint, batchSize int) ([]datastore.NoteReview, error) {
	return nil, nil
}

// GetCommentsBatch returns a batch of comments (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetCommentsBatch(afterID uint, batchSize int) ([]datastore.NoteComment, error) {
	return nil, nil
}

// GetLocksBatch returns a batch of locks (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetLocksBatch(afterID uint, batchSize int) ([]datastore.NoteLock, error) {
	return nil, nil
}

// GetResultsBatch returns a batch of results (for migration - v2only returns empty as no migration needed).
func (ds *Datastore) GetResultsBatch(afterNoteID, afterResultID uint, batchSize int) ([]datastore.Results, error) {
	return nil, nil
}

// CountResults returns the total number of secondary predictions.
// In v2-only mode, this returns 0 since there's no legacy data to count.
func (ds *Datastore) CountResults() (int64, error) {
	return 0, nil
}

// SaveNoteComment saves a comment for a note.
func (ds *Datastore) SaveNoteComment(comment *datastore.NoteComment) error {
	ctx := context.Background()

	v2Comment := &entities.DetectionComment{
		DetectionID: comment.NoteID,
		Entry:       comment.Entry,
		CreatedAt:   comment.CreatedAt,
	}

	return ds.detection.SaveComment(ctx, v2Comment)
}

// UpdateNoteComment updates a comment.
func (ds *Datastore) UpdateNoteComment(commentID, entry string) error {
	ctx := context.Background()
	id, err := parseID(commentID)
	if err != nil {
		return err
	}
	return ds.detection.UpdateComment(ctx, id, entry)
}

// DeleteNoteComment deletes a comment.
func (ds *Datastore) DeleteNoteComment(commentID string) error {
	ctx := context.Background()
	id, err := parseID(commentID)
	if err != nil {
		return err
	}
	return ds.detection.DeleteComment(ctx, id)
}

// ============================================================
// Weather Methods
// ============================================================

// SaveDailyEvents saves daily weather events.
func (ds *Datastore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error {
	if ds.weather == nil {
		return fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	v2Events := &entities.DailyEvents{
		Date:     dailyEvents.Date,
		Sunrise:  dailyEvents.Sunrise,
		Sunset:   dailyEvents.Sunset,
		Country:  dailyEvents.Country,
		CityName: dailyEvents.CityName,
	}
	if err := ds.weather.SaveDailyEvents(ctx, v2Events); err != nil {
		return err
	}
	if v2Events.ID == 0 {
		return fmt.Errorf("SaveDailyEvents: repository returned success but entity ID was not populated")
	}
	// Propagate the auto-generated ID back to the caller so that
	// subsequent SaveHourlyWeather calls can set DailyEventsID correctly.
	dailyEvents.ID = v2Events.ID
	return nil
}

// GetDailyEvents retrieves daily events for a date.
func (ds *Datastore) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	if ds.weather == nil {
		return datastore.DailyEvents{}, fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	events, err := ds.weather.GetDailyEvents(ctx, date)
	if err != nil {
		return datastore.DailyEvents{}, err
	}
	return datastore.DailyEvents{
		ID:       events.ID,
		Date:     events.Date,
		Sunrise:  events.Sunrise,
		Sunset:   events.Sunset,
		Country:  events.Country,
		CityName: events.CityName,
	}, nil
}

// GetAllDailyEvents returns all daily events (used for migration, not needed in v2-only mode).
func (ds *Datastore) GetAllDailyEvents() ([]datastore.DailyEvents, error) {
	return nil, fmt.Errorf("GetAllDailyEvents: %w", ErrOperationNotSupported)
}

// GetAllHourlyWeather returns all hourly weather (used for migration, not needed in v2-only mode).
func (ds *Datastore) GetAllHourlyWeather() ([]datastore.HourlyWeather, error) {
	return nil, fmt.Errorf("GetAllHourlyWeather: %w", ErrOperationNotSupported)
}

// SaveHourlyWeather saves hourly weather data.
func (ds *Datastore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error {
	if hourlyWeather == nil {
		return fmt.Errorf("hourly weather cannot be nil")
	}
	if ds.weather == nil {
		return fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	v2Weather := &entities.HourlyWeather{
		DailyEventsID:     hourlyWeather.DailyEventsID,
		Time:              hourlyWeather.Time,
		Temperature:       hourlyWeather.Temperature,
		FeelsLike:         hourlyWeather.FeelsLike,
		TempMin:           hourlyWeather.TempMin,
		TempMax:           hourlyWeather.TempMax,
		Pressure:          hourlyWeather.Pressure,
		Humidity:          hourlyWeather.Humidity,
		Visibility:        hourlyWeather.Visibility,
		WindSpeed:         hourlyWeather.WindSpeed,
		WindDeg:           hourlyWeather.WindDeg,
		WindGust:          hourlyWeather.WindGust,
		Clouds:            hourlyWeather.Clouds,
		Precipitation:     hourlyWeather.Precipitation,
		PrecipitationType: hourlyWeather.PrecipitationType,
		WeatherMain:       hourlyWeather.WeatherMain,
		WeatherDesc:       hourlyWeather.WeatherDesc,
		WeatherIcon:       hourlyWeather.WeatherIcon,
	}
	return ds.weather.SaveHourlyWeather(ctx, v2Weather)
}

// GetHourlyWeather retrieves hourly weather for a date.
func (ds *Datastore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) {
	if ds.weather == nil {
		return nil, fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	v2Weather, err := ds.weather.GetHourlyWeather(ctx, date)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.HourlyWeather, 0, len(v2Weather))
	for i := range v2Weather {
		w := &v2Weather[i]
		result = append(result, datastore.HourlyWeather{
			ID:                w.ID,
			DailyEventsID:     w.DailyEventsID,
			Time:              w.Time,
			Temperature:       w.Temperature,
			FeelsLike:         w.FeelsLike,
			TempMin:           w.TempMin,
			TempMax:           w.TempMax,
			Pressure:          w.Pressure,
			Humidity:          w.Humidity,
			Visibility:        w.Visibility,
			WindSpeed:         w.WindSpeed,
			WindDeg:           w.WindDeg,
			WindGust:          w.WindGust,
			Clouds:            w.Clouds,
			Precipitation:     w.Precipitation,
			PrecipitationType: w.PrecipitationType,
			WeatherMain:       w.WeatherMain,
			WeatherDesc:       w.WeatherDesc,
			WeatherIcon:       w.WeatherIcon,
		})
	}
	return result, nil
}

// LatestHourlyWeather retrieves the most recent hourly weather record.
func (ds *Datastore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	if ds.weather == nil {
		return nil, fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	w, err := ds.weather.LatestHourlyWeather(ctx)
	if err != nil {
		return nil, err
	}
	return &datastore.HourlyWeather{
		ID:                w.ID,
		DailyEventsID:     w.DailyEventsID,
		Time:              w.Time,
		Temperature:       w.Temperature,
		FeelsLike:         w.FeelsLike,
		TempMin:           w.TempMin,
		TempMax:           w.TempMax,
		Pressure:          w.Pressure,
		Humidity:          w.Humidity,
		Visibility:        w.Visibility,
		WindSpeed:         w.WindSpeed,
		WindDeg:           w.WindDeg,
		WindGust:          w.WindGust,
		Clouds:            w.Clouds,
		Precipitation:     w.Precipitation,
		PrecipitationType: w.PrecipitationType,
		WeatherMain:       w.WeatherMain,
		WeatherDesc:       w.WeatherDesc,
		WeatherIcon:       w.WeatherIcon,
	}, nil
}

// ============================================================
// Detection Count/Search Methods
// ============================================================

// GetHourlyDetections retrieves detections for a specific hour.
func (ds *Datastore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	ctx := context.Background()
	t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
	if err != nil {
		return nil, err
	}
	h, err := parseHour(hour)
	if err != nil {
		return nil, err
	}
	t = t.Add(time.Duration(h) * time.Hour)
	startTime := t.Unix()
	endTime := t.Add(time.Duration(duration) * time.Hour).Unix()

	filters := &repository.SearchFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
		Limit:     limit,
		Offset:    offset,
		SortBy:    "detected_at",
		SortDesc:  true,
	}
	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	// Load relations (label, source, review, lock, comments) for proper Note conversion
	if err := ds.loadDetectionRelations(ctx, dets); err != nil {
		if ds.log != nil {
			ds.log.Debug("failed to load some detection relations", logger.Error(err))
		}
	}

	return ds.detectionsToNotes(dets), nil
}

// CountSpeciesDetections counts detections for a species.
// The species parameter is expected to be a scientific name.
func (ds *Datastore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	ctx := context.Background()
	var startTime, endTime *int64
	if date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
		if err == nil {
			if hour != "" {
				// Specific hour requested - apply hour+duration filter
				h, err := parseHour(hour)
				if err != nil {
					return 0, err
				}
				t = t.Add(time.Duration(h) * time.Hour)
				start := t.Unix()
				end := t.Add(time.Duration(duration) * time.Hour).Unix()
				if duration == 0 {
					end = t.Add(1 * time.Hour).Unix()
				}
				startTime = &start
				endTime = &end
			} else {
				// No hour specified - search the full day (matches legacy behavior)
				start := t.Unix()
				end := t.AddDate(0, 0, 1).Unix()
				startTime = &start
				endTime = &end
			}
		}
	}

	var labelIDs []uint
	if species != "" {
		// Species is now always scientific name - query directly
		ids, err := ds.label.GetLabelIDsByScientificName(ctx, species)
		if err != nil {
			return 0, err
		}
		if len(ids) == 0 {
			// Species not found - return zero count instead of all detections count
			return 0, nil
		}
		labelIDs = ids
	}

	filters := &repository.SearchFilters{
		LabelIDs:  labelIDs,
		StartTime: startTime,
		EndTime:   endTime,
	}
	_, count, err := ds.detection.Search(ctx, filters)
	return count, err
}

// CountHourlyDetections counts detections for a specific hour.
func (ds *Datastore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	ctx := context.Background()
	t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
	if err != nil {
		return 0, err
	}
	h, err := parseHour(hour)
	if err != nil {
		return 0, err
	}
	t = t.Add(time.Duration(h) * time.Hour)
	startTime := t.Unix()
	endTime := t.Add(time.Duration(duration) * time.Hour).Unix()

	filters := &repository.SearchFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
	}
	_, count, err := ds.detection.Search(ctx, filters)
	return count, err
}

// SearchDetections performs a detection search with filters.
func (ds *Datastore) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	ctx := filters.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	// Note: Validation is handled by ConvertSearchFilters which applies defaults
	// for Page, PerPage, ConfidenceMax, etc.

	// Set up dependencies for entity lookups (species/common-name and device resolution).
	deps := ds.filterLookupDeps()

	// Convert API-level filters to repository filters
	repoFilters, err := repository.ConvertSearchFilters(ctx, filters, deps, ds.timezone)
	if err != nil {
		return nil, 0, err
	}

	// Execute search
	dets, total, err := ds.detection.Search(ctx, repoFilters)
	if err != nil {
		return nil, 0, err
	}

	if len(dets) == 0 {
		return []datastore.DetectionRecord{}, int(total), nil
	}

	// Batch load relations for the detections
	if err := ds.loadDetectionRelations(ctx, dets); err != nil {
		// Log but continue - some relations may still be usable
		if ds.log != nil {
			ds.log.Debug("failed to load some detection relations", logger.Error(err))
		}
	}

	// Convert to DetectionRecord format
	records := ds.detectionsToRecords(dets)

	return records, int(total), nil
}

// loadDetectionRelations loads Label, Source, Model, Review, Lock, and Comments for detections.
// Uses batch queries to minimize database round-trips.
func (ds *Datastore) loadDetectionRelations(ctx context.Context, dets []*entities.Detection) error {
	if len(dets) == 0 {
		return nil
	}

	// Collect IDs for batch loading
	detectionIDs := make([]uint, len(dets))
	labelIDSet := make(map[uint]struct{})
	sourceIDSet := make(map[uint]struct{})
	modelIDSet := make(map[uint]struct{})

	for i, det := range dets {
		detectionIDs[i] = det.ID
		labelIDSet[det.LabelID] = struct{}{}
		modelIDSet[det.ModelID] = struct{}{}
		if det.SourceID != nil {
			sourceIDSet[*det.SourceID] = struct{}{}
		}
	}

	// Convert sets to slices
	labelIDs := slices.Collect(maps.Keys(labelIDSet))
	sourceIDs := slices.Collect(maps.Keys(sourceIDSet))

	// Batch load all relations (nil-safe for partially initialized datastores)
	var labelMap map[uint]*entities.Label
	if ds.label != nil {
		var err error
		labelMap, err = ds.label.GetByIDs(ctx, labelIDs)
		if err != nil {
			return fmt.Errorf("load labels: %w", err)
		}
	}

	var sourceMap map[uint]*entities.AudioSource
	if ds.source != nil {
		var err error
		sourceMap, err = ds.source.GetByIDs(ctx, sourceIDs)
		if err != nil {
			return fmt.Errorf("load sources: %w", err)
		}
	}

	modelIDs := slices.Collect(maps.Keys(modelIDSet))
	var modelMap map[uint]*entities.AIModel
	if ds.model != nil {
		var err error
		modelMap, err = ds.model.GetByIDs(ctx, modelIDs)
		if err != nil {
			return fmt.Errorf("load models: %w", err)
		}
	}

	reviewMap, err := ds.detection.GetReviewsByDetectionIDs(ctx, detectionIDs)
	if err != nil {
		return fmt.Errorf("load reviews: %w", err)
	}

	lockMap, err := ds.detection.GetLocksByDetectionIDs(ctx, detectionIDs)
	if err != nil {
		return fmt.Errorf("load locks: %w", err)
	}

	commentMap, err := ds.detection.GetCommentsByDetectionIDs(ctx, detectionIDs)
	if err != nil {
		return fmt.Errorf("load comments: %w", err)
	}

	// Assign loaded relations to detections
	for _, det := range dets {
		if label, ok := labelMap[det.LabelID]; ok {
			det.Label = label
		}
		if det.SourceID != nil {
			if source, ok := sourceMap[*det.SourceID]; ok {
				det.Source = source
			}
		}
		if review, ok := reviewMap[det.ID]; ok {
			det.Review = review
		}
		if lockMap[det.ID] {
			det.Lock = &entities.DetectionLock{DetectionID: det.ID}
		}
		if comments, ok := commentMap[det.ID]; ok {
			det.Comments = comments
		}
		if model, ok := modelMap[det.ModelID]; ok {
			det.Model = model
		}
	}

	return nil
}

// ============================================================
// Lock Methods
// ============================================================

// LockNote locks a note.
func (ds *Datastore) LockNote(noteID string) error {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return err
	}
	return ds.detection.Lock(ctx, id)
}

// UnlockNote unlocks a note.
func (ds *Datastore) UnlockNote(noteID string) error {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return err
	}
	return ds.detection.Unlock(ctx, id)
}

// GetNoteLock retrieves the lock for a note.
func (ds *Datastore) GetNoteLock(noteID string) (*datastore.NoteLock, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	// Single query to get the lock - check ErrRecordNotFound for missing lock
	var lock entities.DetectionLock
	err = ds.manager.DB().WithContext(ctx).
		Where("detection_id = ?", id).
		First(&lock).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, datastore.ErrNoteLockNotFound
		}
		return nil, err
	}

	return &datastore.NoteLock{
		ID:       lock.ID,
		NoteID:   id,
		LockedAt: lock.LockedAt,
	}, nil
}

// IsNoteLocked checks if a note is locked.
func (ds *Datastore) IsNoteLocked(noteID string) (bool, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return false, err
	}
	return ds.detection.IsLocked(ctx, id)
}

// GetLockedNotesClipPaths retrieves clip paths for locked notes.
func (ds *Datastore) GetLockedNotesClipPaths() ([]string, error) {
	ctx := context.Background()
	return ds.detection.GetLockedClipPaths(ctx)
}

// ClearNoteClipPathsByNames clears the clip_name field for detections matching the given filenames.
// Updates are batched to stay within SQLite's parameter limit (999).
func (ds *Datastore) ClearNoteClipPathsByNames(clipNames []string) (int64, error) {
	if len(clipNames) == 0 {
		return 0, nil
	}

	const batchSize = 500
	var totalAffected int64
	ctx := context.Background()
	detectionsTable := ds.manager.TablePrefix() + "detections"

	for i := 0; i < len(clipNames); i += batchSize {
		end := min(i+batchSize, len(clipNames))
		batch := clipNames[i:end]

		result := ds.manager.DB().WithContext(ctx).
			Table(detectionsTable).
			Where("clip_name IN ?", batch).
			Update("clip_name", nil)
		if result.Error != nil {
			return totalAffected, fmt.Errorf("failed to clear clip paths for %d names: %w", len(batch), result.Error)
		}
		totalAffected += result.RowsAffected
	}

	return totalAffected, nil
}

// GetNoteClipReferences returns up to limit detections with a non-empty clip_name
// and ID greater than afterID, ordered by ID ascending (keyset pagination). It is
// used by the clip reconcile crawler to walk clip references in bounded chunks.
//
// CompletionTime keys on the end_time column, which stores the capture COMPLETION
// time as an absolute Unix-millis value (Save writes note.EndTime.UnixMilli(); the
// entity field's "offset from source start" comment is stale). This matches the v1
// store's use of Note.EndTime and the media grace-poll, so the crawler's recency
// guard protects a clip until its capture actually completes, even for extended
// captures. When end_time is NULL, fall back to detected_at (detection time); such
// rows are older detections without a recorded end, safe to treat as long-complete.
func (ds *Datastore) GetNoteClipReferences(afterID uint, limit int) ([]diskmanager.ClipReference, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive: %d", limit)
	}

	ctx := context.Background()
	detectionsTable := ds.manager.TablePrefix() + "detections"

	var rows []struct {
		ID         uint
		ClipName   *string
		DetectedAt int64
		EndTime    *int64
	}
	err := ds.manager.DB().WithContext(ctx).
		Table(detectionsTable).
		Select("id", "clip_name", "detected_at", "end_time").
		Where("id > ? AND clip_name IS NOT NULL AND clip_name <> ''", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get clip references after id %d: %w", afterID, err)
	}

	refs := make([]diskmanager.ClipReference, 0, len(rows))
	for i := range rows {
		clip := ""
		if rows[i].ClipName != nil {
			clip = *rows[i].ClipName
		}
		// Prefer end_time (absolute completion, ms); fall back to detected_at (s).
		var completion time.Time
		switch {
		case rows[i].EndTime != nil:
			completion = time.UnixMilli(*rows[i].EndTime)
		case rows[i].DetectedAt > 0:
			completion = time.Unix(rows[i].DetectedAt, 0)
		}
		refs = append(refs, diskmanager.ClipReference{
			ID:             rows[i].ID,
			ClipName:       clip,
			CompletionTime: completion,
		})
	}
	return refs, nil
}

// ============================================================
// Image Cache Methods
// ============================================================

// imageCacheScientificName extracts the scientific name from an image cache's label.
// Handles legacy concatenated "ScientificName_CommonName" format.
func imageCacheScientificName(cache *entities.ImageCache) string {
	if cache.Label != nil && cache.Label.ScientificName != "" {
		return detection.ExtractScientificName(cache.Label.ScientificName)
	}
	return ""
}

// GetImageCache retrieves an image cache entry.
func (ds *Datastore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	if ds.imageCache == nil {
		return nil, datastore.ErrImageCacheNotFound
	}
	ctx := context.Background()
	cache, err := ds.imageCache.GetImageCache(ctx, query.ProviderName, query.ScientificName)
	if err != nil {
		// Convert repository-level error to datastore-level error for API consistency
		if errors.Is(err, repository.ErrImageCacheNotFound) {
			return nil, datastore.ErrImageCacheNotFound
		}
		return nil, err
	}
	return &datastore.ImageCache{
		ID:             cache.ID,
		ProviderName:   cache.ProviderName,
		ScientificName: imageCacheScientificName(cache),
		SourceProvider: cache.SourceProvider,
		URL:            cache.URL,
		LicenseName:    cache.LicenseName,
		LicenseURL:     cache.LicenseURL,
		AuthorName:     cache.AuthorName,
		AuthorURL:      cache.AuthorURL,
		CachedAt:       cache.CachedAt,
	}, nil
}

// GetImageCacheBatch retrieves multiple image cache entries.
func (ds *Datastore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) {
	if ds.imageCache == nil {
		return make(map[string]*datastore.ImageCache), nil
	}
	ctx := context.Background()
	v2Caches, err := ds.imageCache.GetImageCacheBatch(ctx, providerName, scientificNames)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*datastore.ImageCache)
	for name, cache := range v2Caches {
		result[name] = &datastore.ImageCache{
			ID:             cache.ID,
			ProviderName:   cache.ProviderName,
			ScientificName: imageCacheScientificName(cache),
			SourceProvider: cache.SourceProvider,
			URL:            cache.URL,
			LicenseName:    cache.LicenseName,
			LicenseURL:     cache.LicenseURL,
			AuthorName:     cache.AuthorName,
			AuthorURL:      cache.AuthorURL,
			CachedAt:       cache.CachedAt,
		}
	}
	return result, nil
}

// SaveImageCache saves an image cache entry.
// Resolves the scientific name to a label ID before saving.
func (ds *Datastore) SaveImageCache(cache *datastore.ImageCache) error {
	if ds.imageCache == nil {
		return fmt.Errorf("image cache repository not configured")
	}
	ctx := context.Background()

	// Resolve scientific name to label ID using default model
	label, err := ds.label.GetOrCreate(ctx, cache.ScientificName, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	if err != nil {
		return fmt.Errorf("failed to resolve label for image cache: %w", err)
	}

	v2Cache := &entities.ImageCache{
		ProviderName:   cache.ProviderName,
		LabelID:        label.ID,
		SourceProvider: cache.SourceProvider,
		URL:            cache.URL,
		LicenseName:    cache.LicenseName,
		LicenseURL:     cache.LicenseURL,
		AuthorName:     cache.AuthorName,
		AuthorURL:      cache.AuthorURL,
		CachedAt:       cache.CachedAt,
	}
	return ds.imageCache.SaveImageCache(ctx, v2Cache)
}

// GetAllImageCaches retrieves all image caches for a provider.
func (ds *Datastore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	if ds.imageCache == nil {
		return []datastore.ImageCache{}, nil
	}
	ctx := context.Background()
	v2Caches, err := ds.imageCache.GetAllImageCaches(ctx, providerName)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.ImageCache, 0, len(v2Caches))
	for i := range v2Caches {
		cache := &v2Caches[i]
		result = append(result, datastore.ImageCache{
			ID:             cache.ID,
			ProviderName:   cache.ProviderName,
			ScientificName: imageCacheScientificName(cache),
			SourceProvider: cache.SourceProvider,
			URL:            cache.URL,
			LicenseName:    cache.LicenseName,
			LicenseURL:     cache.LicenseURL,
			AuthorName:     cache.AuthorName,
			AuthorURL:      cache.AuthorURL,
			CachedAt:       cache.CachedAt,
		})
	}
	return result, nil
}

// ============================================================
// Analytics Methods
// ============================================================

// parseDateRange parses start and end date strings to Unix timestamps.
// When no dates are provided, returns (0, math.MaxInt64) to match all records.
// The end time is exclusive (start of next day) to be used with < in queries.
func (ds *Datastore) parseDateRange(startDate, endDate string) (start, end int64, err error) {
	if startDate != "" {
		t, parseErr := time.ParseInLocation("2006-01-02", startDate, ds.timezone)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid start date format: %w", parseErr)
		}
		start = t.Unix()
	}
	if endDate != "" {
		t, parseErr := time.ParseInLocation("2006-01-02", endDate, ds.timezone)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid end date format: %w", parseErr)
		}
		// End time is exclusive (start of next day) - use with < in queries
		end = t.AddDate(0, 0, 1).Unix()
	}

	// When no end date specified, use max int64 to include all records.
	// Without this, WHERE detected_at >= 0 AND detected_at < 0 matches nothing.
	if end == 0 {
		end = math.MaxInt64
	}

	return start, end, nil
}

// unixTimeOrZero converts a Unix epoch (seconds) to a time.Time in loc, returning the
// zero value for a non-positive epoch. A zero/negative epoch means "no detection time"
// rather than the 1970 epoch origin, so the API layer (formatTimeIfNotZero) renders it
// as an empty timestamp instead of 1970-01-01.
func unixTimeOrZero(epoch int64, loc *time.Location) time.Time {
	if epoch <= 0 {
		return time.Time{}
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Unix(epoch, 0).In(loc)
}

// zoneOffsetSeconds returns the configured timezone's UTC offset in seconds in effect at
// the given epoch. SQL hour bucketing adds this offset to detected_at so detections group
// by wall-clock hour in ds.timezone rather than the database/OS-local zone. Anchoring the
// offset to the queried epoch (rather than "now") keeps it correct for historical days.
//
// For an open-ended range parseDateRange yields start==0; anchoring to the 1970 epoch would
// pick an arbitrary historical offset, so non-positive epochs fall back to the current offset
// (the best single choice for an all-time range). The single-offset approach is still a DST
// approximation on multi-day ranges; see repository.GetTimezoneOffsetAt for that limitation.
func (ds *Datastore) zoneOffsetSeconds(epoch int64) int {
	ref := time.Unix(epoch, 0)
	if epoch <= 0 {
		ref = time.Now()
	}
	return repository.GetTimezoneOffsetAt(ds.timezone, ref)
}

// zoneOffsetSegment is a half-open [start, end) slice of a query range over which the configured
// timezone's UTC offset is constant, so the whole slice can be hour-bucketed with `offset`.
type zoneOffsetSegment struct {
	start, end int64 // Unix seconds
	offset     int   // seconds east of UTC, constant across the segment
}

// maxZoneSegments caps the segment slice's initial capacity: a year crosses at most a couple of
// DST transitions, so a handful of segments covers any realistic analytics range.
const maxZoneSegments = 4

// splitByZoneOffset divides [start, end) into segments whose UTC offset is constant.
//
// SQL hour bucketing applies one fixed offset to the whole query (see zoneOffsetSeconds), which is
// exact for a single day but wrong for a range spanning a DST change: every detection after the
// transition lands an hour off. Splitting at the transitions lets each part be bucketed with the
// offset actually in effect. A range with no transition returns a single segment, so the common
// case still issues exactly one query.
func (ds *Datastore) splitByZoneOffset(start, end int64) []zoneOffsetSegment {
	segments := make([]zoneOffsetSegment, 0, maxZoneSegments)
	for cur := start; cur < end; {
		at := time.Unix(cur, 0).In(ds.timezone)
		_, offset := at.Zone()

		// ZoneBounds reports when the current zone period ends; zero means it never does.
		segmentEnd := end
		if _, zoneEnd := at.ZoneBounds(); !zoneEnd.IsZero() && zoneEnd.Unix() < end {
			segmentEnd = zoneEnd.Unix()
		}
		// Defensive: a non-advancing bound would loop forever.
		if segmentEnd <= cur {
			segmentEnd = end
		}

		segments = append(segments, zoneOffsetSegment{start: cur, end: segmentEnd, offset: offset})
		cur = segmentEnd
	}
	return segments
}

// dateRangeOffsetAnchor returns the epoch to anchor the timezone offset to for a date-bucketed
// query over [start, end) (epochs from parseDateRange: start==0 means open-start, end==MaxInt64
// means open-end). It prefers the start boundary, falls back to the end boundary for a left-open
// range, and only as a last resort returns 0 (which zoneOffsetSeconds maps to the current offset)
// for a fully open range. Anchoring to a query boundary rather than "now" keeps an end-only
// historical query bucketing the same way regardless of when it runs.
func dateRangeOffsetAnchor(start, end int64) int64 {
	switch {
	case start > 0:
		return start
	case end > 0 && end != math.MaxInt64:
		return end
	default:
		return 0
	}
}

// detectionDateExpr returns a SQL expression for the wall-clock calendar date (YYYY-MM-DD) of
// d.detected_at in the configured timezone. offsetSeconds is added to the epoch before the date
// is taken, so the result buckets by date in ds.timezone and is independent of the database
// session / OS-local zone (the same offset-arithmetic approach as the hour bucketing). The
// MySQL form uses DATE_ADD on a literal date with an integer day count so it does not depend on
// the session time_zone; DATE(FROM_UNIXTIME(...)) would apply that zone on top of the offset and
// double-count. Integer DIV avoids floating-point rounding at exact day boundaries.
//
// SQLite: date(d.detected_at + offset, 'unixepoch')
// MySQL:  DATE_ADD('1970-01-01', INTERVAL (d.detected_at + offset) DIV 86400 DAY)
func (ds *Datastore) detectionDateExpr(offsetSeconds int) string {
	if ds.manager.IsMySQL() {
		return fmt.Sprintf("DATE_ADD('1970-01-01', INTERVAL (d.detected_at + %d) DIV 86400 DAY)", offsetSeconds)
	}
	return fmt.Sprintf("date(d.detected_at + %d, 'unixepoch')", offsetSeconds)
}

// GetSpeciesSummaryData retrieves species summary data.
func (ds *Datastore) GetSpeciesSummaryData(ctx context.Context, startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	v2Data, err := ds.detection.GetSpeciesSummary(ctx, start, end, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.SpeciesSummaryData, 0, len(v2Data))
	for _, d := range v2Data {
		// Labels may contain legacy concatenated "ScientificName_CommonName" format,
		// so extract only the scientific name portion.
		sciName := detection.ExtractScientificName(d.ScientificName)

		// Look up common name from pre-built map, fallback to scientific name
		commonName := ds.resolveCommonName(sciName)

		result = append(result, datastore.SpeciesSummaryData{
			ScientificName: sciName,
			CommonName:     commonName,
			SpeciesCode:    ds.speciesCodeMap[sciName],
			Count:          int(d.TotalDetections),
			FirstSeen:      unixTimeOrZero(d.FirstDetection, ds.timezone),
			LastSeen:       unixTimeOrZero(d.LastDetection, ds.timezone),
			AvgConfidence:  d.AvgConfidence,
			MaxConfidence:  d.MaxConfidence,
		})
	}
	return result, nil
}

// GetHourlyAnalyticsData retrieves hourly analytics data for a specific date and species.
func (ds *Datastore) GetHourlyAnalyticsData(ctx context.Context, date, species string) ([]datastore.HourlyAnalyticsData, error) {
	start, end, err := ds.parseDateRange(date, date)
	if err != nil {
		return nil, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []datastore.HourlyAnalyticsData{}, nil
		}
		return nil, err
	}

	v2Data, err := ds.detection.GetHourlyDistribution(ctx, start, end, ds.zoneOffsetSeconds(start), labelID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.HourlyAnalyticsData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.HourlyAnalyticsData{
			Hour:  d.Hour,
			Count: int(d.Count),
		})
	}
	return result, nil
}

// resolveLabelID looks up a label ID for a species name.
// Returns (nil, nil) if species is empty (no filter).
// Returns (nil, errNotFound) if species not found.
// Returns (&id, nil) if found.
// Returns (nil, err) for other errors.
var errNotFound = errors.NewStd("species not found")

func (ds *Datastore) resolveLabelID(ctx context.Context, species string) (*uint, error) {
	if species == "" {
		return nil, nil //nolint:nilnil // nil means no filter, which is valid
	}
	labelIDs, err := ds.label.GetLabelIDsByScientificName(ctx, species)
	if err != nil {
		return nil, err
	}
	if len(labelIDs) == 0 {
		return nil, errNotFound
	}
	return &labelIDs[0], nil
}

// GetDailyAnalyticsData retrieves daily analytics data.
func (ds *Datastore) GetDailyAnalyticsData(ctx context.Context, startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []datastore.DailyAnalyticsData{}, nil
		}
		return nil, err
	}

	// Bucket dates by the configured timezone, anchored to a query boundary (start, or end for a
	// left-open range) so an end-only historical query buckets stably regardless of run time.
	v2Data, err := ds.detection.GetDailyAnalytics(ctx, start, end, ds.zoneOffsetSeconds(dateRangeOffsetAnchor(start, end)), labelID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.DailyAnalyticsData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.DailyAnalyticsData{
			Date:  d.Date,
			Count: int(d.TotalDetections),
		})
	}
	return result, nil
}

// GetDetectionTrends retrieves detection trends.
func (ds *Datastore) GetDetectionTrends(ctx context.Context, period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	// Trends cover a trailing window ending now, so anchor the offset to the current time.
	v2Data, err := ds.detection.GetDetectionTrends(ctx, period, limit, ds.zoneOffsetSeconds(0), nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.DailyAnalyticsData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.DailyAnalyticsData{
			Date:  d.Date,
			Count: int(d.TotalDetections),
		})
	}
	return result, nil
}

// GetHourlyDistribution retrieves hourly distribution data.
func (ds *Datastore) GetHourlyDistribution(ctx context.Context, startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []datastore.HourlyDistributionData{}, nil
		}
		return nil, err
	}

	v2Data, err := ds.detection.GetHourlyDistribution(ctx, start, end, ds.zoneOffsetSeconds(start), labelID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.HourlyDistributionData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.HourlyDistributionData{
			Hour:  d.Hour,
			Count: int(d.Count),
		})
	}
	return result, nil
}

// speciesFirstSeenInfo holds the common fields for species first detection data.
type speciesFirstSeenInfo struct {
	LabelID        uint
	ScientificName string
	FirstDetected  int64
	LastDetected   int64
}

// convertToNewSpeciesData converts species first-seen data to NewSpeciesData with common name resolution.
func (ds *Datastore) convertToNewSpeciesData(_ context.Context, data []speciesFirstSeenInfo) []datastore.NewSpeciesData {
	if len(data) == 0 {
		return []datastore.NewSpeciesData{}
	}

	result := make([]datastore.NewSpeciesData, 0, len(data))
	for _, d := range data {
		// Labels may contain legacy concatenated "ScientificName_CommonName" format,
		// so extract only the scientific name portion.
		sciName := detection.ExtractScientificName(d.ScientificName)

		// Look up common name from pre-built map, fallback to scientific name
		commonName := ds.resolveCommonName(sciName)

		// A zero/negative epoch means "no detection date"; emit an empty string instead
		// of formatting the 1970 epoch origin (mirrors the LastDetected guard below).
		var firstSeenDate string
		if d.FirstDetected > 0 {
			firstSeenDate = time.Unix(d.FirstDetected, 0).In(ds.timezone).Format(time.DateOnly)
		}
		var lastSeenDate string
		if d.LastDetected > 0 {
			lastSeenDate = time.Unix(d.LastDetected, 0).In(ds.timezone).Format(time.DateOnly)
		}
		result = append(result, datastore.NewSpeciesData{
			ScientificName: sciName,
			CommonName:     commonName,
			FirstSeenDate:  firstSeenDate,
			LastSeenDate:   lastSeenDate,
			CountInPeriod:  0,
		})
	}
	return result
}

// GetNewSpeciesDetections retrieves new species detections (lifetime firsts).
func (ds *Datastore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	v2Data, err := ds.detection.GetNewSpecies(ctx, start, end, limit, offset)
	if err != nil {
		return nil, err
	}

	// Convert to common format
	data := make([]speciesFirstSeenInfo, len(v2Data))
	for i, d := range v2Data {
		data[i] = speciesFirstSeenInfo{
			LabelID:        d.LabelID,
			ScientificName: d.ScientificName,
			FirstDetected:  d.FirstDetected,
			LastDetected:   d.LastDetected,
		}
	}

	return ds.convertToNewSpeciesData(ctx, data), nil
}

// GetSpeciesFirstDetectionInPeriod retrieves first detection of species in a period.
func (ds *Datastore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	v2Data, err := ds.detection.GetSpeciesFirstDetectionInPeriod(ctx, start, end, limit, offset)
	if err != nil {
		return nil, err
	}

	// Convert to common format
	data := make([]speciesFirstSeenInfo, len(v2Data))
	for i, d := range v2Data {
		data[i] = speciesFirstSeenInfo{
			LabelID:        d.LabelID,
			ScientificName: d.ScientificName,
			FirstDetected:  d.FirstDetected,
		}
	}

	return ds.convertToNewSpeciesData(ctx, data), nil
}

// GetSpeciesDetectionDatesInPeriod returns distinct species/date pairs within a period.
func (ds *Datastore) GetSpeciesDetectionDatesInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.SpeciesDetectionDate, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10000
	}

	// Bucket dates by the configured timezone, anchored to a query boundary (start, or end for a
	// left-open range) so an end-only historical query buckets stably regardless of run time.
	dateExpr := ds.detectionDateExpr(ds.zoneOffsetSeconds(dateRangeOffsetAnchor(start, end)))

	type result struct {
		ScientificName string `gorm:"column:scientific_name"`
		Date           string `gorm:"column:date"`
	}
	var rows []result

	prefix := ds.manager.TablePrefix()
	query := ds.manager.DB().WithContext(ctx).
		Table(prefix+"detections d").
		Select(fmt.Sprintf("l.scientific_name as scientific_name, %s as date", dateExpr)).
		Joins(fmt.Sprintf("JOIN %slabels l ON d.label_id = l.id", prefix)).
		Joins(fmt.Sprintf("LEFT JOIN %sdetection_reviews dr ON d.id = dr.detection_id", prefix)).
		Where("d.detected_at >= ? AND d.detected_at < ?", start, end).
		Where("(dr.verified IS NULL OR dr.verified != ?)", string(entities.VerificationFalsePositive)).
		Group(fmt.Sprintf("l.scientific_name, %s", dateExpr)).
		Order("date ASC, l.scientific_name ASC").
		Limit(limit).
		Offset(offset)

	if err := query.Scan(&rows).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_detection_dates_in_period").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	results := make([]datastore.SpeciesDetectionDate, 0, len(rows))
	for _, row := range rows {
		scientificName := detection.ExtractScientificName(row.ScientificName)
		results = append(results, datastore.SpeciesDetectionDate{
			ScientificName: scientificName,
			CommonName:     ds.resolveCommonName(scientificName),
			Date:           row.Date,
		})
	}

	return results, nil
}

// scientificNameLikeEscaper escapes the LIKE metacharacters %, _, and the escape
// character itself in a user-supplied scientific name, using '!' as the escape
// character. '!' is not special in any SQL dialect's string literals, so the
// generated SQL is identical and valid on MySQL, SQLite, and Postgres. A backslash
// escape ('\') must NOT be used: MySQL's default sql_mode treats a lone backslash
// in a string literal as an escape character, so "ESCAPE '\'" swallows the closing
// quote and raises a syntax error (Error 1064). SQLite does not treat backslash as
// special, which is why that only broke MySQL.
//
// It is a package-level value because strings.Replacer precomputes its matcher and
// is safe for concurrent use, so there is no need to rebuild it on every call.
var scientificNameLikeEscaper = strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)

// GetSpeciesLastDetectionDateBefore returns the last detection date before the given date.
func (ds *Datastore) GetSpeciesLastDetectionDateBefore(ctx context.Context, scientificName, beforeDate string) (string, error) {
	before, err := time.ParseInLocation(time.DateOnly, beforeDate, ds.timezone)
	if err != nil {
		return "", fmt.Errorf("invalid before date format: %w", err)
	}

	// Bucket dates by the configured timezone, anchored to the before date.
	dateExpr := ds.detectionDateExpr(ds.zoneOffsetSeconds(before.Unix()))

	var result struct {
		LastSeenDate string `gorm:"column:last_seen_date"`
	}

	// Escape LIKE metacharacters with '!' (see scientificNameLikeEscaper).
	escapedScientificName := scientificNameLikeEscaper.Replace(scientificName)
	prefix := ds.manager.TablePrefix()
	query := ds.manager.DB().WithContext(ctx).
		Table(prefix+"detections d").
		Select(fmt.Sprintf("COALESCE(MAX(%s), '') as last_seen_date", dateExpr)).
		Joins(fmt.Sprintf("LEFT JOIN %sdetection_reviews dr ON d.id = dr.detection_id", prefix)).
		Where("d.detected_at < ?", before.Unix()).
		// Match the bare scientific name exactly, or a legacy concatenated label
		// stored as "ScientificName_CommonName". The "!_%" suffix is "literal
		// underscore separator, then anything" ('!_' is an escaped underscore,
		// '%' is the wildcard), mirroring how such labels are split on the first
		// underscore (see detection.ExtractScientificName).
		Where(fmt.Sprintf("d.label_id IN (SELECT id FROM %slabels WHERE scientific_name = ? OR scientific_name LIKE ? ESCAPE '!')", prefix), scientificName, escapedScientificName+`!_%`).
		Where("(dr.verified IS NULL OR dr.verified != ?)", string(entities.VerificationFalsePositive))

	if err := query.Scan(&result).Error; err != nil {
		return "", errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_last_detection_date_before").
			Context("scientific_name", scientificName).
			Context("before_date", beforeDate).
			Build()
	}

	return result.LastSeenDate, nil
}

// GetSpeciesDiversityData returns unique species count per day.
func (ds *Datastore) GetSpeciesDiversityData(ctx context.Context, startDate, endDate string) ([]datastore.DailyAnalyticsData, error) {
	// Validate date formats before using in SQL
	if startDate != "" {
		if _, err := time.Parse(time.DateOnly, startDate); err != nil {
			return nil, fmt.Errorf("invalid start date format (expected YYYY-MM-DD): %w", err)
		}
	}
	if endDate != "" {
		if _, err := time.Parse(time.DateOnly, endDate); err != nil {
			return nil, fmt.Errorf("invalid end date format (expected YYYY-MM-DD): %w", err)
		}
	}

	var results []datastore.DailyAnalyticsData

	// Bucket dates by the configured timezone, anchored to a query boundary: the start of the
	// window, falling back to the end for a left-open range (and only then to the current offset
	// for a fully open range), so an end-only historical query buckets stably regardless of run
	// time. The SELECT, GROUP BY, and BETWEEN filter all reuse this single expression so they stay
	// internally consistent; the user's date strings are interpreted in the same zone the dates
	// are bucketed in.
	var refEpoch int64
	if startDate != "" {
		if t, perr := time.ParseInLocation(time.DateOnly, startDate, ds.timezone); perr == nil {
			refEpoch = t.Unix()
		}
	}
	if refEpoch == 0 && endDate != "" {
		if t, perr := time.ParseInLocation(time.DateOnly, endDate, ds.timezone); perr == nil {
			refEpoch = t.Unix()
		}
	}
	dateExpr := ds.detectionDateExpr(ds.zoneOffsetSeconds(refEpoch))

	// Build query to count distinct species per day, excluding false positives
	prefix := ds.manager.TablePrefix()
	query := ds.manager.DB().WithContext(ctx).
		Table(prefix+"detections d").
		Select(fmt.Sprintf("%s as date, COUNT(DISTINCT l.scientific_name) as count", dateExpr)).
		Joins(fmt.Sprintf("JOIN %slabels l ON d.label_id = l.id", prefix)).
		Joins(fmt.Sprintf("LEFT JOIN %sdetection_reviews dr ON d.id = dr.detection_id", prefix)).
		Where("(dr.verified IS NULL OR dr.verified != ?)", string(entities.VerificationFalsePositive)).
		Group(dateExpr).
		Order("date")

	// Apply date range filters
	switch {
	case startDate != "" && endDate != "":
		query = query.Where(fmt.Sprintf("%s BETWEEN ? AND ?", dateExpr), startDate, endDate)
	case startDate != "":
		query = query.Where(fmt.Sprintf("%s >= ?", dateExpr), startDate)
	case endDate != "":
		query = query.Where(fmt.Sprintf("%s <= ?", dateExpr), endDate)
	}

	// Execute query
	if err := query.Scan(&results).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_diversity_data").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	return results, nil
}

// GetActivityHeatmap returns detection counts bucketed by (station-local date, intra-day slot)
// over [startDate, endDate]. It fetches the raw detection timestamps in range (false positives
// excluded) and buckets them in Go (buildActivityHeatmap), keeping the slot/date math out of
// dialect SQL and correct across DST. species is an optional scientific-name filter; an unknown
// species yields an empty grid that still carries the full date axis.
func (ds *Datastore) GetActivityHeatmap(ctx context.Context, startDate, endDate, species string) (datastore.ActivityHeatmapData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return datastore.ActivityHeatmapData{}, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return buildActivityHeatmap(nil, ds.timezone, startDate, endDate)
		}
		return datastore.ActivityHeatmapData{}, err
	}

	timestamps, err := ds.detection.GetDetectionTimestamps(ctx, start, end, labelID)
	if err != nil {
		return datastore.ActivityHeatmapData{}, err
	}

	return buildActivityHeatmap(timestamps, ds.timezone, startDate, endDate)
}

// selectTopSpeciesHourly is the shared selection path for the top-N-by-volume hour-of-day species
// charts (who-sings-when ridgeline and acoustic succession). It selects the top `limit` species by
// detection volume over [startDate, endDate] (GetTopSpecies, descending volume) and fetches their
// false-positive-excluded per-hour counts in a single batched (label_id, hour) group-by
// (GetBatchHourlyOccurrences). The two charts differ only in how they fold these counts, so that
// folding stays in the caller. minConfidence is 0 so it counts every detection, matching the heatmap
// and the other time-based analytics endpoints. species is an optional scientific-name filter passed
// straight to GetTopSpecies: when non-empty the ranking is restricted to those species (still
// volume-ordered, capped at `limit`); when nil/empty it is the top-N by volume. Returns a nil top
// slice (with nil error) when no species qualify, so each caller emits its own empty, non-nil result.
func (ds *Datastore) selectTopSpeciesHourly(ctx context.Context, startDate, endDate string, species []string, limit int) ([]repository.SpeciesCount, map[uint][24]int, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, nil, err
	}

	// minConfidence 0 counts every detection (no confidence floor), matching the heatmap and the
	// other time-based analytics; named to avoid a bare magic literal at the two call sites.
	const noConfidenceFloor = 0.0

	// Top-N species by raw detection volume across all models (modelID nil). GetTopSpecies uses an
	// inclusive end (<= end) while GetBatchHourlyOccurrences below uses an exclusive end (< end), so
	// subtract one second to cover the exact same range; otherwise ranking and bucket totals could
	// disagree on a detection landing exactly on the end boundary.
	topEnd := end
	if end != math.MaxInt64 {
		topEnd--
	}
	// A species can own several model labels (one per model). Limiting GetTopSpecies by label ROW
	// would drop the lowest-volume SELECTED species before those rows are merged back into one series
	// per species. An explicit selection is already bounded by the scientific-name filter, so fetch
	// all of its label rows (limit 0 = no limit) and let the merge pick the distinct species. The
	// unfiltered top-N default still honors `limit`.
	topLimit := limit
	if len(species) > 0 {
		topLimit = 0
	}
	top, err := ds.detection.GetTopSpecies(ctx, start, topEnd, noConfidenceFloor, nil, species, topLimit)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "select_top_species_hourly_top").
			Build()
	}
	if len(top) == 0 {
		return nil, nil, nil
	}

	// One label ID per top row; fetch their false-positive-excluded hourly counts in a single
	// batched query (chunked internally for large label sets).
	labelIDs := make([]uint, 0, len(top))
	for i := range top {
		labelIDs = append(labelIDs, top[i].LabelID)
	}

	hourlyByLabel, err := ds.detection.GetBatchHourlyOccurrences(ctx, labelIDs, start, end, ds.zoneOffsetSeconds(start), noConfidenceFloor)
	if err != nil {
		return nil, nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "select_top_species_hourly_buckets").
			Build()
	}

	return top, hourlyByLabel, nil
}

// GetHourlyDistributionBySpecies returns the normalized hour-of-day activity distribution for the
// top `limit` species by detection volume over [startDate, endDate], ordered by descending volume.
// It selects the top-N species and their per-hour counts via selectTopSpeciesHourly, then merges and
// normalizes per species in Go (buildSpeciesHourlyDistribution) so each species' timing shape is
// comparable regardless of raw volume. Powers the who-sings-when ridgeline.
func (ds *Datastore) GetHourlyDistributionBySpecies(ctx context.Context, startDate, endDate string, species []string, limit int) ([]datastore.SpeciesHourlyDistribution, error) {
	top, hourlyByLabel, err := ds.selectTopSpeciesHourly(ctx, startDate, endDate, species, limit)
	if err != nil {
		return nil, err
	}
	if len(top) == 0 {
		return []datastore.SpeciesHourlyDistribution{}, nil
	}
	return buildSpeciesHourlyDistribution(top, hourlyByLabel), nil
}

// GetAcousticSuccession returns the raw hour-of-day detection counts (false positives excluded) for
// the top `limit` species by detection volume over [startDate, endDate], ordered by descending
// volume. It selects the top-N species and their per-hour counts via selectTopSpeciesHourly (the
// same path as the ridgeline), then merges per species in Go (buildAcousticSuccession). Unlike the
// ridgeline it does NOT normalize: the streamgraph stacks raw counts so band width is detection
// volume. Powers the acoustic succession streamgraph.
func (ds *Datastore) GetAcousticSuccession(ctx context.Context, startDate, endDate string, species []string, limit int) ([]datastore.SpeciesHourlyCounts, error) {
	top, hourlyByLabel, err := ds.selectTopSpeciesHourly(ctx, startDate, endDate, species, limit)
	if err != nil {
		return nil, err
	}
	if len(top) == 0 {
		return []datastore.SpeciesHourlyCounts{}, nil
	}
	return buildAcousticSuccession(top, hourlyByLabel), nil
}

// GetDailyActivityOnset returns the per-day dawn-chorus onset relative to civil dawn over the
// inclusive [startDate, endDate] range. It fetches false-positive-excluded detection timestamps
// once (GetDetectionTimestamps), then buckets and computes the per-day onset in a shared,
// table-tested Go helper (buildDailyActivityOnset). Civil dawn comes from the configured SunCalc,
// expressed in the station timezone so it shares the same minute-of-day frame as the bucketed
// detections; a day with no civil dawn (polar day / night) or too few detections gets a nil onset
// that the client renders as a gap. species is an optional scientific-name filter; an unknown
// species yields all-null days that still carry the full date axis (matching the heatmap).
func (ds *Datastore) GetDailyActivityOnset(ctx context.Context, startDate, endDate, species string) ([]datastore.DailyActivityOnset, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	dawn := ds.civilDawnMinuteLookup()

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return buildDailyActivityOnset(nil, ds.timezone, startDate, endDate, onsetDetectionRank, minOnsetDetections, dawn)
		}
		return nil, err
	}

	timestamps, err := ds.detection.GetDetectionTimestamps(ctx, start, end, labelID)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_daily_activity_onset").
			Build()
	}

	return buildDailyActivityOnset(timestamps, ds.timezone, startDate, endDate, onsetDetectionRank, minOnsetDetections, dawn)
}

// GetConfidenceHistogram returns the per-species confidence-score distribution over the date range,
// powering the confidence distribution chart (design spec section 6.5). With no species filter it
// covers the top `limit` species by raw detection volume; with a species filter it covers just that
// species (always included if it has any detections). It fetches each species' false-positive-excluded
// confidences in one batched query (GetBatchConfidences), then bins and normalizes them in a shared,
// table-tested Go helper (buildSpeciesConfidenceHistogram). minConfidence is 0 so every detection is
// counted, matching the who-sings-when ridgeline and the other species analytics endpoints.
func (ds *Datastore) GetConfidenceHistogram(ctx context.Context, startDate, endDate, species string, bins, limit int) ([]datastore.SpeciesConfidenceHistogram, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	// minConfidence 0 counts every detection (no confidence floor), matching the who-sings-when
	// ridgeline and the other time-based analytics; named to avoid a bare magic literal below.
	const noConfidenceFloor = 0.0

	// Select the species set and the per-species detection floor. An explicit species filter yields
	// just that species (always shown if it has any detections); otherwise the top `limit` species by
	// raw volume, with low-volume species dropped as noisy.
	var speciesSet []repository.SpeciesCount
	var minCount int
	if species != "" {
		// Use every label ID that maps to this scientific name (a species can carry one label per
		// model), so the filtered path merges multi-model detections exactly like the top-N path below;
		// resolving a single label ID would silently drop other models' detections for the species.
		labelIDs, labelErr := ds.label.GetLabelIDsByScientificName(ctx, species)
		if labelErr != nil {
			return nil, errors.New(labelErr).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_confidence_histogram_resolve_species").
				Build()
		}
		if len(labelIDs) == 0 {
			return []datastore.SpeciesConfidenceHistogram{}, nil
		}
		speciesSet = make([]repository.SpeciesCount, 0, len(labelIDs))
		for _, labelID := range labelIDs {
			speciesSet = append(speciesSet, repository.SpeciesCount{LabelID: labelID, ScientificName: species})
		}
		minCount = 1
	} else {
		// GetTopSpecies uses an inclusive end (<= end) while GetBatchConfidences uses an exclusive end
		// (< end); subtract one second so ranking and binned totals cover the exact same range and never
		// disagree on a detection landing on the end boundary (mirrors GetHourlyDistributionBySpecies).
		topEnd := end
		if end != math.MaxInt64 {
			topEnd--
		}
		top, topErr := ds.detection.GetTopSpecies(ctx, start, topEnd, noConfidenceFloor, nil, nil, limit)
		if topErr != nil {
			return nil, errors.New(topErr).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_confidence_histogram_top").
				Build()
		}
		speciesSet = top
		minCount = minConfidenceHistogramDetections
	}

	if len(speciesSet) == 0 {
		return []datastore.SpeciesConfidenceHistogram{}, nil
	}

	labelIDs := make([]uint, 0, len(speciesSet))
	for i := range speciesSet {
		labelIDs = append(labelIDs, speciesSet[i].LabelID)
	}

	confByLabel, err := ds.detection.GetBatchConfidences(ctx, labelIDs, start, end, noConfidenceFloor)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_confidence_histogram_confidences").
			Build()
	}

	return buildSpeciesConfidenceHistogram(speciesSet, confByLabel, bins, minCount), nil
}

// GetSpeciesAccumulation returns the species accumulation curve over [startDate, endDate]: per
// calendar day, the cumulative count of distinct species first detected within the range (false
// positives excluded). It fetches each species' in-period first-seen in one grouped query
// (GetSpeciesFirstSeenInPeriod), then builds the cumulative per-day curve in a shared, table-tested
// Go helper (buildSpeciesAccumulation) using the station timezone for date bucketing.
func (ds *Datastore) GetSpeciesAccumulation(ctx context.Context, startDate, endDate string) ([]datastore.SpeciesAccumulationPoint, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	firstSeen, err := ds.detection.GetSpeciesFirstSeenInPeriod(ctx, start, end)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_accumulation").
			Build()
	}

	return buildSpeciesAccumulation(firstSeen, ds.timezone, startDate, endDate)
}

// GetAudioSources returns each audio source with at least one (false-positive-excluded) detection in
// [startDate, endDate] (all history when both dates are empty), with its in-range detection count,
// ordered by count descending. It fetches the grouped summaries in one query
// (GetSourceActivitySummaries) and maps the repository rows onto the datastore result shape; the metric
// needs no date bucketing, so there is no shared Go helper. Powers the analytics source/mic filter's
// option list (the source dimension that the per-mic comparison chart consumes).
func (ds *Datastore) GetAudioSources(ctx context.Context, startDate, endDate string) ([]datastore.AudioSourceSummary, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	rows, err := ds.detection.GetSourceActivitySummaries(ctx, start, end)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_audio_sources").
			Build()
	}

	summaries := make([]datastore.AudioSourceSummary, 0, len(rows))
	for i := range rows {
		displayName := ""
		if rows[i].DisplayName != nil {
			displayName = *rows[i].DisplayName
		}
		summaries = append(summaries, datastore.AudioSourceSummary{
			ID:          rows[i].SourceID,
			DisplayName: displayName,
			NodeName:    rows[i].NodeName,
			SourceType:  rows[i].SourceType,
			Count:       rows[i].Count,
		})
	}
	return summaries, nil
}

// GetYearOverYear returns the year-over-year tracker: the current year-to-date cumulative detection
// count versus the same calendar span one year earlier, per current-year calendar day from Jan 1
// through date (false positives excluded). date is a station-local YYYY-MM-DD bound; empty defaults to
// today in the station timezone. It fetches raw detection timestamps for each window in two separate
// grouped queries (GetDetectionTimestamps) - which skips scanning the multi-month gap between the
// windows - then aligns and cumulates them in a shared, table-tested Go helper (buildYearOverYear).
// Bucketing uses the station timezone while the date axis is enumerated in UTC for DST safety.
func (ds *Datastore) GetYearOverYear(ctx context.Context, date string) (datastore.YearOverYearResult, error) {
	loc := ds.timezone
	if loc == nil {
		loc = time.UTC
	}

	// Resolve the requested date (default: today in the station timezone). Only ref's calendar date
	// (year/month/day) is used downstream by computeYearOverYearWindows; the intraday clock is ignored.
	ref := time.Now().In(loc)
	if date != "" {
		t, parseErr := time.ParseInLocation(time.DateOnly, date, loc)
		if parseErr != nil {
			return datastore.YearOverYearResult{}, errors.New(parseErr).
				Component("datastore").
				Category(errors.CategoryValidation).
				Context("operation", "get_year_over_year").
				Context("date", date).
				Build()
		}
		ref = t
	}
	w := computeYearOverYearWindows(ref, loc)

	thisTs, err := ds.detection.GetDetectionTimestamps(ctx, w.curStartEpoch, w.curEndEpoch, nil)
	if err != nil {
		return datastore.YearOverYearResult{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_year_over_year_current").
			Build()
	}
	lastTs, err := ds.detection.GetDetectionTimestamps(ctx, w.priorStartEpoch, w.priorEndEpoch, nil)
	if err != nil {
		return datastore.YearOverYearResult{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_year_over_year_previous").
			Build()
	}

	return buildYearOverYear(thisTs, lastTs, loc, w.curStart, w.curEnd, w.priorStart, w.priorEnd, w.curYear, w.prevYear)
}

// GetSpeciesPhenology returns the arrival/departure residency span for the top `limit` species by
// volume over [startDate, endDate]: each species' first and last false-positive-excluded detection
// plus the in-range count. It fetches the spans in one grouped query (GetSpeciesPhenologyInPeriod),
// then formats the timestamps to station-local dates and orders the rows by arrival in a shared,
// table-tested Go helper (buildSpeciesPhenology) using the station timezone.
func (ds *Datastore) GetSpeciesPhenology(ctx context.Context, startDate, endDate string, limit int) ([]datastore.SpeciesPhenologyPoint, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	rows, err := ds.detection.GetSpeciesPhenologyInPeriod(ctx, start, end, limit)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_phenology").
			Build()
	}

	return buildSpeciesPhenology(rows, ds.timezone), nil
}

// civilDawnMinuteLookup returns a civilDawnMinuteLookup closure over the datastore's SunCalc and
// station timezone. The closure yields civil dawn's station-local minute-of-day for a date, or
// ok=false when no SunCalc is configured or civil dawn is undefined for the date (polar day/night).
func (ds *Datastore) civilDawnMinuteLookup() civilDawnMinuteLookup {
	return func(date time.Time) (int, bool) {
		if ds.suncalc == nil {
			return 0, false
		}
		// Anchor at local noon before the lookup: SunCalc re-derives the calendar date in its own
		// coordinate-derived zone, so passing midnight could land on the adjacent day (and return
		// the wrong day's civil dawn) when that zone trails the configured station timezone. Noon
		// keeps the intended calendar day for any real timezone offset.
		civilDawn, ok := ds.suncalc.GetCivilDawn(date.Add(12 * time.Hour))
		if !ok {
			return 0, false
		}
		// Express civil dawn in the station timezone so its minute-of-day matches the frame used to
		// bucket detections; this stays correct even if SunCalc's coordinate-derived zone differs
		// from the configured station timezone or across DST.
		lt := civilDawn.In(ds.timezone)
		return lt.Hour()*60 + lt.Minute(), true
	}
}

// ============================================================
// Dynamic Threshold Methods
// ============================================================

// thresholdScientificName extracts the scientific name from a threshold's label.
func thresholdScientificName(t *entities.DynamicThreshold) string {
	if t.Label != nil && t.Label.ScientificName != "" {
		return detection.ExtractScientificName(t.Label.ScientificName)
	}
	return ""
}

// labelModelName constructs the classifier-style model ID ("Name_VVersion") from a
// label's associated AIModel, used for both threshold records and threshold events.
// Requires the caller's query to preload Label.Model; falls back to the default
// BirdNET model identifier when the label or model is absent.
func labelModelName(l *entities.Label) string {
	if l != nil && l.Model != nil && l.Model.Name != "" {
		return l.Model.Name + "_V" + l.Model.Version
	}
	return detection.DefaultModelName + "_V" + detection.DefaultModelVersion
}

// resolveCommonName maps a scientific name to its common name using the
// pre-built name maps. Falls back to the scientific name if no mapping exists.
// Handles legacy concatenated "ScientificName_CommonName" format by extracting
// only the scientific name portion before lookup.
// Logs at info (once per species) when the fallback is used and maps are populated,
// to help diagnose issues where common names stop appearing without surfacing
// the benign fallback on the diagnostics health check.
func (ds *Datastore) resolveCommonName(scientificName string) string {
	sciName := detection.ExtractScientificName(scientificName)
	// OpenFauna is authoritative: override label-derived names, serve localized
	// names, and resolve historic out-of-working-set species via on-demand lookup.
	if r := ds.loadNameResolver(); r != nil {
		if name := r.Resolve(sciName, ""); name != "" {
			return name
		}
	}
	nm := ds.loadNameMaps()
	if cn, ok := nm.common[sciName]; ok {
		return cn
	}
	// Log once per missing species when maps are populated (not during startup with empty maps).
	// Logged at info because the fallback to the scientific name is the intended
	// behavior; surfacing as a warning made it surface on the diagnostics health
	// check as an "elevated error count" for benign missing translations.
	// Guard ds.log: it may be nil when the datastore is constructed without a logger,
	// matching the other logging sites in this file. Skipping the LoadOrStore when there
	// is no logger is harmless: the dedup set only exists to rate-limit this log line.
	if ds.log != nil && len(nm.common) > 0 {
		if _, alreadyLogged := ds.loggedMissingNames.LoadOrStore(sciName, struct{}{}); !alreadyLogged {
			ds.log.Info("common name not found in name maps, falling back to scientific name",
				logger.String("scientific_name", sciName),
				logger.Int("name_map_size", len(nm.common)))
		}
	}
	return sciName
}

// resolveToScientificName converts a species name (which may be a common name
// or scientific name) to a scientific name for v2 label lookups.
// Uses the pre-built species name map (lowercase common name → scientific name).
// Falls back to the input unchanged if no mapping is found.
func (ds *Datastore) resolveToScientificName(name string) string {
	normalized := strings.ToLower(norm.NFC.String(strings.TrimSpace(name)))
	species := ds.loadNameMaps().species
	if sci, ok := species[normalized]; ok {
		return sci
	}
	// Reverse miss: the input did not map to a known scientific name, so callers fall
	// back to substring/LIKE. Log once so an unresolvable name is distinguishable from
	// a name with no detections. Guard ds.log (may be nil; matches resolveCommonName).
	if ds.log != nil && len(species) > 0 {
		ds.log.Debug("species name did not resolve to a scientific name, using input verbatim",
			logger.String("input", name))
	}
	return name
}

// SaveDynamicThreshold saves a dynamic threshold.
// Resolves the scientific name to a label ID before saving.
func (ds *Datastore) SaveDynamicThreshold(threshold *datastore.DynamicThreshold) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()

	// Resolve scientific name to label ID using default model
	label, err := ds.label.GetOrCreate(ctx, threshold.ScientificName, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	if err != nil {
		return fmt.Errorf("failed to resolve label for threshold: %w", err)
	}

	v2Threshold := &entities.DynamicThreshold{
		LabelID:       label.ID,
		Level:         threshold.Level,
		CurrentValue:  threshold.CurrentValue,
		BaseThreshold: threshold.BaseThreshold,
		HighConfCount: threshold.HighConfCount,
		ValidHours:    threshold.ValidHours,
		ExpiresAt:     threshold.ExpiresAt,
		LastTriggered: threshold.LastTriggered,
		FirstCreated:  threshold.FirstCreated,
		TriggerCount:  threshold.TriggerCount,
	}
	return ds.threshold.SaveDynamicThreshold(ctx, v2Threshold)
}

// GetDynamicThreshold retrieves a dynamic threshold by scientific name and model.
// Note: modelName is accepted for interface compatibility but not used in the v2 schema
// because v2 thresholds are scoped through LabelID (which is already per-model).
func (ds *Datastore) GetDynamicThreshold(speciesName, _ string) (*datastore.DynamicThreshold, error) {
	if ds.threshold == nil {
		return nil, fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	// Resolve to scientific name in case caller passes a common name
	t, err := ds.threshold.GetDynamicThreshold(ctx, ds.resolveToScientificName(speciesName))
	if err != nil {
		// Not-found is a benign result, not a DB fault. Wrap it as a CategoryNotFound
		// EnhancedError (never CategoryDatabase, so it is not surfaced to Sentry as a
		// database error, see #1019) so the API layer's handleErrorWithNotFound maps it
		// to HTTP 404 instead of 500, matching the legacy backend (#1068). errors.Is
		// against the sentinel still matches because EnhancedError.Unwrap exposes it, and
		// shouldReportToSentry suppresses the benign "dynamic threshold not found" message
		// so building this error produces no Sentry noise. Genuine failures fall through
		// to the CategoryDatabase telemetry tags below.
		if errors.Is(err, repository.ErrDynamicThresholdNotFound) {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryNotFound).
				Context("operation", "get_dynamic_threshold").
				Build()
		}
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_dynamic_threshold").
			Build()
	}
	scientificName := thresholdScientificName(t)
	return &datastore.DynamicThreshold{
		ID:             t.ID,
		SpeciesName:    strings.ToLower(ds.resolveCommonName(scientificName)),
		ScientificName: scientificName,
		ModelName:      labelModelName(t.Label),
		Level:          t.Level,
		CurrentValue:   t.CurrentValue,
		BaseThreshold:  t.BaseThreshold,
		HighConfCount:  t.HighConfCount,
		ValidHours:     t.ValidHours,
		ExpiresAt:      t.ExpiresAt,
		LastTriggered:  t.LastTriggered,
		FirstCreated:   t.FirstCreated,
		UpdatedAt:      t.UpdatedAt,
		TriggerCount:   t.TriggerCount,
	}, nil
}

// GetAllDynamicThresholds retrieves all dynamic thresholds.
func (ds *Datastore) GetAllDynamicThresholds(limit ...int) ([]datastore.DynamicThreshold, error) {
	if ds.threshold == nil {
		return []datastore.DynamicThreshold{}, nil
	}
	ctx := context.Background()
	v2Thresholds, err := ds.threshold.GetAllDynamicThresholds(ctx, limit...)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_all_dynamic_thresholds").
			Build()
	}
	result := make([]datastore.DynamicThreshold, 0, len(v2Thresholds))
	for i := range v2Thresholds {
		t := &v2Thresholds[i]
		scientificName := thresholdScientificName(t)
		result = append(result, datastore.DynamicThreshold{
			ID:             t.ID,
			SpeciesName:    strings.ToLower(ds.resolveCommonName(scientificName)),
			ScientificName: scientificName,
			ModelName:      labelModelName(t.Label),
			Level:          t.Level,
			CurrentValue:   t.CurrentValue,
			BaseThreshold:  t.BaseThreshold,
			HighConfCount:  t.HighConfCount,
			ValidHours:     t.ValidHours,
			ExpiresAt:      t.ExpiresAt,
			LastTriggered:  t.LastTriggered,
			FirstCreated:   t.FirstCreated,
			UpdatedAt:      t.UpdatedAt,
			TriggerCount:   t.TriggerCount,
		})
	}
	return result, nil
}

// DeleteDynamicThreshold deletes a dynamic threshold.
func (ds *Datastore) DeleteDynamicThreshold(speciesName string) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	return ds.threshold.DeleteDynamicThreshold(ctx, ds.resolveToScientificName(speciesName))
}

// DeleteExpiredDynamicThresholds deletes expired thresholds.
func (ds *Datastore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	if ds.threshold == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteExpiredDynamicThresholds(ctx, before)
}

// UpdateDynamicThresholdExpiry updates the expiry of a threshold.
func (ds *Datastore) UpdateDynamicThresholdExpiry(speciesName string, expiresAt time.Time) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	return ds.threshold.UpdateDynamicThresholdExpiry(ctx, ds.resolveToScientificName(speciesName), expiresAt)
}

// BatchSaveDynamicThresholds saves multiple thresholds.
// Resolves scientific names to label IDs before saving.
func (ds *Datastore) BatchSaveDynamicThresholds(thresholds []datastore.DynamicThreshold) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	if len(thresholds) == 0 {
		return nil
	}
	ctx := context.Background()

	// Collect all scientific names for batch resolution
	names := make([]string, 0, len(thresholds))
	for i := range thresholds {
		if thresholds[i].ScientificName != "" {
			names = append(names, thresholds[i].ScientificName)
		}
	}

	// Batch resolve all labels in one operation using default model
	labels, err := ds.label.BatchGetOrCreate(ctx, names, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	if err != nil {
		return fmt.Errorf("failed to resolve labels for thresholds: %w", err)
	}

	// Build v2 thresholds with resolved label IDs
	v2Thresholds := make([]entities.DynamicThreshold, 0, len(thresholds))
	for i := range thresholds {
		t := &thresholds[i]
		label := labels[t.ScientificName]
		if label == nil {
			return fmt.Errorf("label not found for threshold %s", t.ScientificName)
		}

		v2Thresholds = append(v2Thresholds, entities.DynamicThreshold{
			LabelID:       label.ID,
			Level:         t.Level,
			CurrentValue:  t.CurrentValue,
			BaseThreshold: t.BaseThreshold,
			HighConfCount: t.HighConfCount,
			ValidHours:    t.ValidHours,
			ExpiresAt:     t.ExpiresAt,
			LastTriggered: t.LastTriggered,
			FirstCreated:  t.FirstCreated,
			TriggerCount:  t.TriggerCount,
		})
	}
	return ds.threshold.BatchSaveDynamicThresholds(ctx, v2Thresholds)
}

// DeleteAllDynamicThresholds deletes all thresholds.
func (ds *Datastore) DeleteAllDynamicThresholds() (int64, error) {
	if ds.threshold == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteAllDynamicThresholds(ctx)
}

// GetDynamicThresholdStats returns threshold statistics.
func (ds *Datastore) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	if ds.threshold == nil {
		return 0, 0, 0, make(map[int]int64), nil
	}
	ctx := context.Background()
	totalCount, activeCount, atMinimumCount, levelDistribution, err = ds.threshold.GetDynamicThresholdStats(ctx)
	if err != nil {
		return 0, 0, 0, nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_dynamic_threshold_stats").
			Build()
	}
	return totalCount, activeCount, atMinimumCount, levelDistribution, nil
}

// ============================================================
// Threshold Event Methods
// ============================================================

// eventSpeciesName extracts the species name from an event's label.
// Handles legacy concatenated "ScientificName_CommonName" format.
func eventSpeciesName(e *entities.ThresholdEvent) string {
	if e.Label != nil && e.Label.ScientificName != "" {
		return detection.ExtractScientificName(e.Label.ScientificName)
	}
	return ""
}

// SaveThresholdEvent saves a threshold event.
// Uses event.ScientificName (if provided) for correct label resolution in V2 schema.
// Falls back to event.SpeciesName (common name) for backward compatibility with
// events created before #1907 fix.
func (ds *Datastore) SaveThresholdEvent(event *datastore.ThresholdEvent) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()

	// Use ScientificName if available (new behavior after #1907 fix),
	// otherwise fall back to SpeciesName (common name) for backward compatibility.
	labelName := event.ScientificName
	if labelName == "" {
		// Fallback for events without ScientificName populated.
		// This creates incorrect labels but maintains backward compatibility.
		labelName = event.SpeciesName
	}

	label, err := ds.label.GetOrCreate(ctx, labelName, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	if err != nil {
		return fmt.Errorf("failed to resolve label for event: %w", err)
	}

	v2Event := &entities.ThresholdEvent{
		LabelID:       label.ID,
		PreviousLevel: event.PreviousLevel,
		NewLevel:      event.NewLevel,
		PreviousValue: event.PreviousValue,
		NewValue:      event.NewValue,
		ChangeReason:  event.ChangeReason,
		Confidence:    event.Confidence,
		CreatedAt:     event.CreatedAt,
	}
	return ds.threshold.SaveThresholdEvent(ctx, v2Event)
}

// GetThresholdEvents retrieves threshold events for a species.
// WORKAROUND(#1907): Prior to the fix, events were saved with labels created from common names
// (e.g., "american robin" stored as scientific_name). After the fix, events are saved with
// correct scientific names (e.g., "Turdus migratorius"). This method queries both label types
// to return all events during the transition period.
// TODO: Remove this workaround when legacy database support is dropped. At that point,
// clean up orphaned common-name labels and simplify to a single query using scientific name.
func (ds *Datastore) GetThresholdEvents(speciesName string, limit int) ([]datastore.ThresholdEvent, error) {
	if ds.threshold == nil {
		return []datastore.ThresholdEvent{}, nil
	}
	ctx := context.Background()

	// Query 1: Try with the provided name (common name) - finds legacy/incorrectly saved events
	v2Events, err := ds.threshold.GetThresholdEvents(ctx, speciesName, limit)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_threshold_events").
			Context("query_type", "common_name").
			Build()
	}

	// Query 2: If we can resolve to scientific name, also query with that
	// This finds correctly saved events (after #1907 fix)
	// Resolve through resolveToScientificName so this shares the reverse map's NFC-folded
	// normalization; a decomposed (NFD) localized name must match the NFC-folded keys.
	if scientificName := ds.resolveToScientificName(speciesName); scientificName != speciesName {
		sciEvents, err := ds.threshold.GetThresholdEvents(ctx, scientificName, limit)
		if err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_threshold_events").
				Context("query_type", "scientific_name").
				Build()
		}
		v2Events = append(v2Events, sciEvents...)
	}

	// Note: Deduplication not needed - each event has exactly one LabelID,
	// so queries for different labels return disjoint result sets.
	uniqueEvents := v2Events

	// Sort by CreatedAt DESC (most recent first). Tie-break on ID so events sharing a
	// timestamp truncate deterministically when the limit is applied below.
	sort.Slice(uniqueEvents, func(i, j int) bool {
		if uniqueEvents[i].CreatedAt.Equal(uniqueEvents[j].CreatedAt) {
			return uniqueEvents[i].ID > uniqueEvents[j].ID
		}
		return uniqueEvents[i].CreatedAt.After(uniqueEvents[j].CreatedAt)
	})

	// Apply limit after merge
	if limit > 0 && len(uniqueEvents) > limit {
		uniqueEvents = uniqueEvents[:limit]
	}

	// Convert to datastore.ThresholdEvent
	result := make([]datastore.ThresholdEvent, 0, len(uniqueEvents))
	for i := range uniqueEvents {
		e := &uniqueEvents[i]
		result = append(result, datastore.ThresholdEvent{
			ID:            e.ID,
			SpeciesName:   eventSpeciesName(e),
			ModelName:     labelModelName(e.Label),
			PreviousLevel: e.PreviousLevel,
			NewLevel:      e.NewLevel,
			PreviousValue: e.PreviousValue,
			NewValue:      e.NewValue,
			ChangeReason:  e.ChangeReason,
			Confidence:    e.Confidence,
			CreatedAt:     e.CreatedAt,
		})
	}
	return result, nil
}

// GetRecentThresholdEvents retrieves recent threshold events.
func (ds *Datastore) GetRecentThresholdEvents(limit int) ([]datastore.ThresholdEvent, error) {
	if ds.threshold == nil {
		return []datastore.ThresholdEvent{}, nil
	}
	ctx := context.Background()
	v2Events, err := ds.threshold.GetRecentThresholdEvents(ctx, limit)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_recent_threshold_events").
			Build()
	}
	result := make([]datastore.ThresholdEvent, 0, len(v2Events))
	for i := range v2Events {
		e := &v2Events[i]
		result = append(result, datastore.ThresholdEvent{
			ID:            e.ID,
			SpeciesName:   eventSpeciesName(e),
			ModelName:     labelModelName(e.Label),
			PreviousLevel: e.PreviousLevel,
			NewLevel:      e.NewLevel,
			PreviousValue: e.PreviousValue,
			NewValue:      e.NewValue,
			ChangeReason:  e.ChangeReason,
			Confidence:    e.Confidence,
			CreatedAt:     e.CreatedAt,
		})
	}
	return result, nil
}

// DeleteThresholdEvents deletes threshold events for a species.
// WORKAROUND(#1907): mirrors GetThresholdEvents' dual lookup. It deletes events saved
// under BOTH the provided name (legacy common-name labels) AND the resolved scientific
// name (post-#1907 labels). Without the common-name pass, legacy events survive the
// delete and GetThresholdEvents resurfaces them on the next read.
// TODO: Collapse to a single scientific-name delete when the #1907 workaround is removed
// (after legacy common-name labels have been migrated).
func (ds *Datastore) DeleteThresholdEvents(speciesName string) error {
	if ds.threshold == nil {
		return nil
	}
	ctx := context.Background()

	// Delete by the provided name first - matches legacy/incorrectly saved events
	// whose label scientific_name actually holds the common name.
	if err := ds.threshold.DeleteThresholdEvents(ctx, speciesName); err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "delete_threshold_events").
			Context("query_type", "common_name").
			Build()
	}

	// Also delete by the resolved scientific name when it differs - matches
	// correctly saved events (after the #1907 fix).
	if scientificName := ds.resolveToScientificName(speciesName); scientificName != speciesName {
		if err := ds.threshold.DeleteThresholdEvents(ctx, scientificName); err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "delete_threshold_events").
				Context("query_type", "scientific_name").
				Build()
		}
	}
	return nil
}

// DeleteAllThresholdEvents deletes all threshold events.
func (ds *Datastore) DeleteAllThresholdEvents() (int64, error) {
	if ds.threshold == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteAllThresholdEvents(ctx)
}

// ============================================================
// Notification History Methods
// ============================================================

// notificationScientificName extracts the scientific name from a notification's label.
// Handles legacy concatenated "ScientificName_CommonName" format.
func notificationScientificName(h *entities.NotificationHistory) string {
	if h.Label != nil && h.Label.ScientificName != "" {
		return detection.ExtractScientificName(h.Label.ScientificName)
	}
	return ""
}

// SaveNotificationHistory saves a notification history entry.
// Resolves the scientific name to a label ID before saving.
func (ds *Datastore) SaveNotificationHistory(ctx context.Context, history *datastore.NotificationHistory) error {
	if ds.notification == nil {
		return fmt.Errorf("notification repository not configured")
	}
	if history == nil {
		return fmt.Errorf("notification history cannot be nil")
	}

	// Resolve scientific name to label ID using default model
	label, err := ds.label.GetOrCreate(ctx, history.ScientificName, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
	if err != nil {
		return fmt.Errorf("failed to resolve label for notification history: %w", err)
	}

	v2History := &entities.NotificationHistory{
		LabelID:          label.ID,
		NotificationType: history.NotificationType,
		LastSent:         history.LastSent,
		ExpiresAt:        history.ExpiresAt,
	}
	return ds.notification.SaveNotificationHistory(ctx, v2History)
}

// GetNotificationHistory retrieves a notification history entry.
func (ds *Datastore) GetNotificationHistory(ctx context.Context, scientificName, notificationType string) (*datastore.NotificationHistory, error) {
	if ds.notification == nil {
		return nil, datastore.ErrNotificationHistoryNotFound
	}
	h, err := ds.notification.GetNotificationHistory(ctx, scientificName, notificationType)
	if err != nil {
		return nil, err
	}
	return &datastore.NotificationHistory{
		ID:               h.ID,
		ScientificName:   notificationScientificName(h),
		NotificationType: h.NotificationType,
		LastSent:         h.LastSent,
		ExpiresAt:        h.ExpiresAt,
		CreatedAt:        h.CreatedAt,
		UpdatedAt:        h.UpdatedAt,
	}, nil
}

// GetActiveNotificationHistory retrieves active notification history entries.
func (ds *Datastore) GetActiveNotificationHistory(ctx context.Context, after time.Time) ([]datastore.NotificationHistory, error) {
	if ds.notification == nil {
		return []datastore.NotificationHistory{}, nil
	}
	v2Histories, err := ds.notification.GetActiveNotificationHistory(ctx, after)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.NotificationHistory, 0, len(v2Histories))
	for i := range v2Histories {
		h := &v2Histories[i]
		result = append(result, datastore.NotificationHistory{
			ID:               h.ID,
			ScientificName:   notificationScientificName(h),
			NotificationType: h.NotificationType,
			LastSent:         h.LastSent,
			ExpiresAt:        h.ExpiresAt,
			CreatedAt:        h.CreatedAt,
			UpdatedAt:        h.UpdatedAt,
		})
	}
	return result, nil
}

// DeleteExpiredNotificationHistory deletes expired notification history entries.
func (ds *Datastore) DeleteExpiredNotificationHistory(ctx context.Context, before time.Time) (int64, error) {
	if ds.notification == nil {
		return 0, nil
	}
	return ds.notification.DeleteExpiredNotificationHistory(ctx, before)
}

// SaveAppEvent persists an application event with JSON-encoded metadata.
func (ds *Datastore) SaveAppEvent(ctx context.Context, category, eventType, message string, metadata map[string]any) error {
	if ds.appEvent == nil {
		return nil
	}
	metadataJSON := "{}"
	if len(metadata) > 0 {
		data, err := json.Marshal(metadata)
		if err != nil {
			if ds.log != nil {
				ds.log.Warn("failed to marshal app event metadata",
					logger.String("category", category),
					logger.String("event_type", eventType),
					logger.Error(err))
			}
			metadataJSON = `{"error":"failed to encode metadata"}`
		} else {
			metadataJSON = string(data)
		}
	}
	event := &entities.AppEvent{
		Timestamp: time.Now(),
		Category:  category,
		EventType: eventType,
		Message:   message,
		Metadata:  metadataJSON,
	}
	return ds.appEvent.Save(ctx, event)
}

// GetRecentAppEvents returns recent application events with decoded metadata.
func (ds *Datastore) GetRecentAppEvents(ctx context.Context, limit int) ([]datastore.AppEvent, error) {
	if ds.appEvent == nil {
		return nil, nil
	}
	v2Events, err := ds.appEvent.GetRecent(ctx, limit)
	if err != nil {
		return nil, err
	}
	return convertAppEvents(v2Events), nil
}

// GetAppEventsSince returns application events since the given time.
func (ds *Datastore) GetAppEventsSince(ctx context.Context, since time.Time, limit int) ([]datastore.AppEvent, error) {
	if ds.appEvent == nil {
		return nil, nil
	}
	v2Events, err := ds.appEvent.GetSince(ctx, since, limit)
	if err != nil {
		return nil, err
	}
	return convertAppEvents(v2Events), nil
}

// PruneAppEvents removes events older than retentionDays and enforces the 10k row cap.
func (ds *Datastore) PruneAppEvents(ctx context.Context, retentionDays int) (int64, error) {
	if ds.appEvent == nil {
		return 0, nil
	}
	if retentionDays < 0 {
		return 0, fmt.Errorf("retentionDays must be non-negative, got %d", retentionDays)
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	deleted, err := ds.appEvent.DeleteBefore(ctx, cutoff)
	if err != nil {
		return deleted, err
	}

	const maxRows = 10_000
	count, err := ds.appEvent.Count(ctx)
	if err != nil {
		return deleted, err
	}
	if count > maxRows {
		events, err := ds.appEvent.GetRecent(ctx, maxRows+1)
		if err != nil {
			return deleted, err
		}
		if len(events) > maxRows {
			cutoffEvent := events[maxRows]
			extraDeleted, err := ds.appEvent.DeleteBefore(ctx, cutoffEvent.Timestamp)
			if err != nil {
				return deleted, err
			}
			deleted += extraDeleted
		}
	}

	return deleted, nil
}

// convertAppEvents converts v2 entity events to datastore.AppEvent with decoded metadata.
func convertAppEvents(v2Events []entities.AppEvent) []datastore.AppEvent {
	if len(v2Events) == 0 {
		return nil
	}
	result := make([]datastore.AppEvent, 0, len(v2Events))
	for _, e := range v2Events {
		ae := datastore.AppEvent{
			Timestamp: e.Timestamp,
			Category:  e.Category,
			EventType: e.EventType,
			Message:   e.Message,
		}
		if e.Metadata != "" && e.Metadata != "{}" {
			var meta map[string]any
			if json.Unmarshal([]byte(e.Metadata), &meta) == nil {
				ae.Metadata = meta
			}
		}
		result = append(result, ae)
	}
	return result
}

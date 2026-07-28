package guideprovider

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// GuideCacheEntry is the GORM row for the DB cache tier. The composite unique
// key is (scientific_name, locale, provider), where `provider` is the canonical
// PROVIDER SET that produced the row (see GuideCache.providerSetKey), not the
// single provider credited for the prose — that is SourceProvider.
//
// Keying on the set is what makes a provider change self-invalidating. A row
// written with Wikipedia enabled is stored under a different key than one written
// without it, so disabling Wikipedia simply stops finding those rows; they age out
// on normal retention. The alternative — stamping only the primary provider — made
// every row look identical regardless of what produced it, which is why the cache
// previously needed a whole apparatus (an invalidation generation, a store-then-
// verify memory write, a table-wide DeleteAll, and a tri-state "was Wikipedia
// applied" tracker in the control monitor) to reconstruct that fact, and still
// served Wikipedia prose for a full PositiveTTL after a restart with it switched
// off, because the startup path never ran the invalidation.
//
// The three key columns carry explicit size tags. The MySQL driver is opened
// without DefaultStringSize (internal/datastore/mysql.go), so an unsized string
// maps to longtext, and MySQL refuses a unique index over TEXT columns without a
// key length — AutoMigrate would fail and disable the guide cache on every MySQL
// deployment while working fine on SQLite. Sizes match the convention used by
// every other composite-unique string column in the schema (internal/datastore/
// model.go). Combined key length stays well inside InnoDB's 3072-byte limit.
type GuideCacheEntry struct {
	ID             uint   `gorm:"primaryKey"`
	ScientificName string `gorm:"uniqueIndex:idx_guide_cache_key;not null;size:200"`
	Locale         string `gorm:"uniqueIndex:idx_guide_cache_key;not null;size:20"`
	Provider       string `gorm:"uniqueIndex:idx_guide_cache_key;not null;size:100"`
	// SourceProvider is the cache's canonical/primary provider label, carried for
	// display. Distinct from Provider, which is the whole set that produced the row
	// and is part of the key — without this field the API would surface the
	// composite key ("set:openfauna+wikipedia") to users as a provider name.
	//
	// It is NOT necessarily the origin of the prose: fetchFromProviders stamps the
	// primary provider regardless of which one supplied the description. The prose's
	// true attribution travels on SourceURL/License/LicenseURL, which mergeGuides
	// carries from whichever provider produced it.
	SourceProvider string `gorm:"size:100"`
	CommonName     string
	Description    string `gorm:"type:text"`
	Genus          string
	Family         string
	SourceURL      string
	License        string
	LicenseURL     string
	SimilarSpecies string `gorm:"type:text"` // JSON-encoded []SimilarSpecies
	Negative       bool   `gorm:"index:idx_guide_cache_negative_cached,priority:1"`
	Partial        bool
	// Standalone cached_at index serves GetRecent's ORDER BY and the full-retention
	// sweep; the composite (negative, cached_at) keeps the negative-entry cleanup
	// (`WHERE negative = ? AND cached_at < ?`) off a full-table scan.
	CachedAt  time.Time `gorm:"index;index:idx_guide_cache_negative_cached,priority:2"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// TableName sets the table name for GuideCacheEntry.
func (GuideCacheEntry) TableName() string { return "guide_caches" }

// transientError wraps an error that represents a temporary failure (e.g. a 5xx
// upstream response). The cache must not persist a negative entry for these.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// NewTransientError marks err as transient (retryable).
//
// It also categorizes the wrapped error as a network failure when it is not
// already categorized. Without that, a DNS or connection-reset failure reached the
// telemetry pipeline uncategorized, so errors.IsTransientNetworkError did not
// recognize it and the existing Sentry suppression for expected external-service
// failures never fired — every transient upstream blip was reported as a novel
// error. The local transientError wrapper still drives the cache's own
// "do not persist a negative entry" decision; this makes the two classifications
// agree instead of running in parallel.
func NewTransientError(err error) error {
	if err == nil {
		return nil
	}
	if !errors.IsCategory(err, errors.CategoryNetwork) && !errors.IsCategory(err, errors.CategoryTimeout) {
		err = errors.New(err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	return &transientError{err: err}
}

// IsTransient reports whether err (or anything it wraps) is a transient failure.
func IsTransient(err error) bool {
	var te *transientError
	return errors.As(err, &te)
}

// encodeSimilarSpecies serializes the similar-species list for DB storage.
func encodeSimilarSpecies(list []SimilarSpecies) string {
	if len(list) == 0 {
		return ""
	}
	b, err := json.Marshal(list)
	if err != nil {
		// Marshaling a []SimilarSpecies effectively never fails, but if it did, "" is
		// indistinguishable from "no similar species". Log so a genuinely corrupt encode
		// is visible rather than silently persisted as an empty list.
		GetLogger().Warn("failed to encode similar-species list; storing empty",
			logger.Int("count", len(list)), logger.Error(err))
		return ""
	}
	return string(b)
}

// decodeSimilarSpecies deserializes a DB-stored similar-species list.
func decodeSimilarSpecies(encoded string) []SimilarSpecies {
	if encoded == "" {
		return nil
	}
	var list []SimilarSpecies
	if err := json.Unmarshal([]byte(encoded), &list); err != nil {
		// A corrupt-but-present list decodes to nil; log so the silent drop is visible.
		GetLogger().Warn("failed to decode stored similar-species list; dropping",
			logger.Error(err))
		return nil
	}
	return list
}

// entryToGuide maps a DB row to the domain model.
func entryToGuide(e *GuideCacheEntry) *SpeciesGuide {
	return &SpeciesGuide{
		ScientificName: e.ScientificName,
		CommonName:     e.CommonName,
		Description:    e.Description,
		Genus:          e.Genus,
		Family:         e.Family,
		// Fall back to the key when a row predates the SourceProvider column, so an
		// upgraded install shows an attribution rather than a blank one. The prefix is
		// trimmed so a composite key can never surface to a user as a provider name.
		SourceProvider: cmp.Or(e.SourceProvider, strings.TrimPrefix(e.Provider, providerSetKeyPrefix)),
		SourceURL:      e.SourceURL,
		License:        e.License,
		LicenseURL:     e.LicenseURL,
		SimilarSpecies: decodeSimilarSpecies(e.SimilarSpecies),
		CachedAt:       e.CachedAt,
		Partial:        e.Partial,
		Negative:       e.Negative,
	}
}

// guideToEntry maps the domain model to a DB row keyed by (name, locale,
// providerSet). providerSet identifies the registered provider set that produced
// the guide; g.SourceProvider is carried separately as display attribution.
func guideToEntry(name, locale, providerSet string, g *SpeciesGuide) *GuideCacheEntry {
	return &GuideCacheEntry{
		ScientificName: name,
		Locale:         locale,
		Provider:       providerSet,
		SourceProvider: g.SourceProvider,
		CommonName:     g.CommonName,
		Description:    g.Description,
		Genus:          g.Genus,
		Family:         g.Family,
		SourceURL:      g.SourceURL,
		License:        g.License,
		LicenseURL:     g.LicenseURL,
		SimilarSpecies: encodeSimilarSpecies(g.SimilarSpecies),
		Negative:       g.Negative,
		Partial:        g.Partial,
		CachedAt:       g.CachedAt,
	}
}

// GORMGuideStore is a GORM-backed GuideStore.
type GORMGuideStore struct {
	db      *gorm.DB
	metrics *metrics.GuideProviderMetrics
}

// NewGORMGuideStoreWithMetrics builds a GORM store and auto-migrates the table.
func NewGORMGuideStoreWithMetrics(db *gorm.DB, m *metrics.GuideProviderMetrics) (*GORMGuideStore, error) {
	if db == nil {
		return nil, errors.Newf("nil database handle").
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	if err := db.AutoMigrate(&GuideCacheEntry{}); err != nil {
		return nil, errors.New(err).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Context("operation", "auto_migrate").
			Build()
	}
	return &GORMGuideStore{db: db, metrics: m}, nil
}

// readSession returns a session whose logger is silenced so routine cache reads
// don't spam logs. It only affects this session, leaving the underlying db
// logger (which other callers and GetAll rely on) untouched.
func (s *GORMGuideStore) readSession(ctx context.Context) *gorm.DB {
	return s.db.Session(&gorm.Session{Logger: gormlogger.Discard}).WithContext(ctx)
}

// Get returns the cached entry for the composite key, or ErrCacheEntryNotFound.
func (s *GORMGuideStore) Get(ctx context.Context, scientificName, locale, provider string) (*GuideCacheEntry, error) {
	var entry GuideCacheEntry
	err := s.readSession(ctx).
		Where("scientific_name = ? AND locale = ? AND provider = ?", scientificName, locale, provider).
		First(&entry).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCacheEntryNotFound
		}
		s.recordDBError("read", "get")
		return nil, s.wrapDBError(err, "get")
	}
	return &entry, nil
}

// Save upserts an entry on the composite key.
func (s *GORMGuideStore) Save(ctx context.Context, entry *GuideCacheEntry) error {
	if entry == nil {
		// A nil entry is a programming error; nothing is persisted. Log so a buggy
		// caller does not read the nil (success) return as a completed write.
		GetLogger().Debug("guide store Save called with nil entry; nothing persisted")
		return nil
	}
	if entry.CachedAt.IsZero() {
		// A zero CachedAt (Go zero value, year 1) is self-destructive: Cleanup treats
		// the row as ancient and deletes it on the next sweep, and GetRecent sorts it
		// last so a bounded warm-load drops it. Every production writer stamps
		// time.Now(); stamp it here too so a future writer that forgets cannot persist a
		// row that silently vanishes with no diagnostics.
		GetLogger().Warn("guide store Save received zero CachedAt; stamping current time",
			logger.String("scientific_name", entry.ScientificName),
			logger.String("locale", entry.Locale),
			logger.String("provider", entry.Provider),
		)
		entry.CachedAt = time.Now()
	}
	// Retry on a transient lock/deadlock like every other writer to this database.
	// The guide cache shares the SQLite file with detection ingest, so an upsert
	// landing during a write burst gets SQLITE_BUSY; without the retry it fails
	// outright and the guide is dropped, forcing a re-fetch (an expensive
	// full-dataset scan) on every later request for that species. datastore's own
	// SaveImageCache and this branch's SaveSpeciesNote both wrap the same way.
	err := datastore.RetryOnLock(ctx, "save_guide_cache", func() error {
		return s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "scientific_name"}, {Name: "locale"}, {Name: "provider"},
			},
			UpdateAll: true,
		}).Create(entry).Error
	}, nil)
	if err != nil {
		s.recordDBError("write", "save")
		return s.wrapDBError(err, "save")
	}
	return nil
}

// GetAll returns all cached entries. Unlike Get, it uses the base db session so
// bulk reads remain visible in logs.
//
// Not part of the GuideStore interface: the cache loads through GetRecent, which
// bounds the result set. This is retained as store-level API for administrative and
// test use, where reading every row is the point.
func (s *GORMGuideStore) GetAll(ctx context.Context) ([]GuideCacheEntry, error) {
	var entries []GuideCacheEntry
	if err := s.db.WithContext(ctx).Find(&entries).Error; err != nil {
		s.recordDBError("read", "get_all")
		return nil, s.wrapDBError(err, "get_all")
	}
	return entries, nil
}

// GetRecent returns up to limit entries ordered most-recently-cached first. The
// warm load uses it instead of GetAll so startup cannot materialize an unbounded
// result set: DB rows are bounded only by time-based retention, so a flood of
// short-lived negative entries could otherwise load far more rows than the
// in-memory tier can hold. A non-positive limit returns all rows (matching
// GetAll); the warm path always passes a positive cap.
func (s *GORMGuideStore) GetRecent(ctx context.Context, limit int, providerSet string) ([]GuideCacheEntry, error) {
	// Secondary key id DESC gives a deterministic cutoff among rows sharing a cached_at
	// (e.g. a bulk warm insert), so which entries survive the LIMIT is stable.
	q := s.db.WithContext(ctx).Order("cached_at DESC").Order("id DESC")
	// Only rows produced by the CURRENT provider set may seed the memory tier. The
	// memory key is (name, locale) with no provider component, so without this filter
	// startup happily loaded rows written under a retired set and served them as
	// Tier-1 hits that the provider-keyed Tier-2 read would never have returned —
	// for a full PositiveTTL, and preferring the oldest row when several sets had
	// cached the same species.
	if providerSet != "" {
		q = q.Where("provider = ?", providerSet)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var entries []GuideCacheEntry
	if err := q.Find(&entries).Error; err != nil {
		s.recordDBError("read", "get_recent")
		return nil, s.wrapDBError(err, "get_recent")
	}
	return entries, nil
}

// Delete removes the entry for the composite key. Not part of the GuideStore
// interface: the cache lets entries age out on their TTL, and a provider change
// self-invalidates via the provider-set key rather than deleting anything.
// Retained as store-level API for single-entry eviction.
func (s *GORMGuideStore) Delete(ctx context.Context, scientificName, locale, provider string) error {
	err := s.db.WithContext(ctx).
		Where("scientific_name = ? AND locale = ? AND provider = ?", scientificName, locale, provider).
		Delete(&GuideCacheEntry{}).Error
	if err != nil {
		s.recordDBError("write", "delete")
		return s.wrapDBError(err, "delete")
	}
	return nil
}

// DeleteAll removes every cached entry. GORM refuses a global delete without a
// WHERE clause unless AllowGlobalUpdate is set, so the session enables it.
//
// No longer part of the GuideStore interface and not called by the cache: rows are
// keyed by the provider set that produced them, so a provider change invalidates
// itself and the retired rows age out on normal retention. Retained as store-level
// API for administrative and test use.
func (s *GORMGuideStore) DeleteAll(ctx context.Context) error {
	err := s.db.WithContext(ctx).
		Session(&gorm.Session{AllowGlobalUpdate: true}).
		Delete(&GuideCacheEntry{}).Error
	if err != nil {
		s.recordDBError("write", "delete_all")
		return s.wrapDBError(err, "delete_all")
	}
	return nil
}

// Cleanup removes expired entries. Negative (not-found) entries age out on a
// much shorter schedule (NegativeDBRetention) than positive entries
// (DBRetention) so requests for never-present species cannot accumulate
// long-lived rows. Implements the optional cleaner interface used by the cache
// refresh loop.
func (s *GORMGuideStore) Cleanup(ctx context.Context) error {
	now := time.Now()

	// Aggressively purge stale negative entries first.
	if err := s.db.WithContext(ctx).
		Where("negative = ? AND cached_at < ?", true, now.Add(-NegativeDBRetention)).
		Delete(&GuideCacheEntry{}).Error; err != nil {
		s.recordDBError("write", "cleanup")
		return s.wrapDBError(err, "cleanup")
	}

	// Then purge any entry (positive or lingering negative) past full retention.
	if err := s.db.WithContext(ctx).
		Where("cached_at < ?", now.Add(-DBRetention)).
		Delete(&GuideCacheEntry{}).Error; err != nil {
		s.recordDBError("write", "cleanup")
		return s.wrapDBError(err, "cleanup")
	}
	return nil
}

func (s *GORMGuideStore) recordDBError(errorType, operation string) {
	if s.metrics != nil {
		s.metrics.RecordDBError(errorType, operation)
	}
}

func (s *GORMGuideStore) wrapDBError(err error, operation string) error {
	GetLogger().Debug("Guide store DB error",
		logger.String("operation", operation), logger.Error(err))
	return errors.New(err).
		Component("guideprovider").
		Category(errors.CategoryDatabase).
		Context("operation", operation).
		Build()
}

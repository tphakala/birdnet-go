package guideprovider

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// GuideCacheEntry is the GORM row for the DB cache tier. The composite unique
// key is (scientific_name, locale, provider).
type GuideCacheEntry struct {
	ID             uint   `gorm:"primaryKey"`
	ScientificName string `gorm:"uniqueIndex:idx_guide_cache_key;not null"`
	Locale         string `gorm:"uniqueIndex:idx_guide_cache_key;not null"`
	Provider       string `gorm:"uniqueIndex:idx_guide_cache_key;not null"`
	CommonName     string
	Description    string `gorm:"type:text"`
	Genus          string
	Family         string
	SourceURL      string
	License        string
	LicenseURL     string
	SimilarSpecies string `gorm:"type:text"` // JSON-encoded []SimilarSpecies
	Negative       bool      `gorm:"index:idx_guide_cache_negative_cached,priority:1"`
	Partial        bool
	// Standalone cached_at index serves GetRecent's ORDER BY and the full-retention
	// sweep; the composite (negative, cached_at) keeps the negative-entry cleanup
	// (`WHERE negative = ? AND cached_at < ?`) off a full-table scan.
	CachedAt  time.Time `gorm:"index;index:idx_guide_cache_negative_cached,priority:2"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

// TableName sets the table name for GuideCacheEntry.
func (GuideCacheEntry) TableName() string { return "guide_caches" }

// transientError wraps an error that represents a temporary failure (e.g. a 5xx
// upstream response). The cache must not persist a negative entry for these.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// NewTransientError marks err as transient (retryable).
func NewTransientError(err error) error {
	if err == nil {
		return nil
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
		SourceProvider: e.Provider,
		SourceURL:      e.SourceURL,
		License:        e.License,
		LicenseURL:     e.LicenseURL,
		SimilarSpecies: decodeSimilarSpecies(e.SimilarSpecies),
		CachedAt:       e.CachedAt,
		Partial:        e.Partial,
		Negative:       e.Negative,
	}
}

// guideToEntry maps the domain model to a DB row keyed by (name, locale, provider).
func guideToEntry(name, locale, provider string, g *SpeciesGuide) *GuideCacheEntry {
	return &GuideCacheEntry{
		ScientificName: name,
		Locale:         locale,
		Provider:       provider,
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
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "scientific_name"}, {Name: "locale"}, {Name: "provider"},
		},
		UpdateAll: true,
	}).Create(entry).Error
	if err != nil {
		s.recordDBError("write", "save")
		return s.wrapDBError(err, "save")
	}
	return nil
}

// GetAll returns all cached entries. Unlike Get, it uses the base db session so
// bulk startup loads remain visible in logs.
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
func (s *GORMGuideStore) GetRecent(ctx context.Context, limit int) ([]GuideCacheEntry, error) {
	// Secondary key id DESC gives a deterministic cutoff among rows sharing a cached_at
	// (e.g. a bulk warm insert), so which entries survive the LIMIT is stable.
	q := s.db.WithContext(ctx).Order("cached_at DESC").Order("id DESC")
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

// Delete removes the entry for the composite key.
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
// WHERE clause unless AllowGlobalUpdate is set, so the session enables it. Used to
// invalidate the whole cache when the registered provider set changes.
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

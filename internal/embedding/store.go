package embedding

import (
	"context"
	"database/sql"
	stdlog "log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// DefaultMaxRows bounds the rolling window of retained embeddings when the
// caller does not specify a cap. Sizing math: a 1536-dim fp16 vector is ~3 KB,
// so 50k rows is on the order of 150 MB of vector data plus row overhead.
// It is the single source of truth for the row-cap default; callers that
// resolve a non-positive configured cap should fall back to this value.
const DefaultMaxRows int64 = 50_000

// sqlitePragmas are applied to every pooled connection via the DSN so they are
// not lost on connections opened after the first. WAL plus a generous
// busy_timeout keeps the separate embeddings database from blocking under
// concurrent capture, and isolates it from the main birdnet.db journal. The
// cache is intentionally small (-2000 = ~2 MB) for this auxiliary store.
const sqlitePragmas = "_journal_mode=WAL&_busy_timeout=30000&_synchronous=NORMAL&_foreign_keys=ON&_cache_size=-2000"

// Sentinel errors returned by the Store.
var (
	// ErrNotFound is returned by Get when no embedding exists for the
	// requested detection id.
	ErrNotFound = errors.Newf("embedding: not found").
			Component("embedding").
			Category(errors.CategoryValidation).
			Build()

	// ErrInvalidRecord is returned by Put when a record fails validation, for
	// example an empty detection id or a declared dimension that disagrees with
	// the vector length.
	ErrInvalidRecord = errors.Newf("embedding: invalid record").
				Component("embedding").
				Category(errors.CategoryValidation).
				Build()
)

// Record is a single stored embedding and its provenance. Vector holds the
// decoded components; the Store encodes them to the raw blob on write and
// decodes them on read according to Format.
type Record struct {
	DetectionID string    // logical reference to the saved detection (note) this embedding belongs to
	Model       string    // model identifier that produced the embedding
	Source      string    // capture source (e.g. audio stream) for provenance and filtering
	CapturedAt  time.Time // when the detection was captured; drives ordering and the rolling cap
	Format      Format    // on-disk encoding of the vector blob
	Dim         int       // declared dimension; must equal len(Vector)
	Version     string    // model version for provenance (part of the discriminator)
	Vector      []float32 // decoded embedding components
}

// embeddingRow is the GORM model persisted in the embeddings table. It is kept
// separate from the public Record so the storage layout (the raw blob and the
// format discriminator) is not part of the API surface.
type embeddingRow struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	DetectionID string    `gorm:"column:detection_id;uniqueIndex;not null"`
	Model       string    `gorm:"column:model;index:idx_model_captured;not null"`
	Source      string    `gorm:"column:source;not null"`
	CapturedAt  time.Time `gorm:"column:captured_at;index:idx_captured_at;index:idx_model_captured;not null"`
	Format      string    `gorm:"column:format;not null"`
	Dim         int       `gorm:"column:dim;not null"`
	Version     string    `gorm:"column:version;not null"`
	Vector      []byte    `gorm:"column:vector;not null"` // raw encoded blob; source of truth
}

// TableName sets the physical table name for embeddingRow.
func (embeddingRow) TableName() string { return "embeddings" }

// newGormLogger builds the GORM logger for the store. It logs at warn level and
// suppresses the expected "record not found" noise, which Get translates into a
// typed ErrNotFound rather than a logged warning.
func newGormLogger() gormlogger.Interface {
	return gormlogger.New(
		stdlog.New(os.Stderr, "", stdlog.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             500 * time.Millisecond,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
		},
	)
}

// Option configures a Store at construction time.
type Option func(*storeConfig)

type storeConfig struct {
	maxRows int64
}

// WithMaxRows sets the rolling-window cap (maximum retained rows) enforced by
// Prune. Non-positive values are ignored and the default cap is used.
func WithMaxRows(n int) Option {
	return func(c *storeConfig) {
		if n > 0 {
			c.maxRows = int64(n)
		}
	}
}

// Store persists embeddings in a dedicated SQLite database, separate from the
// main application database, so embedding capture never contends on the main
// database journal. It is safe for concurrent use.
type Store struct {
	db      *gorm.DB
	sqlDB   *sql.DB
	path    string
	maxRows int64
	log     logger.Logger
}

// NewStore opens (creating if necessary) the embeddings database at path and
// migrates its schema. The parent directory is created if it does not exist.
func NewStore(path string, opts ...Option) (*Store, error) {
	cfg := storeConfig{maxRows: DefaultMaxRows}
	for _, opt := range opts {
		opt(&cfg)
	}

	// The DSN appends pragmas after "?"; a path containing a DSN delimiter
	// would corrupt the query string and silently drop the pragmas (WAL,
	// busy_timeout), so reject such paths before touching the filesystem.
	if path == "" {
		return nil, errors.Newf("embedding: database path must not be empty").
			Component("embedding").
			Category(errors.CategoryValidation).
			Build()
	}
	if strings.ContainsAny(path, "?#") {
		return nil, errors.Newf("embedding: database path must not contain '?' or '#': %q", path).
			Component("embedding").
			Category(errors.CategoryValidation).
			Build()
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, errors.New(err).
			Component("embedding").
			Category(errors.CategorySystem).
			Context("operation", "create_embedding_db_directory").
			Context("directory", filepath.Dir(path)).
			Build()
	}

	dsn := path + "?" + sqlitePragmas
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: newGormLogger(),
	})
	if err != nil {
		return nil, errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "open_embedding_db").
			Context("db_path", path).
			Build()
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "get_underlying_sqldb").
			Build()
	}
	// SQLite permits a single writer; serialise all access through one
	// connection so concurrent capture cannot trigger "database is locked".
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&embeddingRow{}); err != nil {
		_ = sqlDB.Close()
		return nil, errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "migrate_embedding_db").
			Context("db_path", path).
			Build()
	}

	s := &Store{
		db:      db,
		sqlDB:   sqlDB,
		path:    path,
		maxRows: cfg.maxRows,
		log:     logger.Global().Module("embedding"),
	}
	s.log.Info("Opened embedding store",
		logger.String("path", path),
		logger.Int64("max_rows", cfg.maxRows))
	return s, nil
}

// Put stores or replaces the embedding for a detection. Storing twice under the
// same detection id replaces the existing row (upsert) so a re-emitted
// detection cannot double-store.
func (s *Store) Put(ctx context.Context, rec *Record) error {
	if rec == nil || rec.DetectionID == "" {
		return ErrInvalidRecord
	}
	if rec.Dim <= 0 || rec.Dim != len(rec.Vector) {
		return ErrInvalidRecord
	}
	format := rec.Format
	if format == "" {
		format = FormatFP16
	}

	blob, err := encodeVector(rec.Vector, format)
	if err != nil {
		return err
	}

	row := embeddingRow{
		DetectionID: rec.DetectionID,
		Model:       rec.Model,
		Source:      rec.Source,
		CapturedAt:  rec.CapturedAt.UTC(),
		Format:      string(format),
		Dim:         rec.Dim,
		Version:     rec.Version,
		Vector:      blob,
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "detection_id"}},
		UpdateAll: true,
	}).Create(&row).Error; err != nil {
		return errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "put_embedding").
			Context("detection_id", rec.DetectionID).
			Build()
	}
	return nil
}

// Get returns the embedding stored for the given detection id, decoding its
// vector. It returns ErrNotFound when no such embedding exists.
func (s *Store) Get(ctx context.Context, detectionID string) (Record, error) {
	var row embeddingRow
	if err := s.db.WithContext(ctx).
		Where("detection_id = ?", detectionID).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Record{}, ErrNotFound
		}
		return Record{}, errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "get_embedding").
			Context("detection_id", detectionID).
			Build()
	}
	return rowToRecord(&row)
}

// Query returns embeddings for a model whose capture time falls within the
// half-open interval [from, to), ordered by capture time ascending.
func (s *Store) Query(ctx context.Context, model string, from, to time.Time) ([]Record, error) {
	var rows []embeddingRow
	if err := s.db.WithContext(ctx).
		Where("model = ? AND captured_at >= ? AND captured_at < ?", model, from.UTC(), to.UTC()).
		Order("captured_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "query_embeddings").
			Context("model", model).
			Build()
	}

	records := make([]Record, 0, len(rows))
	for i := range rows {
		rec, err := rowToRecord(&rows[i])
		if err != nil {
			// A single undecodable blob must not hide the rest of the
			// timeline from consumers; log and skip it.
			s.log.Warn("Skipping undecodable embedding row",
				logger.String("detection_id", rows[i].DetectionID),
				logger.Error(err))
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// Prune enforces the rolling-window cap by deleting the oldest rows so at most
// maxRows remain. It returns the number of rows deleted.
func (s *Store) Prune(ctx context.Context) (int, error) {
	db := s.db.WithContext(ctx)

	var count int64
	if err := db.Model(&embeddingRow{}).Count(&count).Error; err != nil {
		return 0, errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "count_embeddings").
			Build()
	}
	if count <= s.maxRows {
		return 0, nil
	}

	toDelete := count - s.maxRows
	// Limit takes an int; clamp so the int64->int conversion cannot truncate
	// to a wrong value on 32-bit builds (unreachable on our 64-bit targets,
	// but defensive).
	if toDelete > math.MaxInt32 {
		toDelete = math.MaxInt32
	}
	// Delete the oldest rows via a subquery so the candidate IDs never
	// round-trip through the application. An explicit "id IN (?, ?, ...)"
	// list would allocate an unbounded slice and can exceed SQLite's
	// bound-variable limit when the overflow is large.
	oldest := s.db.WithContext(ctx).
		Model(&embeddingRow{}).
		Select("id").
		Order("captured_at ASC, id ASC").
		Limit(int(toDelete))
	res := s.db.WithContext(ctx).
		Where("id IN (?)", oldest).
		Delete(&embeddingRow{})
	if res.Error != nil {
		return 0, errors.New(res.Error).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "prune_embeddings").
			Build()
	}
	return int(res.RowsAffected), nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	if s.sqlDB == nil {
		return nil
	}
	if err := s.sqlDB.Close(); err != nil {
		return errors.New(err).
			Component("embedding").
			Category(errors.CategoryDatabase).
			Context("operation", "close_embedding_db").
			Build()
	}
	return nil
}

// rowToRecord decodes a stored row into a public Record.
func rowToRecord(row *embeddingRow) (Record, error) {
	format := Format(row.Format)
	vec, err := decodeVector(row.Vector, format, row.Dim)
	if err != nil {
		return Record{}, err
	}
	return Record{
		DetectionID: row.DetectionID,
		Model:       row.Model,
		Source:      row.Source,
		CapturedAt:  row.CapturedAt.UTC(),
		Format:      format,
		Dim:         row.Dim,
		Version:     row.Version,
		Vector:      vec,
	}, nil
}

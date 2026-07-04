// Package birdnetpi implements the imports.Source interface for BirdNET-Pi SQLite databases.
package birdnetpi

import (
	"context"
	"database/sql"
	"net/url"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imports"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Source reads detections from a BirdNET-Pi SQLite database.
type Source struct {
	path string
	db   *gorm.DB
}

// New opens the BirdNET-Pi database at path read-only.
// Call Close when done.
func New(path string) (*Source, error) {
	// Build a SQLite file URI that preserves path slashes but escapes URI-special
	// characters (?, #, %, space). OmitHost keeps the "file:/abs/path" form.
	dsn := (&url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: "mode=ro"}).String()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryDatabase).
			Context("operation", "open").
			Context("path", path).
			Build()
	}

	sqlDB, err := db.DB()
	if err != nil {
		// db.DB() failing means the internal connection pool is unavailable.
		// We cannot retrieve the underlying sql.DB to close it, so the gorm
		// instance is abandoned. This path is not reachable under normal operation.
		return nil, errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryDatabase).
			Context("operation", "get_sql_db").
			Build()
	}
	// Single connection avoids shared-cache locking issues on read-only SQLite.
	sqlDB.SetMaxOpenConns(1)

	return &Source{path: path, db: db}, nil
}

// Validate confirms the detections table exists and has the expected schema.
// It issues a LIMIT 0 query selecting all columns that the adapter reads,
// so a missing column causes a clear error before any data is processed.
func (s *Source) Validate(ctx context.Context) error {
	err := s.db.WithContext(ctx).
		Raw("SELECT Sci_Name, Com_Name, Confidence, Lat, Lon, Cutoff, Sens, Time, Date, File_Name FROM detections LIMIT 0").
		Scan(nil).Error
	if err != nil {
		return errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryValidation).
			Context("operation", "validate").
			Context("path", s.path).
			Build()
	}
	return nil
}

// Count returns the total number of rows in the detections table.
func (s *Source) Count(ctx context.Context) (int, error) {
	var count int64
	err := s.db.WithContext(ctx).Table("detections").Count(&count).Error
	if err != nil {
		return 0, errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryDatabase).
			Context("operation", "count").
			Build()
	}
	return int(count), nil
}

// Iterate streams rows in batches using rowid cursor pagination.
// Cursor pagination on the implicit rowid is O(N) total and avoids the skip/duplicate
// hazard of offset-based paging on non-unique (Date, Time) ordering.
// fn is called once per batch; returning an error stops iteration.
func (s *Source) Iterate(ctx context.Context, batchSize int, fn func([]imports.SourceDetection) error) error {
	if batchSize <= 0 {
		batchSize = 500
	}

	var lastRowID int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Use raw SQL to get Date/Time as text, avoiding GORM's automatic time.Time conversion.
		// Cursor on rowid ensures O(N) total scan with no duplicates or skips.
		var rows []struct {
			RowID      int64 `gorm:"column:row_id"`
			Date       string
			Time       string
			SciName    string `gorm:"column:Sci_Name"`
			ComName    string `gorm:"column:Com_Name"`
			Confidence float64
			Lat        float64
			Lon        float64
			Cutoff     float64
			Sens       float64
			FileName   string `gorm:"column:File_Name"`
		}

		err := s.db.WithContext(ctx).
			Raw("SELECT rowid AS row_id, CAST(Date AS TEXT) AS Date, CAST(Time AS TEXT) AS Time, Sci_Name, Com_Name, Confidence, Lat, Lon, Cutoff, Sens, File_Name FROM detections WHERE rowid > ? ORDER BY rowid LIMIT ?", lastRowID, batchSize).
			Scan(&rows).Error
		if err != nil {
			return errors.New(err).
				Component("imports/birdnetpi").
				Category(errors.CategoryDatabase).
				Context("operation", "iterate").
				Context("last_row_id", lastRowID).
				Build()
		}

		if len(rows) == 0 {
			break
		}

		batch := make([]imports.SourceDetection, len(rows))
		for i := range rows {
			r := &rows[i]
			batch[i] = imports.SourceDetection{
				Date:           r.Date,
				Time:           r.Time,
				ScientificName: r.SciName,
				CommonName:     r.ComName,
				Confidence:     r.Confidence,
				Latitude:       r.Lat,
				Longitude:      r.Lon,
				Cutoff:         r.Cutoff,
				Sensitivity:    r.Sens,
				FileName:       r.FileName,
			}
			if r.RowID > lastRowID {
				lastRowID = r.RowID
			}
		}

		fnErr := fn(batch)
		if fnErr != nil {
			return fnErr
		}

		if len(rows) < batchSize {
			break
		}
	}

	return nil
}

// Close releases the database connection.
func (s *Source) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryDatabase).
			Context("operation", "close").
			Build()
	}
	if err := sqlDB.Close(); err != nil {
		return errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryDatabase).
			Context("operation", "close").
			Build()
	}
	return nil
}

// LatestDate returns the most recent detection date as stored ("YYYY-MM-DD"),
// or "" when the table is empty.
func (s *Source) LatestDate(ctx context.Context) (string, error) {
	var date sql.NullString
	// Date is a TEXT column ("YYYY-MM-DD"), so MAX is the lexical (and thus
	// chronological) maximum. No CAST: it is redundant for a TEXT column and
	// would prevent SQLite from using an index on Date.
	err := s.db.WithContext(ctx).
		Raw("SELECT MAX(Date) FROM detections").
		Scan(&date).Error
	if err != nil {
		return "", errors.New(err).
			Component("imports/birdnetpi").
			Category(errors.CategoryDatabase).
			Context("operation", "latest_date").
			Context("table", "detections").
			Build()
	}
	if !date.Valid {
		return "", nil
	}
	return date.String, nil
}

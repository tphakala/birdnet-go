// Package birdnetpi implements the imports.Source interface for BirdNET-Pi SQLite databases.
package birdnetpi

import (
	"context"
	"fmt"

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
	dsn := fmt.Sprintf("file:%s?mode=ro", path)
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

// Validate confirms the detections table exists and is readable.
func (s *Source) Validate(ctx context.Context) error {
	var count int64
	err := s.db.WithContext(ctx).Table("detections").Count(&count).Error
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

// Iterate streams rows in batches ordered by Date, Time.
// fn is called once per batch; returning an error stops iteration.
func (s *Source) Iterate(ctx context.Context, batchSize int, fn func([]imports.SourceDetection) error) error {
	if batchSize <= 0 {
		batchSize = 500
	}

	offset := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Use raw SQL to get Date/Time as text, avoiding GORM's automatic time.Time conversion
		var rows []struct {
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
			Select("CAST(Date AS TEXT) as Date, CAST(Time AS TEXT) as Time, Sci_Name, Com_Name, Confidence, Lat, Lon, Cutoff, Sens, File_Name").
			Table("detections").
			Order("Date, Time").
			Limit(batchSize).
			Offset(offset).
			Scan(&rows).Error
		if err != nil {
			return errors.New(err).
				Component("imports/birdnetpi").
				Category(errors.CategoryDatabase).
				Context("operation", "iterate").
				Context("offset", fmt.Sprintf("%d", offset)).
				Build()
		}

		if len(rows) == 0 {
			break
		}

		batch := make([]imports.SourceDetection, len(rows))
		for i, r := range rows {
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
		}

		fnErr := fn(batch)
		if fnErr != nil {
			return fnErr
		}

		offset += len(rows)
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
		return err
	}
	return sqlDB.Close()
}

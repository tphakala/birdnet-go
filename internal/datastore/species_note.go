// species_note.go implements per-species user notes for the species guide feature.
package datastore

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// SpeciesNoteMaxLength is the maximum allowed length (in bytes) of a note entry.
const SpeciesNoteMaxLength = 10_000

// SpeciesNotesMaxResults bounds how many notes GetSpeciesNotes returns for a
// single species, capping the worst-case result size even for authenticated
// callers; newest-first ordering means the most recent notes are retained.
const SpeciesNotesMaxResults = 500

// GormDBProvider exposes the underlying GORM handle for features that need direct
// database access (e.g. the species guide cache store). The concrete SQLite and
// MySQL stores satisfy it via the embedded DataStore.
type GormDBProvider interface {
	GormDB() *gorm.DB
}

// GormDB returns the underlying GORM database handle, or nil if not connected.
func (ds *DataStore) GormDB() *gorm.DB {
	return ds.DB
}

// ErrSpeciesNoteNotFound is returned when a species note cannot be located.
var ErrSpeciesNoteNotFound = errors.Newf("species note not found").
	Component("datastore").
	Category(errors.CategoryNotFound).
	Build()

// ErrSpeciesNoteTooLong is returned when a note entry exceeds SpeciesNoteMaxLength
// bytes. It is a distinct sentinel (not a generic validation error) so the API
// layer can map specifically the too-long case to its "note too long" message and
// not mislabel other validation failures — an empty entry or an invalid note ID —
// as "too long".
var ErrSpeciesNoteTooLong = errors.Newf("species note exceeds maximum length").
	Component("datastore").
	Category(errors.CategoryValidation).
	Build()

// SpeciesNote is a user-authored note attached to a species (by scientific name,
// not to a single detection). A species can have many notes.
type SpeciesNote struct {
	ID             uint      `gorm:"primaryKey"`
	ScientificName string    `gorm:"index;not null"`
	Entry          string    `gorm:"type:text;not null"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

// NormalizeSpeciesNoteEntry trims an entry and rejects entries exceeding
// SpeciesNoteMaxLength bytes. It is applied on both Save and Update.
func NormalizeSpeciesNoteEntry(entry string) (string, error) {
	trimmed := strings.TrimSpace(entry)
	if len(trimmed) > SpeciesNoteMaxLength {
		// Wrap the sentinel so the API layer can match it with errors.Is while the
		// telemetry context (actual length) is preserved.
		return "", errors.New(ErrSpeciesNoteTooLong).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("entry_length", len(trimmed)).
			Build()
	}
	return trimmed, nil
}

// normalizeSpeciesName normalizes a scientific name for consistent lookups so
// notes match regardless of surrounding whitespace.
//
// It deliberately does NOT canonicalize through openfauna.CanonicalName, unlike the
// content-resolution paths (openfauna/links.go, guideprovider/openfauna.go,
// api/v2/species). That is the intended line, not an oversight: content resolution
// canonicalizes, STORAGE KEYS do not. This key mirrors the detections key, and the
// guide cache key does the same. Reads and writes here are symmetric, so there is no
// read/write drift to fix.
//
// Canonicalizing it now would orphan every note already written under a legacy name,
// and — because the dataset tracks eBird/Clements annual reissues — would create a
// recurring re-migration obligation on every dataset bump. The underlying split-rows
// problem predates this and lives upstream (ingestion was canonicalized without
// backfilling history); the fix for it is a one-shot backfill of the detection table,
// not a change here.
func normalizeSpeciesName(scientificName string) string {
	return strings.TrimSpace(scientificName)
}

// SpeciesNoteOps is the single implementation of the species-note storage
// operations. Both the legacy DataStore and the v2-only store bind it to their
// own GORM handle and metrics recorder and delegate to it, so the two cannot
// drift on ordering, result limit, validation, lock-retry behaviour or error
// context — the same reason ParseSpeciesNoteID, NormalizeSpeciesNoteEntry and
// SpeciesNotesMaxResults are shared rather than copied.
type SpeciesNoteOps struct {
	db      *gorm.DB
	metrics *Metrics
}

// NewSpeciesNoteOps binds the species-note operations to a GORM handle and the
// calling store's metrics recorder. A nil recorder is accepted; RetryOnLock
// treats it as "metrics disabled".
func NewSpeciesNoteOps(db *gorm.DB, metrics *Metrics) SpeciesNoteOps {
	return SpeciesNoteOps{db: db, metrics: metrics}
}

// GetSpeciesNotes returns all notes for a species, newest first.
func (o SpeciesNoteOps) GetSpeciesNotes(ctx context.Context, scientificName string) ([]SpeciesNote, error) {
	name := normalizeSpeciesName(scientificName)
	if name == "" {
		return nil, validationError("scientific name cannot be empty", "scientific_name", scientificName)
	}
	var notes []SpeciesNote
	if err := o.db.WithContext(ctx).
		Where("scientific_name = ?", name).
		Order("created_at DESC, id DESC").
		Limit(SpeciesNotesMaxResults).
		Find(&notes).Error; err != nil {
		return nil, dbError(err, "get_species_notes", errors.PriorityLow,
			"scientific_name", name, "table", "species_notes")
	}
	return notes, nil
}

// GetSpeciesNoteByID returns a single note by ID, or ErrSpeciesNoteNotFound.
func (o SpeciesNoteOps) GetSpeciesNoteByID(ctx context.Context, id uint) (*SpeciesNote, error) {
	var note SpeciesNote
	if err := o.db.WithContext(ctx).First(&note, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSpeciesNoteNotFound
		}
		return nil, dbError(err, "get_species_note_by_id", errors.PriorityLow,
			"note_id", strconv.FormatUint(uint64(id), 10), "table", "species_notes")
	}
	return &note, nil
}

// SaveSpeciesNote persists a new note. The entry is normalized and the
// scientific name trimmed before write.
func (o SpeciesNoteOps) SaveSpeciesNote(ctx context.Context, note *SpeciesNote) error {
	if note == nil {
		return validationError("note cannot be nil", "note", nil)
	}
	note.ScientificName = normalizeSpeciesName(note.ScientificName)
	if note.ScientificName == "" {
		return validationError("scientific name cannot be empty", "scientific_name", note.ScientificName)
	}
	entry, err := NormalizeSpeciesNoteEntry(note.Entry)
	if err != nil {
		return err
	}
	if entry == "" {
		return validationError("entry cannot be empty", "entry", "")
	}
	note.Entry = entry

	return RetryOnLock(ctx, "save_species_note", func() error {
		if err := o.db.WithContext(ctx).Create(note).Error; err != nil {
			return dbError(err, "save_species_note", errors.PriorityMedium,
				"scientific_name", note.ScientificName, "table", "species_notes")
		}
		return nil
	}, o.metrics)
}

// UpdateSpeciesNote updates an existing note's entry, or returns ErrSpeciesNoteNotFound.
func (o SpeciesNoteOps) UpdateSpeciesNote(ctx context.Context, noteID, entry string) error {
	id, err := ParseSpeciesNoteID(noteID)
	if err != nil {
		return err
	}
	normalized, err := NormalizeSpeciesNoteEntry(entry)
	if err != nil {
		return err
	}
	if normalized == "" {
		return validationError("entry cannot be empty", "entry", "")
	}

	return RetryOnLock(ctx, "update_species_note", func() error {
		result := o.db.WithContext(ctx).
			Model(&SpeciesNote{}).
			Where("id = ?", id).
			Update("entry", normalized)
		if result.Error != nil {
			return dbError(result.Error, "update_species_note", errors.PriorityMedium,
				"note_id", noteID, "table", "species_notes")
		}
		if result.RowsAffected == 0 {
			return ErrSpeciesNoteNotFound
		}
		return nil
	}, o.metrics)
}

// DeleteSpeciesNote removes a note by ID, or returns ErrSpeciesNoteNotFound.
func (o SpeciesNoteOps) DeleteSpeciesNote(ctx context.Context, noteID string) error {
	id, err := ParseSpeciesNoteID(noteID)
	if err != nil {
		return err
	}

	return RetryOnLock(ctx, "delete_species_note", func() error {
		result := o.db.WithContext(ctx).Delete(&SpeciesNote{}, id)
		if result.Error != nil {
			return dbError(result.Error, "delete_species_note", errors.PriorityMedium,
				"note_id", noteID, "table", "species_notes")
		}
		if result.RowsAffected == 0 {
			return ErrSpeciesNoteNotFound
		}
		return nil
	}, o.metrics)
}

// speciesNotes binds the shared species-note operations to this store.
func (ds *DataStore) speciesNotes() SpeciesNoteOps {
	return NewSpeciesNoteOps(ds.DB, ds.getMetrics())
}

// GetSpeciesNotes returns all notes for a species, newest first.
func (ds *DataStore) GetSpeciesNotes(ctx context.Context, scientificName string) ([]SpeciesNote, error) {
	return ds.speciesNotes().GetSpeciesNotes(ctx, scientificName)
}

// GetSpeciesNoteByID returns a single note by ID, or ErrSpeciesNoteNotFound.
func (ds *DataStore) GetSpeciesNoteByID(ctx context.Context, id uint) (*SpeciesNote, error) {
	return ds.speciesNotes().GetSpeciesNoteByID(ctx, id)
}

// SaveSpeciesNote persists a new note. The entry is normalized and the
// scientific name trimmed before write.
func (ds *DataStore) SaveSpeciesNote(ctx context.Context, note *SpeciesNote) error {
	return ds.speciesNotes().SaveSpeciesNote(ctx, note)
}

// UpdateSpeciesNote updates an existing note's entry, or returns ErrSpeciesNoteNotFound.
func (ds *DataStore) UpdateSpeciesNote(ctx context.Context, noteID, entry string) error {
	return ds.speciesNotes().UpdateSpeciesNote(ctx, noteID, entry)
}

// DeleteSpeciesNote removes a note by ID, or returns ErrSpeciesNoteNotFound.
func (ds *DataStore) DeleteSpeciesNote(ctx context.Context, noteID string) error {
	return ds.speciesNotes().DeleteSpeciesNote(ctx, noteID)
}

// ParseSpeciesNoteID parses a string note ID into a uint, rejecting invalid input.
// The range guard matters on 32-bit builds (e.g. 32-bit Raspberry Pi OS), where uint
// is 32-bit: without it a value above math.MaxUint32 would silently wrap and address
// the wrong row. The check is a no-op on 64-bit, where math.MaxUint == math.MaxUint64.
//
// Exported so the v2-only store shares it (alongside NormalizeSpeciesNoteEntry and
// SpeciesNotesMaxResults) rather than keeping a second copy: both back the same API
// surface, so the two must agree on which note IDs are accepted.
func ParseSpeciesNoteID(noteID string) (uint, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(noteID), 10, 64)
	if err != nil || id == 0 || id > uint64(math.MaxUint) {
		return 0, validationError("invalid note ID", "note_id", noteID)
	}
	return uint(id), nil
}

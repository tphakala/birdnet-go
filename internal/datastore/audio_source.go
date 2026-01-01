package datastore

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetOrCreateAudioSource upserts an audio source record
func (ds *DataStore) GetOrCreateAudioSource(sourceID, label, sourceType string) (*AudioSourceRecord, error) {
	if sourceID == "" {
		return nil, errors.Newf("source ID cannot be empty").
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}

	now := time.Now()
	record := &AudioSourceRecord{
		ID:        sourceID,
		Label:     label,
		Type:      sourceType,
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := ds.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "type", "updated_at"}),
	}).Create(record).Error

	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_or_create_audio_source").
			Context("source_id", sourceID).
			Build()
	}

	return record, nil
}

// GetAudioSource retrieves an audio source by ID
func (ds *DataStore) GetAudioSource(sourceID string) (*AudioSourceRecord, error) {
	var record AudioSourceRecord
	err := ds.DB.Where("id = ?", sourceID).First(&record).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.Newf("audio source not found: %s", sourceID).
				Component("datastore").
				Category(errors.CategoryNotFound).
				Build()
		}
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_audio_source").
			Context("source_id", sourceID).
			Build()
	}

	return &record, nil
}

// ensureAudioSourceInTransaction upserts audio source within a transaction
func (ds *DataStore) ensureAudioSourceInTransaction(tx *gorm.DB, note *Note, txID string, attempt int) error {
	now := time.Now()
	record := &AudioSourceRecord{
		ID:        note.Source.ID,
		Label:     note.Source.DisplayName,
		Type:      detectSourceType(note.Source.ID),
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "type", "updated_at"}),
	}).Create(record).Error

	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "ensure_audio_source").
			Context("tx_id", txID).
			Context("attempt", attempt).
			Context("source_id", note.Source.ID).
			Build()
	}

	return nil
}

func detectSourceType(sourceID string) string {
	if strings.HasPrefix(sourceID, "rtsp") {
		return "rtsp"
	}
	if strings.HasPrefix(sourceID, "file") {
		return "file"
	}
	return "device"
}

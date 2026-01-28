package repository

import (
	"context"
	"errors"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// audioSourceRepository implements AudioSourceRepository.
type audioSourceRepository struct {
	db          *gorm.DB
	useV2Prefix bool
	isMySQL     bool
}

// NewAudioSourceRepository creates a new AudioSourceRepository.
// Parameters:
//   - db: GORM database connection
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewAudioSourceRepository(db *gorm.DB, useV2Prefix, isMySQL bool) AudioSourceRepository {
	return &audioSourceRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

// tableName returns the appropriate table name.
func (r *audioSourceRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2AudioSources
	}
	return tableAudioSources
}

// GetOrCreate retrieves an existing audio source or creates a new one.
func (r *audioSourceRepository) GetOrCreate(ctx context.Context, sourceURI, nodeName string, displayName *string, sourceType entities.SourceType) (*entities.AudioSource, error) {
	var source entities.AudioSource

	// Try to find existing
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("source_uri = ? AND node_name = ?", sourceURI, nodeName).
		First(&source).Error
	if err == nil {
		return &source, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Auto-detect source type if not provided
	if sourceType == "" {
		sourceType = detectSourceType(sourceURI)
	}

	// Create new source
	source = entities.AudioSource{
		SourceURI:   sourceURI,
		NodeName:    nodeName,
		DisplayName: displayName,
		SourceType:  sourceType,
	}

	createErr := r.db.WithContext(ctx).Table(r.tableName()).Create(&source).Error
	if createErr != nil {
		// Handle race condition - another goroutine may have created it.
		// Try to fetch the existing record; if that also fails, return the original create error.
		findErr := r.db.WithContext(ctx).Table(r.tableName()).
			Where("source_uri = ? AND node_name = ?", sourceURI, nodeName).
			First(&source).Error
		if findErr != nil {
			return nil, createErr
		}
	}

	return &source, nil
}

// detectSourceType attempts to determine the source type from the URI.
func detectSourceType(sourceURI string) entities.SourceType {
	if sourceURI == "" {
		return entities.SourceTypeUnknown
	}

	switch {
	case len(sourceURI) >= 7 && sourceURI[:7] == "rtsp://":
		return entities.SourceTypeRTSP
	case len(sourceURI) >= 3 && sourceURI[:3] == "hw:":
		return entities.SourceTypeALSA
	case len(sourceURI) >= 8 && sourceURI[:8] == "default:":
		return entities.SourceTypePulseAudio
	case sourceURI[0] == '/' || sourceURI[0] == '.':
		return entities.SourceTypeFile
	default:
		return entities.SourceTypeUnknown
	}
}

// GetByID retrieves an audio source by its ID.
func (r *audioSourceRepository) GetByID(ctx context.Context, id uint) (*entities.AudioSource, error) {
	var source entities.AudioSource
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&source, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAudioSourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &source, nil
}

// GetBySourceURI retrieves an audio source by its URI and node name.
func (r *audioSourceRepository) GetBySourceURI(ctx context.Context, sourceURI, nodeName string) (*entities.AudioSource, error) {
	var source entities.AudioSource
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("source_uri = ? AND node_name = ?", sourceURI, nodeName).
		First(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAudioSourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &source, nil
}

// GetAll retrieves all audio sources.
func (r *audioSourceRepository) GetAll(ctx context.Context) ([]*entities.AudioSource, error) {
	var sources []*entities.AudioSource
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("node_name ASC, source_uri ASC").
		Find(&sources).Error
	return sources, err
}

// GetByNodeName retrieves all audio sources for a specific node.
func (r *audioSourceRepository) GetByNodeName(ctx context.Context, nodeName string) ([]*entities.AudioSource, error) {
	var sources []*entities.AudioSource
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("node_name = ?", nodeName).
		Order("source_uri ASC").
		Find(&sources).Error
	return sources, err
}

// Count returns the total number of audio sources.
func (r *audioSourceRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).Count(&count).Error
	return count, err
}

// Delete removes an audio source by ID.
func (r *audioSourceRepository) Delete(ctx context.Context, id uint) error {
	result := r.db.WithContext(ctx).Table(r.tableName()).Delete(&entities.AudioSource{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAudioSourceNotFound
	}
	return nil
}

// Update modifies an audio source's fields.
func (r *audioSourceRepository) Update(ctx context.Context, id uint, updates map[string]any) error {
	result := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAudioSourceNotFound
	}
	return nil
}

// Exists checks if an audio source with the given ID exists.
func (r *audioSourceRepository) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Count(&count).Error
	return count > 0, err
}

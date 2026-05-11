package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// AudioSourceWithDetectionCount pairs an audio source with the number of detections
// recorded against it. Used to populate the analytics source-filter picker, where the
// frontend wants both the metadata and the activity count for a single round trip.
type AudioSourceWithDetectionCount struct {
	Source         *entities.AudioSource
	DetectionCount int64
}

// AudioSourceRepository provides access to the audio_sources table.
type AudioSourceRepository interface {
	// GetOrCreate retrieves an existing audio source or creates a new one.
	// Matches on sourceURI + nodeName combination.
	// sourceURI is the source identifier (e.g., "rtsp://...", "hw:0,0").
	// nodeName identifies the processing node.
	// displayName is an optional human-readable name; if the source already exists
	// and displayName differs from the stored value, the DB row is updated.
	// sourceType is auto-detected from the URI if not provided and is NOT updated
	// on existing sources; use Update() for that.
	GetOrCreate(ctx context.Context, sourceURI, nodeName string, displayName *string, sourceType entities.SourceType) (*entities.AudioSource, error)

	// GetByID retrieves an audio source by its ID.
	// Returns ErrAudioSourceNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.AudioSource, error)

	// GetByIDs retrieves multiple audio sources by their IDs in a single query.
	// Returns a map of ID to AudioSource for efficient lookup.
	// Handles large ID sets by chunking to avoid SQL parameter limits.
	GetByIDs(ctx context.Context, ids []uint) (map[uint]*entities.AudioSource, error)

	// GetBySourceURI retrieves an audio source by its URI and node name.
	// Returns ErrAudioSourceNotFound if not found.
	GetBySourceURI(ctx context.Context, sourceURI, nodeName string) (*entities.AudioSource, error)

	// GetAll retrieves all audio sources.
	GetAll(ctx context.Context) ([]*entities.AudioSource, error)

	// GetAllWithDetectionCount returns every audio source that has at least one detection,
	// joined with its detection count and ordered by detection count descending.
	// Sources with zero detections are excluded so the analytics picker stays focused on
	// sources with usable data. The query uses the prefix-aware table names so it works
	// in both standard and v2_prefixed (MySQL migration) schemas.
	GetAllWithDetectionCount(ctx context.Context) ([]AudioSourceWithDetectionCount, error)

	// GetByNodeName retrieves all audio sources for a specific node.
	GetByNodeName(ctx context.Context, nodeName string) ([]*entities.AudioSource, error)

	// Count returns the total number of audio sources.
	Count(ctx context.Context) (int64, error)

	// Delete removes an audio source by ID.
	// Returns ErrAudioSourceNotFound if not found.
	// Note: Detections referencing this source will have their source_id set to NULL.
	Delete(ctx context.Context, id uint) error

	// Update modifies an audio source's display name or config.
	// Returns ErrAudioSourceNotFound if not found.
	Update(ctx context.Context, id uint, updates map[string]any) error

	// Exists checks if an audio source with the given ID exists.
	Exists(ctx context.Context, id uint) (bool, error)
}

package detection

import (
	"context"
)

// Repository defines operations for detection persistence and retrieval.
// This abstraction allows:
//   - Mocking for unit tests (test business logic without database)
//   - Multiple implementations (SQLite, PostgreSQL, in-memory, etc.)
//   - Flexible caching strategies
//   - Database schema changes without affecting business logic
type Repository interface {
	// Core CRUD operations
	Save(ctx context.Context, detection *Detection, predictions []Prediction) error
	Get(ctx context.Context, id string) (*Detection, error)
	Delete(ctx context.Context, id string) error

	// Batch operations
	SaveBatch(ctx context.Context, detections []*Detection) error
	GetByIDs(ctx context.Context, ids []string) ([]*Detection, error)

	// Query operations
	Search(ctx context.Context, filters SearchFilters) ([]*Detection, int, error)
	GetLastDetections(ctx context.Context, limit int) ([]*Detection, error)
	GetTopBirds(ctx context.Context, date string, minConfidence float64) ([]*Detection, error)

	// Species-specific queries
	GetSpeciesDetections(ctx context.Context, species, date, hour string, params QueryParams) ([]*Detection, error)
	CountSpeciesDetections(ctx context.Context, species, date, hour string, duration int) (int64, error)

	// Review operations
	SaveReview(ctx context.Context, detectionID string, verified string) error
	LockDetection(ctx context.Context, detectionID string) error
	UnlockDetection(ctx context.Context, detectionID string) error

	// Clip path operations
	GetClipPath(ctx context.Context, detectionID string) (string, error)
	DeleteClipPath(ctx context.Context, detectionID string) error
}

// SpeciesRepository manages species lookup and persistence.
// Species are cached in memory for performance, so this interface
// focuses on cache-through operations.
type SpeciesRepository interface {
	// Lookup operations (cache-first)
	GetByID(ctx context.Context, id uint) (*Species, error)
	GetByScientificName(ctx context.Context, name string) (*Species, error)
	GetByEbirdCode(ctx context.Context, code string) (*Species, error)

	// Mutation operations (invalidate cache)
	GetOrCreate(ctx context.Context, species *Species) (*Species, error)
	List(ctx context.Context, limit, offset int) ([]*Species, error)

	// Cache management
	InvalidateCache() error
}

// SearchFilters defines parameters for filtering detections.
type SearchFilters struct {
	Species        string  // Scientific or common name (partial match)
	DateStart      string  // Start date (YYYY-MM-DD)
	DateEnd        string  // End date (YYYY-MM-DD)
	ConfidenceMin  float64 // Minimum confidence (0.0-1.0)
	ConfidenceMax  float64 // Maximum confidence (0.0-1.0)
	VerifiedOnly   bool    // Only show verified detections
	UnverifiedOnly bool    // Only show unverified detections
	LockedOnly     bool    // Only show locked detections
	UnlockedOnly   bool    // Only show unlocked detections
	Device         string  // Source node name (partial match)
	TimeOfDay      string  // "any", "day", "night", "sunrise", "sunset"
	Page           int     // Page number (1-indexed)
	PerPage        int     // Results per page
	SortBy         string  // Sort order: "date_asc", "species_asc", "confidence_desc"
}

// QueryParams defines common query parameters.
type QueryParams struct {
	SortAscending bool // Sort order
	Limit         int  // Maximum results
	Offset        int  // Results to skip
}

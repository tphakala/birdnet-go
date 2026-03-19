package repository

import "context"

// AppMetadataRepository provides access to the app_metadata key-value table.
type AppMetadataRepository interface {
	// Get retrieves the value for the given key.
	// Returns an empty string and nil error if the key does not exist.
	Get(ctx context.Context, key string) (string, error)

	// Set creates or updates the value for the given key (upsert).
	Set(ctx context.Context, key, value string) error
}

package datastore_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

func TestCountAll_DirectCount(t *testing.T) {
	t.Parallel()

	mockStore := mocks.NewMockInterface(t)

	// Expect CountDetectionsSince called with zero time (matches all records)
	mockStore.EXPECT().
		CountDetectionsSince(mock.Anything, time.Time{}).
		Return(42, nil).
		Once()

	// Ensure GetDatabaseStats is NOT called
	mockStore.EXPECT().
		GetDatabaseStats(mock.Anything).
		Return(nil, fmt.Errorf("should not be called")).
		Maybe()

	repo := datastore.NewDetectionRepository(mockStore, time.UTC)
	count, err := repo.CountAll(t.Context())

	require.NoError(t, err)
	assert.Equal(t, int64(42), count)

	mockStore.AssertNotCalled(t, "GetDatabaseStats", mock.Anything)
}

func TestCountAll_Error(t *testing.T) {
	t.Parallel()

	mockStore := mocks.NewMockInterface(t)

	expectedErr := fmt.Errorf("database connection lost")
	mockStore.EXPECT().
		CountDetectionsSince(mock.Anything, time.Time{}).
		Return(0, expectedErr).
		Once()

	repo := datastore.NewDetectionRepository(mockStore, time.UTC)
	count, err := repo.CountAll(t.Context())

	require.Error(t, err)
	assert.Equal(t, int64(0), count)
	assert.Contains(t, err.Error(), "failed to count detections")
	assert.ErrorIs(t, err, expectedErr)
}

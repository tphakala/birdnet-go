package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestNote_ClipSampleRateNotAColumn verifies the legacy notes table gets no
// clip_sample_rate column: Note.ClipSampleRate is runtime-only.
func TestNote_ClipSampleRateNotAColumn(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Note{}))
	assert.False(t, db.Migrator().HasColumn(&Note{}, "clip_sample_rate"))
}

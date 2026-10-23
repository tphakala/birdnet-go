package datastore

import (
	"encoding/json"
	"strings"
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

// TestNote_ClipSampleRateNotInJSON verifies the frozen v1 Note output does not
// carry the runtime-only clip sample rate.
func TestNote_ClipSampleRateNotInJSON(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(Note{ClipName: "a.flac", ClipSampleRate: 384000})
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(body)), "samplerate")
}

package birdnetpi

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBirdsDB creates a BirdNET-Pi style database with the detections schema and
// runs the given INSERT statement (pass "" for an empty table), returning its path.
func newBirdsDB(t *testing.T, insert string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "birds.db")
	db, err := sql.Open("sqlite3", p)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE detections (
		Date TEXT, Time TEXT, Sci_Name TEXT, Com_Name TEXT, Confidence REAL,
		Lat REAL, Lon REAL, Cutoff REAL, Sens REAL, File_Name TEXT)`)
	require.NoError(t, err)
	if insert != "" {
		_, err = db.Exec(insert)
		require.NoError(t, err)
	}
	return p
}

func TestSource_LatestDate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		insert string
		want   string
	}{
		{
			name: "populated table returns the maximum date",
			insert: `INSERT INTO detections VALUES
				('2026-05-01','06:00:00','Turdus merula','Blackbird',0.9,0,0,0,1.0,'a.mp3'),
				('2026-06-20','07:00:00','Parus major','Great Tit',0.8,0,0,0,1.0,'b.mp3')`,
			want: "2026-06-20",
		},
		{
			name:   "empty table returns an empty string",
			insert: "",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(newBirdsDB(t, tt.insert))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, s.Close()) })

			got, err := s.LatestDate(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

package migration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestNoteBoundaryCursor(t *testing.T) {
	t.Parallel()

	// Notes 1 and 2 have two results each, note 3 has three.
	rows := []datastore.Results{
		{ID: 10, NoteID: 1}, {ID: 11, NoteID: 1},
		{ID: 20, NoteID: 2}, {ID: 21, NoteID: 2},
		{ID: 30, NoteID: 3}, {ID: 31, NoteID: 3}, {ID: 32, NoteID: 3},
	}

	tests := []struct {
		name       string
		rows       []datastore.Results
		complete   bool
		wantNote   uint
		wantResult uint
		wantOK     bool
	}{
		{name: "full batch ending mid note stops at the previous note", rows: rows[:6], complete: false, wantNote: 2, wantResult: 21, wantOK: true},
		{name: "full batch ending on a note end still waits for the next row", rows: rows[:4], complete: false, wantNote: 1, wantResult: 11, wantOK: true},
		{name: "batch holding part of one note has no boundary", rows: rows[4:7], complete: false, wantOK: false},
		{name: "short batch takes the last row", rows: rows, complete: true, wantNote: 3, wantResult: 32, wantOK: true},
		{name: "pass blocked after a complete note takes that note's last row", rows: rows[:4], complete: true, wantNote: 2, wantResult: 21, wantOK: true},
		{name: "empty batch", rows: nil, complete: true, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			noteID, resultID, ok := noteBoundaryCursor(tt.rows, tt.complete)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantNote, noteID)
			assert.Equal(t, tt.wantResult, resultID)
		})
	}
}

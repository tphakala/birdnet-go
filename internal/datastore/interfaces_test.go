package datastore

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// createDatabase initializes a temporary database for testing purposes.
// It ensures the database connection is opened and handles potential errors.
func createDatabase(t *testing.T, settings *conf.Settings) Interface {
	tempDir := t.TempDir()
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = tempDir + "/test.db"

	dataStore := New(settings)

	// Attempt to open a database connection.
	if err := dataStore.Open(); err != nil {
		t.Fatalf("Failed to open database: %v", err)  // Use t.Fatalf to immediately fail the test on error.
	}

	// Ensure the database is closed after the test completes.
	t.Cleanup(func() { dataStore.Close() })

	return dataStore
}

// TestGetClipsQualifyingForRemoval verifies the behavior of GetClipsQualifyingForRemoval.
// It sets up a scenario with various bird notes and checks if the correct notes qualify for removal.
func TestGetClipsQualifyingForRemoval(t *testing.T) {
	settings := &conf.Settings{}

	// Create a datastore with a temporary database.
	dataStore := createDatabase(t, settings)

	// Set up test data for various bird notes.

	// One Cool bird should be removed since there is one too many
	// No Amazing bird should be removed since there is only one
	// While there are two Wonderful birds, only one of them are old enough, but too few to be removed
	// While there are two Magnificent birds, only one of them have a clip, meaning that the remaining one should be kept

	testNotes := []Note{
		{ClipName: "test.wav", ScientificName: "Cool bird", BeginTime: time.Now().Add(-2 * time.Hour)},
		{ClipName: "test2.wav", ScientificName: "Amazing bird", BeginTime: time.Now().Add(-2 * time.Hour)},
		{ClipName: "test3.wav", ScientificName: "Cool bird", BeginTime: time.Now().Add(-2 * time.Hour)},
		{ClipName: "test4.wav", ScientificName: "Wonderful bird", BeginTime: time.Now().Add(-2 * time.Hour)},
		{ClipName: "test5.wav", ScientificName: "Magnificent bird", BeginTime: time.Now().Add(-2 * time.Hour)},
		{ClipName: "", ScientificName: "Magnificent bird", BeginTime: time.Now()},
		{ClipName: "test7.wav", ScientificName: "Wonderful bird", BeginTime: time.Now()},
	}

	// Save each note to the datastore and check for errors during save operations.
	for _, note := range testNotes {
		err := dataStore.Save(&note, []Results{})
		if err != nil {
			t.Fatalf("Failed to save note: %v", err)
		}
	}

	// Define removal criteria.
	minHours := 1
	minClips := 1

	// Fetch clips qualifying for removal.
	clipsForRemoval, err := dataStore.GetClipsQualifyingForRemoval(minHours, minClips)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(clipsForRemoval) != 1 {
		t.Errorf("Expected one entry in clipsForRemoval, got %d", len(clipsForRemoval))
	}
	if len(clipsForRemoval) > 0 && clipsForRemoval[0].ScientificName != "Cool bird" {
		t.Errorf("Expected ScientificName to be 'Cool bird', got '%s'", clipsForRemoval[0].ScientificName)
	}
}

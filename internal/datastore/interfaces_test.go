package datastore

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

func createDatabase(t testing.TB, settings *conf.Settings) Interface {
	tempDir := t.TempDir()
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = tempDir + "/test.db"

	dataStore := New(settings)

	// Open a connection to the database and handle possible errors.
	if err := dataStore.Open(); err != nil {
		logger.Error("main", "Failed to open database: %v", err)
	} else {
		t.Cleanup(func() { dataStore.Close() })
	}

	return dataStore
}

func TestGetClipsQualifyingForRemoval(t *testing.T) {

	settings := &conf.Settings{}

	dataStore := createDatabase(t, settings)

	// One Cool bird should be removed since there is one too many
	// No Amazing bird should be removed since there is only one
	// While there are two Wonderful birds, only one of them are old enough, but too few to be removed
	// While there are two Magnificent birds, only one of them have a clip, meaning that the remaining one should be kept
	dataStore.Save(&Note{
		ClipName:       "test.wav",
		ScientificName: "Cool bird",
		BeginTime:      time.Now().Add(-2 * time.Hour),
	}, []Results{})
	dataStore.Save(&Note{
		ClipName:       "test2.wav",
		ScientificName: "Amazing bird",
		BeginTime:      time.Now().Add(-2 * time.Hour),
	}, []Results{})
	dataStore.Save(&Note{
		ClipName:       "test3.wav",
		ScientificName: "Cool bird",
		BeginTime:      time.Now().Add(-2 * time.Hour),
	}, []Results{})
	dataStore.Save(&Note{
		ClipName:       "test4.wav",
		ScientificName: "Wonderful bird",
		BeginTime:      time.Now().Add(-2 * time.Hour),
	}, []Results{})
	dataStore.Save(&Note{
		ClipName:       "test5.wav",
		ScientificName: "Magnificent bird",
		BeginTime:      time.Now().Add(-2 * time.Hour),
	}, []Results{})
	dataStore.Save(&Note{
		ClipName:       "",
		ScientificName: "Magnificent bird",
		BeginTime:      time.Now(),
	}, []Results{})
	dataStore.Save(&Note{
		ClipName:       "test7.wav",
		ScientificName: "Wonderful bird",
		BeginTime:      time.Now(),
	}, []Results{})

	minHours := 1
	minClips := 1

	clipsForRemoval, err := dataStore.GetClipsQualifyingForRemoval(minHours, minClips)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(clipsForRemoval) != 1 {
		t.Errorf("Expected one entry in clipsForRemoval, got %d", len(clipsForRemoval))
	}
	if clipsForRemoval[0].ScientificName != "Cool bird" {
		t.Errorf("Expected ScientificName to be 'Cool bird', got '%s'", clipsForRemoval[0].ScientificName)
	}
}

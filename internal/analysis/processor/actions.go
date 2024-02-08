// processor/actions.go

package processor

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observation"
)

type Action interface {
	Execute(data interface{}) error
}

type LogAction struct {
	Settings     *conf.Settings
	Note         datastore.Note
	EventTracker *EventTracker
}

type DatabaseAction struct {
	Settings     *conf.Settings
	Note         datastore.Note
	Ds           datastore.Interface
	EventTracker *EventTracker
}

type SaveAudioAction struct {
	Settings     *conf.Settings
	ClipName     string
	pcmData      []byte
	EventTracker *EventTracker
}

type BirdWeatherAction struct {
	Settings     *conf.Settings
	Note         datastore.Note
	pcmData      []byte
	BwClient     *birdweather.BwClient
	EventTracker *EventTracker
}

type UpdateRangeFilterAction struct {
	Bn                 *birdnet.BirdNET
	IncludedSpecies    *[]string
	SpeciesListUpdated *time.Time
}

func (a LogAction) Execute(data interface{}) error {
	species := strings.ToLower(a.Note.CommonName)

	// Check if the event should be handled for this species
	if a.EventTracker.TrackEvent(species, LogToFile) {
		if err := observation.LogNoteToFile(a.Settings, a.Note); err != nil {
			// If an error occurs when logging to a file, wrap and return the error.
			log.Printf("Failed to log note to file: %v", err)
		}
		fmt.Printf("%s %s %.2f\n", a.Note.Time, a.Note.CommonName, a.Note.Confidence)
		return nil
	}

	log.Printf("Log action throttled for species: %s", species)
	return nil
}

func (a DatabaseAction) Execute(data interface{}) error {
	species := strings.ToLower(a.Note.CommonName)

	// Check if the event should be handled for this species
	if a.EventTracker.TrackEvent(species, DatabaseSave) {
		if err := a.Ds.Save(a.Note); err != nil {
			log.Printf("Failed to save note to database: %v", err)
			return err
		}
		return nil
	}

	log.Printf("Database save action throttled for species: %s", species)
	return nil
}

func (a SaveAudioAction) Execute(data interface{}) error {
	if err := myaudio.SavePCMDataToWAV(a.ClipName, a.pcmData); err != nil {
		log.Printf("error saving audio clip to %s: %s\n", a.Settings.Realtime.AudioExport.Type, err)
		return err
	} else if a.Settings.Debug {
		log.Printf("Saved audio clip to %s\n", a.ClipName)
	}
	return nil // return an error if the action fails
}

func (a BirdWeatherAction) Execute(data interface{}) error {
	species := strings.ToLower(a.Note.CommonName)

	if a.EventTracker.TrackEvent(species, BirdWeatherSubmit) {
		if err := a.BwClient.Publish(a.Note, a.pcmData); err != nil {
			log.Printf("error uploading to BirdWeather: %s\n", err)
			return err
		} else if a.Settings.Debug {
			log.Printf("Uploaded %s to Birdweather\n", a.Note.ClipName)
		}
		return nil
	}
	log.Printf("BirdWeather Submit action throttled for species: %s", species)
	return nil // return an error if the action fails
}

func (a UpdateRangeFilterAction) Execute(data interface{}) error {
	today := time.Now().Truncate(24 * time.Hour)
	if today.After(*a.SpeciesListUpdated) {
		// Update location based species list
		*a.IncludedSpecies = a.Bn.GetProbableSpecies()
		*a.SpeciesListUpdated = today // Update the timestamp
	}
	return nil
}

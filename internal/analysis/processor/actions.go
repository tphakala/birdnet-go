// processor/actions.go

package processor

import (
	"fmt"
	"log"
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
	Settings *conf.Settings
	Note     datastore.Note
}

type DatabaseAction struct {
	Settings *conf.Settings
	Note     datastore.Note
	Ds       datastore.Interface
}

type SaveAudioAction struct {
	Settings *conf.Settings
	ClipName string
	pcmData  []byte
}

type BirdweatherAction struct {
	Settings *conf.Settings
	Note     datastore.Note
	pcmData  []byte
	BwClient *birdweather.BwClient
}

type UpdateRangeFilterAction struct {
	Bn                 *birdnet.BirdNET
	IncludedSpecies    *[]string
	SpeciesListUpdated time.Time
}

func (a LogAction) Execute(data interface{}) error {
	if err := observation.LogNoteToFile(a.Settings, a.Note); err != nil {
		// If an error occurs when logging to a file, wrap and return the error.
		log.Printf("Failed to log note to file: %v", err)
	}

	fmt.Printf("%s %s %.2f\n", a.Note.Time, a.Note.CommonName, a.Note.Confidence)
	return nil
}

func (a DatabaseAction) Execute(data interface{}) error {
	if err := a.Ds.Save(a.Note); err != nil {
		// If an error occurs when saving to database, wrap and return the error.
		log.Printf("Failed to save note to database: %v", err)
		return err
	}
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

func (a BirdweatherAction) Execute(data interface{}) error {
	if err := a.BwClient.Publish(a.Note, a.pcmData); err != nil {
		log.Printf("error uploading to BirdWeather: %s\n", err)
		return err
	} else if a.Settings.Debug {
		log.Printf("Uploaded %s to Birdweather\n", a.Note.ClipName)
	}
	return nil // return an error if the action fails
}

func (a UpdateRangeFilterAction) Execute(data interface{}) error {
	today := time.Now().Truncate(24 * time.Hour)
	if today.After(a.SpeciesListUpdated) {
		// update location based species list once a day
		*a.IncludedSpecies = a.Bn.GetProbableSpecies()
		a.SpeciesListUpdated = today
	}

	return nil // return an error if the action fails
}

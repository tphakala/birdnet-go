// processor/actions.go

package processor

import (
	"fmt"
	"log"

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
	Ctx  *conf.Context
	Note datastore.Note
}

type DatabaseAction struct {
	Ctx  *conf.Context
	Note datastore.Note
	Ds   datastore.Interface
}

type SaveAudioAction struct {
	Ctx      *conf.Context
	ClipName string
	pcmData  []byte
}

type BirdweatherAction struct {
	Ctx               *conf.Context
	Note              datastore.Note
	pcmData           []byte
	BirdweatherClient birdweather.Interface
}

func (a LogAction) Execute(data interface{}) error {
	if err := observation.LogNoteToFile(a.Ctx, a.Note); err != nil {
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
		log.Printf("error saving audio clip to %s: %s\n", a.Ctx.Settings.Realtime.AudioExport.Type, err)
		return err
	} else if a.Ctx.Settings.Debug {
		log.Printf("Saved audio clip to %s\n", a.ClipName)
	}
	return nil // return an error if the action fails
}

func (a BirdweatherAction) Execute(data interface{}) error {
	if err := a.BirdweatherClient.Publish(a.Note, a.pcmData); err != nil {
		log.Printf("error uploading to Birdweather: %s\n", err)
		return err
	} else if a.Ctx.Settings.Debug {
		log.Printf("Uploaded %s to Birdweather\n", a.Note.ClipName)
	}
	return nil // return an error if the action fails
}

// Add more actions as needed.

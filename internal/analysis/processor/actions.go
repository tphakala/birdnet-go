// processor/actions.go

package processor

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ricochet2200/go-disk-usage/du"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/mqtt"
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
	Ds           datastore.Interface
	Note         datastore.Note
	Results      []datastore.Results
	EventTracker *EventTracker
	AudioBuffer  *myaudio.AudioBuffer
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

type MqttAction struct {
	Settings     *conf.Settings
	Note         datastore.Note
	MqttClient   *mqtt.Client
	EventTracker *EventTracker
}

type UpdateRangeFilterAction struct {
	Bn                 *birdnet.BirdNET
	IncludedSpecies    *[]string
	SpeciesListUpdated *time.Time
}

// Execute logs the note to the chag log file
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

	//log.Printf("Log action throttled for species: %s", species)
	return nil
}

type ByModTime []os.FileInfo

func (a ByModTime) Len() int           { return len(a) }
func (a ByModTime) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByModTime) Less(i, j int) bool { return a[i].ModTime().Before(a[j].ModTime()) }

type DiskUsageCalculator interface {
	Used(basePath string) (uint64, error) // Returns the amount of disk space used for the given basePath.
	Size(basePath string) (uint64, error) // Returns the total size of the disk for the given basePath.
}

// RealDiskUsage implements DiskUsageCalculator using the actual disk usage.
type RealDiskUsage struct{}

func (r RealDiskUsage) Used(basePath string) (uint64, error) {
	return du.NewDiskUsage(basePath).Used(), nil
}

func (r RealDiskUsage) Size(basePath string) (uint64, error) {
	return du.NewDiskUsage(basePath).Size(), nil
}

// TODO: Other idea.
// Have to variables:
// MinEvictionHours - Minimum time before recording is considered for detection
// MinRecordingsPerSpecies - Number of recordings to always keep per species

func (a DatabaseAction) DiskCleanUp(calculator DiskUsageCalculator) error {
	basePath := conf.GetBasePath(a.Settings.Realtime.AudioExport.Path)

	const cleanupThreshold = 0.9 // Example threshold for disk cleanup (90% usage)

	// Calculate bytesToRemove based on available disk space and cleanup threshold
	diskUsed, err := calculator.Used(basePath)
	if err != nil {
		return err
	}

	diskSize, err := calculator.Size(basePath)
	if err != nil {
		return err
	}
	bytesToRemove := int64(diskUsed) - int64(float64(diskSize)*cleanupThreshold)

	// Only cleanup if required (positive bytes to remove)
	if bytesToRemove < 0 {
		return nil
	}

	log.Printf("Reached disk usage treshold of %f percent. Will try to remove: %d bytes of files.\n", cleanupThreshold*100, bytesToRemove)

	var files []os.FileInfo
	var totalSize int64

	errr := filepath.Walk(basePath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			// Check if the file is a regular file
			if info.Mode().IsRegular() {
				files = append(files, info)
				totalSize += info.Size() // Accumulate file sizes
			}
			return nil
		})
	if errr != nil {
		log.Println(errr)
		return err
	}

	// Sort files by modification time
	sort.Sort(ByModTime(files))

	// Check if it's possible to remove X bytes
	if totalSize < bytesToRemove {
		return fmt.Errorf("failed to remove %d bytes: insufficient files to meet the target size", bytesToRemove)
	}

	// Remove files until total size exceeds or equals bytesToRemove
	var removedSize int64
	for i := 0; i < len(files) && removedSize < bytesToRemove; i++ {
		err := os.Remove(filepath.Join(basePath, files[i].Name()))
		if err != nil {
			log.Printf("Error removing file %s: %v\n", files[i].Name(), err)
		} else {
			removedSize += files[i].Size() // Update removedSize after removal
			log.Printf("File %s (%d bytes) removed successfully. %d bytes remaning to remove.\n", files[i].Name(), files[i].Size(), max(bytesToRemove-removedSize, 0))
		}
	}

	// Perform a check afterward again. It could be the case that because of removal errors,
	// it is still not possible to remove enough files to reach the threshold
	if removedSize < bytesToRemove {
		return fmt.Errorf("only managed to remove %d bytes: insufficient files to meet the target size %d", removedSize, bytesToRemove)
	}

	return nil
}

// Execute saves the note to the database
func (a DatabaseAction) Execute(data interface{}) error {
	species := strings.ToLower(a.Note.CommonName)

	// Check if the event should be handled for this species
	if a.EventTracker.TrackEvent(species, DatabaseSave) {
		// Save note to database
		/*
			if err := a.Ds.Save(a.Note); err != nil {
				log.Printf("Failed to save note to database: %v", err)
				return err
			}*/
		if err := a.Ds.Save(&a.Note, a.Results); err != nil {
			log.Printf("Failed to save note and results to database: %v", err)
			return err
		}

		// Save audio clip to file if enabled
		if a.Settings.Realtime.AudioExport.Enabled {
			time.Sleep(1 * time.Second) // Sleep for 1 second to allow the audio buffer to fill
			pcmData, _ := a.AudioBuffer.ReadSegment(a.Note.BeginTime, time.Now())

			a.DiskCleanUp(RealDiskUsage{})

			if err := myaudio.SavePCMDataToWAV(a.Note.ClipName, pcmData); err != nil {
				log.Printf("error saving audio clip to %s: %s\n", a.Settings.Realtime.AudioExport.Type, err)
				return err
			} else if a.Settings.Debug {
				log.Printf("Saved audio clip to %s\n", a.Note.ClipName)
				log.Printf("detection time %v, begin time %v, end time %v\n", a.Note.Time, a.Note.BeginTime, time.Now())
			}
		}

		return nil
	}

	//log.Printf("Database save action throttled for species: %s", species)
	return nil
}

// Execute saves the audio clip to a file
func (a SaveAudioAction) Execute(data interface{}) error {
	if err := myaudio.SavePCMDataToWAV(a.ClipName, a.pcmData); err != nil {
		log.Printf("error saving audio clip to %s: %s\n", a.Settings.Realtime.AudioExport.Type, err)
		return err
	} else if a.Settings.Debug {
		log.Printf("Saved audio clip to %s\n", a.ClipName)
	}
	return nil // return an error if the action fails
}

// Execute sends the note to the BirdWeather API
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
	//log.Printf("BirdWeather Submit action throttled for species: %s", species)
	return nil // return an error if the action fails
}

// Execute sends the note to the MQTT broker
func (a MqttAction) Execute(data interface{}) error {
	// Validate MQTT settings
	if a.Settings.Realtime.MQTT.Topic == "" {
		return errors.New("MQTT topic is not specified")
	}

	// Create a JSON representation of the note
	noteJson, err := json.Marshal(a.Note)
	if err != nil {
		log.Printf("error marshalling note to JSON: %s\n", err)
		return err
	}

	// Publish the note to the MQTT broker
	err = a.MqttClient.Publish(a.Settings.Realtime.MQTT.Topic, string(noteJson))
	if err != nil {
		return err
	}

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

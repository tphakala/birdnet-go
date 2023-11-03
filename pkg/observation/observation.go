package observation

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tphakala/go-birdnet/pkg/config"
)

// Observation represents a single observation data point
type Note struct {
	ID             uint `gorm:"primaryKey"`
	Date           string
	Time           string
	InputFile      string
	BeginTime      float64
	EndTime        float64
	SpeciesCode    string
	ScientificName string
	CommonName     string
	Confidence     float64
	Latitude       float64
	Longitude      float64
	Threshold      float64
	Sensitivity    float64
	ClipName       string
	ProcessingTime time.Duration
}

// ParseSpeciesString extracts the scientific name, common name, and species code from the species string.
func ParseSpeciesString(species string) (string, string, string) {
	parts := strings.SplitN(species, "_", 3) // Split into 3 parts at most: scientificName, commonName, speciesCode
	if len(parts) == 3 {
		// Return scientificName (parts[0]), commonName (parts[1]), and speciesCode (parts[2])
		return parts[0], parts[1], parts[2]
	}
	// Log this to see what is being returned
	fmt.Printf("Species string has an unexpected format: %s\n", species)
	// Return the original species string for all parts if the format doesn't match the expected
	return species, species, ""
}

// New creates a new Observation.
func New(cfg *config.Settings, beginTime, endTime float64, species string, confidence float64, latitude, longitude float64, clipName string, elapsedTime time.Duration) Note {
	scientificName, commonName, speciesCode := ParseSpeciesString(species)

	return Note{
		Date:           time.Now().Format("2006-01-02"), // Using the current date
		Time:           time.Now().Format("15:04:05"),   // Using the current time (24-hour format
		InputFile:      cfg.InputFile,
		BeginTime:      beginTime,
		EndTime:        endTime,
		SpeciesCode:    speciesCode,
		ScientificName: scientificName,
		CommonName:     commonName,
		Confidence:     confidence,
		Latitude:       latitude,
		Longitude:      longitude,
		Threshold:      cfg.Threshold,
		Sensitivity:    cfg.Sensitivity,
		ClipName:       clipName,
		ProcessingTime: elapsedTime,
	}
}

// LogNote is the central function for logging observations.
func LogNote(cfg *config.Settings, note Note) error {
	if cfg.LogFile != "" {
		// Save to Log File
		if err := LogNoteToFile(cfg, note); err != nil {
			return fmt.Errorf("failed to log note to file: %v", err)
		}
	}

	if cfg.Database != "none" {
		// Save to Database
		if err := SaveToDatabase(note); err != nil {
			return fmt.Errorf("failed to save note to database: %v", err)
		}
	}

	return nil
}

// WriteNotes writes the slice of notes to the specified destination.
// The output can be directed to either stdout or a file specified by the filename.
// If filename is an empty string, it will write to stdout.
func WriteNotesTable(notes []Note, filename string) error {
	var w io.Writer
	if filename == "" {
		w = os.Stdout
	} else {
		file, err := os.Create(filename)
		if err != nil {
			return err
		}
		defer file.Close()
		w = file
	}

	header := "Selection\tView\tChannel\tBegin File\tBegin Time (s)\tEnd Time (s)\tLow Freq (Hz)\tHigh Freq (Hz)\tSpecies Code\tCommon Name\tConfidence\n"
	if _, err := w.Write([]byte(header)); err != nil {
		return err
	}

	for i, note := range notes {
		line := fmt.Sprintf("%d\tSpectrogram 1\t1\t%s\t%.1f\t%.1f\t0\t15000\t%s\t%s\t%.4f\n",
			i+1, note.InputFile, note.BeginTime, note.EndTime, note.SpeciesCode, note.CommonName, note.Confidence)
		if _, err := w.Write([]byte(line)); err != nil {
			return err
		}
	}

	return nil
}

// WriteNotesCsv writes the slice of notes to the specified destination in CSV format.
// If filename is an empty string, it will write to stdout.
func WriteNotesCsv(notes []Note, filename string) error {
	var w io.Writer
	if filename == "" {
		w = os.Stdout
	} else {
		file, err := os.Create(filename)
		if err != nil {
			return err
		}
		defer file.Close()
		w = file
	}

	header := "Start (s),End (s),Scientific name,Common name,Confidence\n"
	if _, err := w.Write([]byte(header)); err != nil {
		return err
	}

	for _, note := range notes {
		line := fmt.Sprintf("%f,%f,%s,%s,%.4f\n",
			note.BeginTime, note.EndTime, note.ScientificName, note.CommonName, note.Confidence)
		if _, err := w.Write([]byte(line)); err != nil {
			return err
		}
	}

	return nil
}

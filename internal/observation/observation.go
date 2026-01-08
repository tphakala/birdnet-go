package observation

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// ParseSpeciesString extracts the scientific name, common name, and species code from the species string.
// For custom models with species not in the eBird taxonomy, the species code might be a placeholder.
func ParseSpeciesString(species string) (scientificName, commonName, speciesCode string) {
	// Check if the string is empty or contains special characters that would break parsing
	if species == "" || strings.Contains(species, "\t") || strings.Contains(species, "\n") {
		return species, species, ""
	}

	// Split the species string by "_" separator
	parts := strings.SplitN(species, "_", 3) // Split into 3 parts at most: scientificName, commonName, speciesCode

	// Format 1: "ScientificName_CommonName_SpeciesCode" (3 parts)
	if len(parts) == 3 {
		return parts[0], parts[1], parts[2]
	}

	// Format 2: "ScientificName_CommonName" (2 parts) - most common format
	if len(parts) == 2 {
		return parts[0], parts[1], ""
	}

	// If we got here, the format doesn't match expected patterns
	// Check if it has spaces instead, like "Common Blackbird" with no scientific name
	if len(parts) == 1 && strings.Contains(species, " ") {
		// This is likely just a common name without scientific name
		return "", species, ""
	}

	// Log this to see what is being returned if format is unexpected
	fmt.Printf("Species string has an unexpected format: %s\n", species)

	// Default fallback - return the original string for all parts
	return species, species, ""
}

// NoteParams holds the parameters for creating a new Note.
type NoteParams struct {
	Begin      time.Time
	End        time.Time
	Species    string
	Confidence float64
	Source     string
	ClipName   string
	Elapsed    time.Duration
	Occurrence float64
}

// NewWith creates and returns a new Note with the provided NoteParams and current date and time.
// It uses the configuration and parsing functions to set the appropriate fields.
// For custom models, species may have placeholder taxonomy codes if not in the eBird taxonomy.
func NewWith(settings *conf.Settings, p *NoteParams) datastore.Note {
	return New(settings, p.Begin, p.End, p.Species, p.Confidence, p.Source, p.ClipName, p.Elapsed, p.Occurrence)
}

// New creates and returns a new Note with the provided parameters and current date and time.
// It uses the configuration and parsing functions to set the appropriate fields.
// For custom models, species may have placeholder taxonomy codes if not in the eBird taxonomy.
func New(settings *conf.Settings, beginTime, endTime time.Time, species string, confidence float64, source, clipName string, elapsedTime time.Duration, occurrence float64) datastore.Note {
	// Parse the species string to get the scientific name, common name, and species code.
	scientificName, commonName, speciesCode := ParseSpeciesString(species)

	// detectionTime is time now minus 3 seconds to account for the delay in the detection
	now := time.Now()
	date := now.Format("2006-01-02")
	detectionTime := now.Add(-2 * time.Second)
	timeStr := detectionTime.Format("15:04:05")

	// Create AudioSource struct with proper fields
	var audioSourceStruct datastore.AudioSource
	if settings.Input.Path != "" {
		audioSourceStruct = datastore.AudioSource{
			ID:          source,                             // Use source as ID
			SafeString:  settings.Input.Path,                // File path is safe to display
			DisplayName: filepath.Base(settings.Input.Path), // Just the filename for display
		}
	} else {
		// For other sources, use basic structure
		audioSourceStruct = datastore.AudioSource{
			ID:          source,
			SafeString:  source, // For analysis mode, source is typically safe
			DisplayName: source, // Use as-is for display
		}
	}

	// Round confidence to two decimal places
	roundedConfidence := math.Round(confidence*100) / 100

	// Return a new Note struct populated with the provided parameters as well as the current date and time.
	return datastore.Note{
		SourceNode:     settings.Main.Name,                       // From the provided configuration settings.
		Date:           date,                                     // Use ISO 8601 date format.
		Time:           timeStr,                                  // Use 24-hour time format.
		Source:         audioSourceStruct,                        // Proper AudioSource struct
		BeginTime:      beginTime,                                // Start time of the observation.
		EndTime:        endTime,                                  // End time of the observation.
		SpeciesCode:    speciesCode,                              // Parsed species code or placeholder.
		ScientificName: scientificName,                           // Parsed scientific name of the species.
		CommonName:     commonName,                               // Parsed common name of the species.
		Confidence:     roundedConfidence,                        // Confidence score of the observation.
		Latitude:       settings.BirdNET.Latitude,                // Geographic latitude where the observation was made.
		Longitude:      settings.BirdNET.Longitude,               // Geographic longitude where the observation was made.
		Threshold:      settings.BirdNET.Threshold,               // Threshold setting from configuration.
		Sensitivity:    settings.BirdNET.Sensitivity,             // Sensitivity setting from configuration.
		ClipName:       clipName,                                 // Name of the audio clip.
		ProcessingTime: elapsedTime,                              // Time taken to process the observation.
		Occurrence:     math.Max(0.0, math.Min(1.0, occurrence)), // Occurrence probability based on location/time, clamped to [0,1].
	}
}

// WriteNotesTable writes a slice of Note structs to a table-formatted text output.
// The output can be directed to either stdout or a file specified by the filename.
// If the filename is an empty string, it writes to stdout.
func WriteNotesTable(settings *conf.Settings, notes []datastore.Note, filename string) error {
	var w io.Writer
	// Determine the output destination based on the filename argument.
	if filename == "" {
		w = os.Stdout
	} else {
		// Ensure the filename has a .txt extension.
		if !strings.HasSuffix(filename, ".txt") {
			filename += ".txt"
		}
		// Create or truncate the file with the specified filename.
		file, err := os.Create(filename) //nolint:gosec // G304: filename is from settings.Output.File.Path
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				fmt.Printf("failed to close output file: %v\n", err)
			}
		}() // Ensure the file is closed when the function exits.
		w = file
	}

	// Add color functions
	yellow := color.New(color.FgYellow)

	// Write the header to the output destination.
	header := "Selection\tFile\tBegin Time (s)\tEnd Time (s)\tSpecies Code\tCommon Name\tConfidence\n"
	if _, err := w.Write([]byte(header)); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Pre-declare err outside the loop to avoid re-declaration
	var err error

	for i := range notes {
		if notes[i].Confidence <= settings.BirdNET.Threshold {
			continue // Skip the current iteration as the note doesn't meet the threshold
		}

		// Prepare the line for notes above the threshold, assuming note.BeginTime and note.EndTime are of type time.Time
		line := fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t%s\t%.4f\n",
			i+1, notes[i].Source.SafeString, notes[i].BeginTime.Format("15:04:05.000"), notes[i].EndTime.Format("15:04:05.000"),
			notes[i].SpeciesCode, notes[i].CommonName, notes[i].Confidence)

		// Attempt to write the note
		if _, err = w.Write([]byte(line)); err != nil {
			break // If an error occurs, exit the loop
		}
	}

	// Check if an error occurred during the loop and return it
	if err != nil {
		return fmt.Errorf("failed to write note: %w", err)
	} else if filename != "" {
		if _, err := yellow.Println("📁 Output written to", filename); err != nil {
			fmt.Printf("failed to print output message: %v\n", err)
		}
	}

	// Return nil if the writing operation completes successfully.
	return nil
}

// WriteNotesCsv writes the slice of notes to the specified destination in CSV format.
// If filename is an empty string, the function writes to stdout.
// The function returns an error if writing to the destination fails.
func WriteNotesCsv(settings *conf.Settings, notes []datastore.Note, filename string) error {
	// Define an io.Writer to abstract the writing operation.
	var w io.Writer

	// Determine the output destination, file or screen
	if settings.Output.File.Enabled {
		// Ensure the filename has a .csv extension.
		if !strings.HasSuffix(filename, ".csv") {
			filename += ".csv"
		}
		// Create or truncate the file with the given filename.
		file, err := os.Create(filename) //nolint:gosec // G304: filename is from settings.Output.File.Path
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filename, err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				fmt.Printf("failed to close CSV file: %v\n", err)
			}
		}()
		w = file
	} else {
		// Print output to stdout if the file output is disabled
		w = os.Stdout
	}

	// Define the CSV header.
	header := "Start (s),End (s),Scientific name,Common name,Confidence\n"
	// Write the header to the output destination.
	if _, err := w.Write([]byte(header)); err != nil {
		return fmt.Errorf("failed to write header to CSV: %w", err)
	}

	// Pre-declare err outside the loop to avoid re-declaration
	var err error

	for i := range notes {
		if notes[i].Confidence <= settings.BirdNET.Threshold {
			continue // Skip the current iteration as the note doesn't meet the threshold
		}

		line := fmt.Sprintf("%s,%s,%s,%s,%.4f\n",
			notes[i].BeginTime.Format("2006-01-02 15:04:05"),
			notes[i].EndTime.Format("2006-01-02 15:04:05"),
			notes[i].ScientificName, notes[i].CommonName, notes[i].Confidence)

		if _, err = w.Write([]byte(line)); err != nil {
			// Break out of the loop at the first sign of an error
			break
		}
	}

	// Handle any errors that occurred during the write operation
	if err != nil {
		return fmt.Errorf("failed to write note to CSV: %w", err)
	} else {
		fmt.Println("Output written to", filename)
	}

	// Return nil if the writing operation completes successfully.
	return nil
}

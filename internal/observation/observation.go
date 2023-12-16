package observation

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/config"
)

// Observation represents a single observation data point
type Note struct {
	Id             uint `gorm:"column:id;primaryKey;autoIncrement"`
	SourceNode     string
	Date           string `gorm:"index"` // Index on the 'Date' field
	Time           string
	InputFile      string
	BeginTime      float64
	EndTime        float64
	SpeciesCode    string
	ScientificName string `gorm:"index"` // Index on the 'ScientificName' field
	CommonName     string `gorm:"index"` // Index on the 'CommonName' field
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

// New creates and returns a new Note with the provided parameters and current date and time.
// It uses the configuration and parsing functions to set the appropriate fields.
func New(ctx *config.Context, beginTime, endTime float64, species string, confidence float64, clipName string, elapsedTime time.Duration) Note {
	// Parse the species string to get the scientific name, common name, and species code.
	scientificName, commonName, speciesCode := ParseSpeciesString(species)

	// detectionTime is time now minus 3 seconds to account for the delay in the detection
	now := time.Now()
	date := now.Format("2006-01-02")
	detectionTime := now.Add(-3 * time.Second)
	time := detectionTime.Format("15:04:05")

	// Return a new Note struct populated with the provided parameters as well as the current date and time.
	return Note{
		SourceNode:     ctx.Settings.Node.Name,           // From the provided configuration settings.
		Date:           date,                             // Use ISO 8601 date format.
		Time:           time,                             // Use 24-hour time format.
		InputFile:      ctx.Settings.Input.Path,          // From the provided configuration settings.
		BeginTime:      beginTime,                        // Start time of the observation.
		EndTime:        endTime,                          // End time of the observation.
		SpeciesCode:    speciesCode,                      // Parsed species code.
		ScientificName: scientificName,                   // Parsed scientific name of the species.
		CommonName:     commonName,                       // Parsed common name of the species.
		Confidence:     confidence,                       // Confidence score of the observation.
		Latitude:       ctx.Settings.BirdNET.Latitude,    // Geographic latitude where the observation was made.
		Longitude:      ctx.Settings.BirdNET.Longitude,   // Geographic longitude where the observation was made.
		Threshold:      ctx.Settings.BirdNET.Threshold,   // Threshold setting from configuration.
		Sensitivity:    ctx.Settings.BirdNET.Sensitivity, // Sensitivity setting from configuration.
		ClipName:       clipName,                         // Name of the audio clip.
		ProcessingTime: elapsedTime,                      // Time taken to process the observation.
	}
}

// LogNote is the central function for logging observations. It writes a note to a log file and/or database
// depending on the provided configuration settings.
func LogNote(ctx *config.Context, note Note) error {
	// If a log file path is specified in the configuration, attempt to log the note to this file.
	if ctx.Settings.Realtime.Log.Enabled {
		if ctx.Settings.Debug {
			fmt.Println("Logging note to file...")
		}
		if err := LogNoteToFile(ctx, note); err != nil {
			// If an error occurs when logging to a file, wrap and return the error.
			fmt.Printf("failed to log note to file: %s", err)
		}
	}

	// If the configuration specifies a database (and it's not set to "none"), attempt to save the note to the database.
	if ctx.Settings.Output.SQLite.Enabled || ctx.Settings.Output.MySQL.Enabled {
		if ctx.Settings.Debug {
			fmt.Println("Saving note to database...")
		}
		if err := SaveToDatabase(ctx, note); err != nil {
			// If an error occurs when saving to the database, wrap and return the error.
			fmt.Printf("failed to save note to database: %s", err)
		}
	}

	if ctx.Settings.Realtime.Birdweather.Enabled {
		// Upload the note to Birdweather as go routine
		go UploadToBirdweather(ctx, note)
	}

	// Return nil to indicate that the logging operations completed without error.
	return nil
}

func UploadToBirdweather(ctx *config.Context, note Note) error {
	if ctx.Settings.Debug {
		fmt.Println("Uploading note to Birdweather...")
	}

	// Combine date and time strings
	dateTimeString := fmt.Sprintf("%sT%s", note.Date, note.Time)

	// Parse the combined string into a time.Time object
	// The format string should match the format of your dateTimeString
	parsedTime, err := time.Parse("2006-01-02T15:04:05", dateTimeString)
	if err != nil {
		return fmt.Errorf("error parsing date: %s", err)
	}

	// Format the parsed time in ISO8601 format with timezone
	timestamp := parsedTime.Format("2006-01-02T15:04:05.000Z07:00")

	soundscapeID, err := ctx.BirdweatherClient.UploadSoundscape(timestamp, note.ClipName)
	if err != nil {
		return fmt.Errorf("failed to upload soundscape to Birdweather: %s", err)
	}

	// Post the detection to Birdweather
	err = ctx.BirdweatherClient.PostDetection(timestamp, soundscapeID)
	if err != nil {
		return fmt.Errorf("failed to post detection to Birdweather: %s", err)
	}

	return nil
}

// WriteNotesTable writes a slice of Note structs to a table-formatted text output.
// The output can be directed to either stdout or a file specified by the filename.
// If the filename is an empty string, it writes to stdout.
func WriteNotesTable(ctx *config.Context, notes []Note, filename string) error {
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
		file, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("failed to create file: %v", err)
		}
		defer file.Close() // Ensure the file is closed when the function exits.
		w = file
	}

	// Write the header to the output destination.
	header := "Selection\tView\tChannel\tBegin File\tBegin Time (s)\tEnd Time (s)\tLow Freq (Hz)\tHigh Freq (Hz)\tSpecies Code\tCommon Name\tConfidence\n"
	if _, err := w.Write([]byte(header)); err != nil {
		return fmt.Errorf("failed to write header: %v", err)
	}

	// Pre-declare err outside the loop to avoid re-declaration
	var err error

	for i, note := range notes {
		if note.Confidence <= ctx.Settings.BirdNET.Threshold {
			continue // Skip the current iteration as the note doesn't meet the threshold
		}

		// Prepare the line for notes above the threshold
		line := fmt.Sprintf("%d\tSpectrogram 1\t1\t%s\t%.1f\t%.1f\t0\t15000\t%s\t%s\t%.4f\n",
			i+1, note.InputFile, note.BeginTime, note.EndTime, note.SpeciesCode, note.CommonName, note.Confidence)

		// Attempt to write the note
		if _, err = w.Write([]byte(line)); err != nil {
			break // If an error occurs, exit the loop
		}
	}

	// Check if an error occurred during the loop and return it
	if err != nil {
		return fmt.Errorf("failed to write note: %v", err)
	} else if filename != "" {
		fmt.Println("Output written to", filename)
	}

	// Return nil if the writing operation completes successfully.
	return nil
}

// WriteNotesCsv writes the slice of notes to the specified destination in CSV format.
// If filename is an empty string, the function writes to stdout.
// The function returns an error if writing to the destination fails.
func WriteNotesCsv(ctx *config.Context, notes []Note, filename string) error {
	// Define an io.Writer to abstract the writing operation.
	var w io.Writer

	// Determine the output destination, file or screen
	if ctx.Settings.Output.File.Enabled {
		// Ensure the filename has a .csv extension.
		if !strings.HasSuffix(filename, ".csv") {
			filename += ".csv"
		}
		// Create or truncate the file with the given filename.
		file, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filename, err)
		}
		defer file.Close()
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

	for _, note := range notes {
		if note.Confidence <= ctx.Settings.BirdNET.Threshold {
			continue // Skip the current iteration as the note doesn't meet the threshold
		}

		line := fmt.Sprintf("%f,%f,%s,%s,%.4f\n",
			note.BeginTime, note.EndTime, note.ScientificName, note.CommonName, note.Confidence)

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

package observation

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// LogNoteToFile saves the Note to a log file.
func LogNoteToFile(settings *conf.Settings, note *datastore.Note) error {
	// Separate the directory and file name from the log path
	dir, fileName := filepath.Split(settings.Realtime.Log.Path)

	// Expand the directory path to an absolute path
	basePath := conf.GetBasePath(dir)

	// Recombine to form the full absolute path of the log file
	absoluteFilePath := filepath.Join(basePath, fileName)

	// Open the log file for appending, creating it if it doesn't exist
	file, err := os.OpenFile(absoluteFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Printf("failed to open file '%s': %v\n", absoluteFilePath, err)
		return fmt.Errorf("failed to open file '%s': %w", absoluteFilePath, err)
	}
	defer file.Close()

	// Parse the time from the note, assuming it's in the "15:04:05" format
	t, err := time.Parse("15:04:05", note.Time)
	if err != nil {
		fmt.Printf("failed to parse time '%s': %v\n", note.Time, err)
		return fmt.Errorf("failed to parse time '%s': %w", note.Time, err)
	}

	// Determine the time format string based on the user's preference
	timeFormat := "15:04:05"
	if !settings.Main.TimeAs24h {
		timeFormat = "03:04:05 PM"
	}

	// Format the note data for logging
	logString := fmt.Sprintf("%s %s\n", t.Format(timeFormat), note.CommonName)

	// Write the formatted log string to the file
	if _, err := file.WriteString(logString); err != nil {
		fmt.Printf("failed to write to file '%s': %v\n", absoluteFilePath, err)
		return fmt.Errorf("failed to write to file '%s': %w", absoluteFilePath, err)
	}

	return nil
}

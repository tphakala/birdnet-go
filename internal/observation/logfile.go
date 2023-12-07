package observation

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/config"
)

// LogNoteToFile saves the Note to a log file.
func LogNoteToFile(settings *config.Settings, note Note) error {
	// Check if the directory of the log file exists. If not, create it.
	dir := filepath.Dir(settings.Realtime.Log.Path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, 0755) // The 0755 permission sets the directory as readable and executable to everyone, and only writable by the owner.
		if err != nil {
			return fmt.Errorf("failed to create directory: %v", err)
		}
	}

	// Open the file for appending. If it doesn't exist, create it.
	file, err := os.OpenFile(settings.Realtime.Log.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("failed to open file: %v\n", err)
		return err
	}
	defer file.Close()

	// Parse the time from the note
	t, err := time.Parse("15:04:05", note.Time) // Assuming note.Time is already in this format
	if err != nil {
		return fmt.Errorf("failed to parse time: %v", err)
	}

	// Determine the time format string based on the user's preference
	var timeFormat string
	if settings.Node.TimeAs24h {
		timeFormat = "15:04:05"
	} else {
		timeFormat = "03:04:05 PM"
	}

	// Format the note data
	logString := fmt.Sprintf("%s %s\n", t.Format(timeFormat), note.CommonName)

	// Write the formatted data to the file
	if _, err := file.WriteString(logString); err != nil {
		fmt.Printf("failed to write to file: %v\n", err)
		return err
	}

	return nil
}

package detection

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// LogToFile saves the detection Result to a log file.
// The log format is: "HH:MM:SS CommonName" (or "HH:MM:SS PM CommonName" for 12h format).
func LogToFile(settings *conf.Settings, result *Result) error {
	if result == nil {
		return fmt.Errorf("result cannot be nil")
	}
	// Separate the directory and file name from the log path
	dir, fileName := filepath.Split(settings.Realtime.Log.Path)

	// Expand the directory path to an absolute path
	basePath := conf.GetBasePath(dir)

	// Recombine to form the full absolute path of the log file
	absoluteFilePath := filepath.Join(basePath, fileName)

	// Open the log file for appending, creating it if it doesn't exist
	file, err := os.OpenFile(absoluteFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: absoluteFilePath is from settings
	if err != nil {
		return fmt.Errorf("failed to open file '%s': %w", absoluteFilePath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			logger.Global().Module("detection").Error("failed to close log file", logger.Error(closeErr))
		}
	}()

	// Determine the time format string based on the user's preference
	timeFormat := "15:04:05"
	if !settings.Main.TimeAs24h {
		timeFormat = "03:04:05 PM"
	}

	// Format the detection data for logging
	logString := fmt.Sprintf("%s %s\n", result.Timestamp.Format(timeFormat), result.Species.CommonName)

	// Write the formatted log string to the file
	if _, err := file.WriteString(logString); err != nil {
		return fmt.Errorf("failed to write to file '%s': %w", absoluteFilePath, err)
	}

	return nil
}

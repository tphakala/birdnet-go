package output

import (
	"fmt"
	"os"
	"time"
)

// State to remember between log messages
var (
	lastWrittenLine         string
	lastWriteTime           time.Time
	lastWriteDay            int
	duplicateMessageTimeout = 5 * time.Minute
)

// WriteToLogfile appends the provided text to a log file at the specified logPath
// The function rotates the log file daily and prevents flooding the log with duplicate entries
func WriteToLogfile(logPath, text string) (err error) {
	// Determine the full path for the log file.
	fileName := fmt.Sprintf("%s/detections.txt", logPath)

	currentTime := time.Now()

	// Prevent the log from being flooded by duplicate detections
	// If the message is the same as the previous one and was written recently, we skip it
	if time.Since(lastWriteTime) <= duplicateMessageTimeout && text == lastWrittenLine {
		return nil
	}

	lastWrittenLine = text

	// Check if we need to rotate the log file
	// We rotate the file if the date has changed since the last write
	currentDay := currentTime.Day()
	if lastWriteDay != 0 && lastWriteDay != currentDay {
		// Rename the log file to indicate the date it covers
		yesterday := currentTime.AddDate(0, 0, -1)
		newName := fmt.Sprintf("log_%s.txt", yesterday.Format("02-01-06"))
		if err = os.Rename(fileName, newName); err != nil {
			return err
		}
	}
	lastWriteDay = currentDay

	// Open the log file for appending. If the file doesn't exist, create it
	file, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the detection with its timestamp to the log file
	// TODO: add support for 12 hour time format
	_, err = file.WriteString(fmt.Sprintf("%s %s\n", currentTime.Format("15:04:05"), text))
	lastWriteTime = currentTime

	return err
}

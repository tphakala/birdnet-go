package logger

import (
	"os"
	"time"
)

// FileHandler defines an interface for log file operations.
type FileHandler interface {
	Open(filename string) error
	Write(p []byte) (n int, err error)
	Close() error
}

// DefaultFileHandler implements FileHandler with support for log rotation.
type DefaultFileHandler struct {
	file        *os.File  // Reference to the open file
	filename    string    // Name of the log file
	currentSize int64     // Current size of the file in bytes
	settings    Settings  // Log rotation settings
	lastModTime time.Time // Last modification time of the file
}

// Settings contains configuration parameters for log rotation.
type Settings struct {
	RotationType RotationType // Type of rotation (daily, weekly, size-based)
	MaxSize      int64        // Maximum file size in bytes for size-based rotation
	RotationDay  time.Weekday // Day of the week for weekly rotation
}

// RotationType enumerates different strategies for log rotation.
type RotationType int

const (
	RotationDaily  RotationType = iota // Rotate logs daily
	RotationWeekly                     // Rotate logs weekly
	RotationSize                       // Rotate logs based on size
)

// Open initializes the log file for writing and handles log rotation if necessary.
func (f *DefaultFileHandler) Open(filename string) error {
	f.filename = filename
	f.currentSize = 0

	// Check file information to set the initial last modification time
	fileInfo, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// If file does not exist, set lastModTime to the start of the day
			f.lastModTime = time.Now().Truncate(24 * time.Hour)
		} else {
			return err
		}
	} else {
		f.lastModTime = fileInfo.ModTime()
	}

	// Rotate the file if needed, otherwise open the existing file
	if f.needsRotation(0) {
		return f.rotateFile()
	}
	f.file, err = os.OpenFile(f.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	return err
}

// Write appends data to the log file and rotates the file if necessary.
func (f *DefaultFileHandler) Write(p []byte) (n int, err error) {
	if f.needsRotation(len(p)) {
		if err := f.rotateFile(); err != nil {
			return 0, err
		}
	}
	written, err := f.file.Write(p)
	f.currentSize += int64(written)
	return written, err
}

// Close finalizes the log file operations by closing the file.
func (f *DefaultFileHandler) Close() error {
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

// needsRotation determines if the file requires rotation based on the configured rotation type.
func (f *DefaultFileHandler) needsRotation(size int) bool {
	switch f.settings.RotationType {
	case RotationDaily:
		// Rotate if the current date is different from the last modification date
		return time.Now().Format("20060102") != f.lastModTime.Format("20060102")
	case RotationWeekly:
		// Rotate on the specified day if the week number has changed
		return time.Now().Weekday() == f.settings.RotationDay &&
			time.Now().Format("2006W01") != f.lastModTime.Format("2006W01")
	case RotationSize:
		// Rotate if the new size exceeds the maximum size
		return f.currentSize+int64(size) > f.settings.MaxSize
	default:
		return false
	}
}

// rotateFile handles the log file rotation process.
func (f *DefaultFileHandler) rotateFile() error {
	// Close the current file if open
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return err
		}
	}

	// Rename the existing file with a timestamp before creating a new one
	if _, err := os.Stat(f.filename); err == nil {
		newPath := f.filename + "." + time.Now().Format("20060102150405")
		if err := os.Rename(f.filename, newPath); err != nil {
			return err
		}
	}

	// Create or open the file for appending
	var err error
	f.file, err = os.OpenFile(f.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	// Reset the current size and update the last modification time to the start of the current day
	f.currentSize = 0
	f.lastModTime = time.Now().Truncate(24 * time.Hour)
	return nil
}

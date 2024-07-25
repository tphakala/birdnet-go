// filehandler.go
package logger

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// FileHandler defines an interface for log file operations.
type FileHandler interface {
	Open(filename string) error
	Write(p []byte) (n int, err error)
	Close() error
}

// Settings contains configuration parameters for log rotation.
type Settings struct {
	RotationType RotationType // Type of rotation (daily, weekly, size-based)
	MaxSize      int64        // Maximum file size in bytes for size-based rotation
	RotationDay  string       // Day of the week for weekly rotation (as a string: "Sunday", "Monday", etc.)
}

// RotationType enumerates different strategies for log rotation.
type RotationType int

const (
	RotationDaily RotationType = iota
	RotationWeekly
	RotationSize
)

// DefaultFileHandler implements FileHandler with support for log rotation.
type DefaultFileHandler struct {
	file        *os.File
	filename    string
	currentSize int64
	settings    Settings
	lastModTime time.Time
}

// ParseWeekday converts a string to time.Weekday
func ParseWeekday(day string) (time.Weekday, error) {
	switch strings.ToLower(day) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return time.Sunday, fmt.Errorf("invalid weekday: %s", day)
	}
}

// Open initializes the log file for writing and handles log rotation if necessary.
func (f *DefaultFileHandler) Open(filename string) error {
	f.filename = filename
	f.currentSize = 0

	fileInfo, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			f.lastModTime = time.Now().Truncate(24 * time.Hour)
		} else {
			return err
		}
	} else {
		f.lastModTime = fileInfo.ModTime()
		f.currentSize = fileInfo.Size()
	}

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
		return time.Now().Format("20060102") != f.lastModTime.Format("20060102")
	case RotationWeekly:
		rotationDay, err := ParseWeekday(f.settings.RotationDay)
		if err != nil {
			return false
		}
		return time.Now().Weekday() == rotationDay &&
			time.Now().Format("2006W01") != f.lastModTime.Format("2006W01")
	case RotationSize:
		return f.currentSize+int64(size) > f.settings.MaxSize
	default:
		return false
	}
}

// rotateFile handles the log file rotation process.
func (f *DefaultFileHandler) rotateFile() error {
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return err
		}
	}

	if _, err := os.Stat(f.filename); err == nil {
		newPath := f.filename + "." + time.Now().Format("20060102")
		if err := os.Rename(f.filename, newPath); err != nil {
			return err
		}
	}

	var err error
	f.file, err = os.OpenFile(f.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	f.currentSize = 0
	f.lastModTime = time.Now().Truncate(24 * time.Hour)
	return nil
}

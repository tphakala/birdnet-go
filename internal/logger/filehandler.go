// file_handler.go
package logger

import (
	"os"
	"time"
)

// FileHandler defines the interface for handling log files.
type FileHandler interface {
	Open(filename string) error
	Write(p []byte) (n int, err error)
	Close() error
}

type DefaultFileHandler struct {
	file        *os.File
	filename    string
	currentSize int64
	settings    Settings
	lastModTime time.Time // Last modification time of the file
}

func (f *DefaultFileHandler) Open(filename string) error {
	f.filename = filename
	f.currentSize = 0
	// Set initial lastModTime
	fileInfo, err := os.Stat(filename)
	if os.IsNotExist(err) {
		f.lastModTime = time.Now()
	} else if err != nil {
		return err
	} else {
		f.lastModTime = fileInfo.ModTime()
	}
	return f.rotateFile()
}

// Write writes data to the file.
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

// Close closes the file.
func (f *DefaultFileHandler) Close() error {
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

func (f *DefaultFileHandler) needsRotation(size int) bool {
	switch f.settings.RotationType {
	case RotationDaily:
		return time.Now().Format("20060102") != f.lastModTime.Format("20060102")
	case RotationWeekly:
		return time.Now().Weekday() == f.settings.RotationDay &&
			time.Now().Format("2006W01") != f.lastModTime.Format("2006W01")
	case RotationSize:
		return f.currentSize+int64(size) > f.settings.MaxSize
	default:
		return false
	}
}

// rotateFile handles the actual log file rotation.
func (f *DefaultFileHandler) rotateFile() error {
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return err
		}
	}

	// Check if the file exists before trying to rotate
	_, err := os.Stat(f.filename)
	if err == nil {
		newPath := f.filename + "." + time.Now().Format("20060102150405")
		err := os.Rename(f.filename, newPath)
		if err != nil {
			return err
		}
	}

	f.file, err = os.OpenFile(f.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	f.currentSize = 0
	f.lastModTime = time.Now() // Update last modification time
	return nil
}

// Settings contains configuration for log rotation.
type Settings struct {
	RotationType RotationType
	MaxSize      int64        // Max size in bytes for RotationSize
	RotationDay  time.Weekday // for RotationWeekly
}

// RotationType defines different types of log rotations.
type RotationType int

const (
	RotationDaily RotationType = iota
	RotationWeekly
	RotationSize
)

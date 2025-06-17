// label_files.go contains embedded label files for various models and locales
package birdnet

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Model version constants
const (
	BirdNET_GLOBAL_6K_V2_4 = "BirdNET_GLOBAL_6K_V2.4"
)

//go:embed data/labels/V2.4/*.txt
var v24LabelFiles embed.FS

// GetLabelFileData loads a label file by model version and locale code
func GetLabelFileData(modelVersion, localeCode string) ([]byte, error) {
	if !strings.HasPrefix(modelVersion, BirdNET_GLOBAL_6K_V2_4) {
		return nil, fmt.Errorf("unsupported model version: %s", modelVersion)
	}

	// Use the proper locale mapping from conf package
	filename, err := conf.GetLabelFilename(modelVersion, localeCode)
	if err != nil {
		// If the locale mapping fails, fall back to English
		fallbackFilename, fallbackErr := conf.GetLabelFilename(modelVersion, "en-uk")
		if fallbackErr != nil {
			return nil, fmt.Errorf("failed to get filename for locale '%s' and fallback failed: %w", localeCode, fallbackErr)
		}
		filename = fallbackFilename
	}

	// Try to read the file
	data, err := v24LabelFiles.ReadFile(path.Join("data", "labels", filename))
	if err == nil {
		return data, nil
	}

	// If the mapped file doesn't exist, fall back to English
	fallbackFilename, fallbackErr := conf.GetLabelFilename(modelVersion, "en-uk")
	if fallbackErr != nil {
		return nil, fmt.Errorf("failed to load locale '%s' and fallback configuration failed: %w", localeCode, fallbackErr)
	}

	data, err = v24LabelFiles.ReadFile(path.Join("data", "labels", fallbackFilename))
	if err == nil {
		return data, nil
	}

	// List available files for debugging
	availableFiles := []string{}
	walkErr := fs.WalkDir(v24LabelFiles, "data/labels/V2.4", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			availableFiles = append(availableFiles, filepath.Base(path))
		}
		return nil
	})

	if walkErr != nil {
		return nil, fmt.Errorf("error listing available label files: %w", walkErr)
	}

	return nil, fmt.Errorf("label file for locale '%s' not found. Available files: %v",
		localeCode, availableFiles)
}

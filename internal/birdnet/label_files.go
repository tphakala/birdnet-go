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

// tryReadFallbackFile attempts to read the English fallback label file
func tryReadFallbackFile(modelVersion string) ([]byte, error) {
	fallbackFilename, err := conf.GetLabelFilename(modelVersion, "en-uk")
	if err != nil {
		return nil, fmt.Errorf("failed to get fallback filename: %w", err)
	}

	data, err := v24LabelFiles.ReadFile(path.Join("data", "labels", fallbackFilename))
	if err != nil {
		return nil, fmt.Errorf("failed to read fallback file '%s': %w", fallbackFilename, err)
	}

	return data, nil
}

// GetLabelFileData loads a label file by model version and locale code
func GetLabelFileData(modelVersion, localeCode string) ([]byte, error) {
	if !strings.HasPrefix(modelVersion, BirdNET_GLOBAL_6K_V2_4) {
		return nil, fmt.Errorf("unsupported model version: %s", modelVersion)
	}

	// Use the proper locale mapping from conf package
	filename, originalMappingErr := conf.GetLabelFilename(modelVersion, localeCode)
	if originalMappingErr != nil {
		// If the locale mapping fails, try fallback to English
		data, fallbackErr := tryReadFallbackFile(modelVersion)
		if fallbackErr != nil {
			return nil, fmt.Errorf("failed to get filename for locale '%s' (original error: %v) and fallback failed: %w",
				localeCode, originalMappingErr, fallbackErr)
		}
		return data, nil
	}

	// Try to read the file
	data, originalReadErr := v24LabelFiles.ReadFile(path.Join("data", "labels", filename))
	if originalReadErr == nil {
		return data, nil
	}

	// If the mapped file doesn't exist, try fallback to English
	data, fallbackErr := tryReadFallbackFile(modelVersion)
	if fallbackErr != nil {
		return nil, fmt.Errorf("failed to load locale '%s' (original read error: %v) and fallback failed: %w",
			localeCode, originalReadErr, fallbackErr)
	}

	return data, nil
}

// listAvailableFiles returns a list of available label files for debugging
func listAvailableFiles() ([]string, error) {
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

	return availableFiles, nil
}

// label_files.go contains embedded label files for various models and locales
package birdnet

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
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

	// Normalize locale code for consistent handling
	normalizedLocale := strings.ToLower(localeCode)

	// Get the file name pattern based on the locale code
	filePattern := fmt.Sprintf("%s_Labels_%s.txt", BirdNET_GLOBAL_6K_V2_4, normalizedLocale)

	// Try to find the exact match
	data, err := v24LabelFiles.ReadFile(filepath.Join("data", "labels", "V2.4", filePattern))
	if err == nil {
		return data, nil
	}

	// If the locale has a hyphen (like pt-br), try the underscore version
	if strings.Contains(normalizedLocale, "-") {
		altLocale := strings.ReplaceAll(normalizedLocale, "-", "_")
		if strings.ToUpper(altLocale[3:]) == altLocale[3:] {
			// Adjust format from "pt-br" to "pt_BR"
			altLocale = altLocale[:3] + strings.ToUpper(altLocale[3:])
		}
		filePattern = fmt.Sprintf("%s_Labels_%s.txt", BirdNET_GLOBAL_6K_V2_4, altLocale)
		data, err = v24LabelFiles.ReadFile(filepath.Join("data", "labels", "V2.4", filePattern))
		if err == nil {
			return data, nil
		}
	}

	// Fall back to English if requested locale isn't found
	if normalizedLocale != "en" && normalizedLocale != "en-uk" {
		filePattern = fmt.Sprintf("%s_Labels_en_uk.txt", BirdNET_GLOBAL_6K_V2_4)
		data, err = v24LabelFiles.ReadFile(filepath.Join("data", "labels", "V2.4", filePattern))
		if err == nil {
			return data, nil
		}
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

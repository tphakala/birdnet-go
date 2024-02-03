package analysis

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// DirectoryAnalysis processes all .wav files in the given directory for analysis.
func DirectoryAnalysis(ctx *conf.Context, bn birdnet.BirdNET) error {
	analyzeFunc := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Return the error to stop the walking process.
			return err
		}

		if d.IsDir() {
			// If recursion is not enabled and this is a subdirectory, skip it.
			if !ctx.Settings.Input.Recursive && path != ctx.Settings.Input.Path {
				return filepath.SkipDir
			}
			// If it's the root directory or recursion is enabled, continue walking.
			return nil
		}

		if strings.HasSuffix(d.Name(), ".wav") {
			fmt.Println("Analyzing file:", path)
			ctx.Settings.Input.Path = path
			if err := FileAnalysis(ctx, bn); err != nil {
				// If FileAnalysis returns an error log it and continue
				log.Printf("Error analyzing file '%s': %v", path, err)
				return nil
			}
		}
		return nil
	}

	// Start walking through the directory
	err := filepath.WalkDir(ctx.Settings.Input.Path, analyzeFunc)
	if err != nil {
		log.Fatalf("Failed to walk directory: %v", err)
	}

	return nil
}

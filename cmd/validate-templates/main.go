package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/httpcontroller"
)

func main() {
	var templatesDir string
	flag.StringVar(&templatesDir, "dir", "views", "Directory containing template files")
	flag.Parse()

	// Convert to absolute path
	absDir, err := filepath.Abs(templatesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Validating templates in: %s\n", absDir)

	// Run validation
	result, err := httpcontroller.ValidateTemplates(absDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Validation error: %v\n", err)
		os.Exit(1)
	}

	// Print results
	fmt.Println(result.String())

	// Exit with error code if issues found
	if result.HasIssues() {
		os.Exit(1)
	}
}
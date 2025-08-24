package httpcontroller

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TemplateIssue represents a template validation issue
type TemplateIssue struct {
	File    string
	Line    int
	Content string
	Issue   string
}

// TemplateValidationResult holds the results of template validation
type TemplateValidationResult struct {
	Issues    []TemplateIssue
	FilesScanned int
}

// ValidateTemplates scans templates for old patterns that need migration
// This ensures templates use the new RenderData.Version instead of .Settings.Version
func ValidateTemplates(templatesDir string) (*TemplateValidationResult, error) {
	result := &TemplateValidationResult{
		Issues: make([]TemplateIssue, 0),
	}

	// Pattern to match old .Settings.Version usage
	settingsVersionPattern := regexp.MustCompile(`\{\{\s*\.Settings\.Version\s*\}\}`)
	
	// Walk through all template files
	err := filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only check HTML template files
		if !strings.HasSuffix(path, ".html") {
			return nil
		}

		result.FilesScanned++

		// Read and scan the file
		issues, scanErr := scanTemplateFile(path, settingsVersionPattern)
		if scanErr != nil {
			return fmt.Errorf("error scanning %s: %w", path, scanErr)
		}

		result.Issues = append(result.Issues, issues...)
		return nil
	})

	return result, err
}

// scanTemplateFile scans a single template file for migration issues
func scanTemplateFile(filePath string, pattern *regexp.Regexp) ([]TemplateIssue, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var issues []TemplateIssue
	scanner := bufio.NewScanner(file)
	lineNum := 1

	for scanner.Scan() {
		line := scanner.Text()
		
		// Check for old .Settings.Version pattern
		if pattern.MatchString(line) {
			issues = append(issues, TemplateIssue{
				File:    filePath,
				Line:    lineNum,
				Content: strings.TrimSpace(line),
				Issue:   "Uses deprecated .Settings.Version - should use .Version instead",
			})
		}

		lineNum++
	}

	return issues, scanner.Err()
}

// HasIssues returns true if validation found any issues
func (r *TemplateValidationResult) HasIssues() bool {
	return len(r.Issues) > 0
}

// String formats the validation result as a string
func (r *TemplateValidationResult) String() string {
	if !r.HasIssues() {
		return fmt.Sprintf("Template validation passed: %d files scanned, no issues found", r.FilesScanned)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Template validation found %d issues in %d files:\n", len(r.Issues), r.FilesScanned))

	for _, issue := range r.Issues {
		sb.WriteString(fmt.Sprintf("  %s:%d - %s\n", issue.File, issue.Line, issue.Issue))
		sb.WriteString(fmt.Sprintf("    Content: %s\n", issue.Content))
	}

	return sb.String()
}
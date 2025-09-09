package httpcontroller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTemplates(t *testing.T) {
	// Create a temporary directory for test templates
	tempDir := t.TempDir()

	// Create test template files
	testFiles := map[string]string{
		"good.html": `
<html>
<body>
	<h1>Version: {{.Version}}</h1>
	<p>Build Date: {{.Settings.BuildDate}}</p>
</body>
</html>`,
		"bad.html": `
<html>
<body>
	<h1>Version: {{.Settings.Version}}</h1>
	<script>
		window.config = {
			version: '{{.Settings.Version}}'
		};
	</script>
</body>
</html>`,
		"mixed.html": `
<html>
<body>
	<h1>Good: {{.Version}}</h1>
	<p>Bad: {{.Settings.Version}}</p>
</body>
</html>`,
		"non-template.txt": `This is not a template file with {{.Settings.Version}}`,
	}

	// Write test files
	for filename, content := range testFiles {
		filePath := filepath.Join(tempDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	// Run validation
	result, err := ValidateTemplates(tempDir)
	if err != nil {
		t.Fatalf("ValidateTemplates returned error: %v", err)
	}

	// Verify results
	if result == nil {
		t.Fatal("ValidateTemplates returned nil result")
	}

	// Should scan 3 HTML files (ignoring .txt file)
	expectedFilesScanned := 3
	if result.FilesScanned != expectedFilesScanned {
		t.Errorf("Expected %d files scanned, got %d", expectedFilesScanned, result.FilesScanned)
	}

	// Should find 3 issues total (2 in bad.html, 1 in mixed.html)
	expectedIssues := 3
	if len(result.Issues) != expectedIssues {
		t.Errorf("Expected %d issues, got %d", expectedIssues, len(result.Issues))
		
		// Print issues for debugging
		for _, issue := range result.Issues {
			t.Logf("Issue: %s:%d - %s", issue.File, issue.Line, issue.Issue)
		}
	}

	// Verify HasIssues
	if !result.HasIssues() {
		t.Error("Expected HasIssues() to return true")
	}

	// Check specific issues
	foundBadHtml := false
	foundMixedHtml := false
	
	for _, issue := range result.Issues {
		if filepath.Base(issue.File) == "bad.html" {
			foundBadHtml = true
		}
		if filepath.Base(issue.File) == "mixed.html" {
			foundMixedHtml = true
		}
		
		// All issues should be about .Settings.Version
		if !strings.Contains(issue.Issue, ".Settings.Version") {
			t.Errorf("Expected issue to mention .Settings.Version, got: %s", issue.Issue)
		}
	}

	if !foundBadHtml {
		t.Error("Expected to find issues in bad.html")
	}
	if !foundMixedHtml {
		t.Error("Expected to find issues in mixed.html")
	}

	// Verify String() method
	resultStr := result.String()
	if !strings.Contains(resultStr, "validation found") {
		t.Error("Expected String() to mention validation found")
	}
}

func TestValidateTemplates_NoIssues(t *testing.T) {
	// Create a temporary directory for test templates
	tempDir := t.TempDir()

	// Create test template file with no issues
	goodTemplate := `
<html>
<body>
	<h1>Version: {{.Version}}</h1>
	<p>System: {{.Settings.System}}</p>
	<p>Other: {{.SomeOtherField}}</p>
</body>
</html>`

	filePath := filepath.Join(tempDir, "good.html")
	err := os.WriteFile(filePath, []byte(goodTemplate), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Run validation
	result, err := ValidateTemplates(tempDir)
	if err != nil {
		t.Fatalf("ValidateTemplates returned error: %v", err)
	}

	// Verify no issues found
	if result.HasIssues() {
		t.Error("Expected no issues, but found some:")
		for _, issue := range result.Issues {
			t.Logf("  %s:%d - %s", issue.File, issue.Line, issue.Issue)
		}
	}

	// Verify String() method for clean result
	resultStr := result.String()
	if !strings.Contains(resultStr, "no issues found") {
		t.Errorf("Expected String() to mention no issues found, got: %s", resultStr)
	}
}

func TestValidateTemplates_EmptyDirectory(t *testing.T) {
	// Create empty temporary directory
	tempDir := t.TempDir()

	// Run validation on empty directory
	result, err := ValidateTemplates(tempDir)
	if err != nil {
		t.Fatalf("ValidateTemplates returned error: %v", err)
	}

	// Verify results for empty directory
	if result.FilesScanned != 0 {
		t.Errorf("Expected 0 files scanned, got %d", result.FilesScanned)
	}

	if result.HasIssues() {
		t.Error("Expected no issues in empty directory")
	}
}

func TestValidateTemplates_NonExistentDirectory(t *testing.T) {
	// Try to validate non-existent directory
	nonExistentDir := "/path/that/does/not/exist"
	
	_, err := ValidateTemplates(nonExistentDir)
	if err == nil {
		t.Error("Expected error for non-existent directory")
	}
}


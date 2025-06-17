package birdnet

import (
    "io/ioutil"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "sync"
    "testing"
)

// createTempFile creates a temporary file with the given content and returns its path.
func createTempFile(t *testing.T, content string) string {
    t.Helper()
    tmpFile, err := ioutil.TempFile("", "label_test_")
    if err != nil {
        t.Fatalf("failed to create temp file: %v", err)
    }
    if _, err := tmpFile.WriteString(content); err != nil {
        tmpFile.Close()
        os.Remove(tmpFile.Name())
        t.Fatalf("failed to write to temp file: %v", err)
    }
    tmpFile.Close()
    return tmpFile.Name()
}

// removeFile removes a file and reports a test error on failure.
func removeFile(t *testing.T, path string) {
    t.Helper()
    if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
        t.Errorf("failed to remove file %s: %v", path, err)
    }
}

// TestParseLabelLine_ValidInput_ReturnsLabel tests parsing valid label lines.
func TestParseLabelLine_ValidInput_ReturnsLabel(t *testing.T) {
    cases := []struct {
        line     string
        expected Label
    }{
        {"sparrow,1", Label{Species: "sparrow", Count: 1}},
        {"robin, 5", Label{Species: "robin", Count: 5}},
        {"フクロウ,2", Label{Species: "フクロウ", Count: 2}},
    }
    for _, c := range cases {
        lbl, err := ParseLabelLine(c.line)
        if err != nil {
            t.Errorf("ParseLabelLine(%q) returned unexpected error: %v", c.line, err)
            continue
        }
        if !reflect.DeepEqual(lbl, c.expected) {
            t.Errorf("ParseLabelLine(%q) = %+v; want %+v", c.line, lbl, c.expected)
        }
    }
}

// TestParseLabelLine_InvalidInput_ReturnsError tests parsing malformed label lines.
func TestParseLabelLine_InvalidInput_ReturnsError(t *testing.T) {
    invalid := []string{"", "sparrow", "sparrow,abc", ",5", " , ", "sparrow,-1"}
    for _, line := range invalid {
        if _, err := ParseLabelLine(line); err == nil {
            t.Errorf("ParseLabelLine(%q) expected error, got nil", line)
        }
    }
}

// TestLoadLabels_HappyPath tests loading a well-formed label file.
func TestLoadLabels_HappyPath(t *testing.T) {
    content := `
# Sample label file
sparrow,1
robin,2

`
    path := createTempFile(t, content)
    defer removeFile(t, path)

    labels, err := LoadLabels(path)
    if err != nil {
        t.Fatalf("LoadLabels returned error: %v", err)
    }
    expected := []Label{
        {Species: "sparrow", Count: 1},
        {Species: "robin", Count: 2},
    }
    if !reflect.DeepEqual(labels, expected) {
        t.Errorf("LoadLabels = %+v; want %+v", labels, expected)
    }
}

// TestLoadLabels_EmptyFile_ReturnsEmptySlice tests loading an empty file.
func TestLoadLabels_EmptyFile_ReturnsEmptySlice(t *testing.T) {
    path := createTempFile(t, "")
    defer removeFile(t, path)

    labels, err := LoadLabels(path)
    if err != nil {
        t.Fatalf("LoadLabels returned error on empty file: %v", err)
    }
    if len(labels) != 0 {
        t.Errorf("LoadLabels on empty file = %d labels; want 0", len(labels))
    }
}

// TestLoadLabels_MalformedContent_ReturnsError tests error on malformed content.
func TestLoadLabels_MalformedContent_ReturnsError(t *testing.T) {
    path := createTempFile(t, "not_a_label_line")
    defer removeFile(t, path)

    if _, err := LoadLabels(path); err == nil {
        t.Error("LoadLabels expected error for malformed content, got nil")
    }
}

// TestLoadLabels_FileNotFound_ReturnsError tests error when file does not exist.
func TestLoadLabels_FileNotFound_ReturnsError(t *testing.T) {
    _, err := LoadLabels("nonexistent_file.csv")
    if err == nil {
        t.Error("LoadLabels expected error for missing file, got nil")
    }
}

// TestLoadLabels_WindowsLineEndings_ReturnsLabels tests handling CRLF.
func TestLoadLabels_WindowsLineEndings_ReturnsLabels(t *testing.T) {
    content := "sparrow,1\r\nrobin,2\r\n"
    path := createTempFile(t, content)
    defer removeFile(t, path)

    labels, err := LoadLabels(path)
    if err != nil {
        t.Fatalf("LoadLabels returned error: %v", err)
    }
    if len(labels) != 2 {
        t.Errorf("LoadLabels = %d labels; want 2", len(labels))
    }
}

// TestLoadLabels_UnicodeContent_ReturnsLabels tests handling Unicode species names.
func TestLoadLabels_UnicodeContent_ReturnsLabels(t *testing.T) {
    content := "águia,3\nフクロウ,4\n"
    path := createTempFile(t, content)
    defer removeFile(t, path)

    labels, err := LoadLabels(path)
    if err != nil {
        t.Fatalf("LoadLabels returned error: %v", err)
    }
    if labels[0].Species != "águia" || labels[1].Species != "フクロウ" {
        t.Errorf("LoadLabels unicode species mismatch: %+v", labels)
    }
}

// TestWriteLabels_HappyPath_WritesFile tests writing and re-loading labels.
func TestWriteLabels_HappyPath_WritesFile(t *testing.T) {
    labels := []Label{
        {Species: "sparrow", Count: 1},
        {Species: "robin", Count: 2},
    }
    dir := t.TempDir()
    path := filepath.Join(dir, "test_labels.csv")

    if err := WriteLabels(path, labels); err != nil {
        t.Fatalf("WriteLabels returned error: %v", err)
    }

    loaded, err := LoadLabels(path)
    if err != nil {
        t.Fatalf("LoadLabels returned error: %v", err)
    }
    if !reflect.DeepEqual(loaded, labels) {
        t.Errorf("Re-loaded labels = %+v; want %+v", loaded, labels)
    }
}

// TestWriteLabels_InvalidPath_ReturnsError tests error on writing to invalid path.
func TestWriteLabels_InvalidPath_ReturnsError(t *testing.T) {
    invalidDir := filepath.Join(os.TempDir(), "no_perm_dir")
    path := filepath.Join(invalidDir, "labels.csv")
    if err := WriteLabels(path, []Label{{Species: "sparrow", Count: 1}}); err == nil {
        t.Error("WriteLabels expected error for invalid path, got nil")
    }
}

// TestConcurrentLoadLabels_NoRaceCondition tests concurrent access to LoadLabels.
func TestConcurrentLoadLabels_NoRaceCondition(t *testing.T) {
    content := strings.Repeat("sparrow,1\n", 100)
    path := createTempFile(t, content)
    defer removeFile(t, path)

    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if _, err := LoadLabels(path); err != nil {
                t.Errorf("LoadLabels in goroutine returned error: %v", err)
            }
        }()
    }
    wg.Wait()
}

// TestFullLabelPipeline_CreatesAndLoadsLabels verifies the complete workflow.
func TestFullLabelPipeline_CreatesAndLoadsLabels(t *testing.T) {
    original := []Label{
        {Species: "finch", Count: 5},
        {Species: "swan", Count: 2},
    }
    dir := t.TempDir()
    path := filepath.Join(dir, "pipeline.csv")

    // Write initial labels
    if err := WriteLabels(path, original); err != nil {
        t.Fatalf("WriteLabels error: %v", err)
    }
    // Load and verify
    loaded, err := LoadLabels(path)
    if err != nil {
        t.Fatalf("LoadLabels error: %v", err)
    }
    if !reflect.DeepEqual(loaded, original) {
        t.Errorf("Pipeline loaded = %+v; want %+v", loaded, original)
    }
}

// BenchmarkParseLabelLine measures performance of parsing a single line.
func BenchmarkParseLabelLine(b *testing.B) {
    line := "sparrow,1"
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        _, _ = ParseLabelLine(line)
    }
}

// BenchmarkLoadLabels_Small measures loading a small label file.
func BenchmarkLoadLabels_Small(b *testing.B) {
    content := strings.Repeat("sparrow,1\n", 100)
    dir := b.TempDir()
    path := filepath.Join(dir, "small.csv")
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        b.Fatalf("failed to write temp file: %v", err)
    }
    b.ReportAllocs()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        if _, err := LoadLabels(path); err != nil {
            b.Fatalf("LoadLabels error: %v", err)
        }
    }
}

// BenchmarkLoadLabels_Large measures loading a large label file.
func BenchmarkLoadLabels_Large(b *testing.B) {
    content := strings.Repeat("sparrow,1\n", 100000)
    dir := b.TempDir()
    path := filepath.Join(dir, "large.csv")
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        b.Fatalf("failed to write temp file: %v", err)
    }
    b.ReportAllocs()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        if _, err := LoadLabels(path); err != nil {
            b.Fatalf("LoadLabels error: %v", err)
        }
    }
}
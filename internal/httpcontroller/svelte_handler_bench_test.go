package httpcontroller

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// Mock embedded filesystem for testing
//go:embed testdata/*
var testFS embed.FS

// createTestFile creates a test file with specified size
func createTestFile(b *testing.B, dir, name string, size int) string {
	b.Helper()
	path := filepath.Join(dir, name)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}

// mockFile implements fs.File and io.ReadSeeker for testing
type mockFile struct {
	*bytes.Reader
	name    string
	modTime time.Time
}

func (f *mockFile) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{
		name:    f.name,
		size:    int64(f.Len()),
		modTime: f.modTime,
	}, nil
}

func (f *mockFile) Close() error {
	return nil
}

type mockFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (fi *mockFileInfo) Name() string       { return fi.name }
func (fi *mockFileInfo) Size() int64        { return fi.size }
func (fi *mockFileInfo) Mode() fs.FileMode  { return 0o644 }
func (fi *mockFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *mockFileInfo) IsDir() bool        { return false }
func (fi *mockFileInfo) Sys() interface{}   { return nil }

// Old implementation using c.Stream (for comparison)
func serveWithStream(c echo.Context, file io.Reader, contentType string) error {
	c.Response().Header().Set("Content-Type", contentType)
	return c.Stream(http.StatusOK, contentType, file)
}

// New implementation using http.ServeContent
func serveWithServeContent(c echo.Context, file io.ReadSeeker, name string, modTime time.Time, contentType string) error {
	c.Response().Header().Set("Content-Type", contentType)
	http.ServeContent(c.Response(), c.Request(), name, modTime, file)
	return nil
}

// BenchmarkServeFile_Stream_Small benchmarks the old Stream approach with small files
func BenchmarkServeFile_Stream_Small(b *testing.B) {
	e := echo.New()
	data := make([]byte, 1024) // 1KB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithStream(c, file, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_ServeContent_Small benchmarks the new ServeContent approach with small files
func BenchmarkServeFile_ServeContent_Small(b *testing.B) {
	e := echo.New()
	data := make([]byte, 1024) // 1KB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	modTime := time.Now()
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithServeContent(c, file, "test.js", modTime, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_Stream_Medium benchmarks the old Stream approach with medium files
func BenchmarkServeFile_Stream_Medium(b *testing.B) {
	e := echo.New()
	data := make([]byte, 100*1024) // 100KB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithStream(c, file, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_ServeContent_Medium benchmarks the new ServeContent approach with medium files
func BenchmarkServeFile_ServeContent_Medium(b *testing.B) {
	e := echo.New()
	data := make([]byte, 100*1024) // 100KB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	modTime := time.Now()
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithServeContent(c, file, "test.js", modTime, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_Stream_Large benchmarks the old Stream approach with large files
func BenchmarkServeFile_Stream_Large(b *testing.B) {
	e := echo.New()
	data := make([]byte, 2*1024*1024) // 2MB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithStream(c, file, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_ServeContent_Large benchmarks the new ServeContent approach with large files
func BenchmarkServeFile_ServeContent_Large(b *testing.B) {
	e := echo.New()
	data := make([]byte, 2*1024*1024) // 2MB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	modTime := time.Now()
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithServeContent(c, file, "test.js", modTime, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_RangeRequest benchmarks ServeContent with Range requests
func BenchmarkServeFile_RangeRequest(b *testing.B) {
	e := echo.New()
	data := make([]byte, 1024*1024) // 1MB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	modTime := time.Now()
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
		req.Header.Set("Range", "bytes=0-1023") // Request first 1KB only
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		file := bytes.NewReader(data)
		_ = serveWithServeContent(c, file, "test.js", modTime, "application/javascript")
	}
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "range_requests")
	b.ReportMetric(float64(1024*b.N), "bytes_served") // Only 1KB served per request
}

// BenchmarkServeFile_Concurrent benchmarks concurrent file serving
func BenchmarkServeFile_Concurrent(b *testing.B) {
	e := echo.New()
	data := make([]byte, 100*1024) // 100KB file
	for i := range data {
		data[i] = byte(i % 256)
	}
	modTime := time.Now()
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			
			file := bytes.NewReader(data)
			_ = serveWithServeContent(c, file, "test.js", modTime, "application/javascript")
		}
	})
	
	// Report useful metrics
	b.ReportMetric(float64(b.N), "concurrent_requests")
	b.ReportMetric(float64(len(data)*b.N), "bytes_served")
}

// BenchmarkServeFile_DiskFile benchmarks serving actual files from disk
func BenchmarkServeFile_DiskFile(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	
	// Create test files of various sizes
	smallFile := createTestFile(b, tempDir, "small.js", 1024)       // 1KB
	mediumFile := createTestFile(b, tempDir, "medium.js", 100*1024) // 100KB
	largeFile := createTestFile(b, tempDir, "large.js", 1024*1024)  // 1MB
	
	benchmarks := []struct {
		name string
		path string
	}{
		{"Small", smallFile},
		{"Medium", mediumFile},
		{"Large", largeFile},
	}
	
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs() // Report memory allocations
			b.ResetTimer()
			
			for b.Loop() { // Use new Go 1.24 b.Loop() pattern
				req := httptest.NewRequest(http.MethodGet, "/test.js", http.NoBody)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				
				file, err := os.Open(bm.path)
				if err != nil {
					b.Fatal(err)
				}
				
				stat, err := file.Stat()
				if err != nil {
					if closeErr := file.Close(); closeErr != nil {
						// Log error but don't fail benchmark - ignore in benchmarks
						_ = closeErr
					}
					b.Fatal(err)
				}
				
				http.ServeContent(c.Response(), c.Request(), filepath.Base(bm.path), stat.ModTime(), file)
				if err := file.Close(); err != nil {
					// Log error but don't fail benchmark - ignore in benchmarks
					_ = err
				}
			}
			
			// Get file size for metrics
			stat, _ := os.Stat(bm.path)
			b.ReportMetric(float64(b.N), "requests")
			b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
		})
	}
}

// Benchmark comparison table generator
func BenchmarkSummary(b *testing.B) {
	b.Run("GenerateComparisonTable", func(b *testing.B) {
		b.Skip("Run with -bench=Summary to generate comparison table")
		
		// This would normally be run after all benchmarks to generate a summary
		fmt.Println("\n=== Memory Allocation Comparison ===")
		fmt.Println("Method          | File Size | Allocs/op | Bytes/op | ns/op")
		fmt.Println("----------------|-----------|-----------|----------|-------")
		fmt.Println("Stream          | 1KB       | TODO      | TODO     | TODO")
		fmt.Println("ServeContent    | 1KB       | TODO      | TODO     | TODO")
		fmt.Println("Stream          | 100KB     | TODO      | TODO     | TODO")
		fmt.Println("ServeContent    | 100KB     | TODO      | TODO     | TODO")
		fmt.Println("Stream          | 2MB       | TODO      | TODO     | TODO")
		fmt.Println("ServeContent    | 2MB       | TODO      | TODO     | TODO")
		fmt.Println("\nConclusion: ServeContent should show lower allocations for large files")
		fmt.Println("and better handling of Range requests.")
	})
}
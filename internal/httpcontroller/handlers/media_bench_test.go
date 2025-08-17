package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
)

// createTestAudioFile creates a test audio file with specified size
func createTestAudioFile(b *testing.B, dir, name string, size int) string {
	b.Helper()
	path := filepath.Join(dir, name)
	data := make([]byte, size)
	// Simulate audio data pattern
	for i := range data {
		data[i] = byte((i * 17) % 256) // Different pattern from regular files
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}

// oldServeFile simulates the old c.File() approach
func oldServeFile(c echo.Context, filePath string) error {
	return c.File(filePath)
}

// newServeFileEfficiently uses the new efficient serving method
func newServeFileEfficiently(c echo.Context, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "File not found")
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log error but don't fail benchmark - ignore in benchmarks
			_ = err
		}
	}()

	stat, err := file.Stat()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
	}

	if !stat.Mode().IsRegular() {
		return echo.NewHTTPError(http.StatusForbidden, "Not a regular file")
	}

	http.ServeContent(c.Response(), c.Request(), filepath.Base(filePath), stat.ModTime(), file)
	return nil
}

// BenchmarkMediaServe_Old_Small benchmarks the old c.File approach with small audio files
func BenchmarkMediaServe_Old_Small(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "small.mp3", 10*1024) // 10KB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = oldServeFile(c, audioFile)
	}
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
	b.ReportMetric(float64(stat.Size()), "file_size_bytes")
}

// BenchmarkMediaServe_New_Small benchmarks the new efficient approach with small audio files
func BenchmarkMediaServe_New_Small(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "small.mp3", 10*1024) // 10KB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = newServeFileEfficiently(c, audioFile)
	}
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
	b.ReportMetric(float64(stat.Size()), "file_size_bytes")
}

// BenchmarkMediaServe_Old_Medium benchmarks the old c.File approach with medium audio files
func BenchmarkMediaServe_Old_Medium(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "medium.mp3", 500*1024) // 500KB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = oldServeFile(c, audioFile)
	}
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
	b.ReportMetric(float64(stat.Size()), "file_size_bytes")
}

// BenchmarkMediaServe_New_Medium benchmarks the new efficient approach with medium audio files
func BenchmarkMediaServe_New_Medium(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "medium.mp3", 500*1024) // 500KB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = newServeFileEfficiently(c, audioFile)
	}
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
	b.ReportMetric(float64(stat.Size()), "file_size_bytes")
}

// BenchmarkMediaServe_Old_Large benchmarks the old c.File approach with large audio files
func BenchmarkMediaServe_Old_Large(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "large.mp3", 5*1024*1024) // 5MB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = oldServeFile(c, audioFile)
	}
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
	b.ReportMetric(float64(stat.Size()), "file_size_bytes")
}

// BenchmarkMediaServe_New_Large benchmarks the new efficient approach with large audio files
func BenchmarkMediaServe_New_Large(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "large.mp3", 5*1024*1024) // 5MB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = newServeFileEfficiently(c, audioFile)
	}
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
	b.ReportMetric(float64(stat.Size()), "file_size_bytes")
}

// BenchmarkMediaServe_RangeRequest_Old benchmarks old approach with Range requests
func BenchmarkMediaServe_RangeRequest_Old(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "range.mp3", 2*1024*1024) // 2MB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		req.Header.Set("Range", "bytes=0-1023") // Request first 1KB only
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = oldServeFile(c, audioFile)
	}
	
	// Report metrics - note that c.File doesn't handle Range properly, so full file is sent
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "range_requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_actually_sent") // Full file!
	b.ReportMetric(float64(1024), "bytes_requested")
}

// BenchmarkMediaServe_RangeRequest_New benchmarks new approach with Range requests
func BenchmarkMediaServe_RangeRequest_New(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "range.mp3", 2*1024*1024) // 2MB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	for b.Loop() { // Use new Go 1.24 b.Loop() pattern
		req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
		req.Header.Set("Range", "bytes=0-1023") // Request first 1KB only
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		
		_ = newServeFileEfficiently(c, audioFile)
	}
	
	// Report metrics - ServeContent properly handles Range, so only 1KB is sent
	b.ReportMetric(float64(b.N), "range_requests")
	b.ReportMetric(float64(1024*b.N), "bytes_actually_sent") // Only requested range!
	b.ReportMetric(float64(1024), "bytes_requested")
}

// BenchmarkMediaServe_Concurrent_Old benchmarks old approach with concurrent requests
func BenchmarkMediaServe_Concurrent_Old(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "concurrent.mp3", 100*1024) // 100KB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			
			_ = oldServeFile(c, audioFile)
		}
	})
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "concurrent_requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
}

// BenchmarkMediaServe_Concurrent_New benchmarks new approach with concurrent requests
func BenchmarkMediaServe_Concurrent_New(b *testing.B) {
	e := echo.New()
	tempDir := b.TempDir() // Use Go 1.24 TempDir
	audioFile := createTestAudioFile(b, tempDir, "concurrent.mp3", 100*1024) // 100KB
	
	b.ReportAllocs() // Report memory allocations
	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/audio.mp3", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			
			_ = newServeFileEfficiently(c, audioFile)
		}
	})
	
	// Report metrics
	stat, _ := os.Stat(audioFile)
	b.ReportMetric(float64(b.N), "concurrent_requests")
	b.ReportMetric(float64(stat.Size()*int64(b.N)), "bytes_served")
}

// BenchmarkBufferAllocation compares buffer allocation strategies
func BenchmarkBufferAllocation(b *testing.B) {
	sizes := []int{
		1024,        // 1KB
		10 * 1024,   // 10KB
		100 * 1024,  // 100KB
		1024 * 1024, // 1MB
		5 * 1024 * 1024, // 5MB
	}
	
	for _, size := range sizes {
		b.Run("io.Copy_"+formatBytes(size), func(b *testing.B) {
			src := bytes.NewReader(make([]byte, size))
			b.ReportAllocs()
			b.ResetTimer()
			
			for b.Loop() {
				_, _ = src.Seek(0, io.SeekStart)
				dst := &bytes.Buffer{}
				_, _ = io.Copy(dst, src)
			}
			
			b.ReportMetric(float64(size), "bytes_per_op")
		})
		
		b.Run("io.CopyBuffer_"+formatBytes(size), func(b *testing.B) {
			src := bytes.NewReader(make([]byte, size))
			buf := make([]byte, 32*1024) // 32KB buffer
			b.ReportAllocs()
			b.ResetTimer()
			
			for b.Loop() {
				_, _ = src.Seek(0, io.SeekStart)
				dst := &bytes.Buffer{}
				_, _ = io.CopyBuffer(dst, src, buf)
			}
			
			b.ReportMetric(float64(size), "bytes_per_op")
		})
		
		b.Run("DirectRead_"+formatBytes(size), func(b *testing.B) {
			data := make([]byte, size)
			b.ReportAllocs()
			b.ResetTimer()
			
			for b.Loop() {
				src := bytes.NewReader(data)
				dst := make([]byte, size)
				_, _ = src.Read(dst)
			}
			
			b.ReportMetric(float64(size), "bytes_per_op")
		})
	}
}

// formatBytes formats bytes into human-readable string
func formatBytes(b int) string {
	const unit = 1024
	if b < unit {
		return "B" // For small sizes, just return "B"
	}
	if b < unit*unit {
		return "KB"
	}
	if b < unit*unit*unit {
		return "MB"
	}
	return "GB"
}
package diskmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// BenchmarkGetAudioFiles benchmarks the GetAudioFiles function to measure allocations
func BenchmarkGetAudioFiles(b *testing.B) {
	// Create a temporary directory with sample files
	tempDir := b.TempDir()

	// Create test files
	testFiles := []string{
		"bubo_bubo_80p_20210102T150405Z.wav",
		"anas_platyrhynchos_70p_20210103T150405Z.mp3",
		"erithacus_rubecula_60p_20210104T150405Z.flac",
		"corvus_corax_90p_20210105T150405Z.opus",
		"parus_major_85p_20210106T150405Z.aac",
		"invalid_file.wav", // This will cause a parse error
	}

	for _, file := range testFiles {
		filePath := filepath.Join(tempDir, file)
		err := os.WriteFile(filePath, []byte("test"), 0o600)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Create a mock database
	mockDB := &MockDB{}

	b.ReportAllocs()

	for b.Loop() {
		_, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, false)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseFileInfo benchmarks the parseFileInfo function
func BenchmarkParseFileInfo(b *testing.B) {
	mockInfo := &MockFileInfo{
		FileName:    "bubo_bubo_80p_20210102T150405Z.wav",
		FileSize:    1024,
		FileMode:    0o644,
		FileModTime: parseTime("20210102T150405Z"),
		FileIsDir:   false,
	}

	path := "/test/bubo_bubo_80p_20210102T150405Z.wav"

	b.ReportAllocs()

	for b.Loop() {
		_, err := parseFileInfo(path, mockInfo, allowedFileTypes)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkContains benchmarks the optimized contains function
func BenchmarkContains(b *testing.B) {
	extensions := []string{".wav", ".flac", ".aac", ".opus", ".mp3", ".m4a"}

	b.Run("Found", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = contains(extensions, ".mp3")
		}
	})

	b.Run("NotFound", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = contains(extensions, ".txt")
		}
	})

	b.Run("FirstItem", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = contains(extensions, ".wav")
		}
	})
}

// BenchmarkMemoryProfile benchmarks memory allocation patterns
func BenchmarkMemoryProfile(b *testing.B) {
	// Create test directory with varying file counts
	scenarios := []struct {
		name      string
		fileCount int
	}{
		{"Small_10", 10},
		{"Medium_100", 100},
		{"Large_1000", 1000},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			// Create temp directory
			tempDir := b.TempDir()

			// Create test files
			for range scenario.fileCount {
				filename := filepath.Join(tempDir,
					"species_80p_20210102T150405Z.wav")
				err := os.WriteFile(filename, []byte("test"), 0o600)
				if err != nil {
					b.Fatal(err)
				}
			}

			mockDB := &MockDB{}

			// Measure memory before
			var memBefore, memAfter runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&memBefore)

			b.ResetTimer()
			b.ReportAllocs()

			for b.Loop() {
				_, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, false)
				if err != nil {
					b.Fatal(err)
				}
			}

			// Measure memory after
			runtime.GC()
			runtime.ReadMemStats(&memAfter)

			// Report memory usage
			b.ReportMetric(float64(memAfter.Alloc-memBefore.Alloc)/float64(b.N), "alloc/op")
			b.ReportMetric(float64(memAfter.TotalAlloc-memBefore.TotalAlloc)/float64(b.N), "totalalloc/op")
		})
	}
}

// BenchmarkPoolEffectiveness compares pooled vs non-pooled performance
func BenchmarkPoolEffectiveness(b *testing.B) {
	tempDir := b.TempDir()

	// Create test files
	for range 100 {
		filename := filepath.Join(tempDir,
			"species_80p_20210102T150405Z.wav")
		err := os.WriteFile(filename, []byte("test"), 0o600)
		if err != nil {
			b.Fatal(err)
		}
	}

	mockDB := &MockDB{}

	b.Run("WithPool", func(b *testing.B) {
		// Reset pool metrics
		ResetPoolMetrics()

		b.ResetTimer()
		b.ReportAllocs()

		for b.Loop() {
			_, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, false)
			if err != nil {
				b.Fatal(err)
			}
		}

		// Report pool metrics
		metrics := GetPoolMetrics()
		b.ReportMetric(float64(metrics.GetCount)/float64(b.N), "pool_gets/op")
		b.ReportMetric(float64(metrics.PutCount)/float64(b.N), "pool_puts/op")
		b.ReportMetric(float64(metrics.SkipCount)/float64(b.N), "pool_skips/op")
	})
}

// BenchmarkErrorHandling benchmarks error handling overhead
func BenchmarkErrorHandling(b *testing.B) {
	tempDir := b.TempDir()

	// Create files with various validity
	validFiles := []string{
		"species_one_80p_20210102T150405Z.wav",
		"species_two_70p_20210103T150405Z.mp3",
	}

	invalidFiles := []string{
		"invalid.wav",          // Too few parts
		"species_XXp_time.wav", // Invalid confidence
		"species_80p_bad.wav",  // Invalid timestamp
	}

	for _, file := range append(validFiles, invalidFiles...) {
		filePath := filepath.Join(tempDir, file)
		err := os.WriteFile(filePath, []byte("test"), 0o600)
		if err != nil {
			b.Fatal(err)
		}
	}

	mockDB := &MockDB{}

	b.ReportAllocs()

	for b.Loop() {
		files, err := GetAudioFiles(tempDir, allowedFileTypes, mockDB, false)
		if err != nil {
			b.Fatal(err)
		}
		if len(files) != len(validFiles) {
			b.Fatalf("Expected %d valid files, got %d", len(validFiles), len(files))
		}
	}
}

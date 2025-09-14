package diskmanager

import (
	"os"
	"path/filepath"
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
		err := os.WriteFile(filePath, []byte("test"), 0o644)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Create a mock database
	mockDB := &MockDB{}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
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

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := parseFileInfo(path, mockInfo)
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
		for i := 0; i < b.N; i++ {
			_ = contains(extensions, ".mp3")
		}
	})

	b.Run("NotFound", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = contains(extensions, ".txt")
		}
	})

	b.Run("FirstItem", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = contains(extensions, ".wav")
		}
	})
}

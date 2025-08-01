package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkRepeatedStatOperationsWithoutCache simulates the real-world scenario
// where the same files are checked multiple times (like in spectrogram generation)
func BenchmarkRepeatedStatOperationsWithoutCache(b *testing.B) {
	// Create a temporary directory with actual files
	tempDir, err := os.MkdirTemp("", "securefs_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create actual test files
	testFiles := []string{
		"audio1.mp3",
		"audio2.wav", 
		"audio3.flac",
		"nested/audio4.mp3",
		"deeply/nested/path/audio5.wav",
	}

	for _, file := range testFiles {
		fullPath := filepath.Join(tempDir, file)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("test content"), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	// Create SecureFS without cache
	sfs := &SecureFS{
		baseDir: tempDir,
		cache:   nil, // No cache
	}

	// Create a fake root to avoid the os.Root dependency in benchmarks
	sfs.root = nil

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, file := range testFiles {
			// Simulate the expensive operations that happen in real usage
			
			// 1. Multiple absolute path resolutions (expensive)
			_, _ = filepath.Abs(filepath.Join(tempDir, file))
			_, _ = filepath.Abs(tempDir)
			 
			// 2. Symlink resolution (expensive)
			_, _ = filepath.EvalSymlinks(filepath.Join(tempDir, file))
			_, _ = filepath.EvalSymlinks(tempDir)
			
			// 3. File stat operations (expensive)
			_, _ = os.Stat(filepath.Join(tempDir, file))
			
			// 4. Path validation (less expensive but repeated)
			cleanPath := filepath.Clean(file)
			_ = filepath.IsAbs(cleanPath)
		}
	}
}

// BenchmarkRepeatedStatOperationsWithCache simulates the same scenario with caching
func BenchmarkRepeatedStatOperationsWithCache(b *testing.B) {
	// Create a temporary directory with actual files
	tempDir, err := os.MkdirTemp("", "securefs_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create actual test files
	testFiles := []string{
		"audio1.mp3",
		"audio2.wav", 
		"audio3.flac",
		"nested/audio4.mp3",
		"deeply/nested/path/audio5.wav",
	}

	for _, file := range testFiles {
		fullPath := filepath.Join(tempDir, file)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("test content"), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	// Create SecureFS with cache
	cache := NewPathCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, file := range testFiles {
			// Simulate the same expensive operations but with caching
			
			// 1. Cached absolute path resolutions
			_, _ = cache.GetAbsPath(filepath.Join(tempDir, file), filepath.Abs)
			_, _ = cache.GetAbsPath(tempDir, filepath.Abs)
			 
			// 2. Cached symlink resolution
			_, _ = cache.GetSymlinkResolution(filepath.Join(tempDir, file), filepath.EvalSymlinks)
			_, _ = cache.GetSymlinkResolution(tempDir, filepath.EvalSymlinks)
			
			// 3. Cached file stat operations
			_, _ = cache.GetStat(filepath.Join(tempDir, file), os.Stat)
			
			// 4. Cached path validation
			_, _ = cache.GetValidatePath(file, func(path string) (string, error) {
				cleanPath := filepath.Clean(path)
				if filepath.IsAbs(cleanPath) {
					return "", nil
				}
				return cleanPath, nil
			})
		}
	}
}

// BenchmarkManyUniqueOperationsWithoutCache tests performance with many unique operations
func BenchmarkManyUniqueOperationsWithoutCache(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "securefs_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create unique paths each time (cache won't help)
		uniquePath := filepath.Join(tempDir, "unique", string(rune(i)), "file.mp3")
		_, _ = filepath.Abs(uniquePath)
		_, _ = filepath.EvalSymlinks(filepath.Dir(uniquePath))
	}
}

// BenchmarkManyUniqueOperationsWithCache tests the same with cache (should perform similarly)
func BenchmarkManyUniqueOperationsWithCache(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "securefs_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	cache := NewPathCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create unique paths each time (cache won't help much)
		uniquePath := filepath.Join(tempDir, "unique", string(rune(i)), "file.mp3")
		_, _ = cache.GetAbsPath(uniquePath, filepath.Abs)
		_, _ = cache.GetSymlinkResolution(filepath.Dir(uniquePath), filepath.EvalSymlinks)
	}
}

// BenchmarkCacheOverhead measures just the cache overhead
func BenchmarkCacheOverhead(b *testing.B) {
	cache := NewPathCache()
	testPath := "test/path/file.txt"
	
	// Pre-populate cache to measure lookup overhead
	_, _ = cache.GetValidatePath(testPath, func(path string) (string, error) {
		return filepath.Clean(path), nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This should hit cache every time
		_, _ = cache.GetValidatePath(testPath, func(path string) (string, error) {
			b.Fatal("Should not be called - should use cache")
			return "", nil
		})
	}
}

// BenchmarkRealWorldSpectrogramScenario simulates the actual spectrogram generation scenario
func BenchmarkRealWorldSpectrogramScenario(b *testing.B) {
	// Create a temporary directory structure similar to real usage
	tempDir, err := os.MkdirTemp("", "securefs_bench")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create actual audio files
	audioFiles := []string{
		"clips/2025/08/muscicapa_striata_33p_20250801T085157Z.mp3",
		"clips/2025/08/muscicapa_striata_38p_20250801T085349Z.mp3", 
		"clips/2025/08/muscicapa_striata_8p_20250801T084745Z.mp3",
		"clips/2025/08/muscicapa_striata_60p_20250801T085252Z.mp3",
		"clips/2025/08/muscicapa_striata_42p_20250801T085230Z.mp3",
	}

	for _, file := range audioFiles {
		fullPath := filepath.Join(tempDir, file)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("fake audio content"), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("WithoutCache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, audioFile := range audioFiles {
				// Simulate what happens in the v2 API for each spectrogram request
				audioPath := filepath.Join(tempDir, audioFile)
				
				// 1. Path validation operations (ValidateRelativePath equivalent)
				_, _ = filepath.Abs(audioPath)
				cleanPath := filepath.Clean(audioFile)
				_ = filepath.IsAbs(cleanPath)
				
				// 2. Path within base check (IsPathWithinBase equivalent) 
				_, _ = filepath.Abs(tempDir)
				_, _ = filepath.EvalSymlinks(audioPath)
				_, _ = filepath.EvalSymlinks(tempDir)
				
				// 3. Stat operations (StatRel equivalent)
				_, _ = os.Stat(audioPath)
				
				// 4. Generate spectrogram path and check existence
				spectrogramPath := audioPath[:len(audioPath)-4] + "_400px.png"
				_, _ = os.Stat(spectrogramPath) // This will fail but simulates the check
			}
		}
	})

	b.Run("WithCache", func(b *testing.B) {
		cache := NewPathCache()
		
		for i := 0; i < b.N; i++ {
			for _, audioFile := range audioFiles {
				// Simulate the same operations but with caching
				audioPath := filepath.Join(tempDir, audioFile)
				
				// 1. Cached path validation
				_, _ = cache.GetAbsPath(audioPath, filepath.Abs)
				_, _ = cache.GetValidatePath(audioFile, func(path string) (string, error) {
					cleanPath := filepath.Clean(path)
					if filepath.IsAbs(cleanPath) {
						return "", nil 
					}
					return cleanPath, nil
				})
				
				// 2. Cached path within base check
				_, _ = cache.GetAbsPath(tempDir, filepath.Abs)
				_, _ = cache.GetSymlinkResolution(audioPath, filepath.EvalSymlinks)
				_, _ = cache.GetSymlinkResolution(tempDir, filepath.EvalSymlinks)
				
				// 3. Cached stat operations
				_, _ = cache.GetStat(audioPath, os.Stat)
				
				// 4. Cached spectrogram existence check
				spectrogramPath := audioPath[:len(audioPath)-4] + "_400px.png"
				_, _ = cache.GetStat(spectrogramPath, os.Stat)
			}
		}
	})
}

// TestCacheHitRate tests that the cache actually provides hits for repeated operations
func TestCacheHitRate(t *testing.T) {
	cache := NewPathCache()
	testPath := "test/file.txt"
	
	callCount := 0
	computeFunc := func(path string) (string, error) {
		callCount++
		return filepath.Clean(path), nil
	}
	
	// First call should invoke the compute function
	_, _ = cache.GetValidatePath(testPath, computeFunc)
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
	
	// Subsequent calls should use cache
	for i := 0; i < 10; i++ {
		_, _ = cache.GetValidatePath(testPath, computeFunc)
	}
	
	if callCount != 1 {
		t.Errorf("Expected still 1 call after cache hits, got %d", callCount)
	}
	
	// Test stats
	stats := cache.GetCacheStats()
	if stats.ValidateTotal != 1 {
		t.Errorf("Expected 1 cache entry, got %d", stats.ValidateTotal)
	}
}
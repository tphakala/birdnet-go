package detection

import (
	"context"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// BenchmarkSpeciesCache_Lookup benchmarks cache lookup performance.
// Target: <5ms per lookup (currently ~50ms without cache).
func BenchmarkSpeciesCache_Lookup(b *testing.B) {
	repo := newMockSpeciesRepo()
	cache := NewSpeciesCache(repo, time.Hour)

	// Pre-populate cache with test data
	species := &Species{
		ID:             1,
		SpeciesCode:    "amecro",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Prime the cache
	_, _ = cache.GetByScientificName(ctx, "Corvus brachyrhynchos")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetByScientificName(ctx, "Corvus brachyrhynchos")
	}
}

// BenchmarkSpeciesCache_LookupByID benchmarks ID-based lookups.
func BenchmarkSpeciesCache_LookupByID(b *testing.B) {
	repo := newMockSpeciesRepo()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             42,
		SpeciesCode:    "norcar",
		ScientificName: "Cardinalis cardinalis", //nolint:misspell // Latin species name
		CommonName:     "Northern Cardinal",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Prime the cache
	_, _ = cache.GetByID(ctx, 42)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetByID(ctx, 42)
	}
}

// BenchmarkSpeciesCache_LookupByEbirdCode benchmarks eBird code lookups.
func BenchmarkSpeciesCache_LookupByEbirdCode(b *testing.B) {
	repo := newMockSpeciesRepo()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             99,
		SpeciesCode:    "rebwoo",
		ScientificName: "Melanerpes carolinus",
		CommonName:     "Red-bellied Woodpecker",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Prime the cache
	_, _ = cache.GetByEbirdCode(ctx, "rebwoo")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetByEbirdCode(ctx, "rebwoo")
	}
}

// BenchmarkSpeciesCache_Miss benchmarks cache miss performance.
func BenchmarkSpeciesCache_Miss(b *testing.B) {
	repo := newMockSpeciesRepo()
	cache := NewSpeciesCache(repo, time.Hour)

	// Add many species to simulate realistic scenario
	for i := 1; i <= 100; i++ {
		repo.AddSpecies(&Species{
			ID:             uint(i),
			ScientificName: "Test Species",
			CommonName:     "Test Bird",
		})
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Always miss (species not in mock repo)
		_, _ = cache.GetByScientificName(ctx, "Nonexistent Species")
	}
}

// BenchmarkSpeciesCache_ConcurrentReads benchmarks concurrent read performance.
func BenchmarkSpeciesCache_ConcurrentReads(b *testing.B) {
	repo := newMockSpeciesRepo()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             1,
		SpeciesCode:    "amecro",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Prime the cache
	_, _ = cache.GetByScientificName(ctx, "Corvus brachyrhynchos")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.GetByScientificName(ctx, "Corvus brachyrhynchos")
		}
	})
}

// BenchmarkMapper_ToDatastore benchmarks domain to database conversion.
func BenchmarkMapper_ToDatastore(b *testing.B) {
	mapper := NewMapper(nil)

	now := time.Now()
	detection := &Detection{
		ID:             123,
		SourceNode:     "test-node",
		Date:           "2025-01-15",
		Time:           "14:30:00",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		SpeciesCode:    "amecro",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
		Confidence:     0.95,
		Threshold:      0.1,
		Sensitivity:    1.2,
		Latitude:       47.6062,
		Longitude:      -122.3321,
		ClipName:       "/clips/test.wav",
		ProcessingTime: 50 * time.Millisecond,
		Source: AudioSource{
			ID:          "rtsp_123",
			SafeString:  "rtsp://camera1",
			DisplayName: "Front Camera",
		},
		Occurrence: 0.85,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mapper.ToDatastore(detection)
	}
}

// BenchmarkMapper_FromDatastore benchmarks database to domain conversion.
func BenchmarkMapper_FromDatastore(b *testing.B) {
	mapper := NewMapper(nil)

	now := time.Now()
	note := &datastore.Note{
		ID:             456,
		SourceNode:     "test-node-2",
		Date:           "2025-01-16",
		Time:           "15:45:30",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		SpeciesCode:    "norcar",
		ScientificName: "Cardinalis cardinalis", //nolint:misspell // Latin species name
		CommonName:     "Northern Cardinal",
		Confidence:     0.88,
		Threshold:      0.15,
		Sensitivity:    1.0,
		Latitude:       40.7128,
		Longitude:      -74.0060,
		ClipName:       "/clips/cardinal.wav",
		ProcessingTime: 75 * time.Millisecond,
		Occurrence:     0.92,
		Verified:       "correct",
		Locked:         true,
	}

	source := AudioSource{
		ID:          "mic_456",
		SafeString:  "USB Microphone",
		DisplayName: "Back Yard Mic",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mapper.FromDatastore(note, source)
	}
}

// BenchmarkMapper_RoundTrip benchmarks full conversion cycle.
func BenchmarkMapper_RoundTrip(b *testing.B) {
	mapper := NewMapper(nil)

	now := time.Now()
	original := &Detection{
		SourceNode:     "node-1",
		Date:           "2025-01-15",
		Time:           "10:30:00",
		BeginTime:      now,
		EndTime:        now.Add(3 * time.Second),
		SpeciesCode:    "rebwoo",
		ScientificName: "Melanerpes carolinus",
		CommonName:     "Red-bellied Woodpecker",
		Confidence:     0.92,
		Threshold:      0.1,
		Sensitivity:    1.5,
		Latitude:       35.0,
		Longitude:      -85.0,
		ClipName:       "/clips/woodpecker.wav",
		ProcessingTime: 60 * time.Millisecond,
		Source: AudioSource{
			ID:          "audio_1",
			SafeString:  "input.wav",
			DisplayName: "Test Input",
		},
		Occurrence: 0.75,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		note := mapper.ToDatastore(original)
		_ = mapper.FromDatastore(&note, original.Source)
	}
}

// BenchmarkNewDetection benchmarks detection construction with validation.
func BenchmarkNewDetection(b *testing.B) {
	now := time.Now()
	source := AudioSource{
		ID:          "test_source",
		SafeString:  "safe",
		DisplayName: "Test Source",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewDetection(
			"test-node",
			"2025-01-15",
			"14:30:00",
			now,
			now.Add(3*time.Second),
			source,
			"amecro",
			"Corvus brachyrhynchos",
			"American Crow",
			0.95,
			0.1,
			1.2,
			47.6062,
			-122.3321,
			"/clips/test.wav",
			50*time.Millisecond,
			0.85,
		)
	}
}

// Package testutil provides test helpers for migration integration tests.
// This includes data builders, legacy database seeding, and assertion helpers.
//
// # Overview
//
// This package provides comprehensive testing infrastructure for the database
// migration from legacy (denormalized) schema to V2 (normalized) schema. It
// enables writing integration tests that verify the migration process end-to-end.
//
// # Key Components
//
// ## Builders (builders.go)
//
// Fluent API for creating test data with sensible defaults:
//
//	note := NewDetectionBuilder().
//		WithSpecies("amecro", "Corvus brachyrhynchos", "American Crow").
//		WithConfidence(0.95).
//		Build()
//
// Available builders:
//   - DetectionBuilder: Creates datastore.Note with species, confidence, timestamps
//   - ResultsBuilder: Creates secondary predictions (datastore.Results)
//   - ReviewBuilder: Creates note reviews (datastore.NoteReview)
//   - CommentBuilder: Creates note comments (datastore.NoteComment)
//   - LockBuilder: Creates note locks (datastore.NoteLock)
//   - DailyEventsBuilder: Creates daily weather events
//   - HourlyWeatherBuilder: Creates hourly weather data
//   - DynamicThresholdBuilder: Creates dynamic threshold records
//   - ImageCacheBuilder: Creates image cache entries
//   - NotificationHistoryBuilder: Creates notification history records
//
// ## Data Generators (builders.go)
//
// Helper functions for generating bulk test data:
//
//	notes := GenerateDetections(100)  // 100 varied detections
//	daily, hourly := GenerateWeatherData(7)  // 7 days of weather
//	relatedData := GenerateRelatedData(notes[:10], config)  // Related records
//
// ## Legacy Seeder (legacy_seeder.go)
//
// Direct SQL-based seeding for legacy database tables. Uses batch inserts
// and transactions for performance:
//
//	seeder := NewLegacySeeder(db)
//	seeder.SeedDetections(notes)  // Batch insert with 500 records per batch
//	seeder.SeedWeather(dailyEvents, hourlyWeather)
//	seeder.SeedAll(seedData)  // Seed all tables in correct order
//
// ## Test Context (setup.go)
//
// Complete test environment setup and teardown:
//
//	func TestMigration(t *testing.T) {
//		ctx := SetupIntegrationTest(t)  // Cleanup is automatic via t.Cleanup()
//
//		// Seed legacy data
//		notes := GenerateDetections(100)
//		ctx.Seeder.SeedDetections(notes)
//
//		// Run migration
//		ctx.StartMigration(t, len(notes))
//		ctx.WaitForCompletion(t, 60*time.Second)
//
//		// Verify
//		assert.Equal(t, int64(100), ctx.GetV2DetectionCount(t))
//	}
//
// ## Assertion Helpers (assertions.go)
//
// Field-by-field verification helpers using testify:
//
//	AssertDetectionMatches(t, note, detection)
//	AssertReviewMatches(t, noteReview, detectionReview)
//	AssertAllDataMigrated(t, expected, actual)
//
// # Running Integration Tests
//
// Integration tests require the "integration" build tag:
//
//	go test -tags=integration -v ./internal/datastore/v2/migration/...
//
// With race detection (recommended):
//
//	go test -tags=integration -race -v ./internal/datastore/v2/migration/...
//
// Skip large dataset tests in quick runs:
//
//	go test -tags=integration -short -v ./internal/datastore/v2/migration/...
//
//nolint:dupl,gosec,nilnil // Test utilities intentionally have similar patterns, use int->uint for test IDs, and return nil,nil in stubs
package testutil

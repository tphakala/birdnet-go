// Package testutil provides test helpers for migration integration tests.
// This includes data builders, legacy database seeding, and assertion helpers.
//
// Key components:
//   - Builders: Fluent API for creating test data with sensible defaults
//   - LegacySeeder: Direct SQL-based seeding for legacy database tables
//   - Assertions: Field-by-field verification helpers
//   - TestContext: Complete test environment setup and teardown
//
//nolint:dupl,gosec // Test utilities intentionally have similar patterns and use int->uint for test IDs
package testutil

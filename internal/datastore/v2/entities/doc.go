// Package entities defines the GORM entity models for the v2 normalized database schema.
//
// The v2 schema replaces the denormalized legacy schema with a fully normalized structure
// that supports multiple AI models (BirdNET, Google Perch, bat models) and provides
// significant storage savings through proper normalization.
//
// # Core Entities
//
//   - Label: Classification labels (species and non-species like noise, wind)
//   - AIModel: AI model registry (BirdNET, Perch, BatNET)
//   - ModelLabel: Maps model raw output to normalized labels
//   - AudioSource: Normalized audio input sources
//
// # Detection Entities
//
//   - Detection: Main detection table with foreign keys (replaces 'notes')
//   - DetectionPrediction: Additional predictions per detection (replaces 'results')
//   - DetectionReview: Verification status
//   - DetectionComment: User comments
//   - DetectionLock: Lock status
//
// # Migration
//
//   - MigrationState: Tracks migration progress (singleton table)
//
// See docs/plans/2026-01-22-database-normalization-rfc.md for full schema design.
package entities

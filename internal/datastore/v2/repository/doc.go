// Package repository provides V2 repository interfaces and implementations
// for the normalized database schema supporting multiple AI models.
//
// # Table Naming
//
// These repositories support both SQLite and MySQL backends:
//   - SQLite: Uses standard table names (labels, detections, etc.)
//   - MySQL: Uses v2_ prefixed table names (v2_labels, v2_detections, etc.)
//
// The useV2Prefix constructor parameter controls this behavior.
//
// # IMPORTANT: Preload/Association Hazard
//
// When useV2Prefix is true (MySQL mode), DO NOT use GORM's automatic
// Preload(), Joins(), or Association features without explicit table
// configuration. The entity structs have hardcoded TableName() methods
// returning standard names ("labels"), which will cause GORM to query
// the wrong tables.
//
// WRONG (will query "labels" instead of "v2_labels"):
//
//	db.Preload("Label").Find(&detections)
//
// CORRECT (use explicit table names):
//
//	db.Table("v2_detections").
//	    Joins("JOIN v2_labels ON v2_labels.id = v2_detections.label_id").
//	    Find(&detections)
//
// Or use the repository methods which handle this correctly.
//
// # Error Handling
//
// All repositories return sentinel errors (ErrLabelNotFound, etc.)
// instead of leaking GORM errors. This enables future storage backend
// changes without breaking callers.
//
// # Thread Safety
//
// All repository methods are safe for concurrent use. GetOrCreate methods
// handle race conditions where multiple goroutines may attempt to create
// the same record simultaneously.
//
// # Required Schema Constraints
//
// The GetOrCreate methods rely on database unique constraints for race
// condition safety. If these constraints are missing, duplicate records
// could be created. Required unique constraints:
//
//   - labels: UNIQUE(scientific_name) for species, UNIQUE(scientific_name, label_type) for non-species
//   - ai_models: UNIQUE(name, version)
//   - audio_sources: UNIQUE(source_uri, node_name)
//   - model_labels: UNIQUE(model_id, raw_label)
//
// The v2 schema migration should create these constraints automatically.
package repository

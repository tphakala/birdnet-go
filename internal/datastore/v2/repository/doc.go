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
// # Table Prefix Behavior
//
// V2 entity structs no longer define TableName() methods. GORM's
// NamingStrategy.TablePrefix (set via UseV2Prefix in MySQLConfig)
// controls whether tables use the "v2_" prefix. This means standard
// GORM operations (Preload, Joins, Find, etc.) automatically use
// the correct prefixed table names when the db instance has the
// prefix configured.
//
// Repository implementations use explicit db.Table() calls with
// constants from tables.go for clarity and consistency.
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

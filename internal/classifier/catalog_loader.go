// catalog_loader.go loads and persists the user-editable model catalog.
//
// The embedded catalog (EmbeddedCatalog, see model_catalog.go) remains the
// built-in default that ships inside the binary; no extra files are shipped.
// On first run the catalog is seeded to <config-dir>/model-catalog.json so the
// user can hand-edit it. On later runs the file is loaded and used.
//
// Staleness handling: the on-disk file records a checksum of the embedded
// catalog it was generated from. When a new binary release ships a changed
// embedded catalog, a file that the user has NOT edited is automatically
// refreshed to the new catalog (so the manifest never goes stale on upgrade).
// A file the user HAS edited is preserved, with a warning telling the user how
// to regenerate it. Parse or validation errors are non-fatal: the loader logs a
// warning, falls back to the embedded catalog, and never overwrites the file.
package classifier

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// catalogFileName is the on-disk name of the user-editable model catalog,
	// co-located with config.yaml in the resolved config directory.
	catalogFileName = "model-catalog.json"

	// catalogSchemaVersion is the current on-disk schema version. It versions
	// the file format (the wrapper and field shape), independent of the catalog
	// content fingerprint stored in CatalogChecksum.
	catalogSchemaVersion = 1

	// catalogFileMode is the permission mode for the catalog file (world-readable
	// so it can be inspected; owner-writable for hand-editing).
	catalogFileMode = 0o644
	// catalogDirMode is the permission mode used when creating the config
	// directory as a fallback during seeding.
	catalogDirMode = 0o755
)

// catalogManifest is the on-disk wrapper for the model catalog. The 2-space
// indented JSON is meant to be hand-editable.
type catalogManifest struct {
	// SchemaVersion is the file-format version (see catalogSchemaVersion).
	SchemaVersion int `json:"schema_version"`
	// CatalogChecksum fingerprints the embedded catalog this file was generated
	// from. It is how an upgrade detects that a pristine file should be refreshed
	// and that an edited file diverges from its baseline. Hand-edited files may
	// omit it (empty), which is treated as "edited / unknown baseline".
	CatalogChecksum string `json:"catalog_checksum"`
	// Entries is the catalog itself.
	Entries []CatalogEntry `json:"entries"`
}

// LoadCatalog initializes the runtime model catalog from configDir. It seeds the
// file on first run, refreshes a pristine file when the embedded catalog changed
// (avoiding staleness on upgrade), preserves user edits, and always falls back to
// the embedded catalog on any error so startup never fails. The returned error is
// advisory (a seed/refresh write failure); the active catalog is always set.
func LoadCatalog(configDir string) error {
	log := GetLogger()
	path := filepath.Join(configDir, catalogFileName)

	embeddedChecksum, err := catalogChecksum(EmbeddedCatalog)
	if err != nil {
		// Should never happen (the embedded catalog always marshals); fall back.
		setActiveCatalog(EmbeddedCatalog)
		return errors.New(err).
			Component("classifier.catalog_loader").
			Category(errors.CategoryConfiguration).
			Context("operation", "checksum_embedded_catalog").
			Build()
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path derived from the resolved config dir, not user input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return seedCatalog(log, path, embeddedChecksum)
		}
		// Unexpected read error (permissions, etc.): use embedded, surface error.
		setActiveCatalog(EmbeddedCatalog)
		return errors.New(err).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "read_model_catalog").
			Context("path", path).
			Build()
	}

	var manifest catalogManifest
	if uerr := json.Unmarshal(data, &manifest); uerr != nil {
		log.Warn("model catalog is malformed; using built-in catalog (file left untouched)",
			logger.String("path", path),
			logger.Error(uerr))
		setActiveCatalog(EmbeddedCatalog)
		return nil
	}

	if manifest.SchemaVersion != catalogSchemaVersion {
		log.Warn("model catalog schema version differs from this build; loading best-effort",
			logger.String("path", path),
			logger.Int("file_schema_version", manifest.SchemaVersion),
			logger.Int("app_schema_version", catalogSchemaVersion))
	}

	if verr := validateCatalog(manifest.Entries); verr != nil {
		log.Warn("model catalog failed validation; using built-in catalog (file left untouched)",
			logger.String("path", path),
			logger.Error(verr))
		setActiveCatalog(EmbeddedCatalog)
		return nil
	}

	// The file's baseline matches the current binary: use it as-is. This honors
	// any edits the user layered on the current built-in catalog.
	if manifest.CatalogChecksum == embeddedChecksum {
		setActiveCatalog(manifest.Entries)
		log.Info("loaded model catalog",
			logger.String("path", path),
			logger.Int("entries", len(manifest.Entries)))
		return nil
	}

	// The binary shipped a different embedded catalog than this file's recorded
	// baseline. Decide whether the file is pristine (safe to refresh) or edited.
	fileChecksum, err := catalogChecksum(manifest.Entries)
	if err != nil {
		// Cannot fingerprint the file; treat as edited and keep it.
		setActiveCatalog(manifest.Entries)
		log.Warn("could not fingerprint model catalog; keeping on-disk entries",
			logger.String("path", path),
			logger.Error(err))
		return nil
	}

	pristine := fileChecksum == manifest.CatalogChecksum
	if pristine {
		// User never edited the file: refresh it to the new release so it does
		// not go stale (e.g. updated checksums, new models). Even if the on-disk
		// write fails, the in-memory catalog is the fresh embedded one.
		setActiveCatalog(EmbeddedCatalog)
		if werr := writeCatalogFileAtomic(path, EmbeddedCatalog, embeddedChecksum); werr != nil {
			return werr
		}
		log.Info("refreshed model catalog to match new release",
			logger.String("path", path),
			logger.Int("entries", len(EmbeddedCatalog)))
		return nil
	}

	// User edited the file and the built-in catalog also changed. Preserve the
	// user's edits, but warn that the built-in catalog moved on so the user can
	// regenerate if they want the new baseline.
	setActiveCatalog(manifest.Entries)
	log.Warn("built-in model catalog changed since this file was generated; keeping your edits. "+
		"Delete the file to regenerate it from the new built-in catalog.",
		logger.String("path", path),
		logger.Int("entries", len(manifest.Entries)))
	return nil
}

// seedCatalog writes the initial catalog file from the embedded catalog and sets
// the active catalog to the embedded catalog.
func seedCatalog(log logger.Logger, path, embeddedChecksum string) error {
	setActiveCatalog(EmbeddedCatalog)
	if err := writeCatalogFileAtomic(path, EmbeddedCatalog, embeddedChecksum); err != nil {
		return err
	}
	log.Info("seeded model catalog",
		logger.String("path", path),
		logger.Int("entries", len(EmbeddedCatalog)))
	return nil
}

// validateCatalog checks that a loaded catalog is well-formed: it has at least
// one entry, every entry has a unique non-empty id, and every file declares the
// fields needed to download it (remote_path, local_name, role).
func validateCatalog(entries []CatalogEntry) error {
	if len(entries) == 0 {
		return errors.Newf("model catalog has no entries").
			Component("classifier.catalog_loader").
			Category(errors.CategoryValidation).
			Build()
	}
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		entry := &entries[i]
		if entry.ID == "" {
			return errors.Newf("catalog entry %d has an empty id", i).
				Component("classifier.catalog_loader").
				Category(errors.CategoryValidation).
				Build()
		}
		if _, dup := seen[entry.ID]; dup {
			return errors.Newf("duplicate catalog entry id %q", entry.ID).
				Component("classifier.catalog_loader").
				Category(errors.CategoryValidation).
				Build()
		}
		seen[entry.ID] = struct{}{}
		if len(entry.Files) == 0 {
			return errors.Newf("catalog entry %q declares no files", entry.ID).
				Component("classifier.catalog_loader").
				Category(errors.CategoryValidation).
				Build()
		}
		for j := range entry.Files {
			f := &entry.Files[j]
			if f.RemotePath == "" || f.LocalName == "" || f.Role == "" {
				return errors.Newf("catalog entry %q file %d is missing remote_path, local_name, or role", entry.ID, j).
					Component("classifier.catalog_loader").
					Category(errors.CategoryValidation).
					Build()
			}
		}
	}
	return nil
}

// catalogChecksum returns the hex-encoded SHA-256 of the compact JSON encoding
// of entries. The compact encoding is deterministic (fixed struct field order,
// ordered slices, no maps), so a pristine file round-trips to the same checksum
// it was written with, and a changed embedded catalog produces a different one.
//
// The checksum is coupled to the Go struct layout and JSON tags, not just the
// semantic content. Reordering CatalogEntry/CatalogFile fields or renaming a tag
// shifts every checksum, so after such a refactor existing on-disk files look
// "changed" and are preserved with a warning (not auto-refreshed) until the user
// regenerates them. That degradation is benign (the file still loads), but a
// maintainer making such a change should bump catalogSchemaVersion to signal it.
func catalogChecksum(entries []CatalogEntry) (string, error) {
	data, err := json.Marshal(entries)
	if err != nil {
		return "", errors.New(err).
			Component("classifier.catalog_loader").
			Category(errors.CategoryConfiguration).
			Context("operation", "marshal_catalog_checksum").
			Build()
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// writeCatalogFileAtomic writes the catalog manifest to path atomically: it
// writes a temp file in the same directory, fsyncs it, then renames it into
// place. A partial write therefore never leaves a corrupt catalog file.
func writeCatalogFileAtomic(path string, entries []CatalogEntry, checksum string) error {
	manifest := catalogManifest{
		SchemaVersion:   catalogSchemaVersion,
		CatalogChecksum: checksum,
		Entries:         entries,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New(err).
			Component("classifier.catalog_loader").
			Category(errors.CategoryConfiguration).
			Context("operation", "marshal_model_catalog").
			Build()
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, catalogDirMode); mkErr != nil {
		return errors.New(mkErr).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "create_config_dir").
			Context("path", dir).
			Build()
	}

	tmp, err := os.CreateTemp(dir, catalogFileName+".*.tmp")
	if err != nil {
		return errors.New(err).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "create_temp_model_catalog").
			Context("dir", dir).
			Build()
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: removes the temp file on any failure path. After a
	// successful rename the temp file no longer exists and Remove is a no-op.
	defer func() { _ = os.Remove(tmpName) }()

	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		return errors.New(werr).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "write_model_catalog").
			Context("path", tmpName).
			Build()
	}
	// Set the final mode on the open descriptor (CreateTemp makes it 0600) rather
	// than os.Chmod(path) after Close: operating on the fd avoids a TOCTOU window
	// where the path could be swapped between close and chmod.
	if cerr := tmp.Chmod(catalogFileMode); cerr != nil {
		_ = tmp.Close()
		return errors.New(cerr).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "chmod_model_catalog").
			Context("path", tmpName).
			Build()
	}
	if serr := tmp.Sync(); serr != nil {
		_ = tmp.Close()
		return errors.New(serr).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "sync_model_catalog").
			Context("path", tmpName).
			Build()
	}
	if cerr := tmp.Close(); cerr != nil {
		return errors.New(cerr).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "close_model_catalog").
			Context("path", tmpName).
			Build()
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		return errors.New(rerr).
			Component("classifier.catalog_loader").
			Category(errors.CategoryFileIO).
			Context("operation", "rename_model_catalog").
			Context("path", path).
			Build()
	}
	return nil
}

package classifier

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// modelFileSet is the complete on-disk file set a secondary model family needs.
// The families differ in how many of the three are required: Perch v2 and
// BirdNET v3.0 need model+labels, Bat additionally needs a shared embedding
// extractor. Embeddings is empty (and not required) for the first two.
type modelFileSet struct {
	model      string
	labels     string
	embeddings string
}

// complete reports whether every required member of the set is populated.
// needEmbeddings is true only for the bat family.
func (s modelFileSet) complete(needEmbeddings bool) bool {
	if s.model == "" || s.labels == "" {
		return false
	}
	return !needEmbeddings || s.embeddings != ""
}

// allPresentOnDisk reports whether every required member exists on disk. A set
// that is not complete() is never present.
func (s modelFileSet) allPresentOnDisk(needEmbeddings bool) bool {
	if !s.complete(needEmbeddings) {
		return false
	}
	paths := []string{s.model, s.labels}
	if needEmbeddings {
		paths = append(paths, s.embeddings)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// diskPresence classifies the on-disk state of a required member set. It
// separates a member that is confirmed absent (fs.ErrNotExist), which marks the
// configured set stale and worth repairing, from a member that could not be
// stat'd for another reason (EACCES, EIO, a stale NFS handle, a half-initialised
// mount that reports EIO). The latter must not trigger a permanent config
// rewrite, because the file may well reappear.
//
// Note the boundary: a FULLY unmounted path reports ErrNotExist, not one of the
// above, so it classifies as presenceMissing (repairable), NOT indeterminate.
// The guard against that rewriting a user's own path is the gallery-layout
// requirement in isGalleryManagedPath, not this classification.
type diskPresence int

const (
	// presenceComplete means every non-empty required member exists on disk.
	presenceComplete diskPresence = iota
	// presenceMissing means at least one non-empty required member is confirmed
	// absent (fs.ErrNotExist) and no member was indeterminate.
	presenceMissing
	// presenceIndeterminate means at least one non-empty required member could
	// not be stat'd for a reason OTHER than absence (a permissions failure, an I/O
	// error, a half-initialised mount reporting EIO). It dominates presenceMissing
	// so a transient error is never mistaken for a stale path. A fully unmounted
	// path reports ErrNotExist and is therefore presenceMissing, not this.
	presenceIndeterminate
)

// nonEmptyMembersPresence classifies whether every NON-EMPTY required member of
// the set exists on disk. An empty member is skipped, not treated as missing:
// leaving a path empty is the supported way to let the gallery own it, so an
// empty member is not a stale member. This is what distinguishes an empty
// configured path (fill it from the gallery, keep the rest) from a stale one (a
// non-empty path that has gone missing, which makes the whole set stale).
//
// A member that is unreadable for a reason other than absence yields
// presenceIndeterminate, which dominates: a single unreadable member classifies
// the whole set as indeterminate so a slow-to-mount volume is never mistaken for
// a stale configuration.
func (s modelFileSet) nonEmptyMembersPresence(needEmbeddings bool) diskPresence {
	paths := []string{s.model, s.labels}
	if needEmbeddings {
		paths = append(paths, s.embeddings)
	}
	sawMissing := false
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				sawMissing = true
				continue
			}
			return presenceIndeterminate
		}
	}
	if sawMissing {
		return presenceMissing
	}
	return presenceComplete
}

// resolveFamilyPaths decides which file set a secondary model family should be
// built from, choosing between the paths configured in settings and the variant
// the model gallery has installed on disk.
//
// A configured member left EMPTY is filled from the gallery (the supported way
// to let the gallery own a path); the other configured members are kept. When a
// non-empty configured member is CONFIRMED missing on disk, the whole configured
// set is discarded and the gallery set is used INSTEAD, as a unit. Resolving per
// file would be wrong: pairing a configured model path from one variant with a
// fallback label path from another yields a model and a label file that describe
// different species sets, which either fails late with a label-count mismatch
// or, when the counts happen to agree, silently mislabels every detection.
//
// The second return value reports whether the returned set should reconcile the
// stale configuration (see Orchestrator.deferPathCorrection). It is true only
// when a configured path was confirmed missing. A configured path that is merely
// unreadable right now (an external volume that has not finished mounting) still
// falls back so the model can run, but returns false so the transient state is
// never persisted to config.yaml.
//
// A configured path that points at a file which does not exist is the state
// GitHub issues #4201 and #4204 describe: a gallery variant switch or a change
// of container HOME leaves settings pointing at a file that is no longer there,
// and before this fallback the model simply never loaded again.
func (o *Orchestrator) resolveFamilyPaths(registryID string, configured modelFileSet, needEmbeddings bool) (resolved modelFileSet, usedFallback bool) {
	presence := configured.nonEmptyMembersPresence(needEmbeddings)

	// An empty configured member is not a stale member: leaving a path empty is
	// the supported way to let the gallery own it. When every NON-EMPTY
	// configured member exists on disk, keep the configured set and fill only the
	// empty members from the gallery (main's behaviour). usedFallback stays false:
	// nothing configured went stale, so there is nothing to repair.
	if presence == presenceComplete {
		if configured.complete(needEmbeddings) {
			return configured, false
		}
		filled := configured

		// Fill the empty members, anchoring on the variant the configuration
		// NAMES rather than on whichever variant the catalog lists first. When
		// configured.model matches a catalog variant, resolveSiblingSet supplies
		// its companion files from the SAME variant, so an empty labels member is
		// filled with that model's own labels. Anchoring on resolveInstalledPaths
		// instead can pair a configured regional model with a different variant's
		// labels (say a global build that is also installed); for bat, whose label
		// files are per-region with region-specific counts, a count coincidence
		// then silently mislabels. resolveSiblingSet returns ok=false when
		// configured.model is empty or names no catalog variant, so the
		// resolveInstalledPaths fallback preserves the previous behaviour for
		// those cases.
		var m, l, e string
		if sibling, ok := o.resolveSiblingSet(registryID, configured.model); ok {
			m, l, e = sibling.model, sibling.labels, sibling.embeddings
		} else {
			m, l, e = o.resolveInstalledPaths(registryID)
		}
		if filled.model == "" {
			filled.model = m
		}
		if filled.labels == "" {
			filled.labels = l
		}
		if needEmbeddings && filled.embeddings == "" {
			filled.embeddings = e
		}
		return filled, false
	}

	// A non-empty configured path is missing or unreadable. Only a CONFIRMED
	// absence (presenceMissing) marks the set stale and may rewrite config; a
	// member that is unreadable for a reason OTHER than absence
	// (presenceIndeterminate: a permissions failure, an I/O error, a
	// half-initialised mount reporting EIO) still falls back at runtime so
	// analysis can run, but must NOT persist a repair, or a transient error
	// rewrites config.yaml permanently. repairable carries that distinction to
	// both fallback returns below. (A fully unmounted path reports ErrNotExist, so
	// it is presenceMissing here; isGalleryManagedPath is what stops that rewrite
	// from taking over a user's own path.)
	repairable := presence == presenceMissing

	// Before the generic gallery probe, try to keep the variant the configured
	// model file names. When only a companion file went missing (a partial
	// cleanup, a half-finished variant switch that left both variants' models on
	// disk), the generic probe returns whichever variant the catalog lists first,
	// which can be a superseded one. That is a consistent pair, so nothing fails
	// loudly, but the user silently ends up running a different regional model
	// than the one they chose.
	if sameVariant, ok := o.resolveSiblingSet(registryID, configured.model); ok &&
		sameVariant.allPresentOnDisk(needEmbeddings) {
		GetLogger().Debug("recovered the configured variant's companion files",
			logger.String("registry_id", registryID),
			logger.String("model_path", sameVariant.model))
		return sameVariant, repairable
	}

	m, l, e := o.resolveInstalledPaths(registryID)
	fallback := modelFileSet{model: m, labels: l, embeddings: e}

	// Nothing installed to fall back to. Return the configured set unchanged so
	// the caller reports its existing "not installed or configured" error, and so
	// a set that is merely unreadable right now (an unmounted volume) is not
	// replaced by an empty one.
	if !fallback.complete(needEmbeddings) {
		return configured, false
	}

	// Debug, not Info: this fires once per build and once more when the loader
	// re-resolves to decide whether the configuration needs repairing, and the
	// noteworthy outcomes already log at Info from planPathCorrection (either the
	// repair itself, or the decision to leave a non-gallery path alone). Falling
	// back for an empty configured path is the normal, uninteresting case.
	GetLogger().Debug("configured model path is missing on disk, falling back to the installed model",
		logger.String("registry_id", registryID),
		logger.String("configured_model_path", configured.model),
		logger.String("resolved_model_path", fallback.model))

	return fallback, repairable
}

// resolveSiblingSet rebuilds a family's complete file set from the variant that
// modelPath names, taking the companion files from modelPath's own directory.
//
// It exists so a family whose model file is present but whose labels went
// missing recovers the RIGHT variant rather than whichever one the catalog
// happens to list first. The embedding extractor is shared across a family's
// variants and lives in the gallery's shared/ directory, so it is resolved from
// there rather than beside the model.
//
// Returns ok=false when modelPath is empty, does not exist, or does not match
// any catalog variant for the family.
func (o *Orchestrator) resolveSiblingSet(registryID, modelPath string) (set modelFileSet, ok bool) {
	if modelPath == "" {
		return modelFileSet{}, false
	}
	if _, err := os.Stat(modelPath); err != nil {
		// Any stat error skips recovery: without confirming the model file exists
		// there is nothing to anchor the companion files to. A non-ErrNotExist
		// error (an unreadable volume) is treated the same as absence here, so a
		// half-mounted volume never yields a spurious sibling set. Suppressing the
		// config repair for the merely-unreadable case is the caller's job, via the
		// presenceIndeterminate classification from nonEmptyMembersPresence.
		return modelFileSet{}, false
	}

	// Read o.modelsDir WITHOUT taking o.mu. resolveFamilyPaths runs inside the
	// model loaders, which hold o.mu.Lock(), and sync.RWMutex is not reentrant:
	// acquiring a read lock here self-deadlocks the loader. This mirrors
	// resolveInstalledPaths, which reads the same field on the same call path.
	// The field is written once at construction and again by SetModelsDir before
	// the pipeline starts, so it is stable by the time any of this runs.
	modelsDir := o.modelsDir

	base := filepath.Base(modelPath)
	dir := filepath.Dir(modelPath)

	catalog := ActiveCatalog()
	for i := range catalog {
		entry := &catalog[i]
		if entry.RegistryID != registryID {
			continue
		}
		fileSets := [][]CatalogFile{entry.Files}
		if len(entry.Variants) > 0 {
			fileSets = fileSets[:0]
			for j := range entry.Variants {
				fileSets = append(fileSets, entry.Variants[j].Files)
			}
		}
		for _, files := range fileSets {
			if !declaresModelFile(files, base) {
				continue
			}
			candidate := modelFileSet{model: modelPath}
			for _, f := range files {
				switch f.Role {
				case RoleLabels:
					candidate.labels = filepath.Join(dir, f.LocalName)
				case RoleEmbeddings:
					if modelsDir != "" {
						candidate.embeddings = filepath.Join(modelsDir, "shared", f.LocalName)
					}
				}
			}
			return candidate, true
		}
	}
	return modelFileSet{}, false
}

// declaresModelFile reports whether files contains a model-role entry named
// localName.
func declaresModelFile(files []CatalogFile, localName string) bool {
	for _, f := range files {
		if f.Role == RoleModel && f.LocalName == localName {
			return true
		}
	}
	return false
}

// isGalleryManagedPath reports whether path looks like a file the model gallery
// owns, rather than a custom model the user configured by hand.
//
// This gates the automatic configuration repair in planPathCorrection.
// Rewriting a user's own path would silently take their custom model away, and
// a path that is temporarily unreadable (an external volume that has not
// mounted yet) is indistinguishable from a stale one at the filesystem level.
// So repair only what the gallery itself wrote.
//
// A path qualifies only when BOTH hold: its basename is a LocalName the catalog
// entry for registryID declares, AND its parent directory's base name is that
// entry's ID (or "shared" for a shared-role file such as the embedding
// extractor). Matching on the parent directory's base name rather than the full
// models-directory prefix is what catches the case the issues report: the models
// directory prefix changed (a different container HOME), so the stale path no
// longer sits under the current models directory, yet it still lives in a
// gallery-shaped <entry ID>/<local name> layout. A user's own copy that merely
// shares a catalog file name (for example /mnt/nas/models/perch_v2_labels.txt,
// whose parent directory is "models", not the entry ID) does NOT qualify.
//
// Takes no Orchestrator lock: it reads only its arguments and the ActiveCatalog
// snapshot (whose accessor takes its own unrelated catalogMu). It is called only
// from planPathCorrection, after the loaders have released o.mu. It would in
// fact be safe on a loader path today, but keep it off one: the drainer that
// reaches it DOES take o.mu, so moving the pair inward reintroduces the
// self-deadlock this branch already shipped once.
func (o *Orchestrator) isGalleryManagedPath(registryID, path string) bool {
	if path == "" {
		return false
	}

	base := filepath.Base(path)
	parent := filepath.Base(filepath.Dir(path))

	// Require the grandparent directory to be the models directory itself (its base
	// name, normally "models"). Matching only base + parent would qualify any path
	// that merely MIRRORS the gallery layout, e.g. /mnt/nas/perch-v2/perch_v2.onnx:
	// a NAS or USB mount that is late at boot yields ENOENT (the mountpoint exists
	// but is empty), which classifies as presenceMissing with repairable=true, so a
	// user who moved their models to a NAS but left a local gallery copy would have
	// the NAS path permanently rewritten. Dropping only the models-directory PREFIX
	// (not its base name) still handles the changed-container-HOME case the issues
	// report, because base(modelsDir) stays "models" across a HOME change, while a
	// look-alike under a different grandparent ("nas") is rejected. This matches the
	// stricter precedent in Settings.MigrateOrphanGeomodelRangeFilter
	// (internal/conf/range_filter_migration.go), which acts only on exact
	// gallery-managed paths and never touches custom or hand-edited ones.
	if o.modelsDir == "" {
		return false
	}
	grandparent := filepath.Base(filepath.Dir(filepath.Dir(path)))
	if grandparent != filepath.Base(o.modelsDir) {
		return false
	}

	catalog := ActiveCatalog()
	for i := range catalog {
		entry := &catalog[i]
		if entry.RegistryID != registryID {
			continue
		}
		// Check the entry's own files and every variant's files as a union: the
		// stale configured path could name any installed variant's file.
		fileSets := [][]CatalogFile{entry.Files}
		for j := range entry.Variants {
			fileSets = append(fileSets, entry.Variants[j].Files)
		}
		for _, files := range fileSets {
			for _, f := range files {
				if f.LocalName != base {
					continue
				}
				expectedParent := entry.ID
				if isSharedRole(f.Role) {
					expectedParent = "shared"
				}
				if parent == expectedParent {
					return true
				}
			}
		}
	}
	return false
}

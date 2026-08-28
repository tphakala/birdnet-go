package classifier

import (
	"os"
	"path/filepath"
	"strings"

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

// resolveFamilyPaths decides which file set a secondary model family should be
// built from, choosing between the paths configured in settings and the variant
// the model gallery has installed on disk.
//
// The configured set is honoured only when it is complete AND every one of its
// files exists. Otherwise the whole configured set is discarded and the gallery
// set is used INSTEAD, as a unit. Resolving per file would be wrong: pairing a
// configured model path from one variant with a fallback label path from
// another yields a model and a label file that describe different species sets,
// which either fails late with a label-count mismatch or, when the counts
// happen to agree, silently mislabels every detection.
//
// The second return value reports whether the returned set came from the
// gallery fallback, so the caller can reconcile the stale configuration once
// the model has actually loaded (see Orchestrator.deferPathCorrection).
//
// A configured path that points at a file which does not exist is the state
// GitHub issues #4201 and #4204 describe: a gallery variant switch or a change
// of container HOME leaves settings pointing at a file that is no longer there,
// and before this fallback the model simply never loaded again.
func (o *Orchestrator) resolveFamilyPaths(registryID string, configured modelFileSet, needEmbeddings bool) (resolved modelFileSet, usedFallback bool) {
	if configured.allPresentOnDisk(needEmbeddings) {
		return configured, false
	}

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
		return sameVariant, true
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

	return fallback, true
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
// A path qualifies when it sits under the gallery models directory, or when its
// basename matches a file name the catalog declares for the family. The second
// test is what catches the case the issues report: the models directory prefix
// changed (a different container HOME), so the stale path no longer sits under
// the current models directory, yet the file name is unmistakably ours.
// Unlike resolveSiblingSet, this one DOES take o.mu, because it is called only
// from planPathCorrection, which runs after the loaders have released the lock.
// Do not call it from a builder or a loader.
func (o *Orchestrator) isGalleryManagedPath(registryID, path string) bool {
	if path == "" {
		return false
	}

	o.mu.RLock()
	modelsDir := o.modelsDir
	o.mu.RUnlock()

	if modelsDir != "" {
		if rel, err := filepath.Rel(modelsDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}

	base := filepath.Base(path)
	catalog := ActiveCatalog()
	for i := range catalog {
		entry := &catalog[i]
		if entry.RegistryID != registryID {
			continue
		}
		for _, f := range entry.Files {
			if f.LocalName == base {
				return true
			}
		}
		for i := range entry.Variants {
			for _, f := range entry.Variants[i].Files {
				if f.LocalName == base {
					return true
				}
			}
		}
	}
	return false
}

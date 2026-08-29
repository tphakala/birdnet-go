package classifier

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
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

// pathResolution carries resolveFamilyPaths' outcome out of a model builder so
// the loader can act on it without resolving a second time.
//
// The builder and the loader need different halves of the same resolution: the
// builder needs the resolved paths to construct from, and the loader needs to
// know whether the gallery fallback was used so it can queue the configuration
// repair. Resolving twice would not only duplicate the stat work, it would open
// a window BETWEEN THE TWO RESOLUTIONS: the model constructor runs between them
// and takes real time for an ONNX session, so a concurrent gallery install,
// uninstall or variant switch could make the second resolution disagree with the
// first, and the repair would then persist paths the model was NOT built from.
// Threading keeps the two resolutions from disagreeing; it does not close the gap
// between resolving and persisting (the drain still runs later), so the persisted
// set can still lag a gallery change that lands after the build.
type pathResolution struct {
	// resolved is the file set the model was actually built from.
	resolved modelFileSet
	// substituted reports that the runtime built from a DIFFERENT file set than
	// the non-empty configured one. Filling an EMPTY configured member from the
	// gallery is NOT a substitution: leaving a path empty is the supported way to
	// let the gallery own it, so nothing the user chose was replaced.
	substituted bool
	// repairable reports that the substitution came from a CONFIRMED absence
	// (ErrNotExist), so the stale configuration MAY be rewritten. It is never true
	// unless substituted is also true. A member that is unreadable for some OTHER
	// reason (presenceIndeterminate: a permissions failure, an I/O error, a
	// half-initialised mount) is substituted but NOT repairable, so a transient
	// failure never rewrites config.yaml permanently.
	repairable bool
	// unreadable distinguishes the ONE substitution cause that is not an absence:
	// the configured file is present but could not be read (a permissions change,
	// an I/O error, a half-initialised mount). It is carried explicitly rather than
	// derived from repairable, because "not repairable" has more than one cause:
	// the primary also declines to repair a path whose written form differs from
	// its expanded form, and telling that user their file "could not be read" would
	// send them to check permissions on a file that is simply gone.
	unreadable bool
}

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
// The returned pathResolution carries two independent flags. substituted reports
// that a NON-EMPTY configured set was replaced at runtime, so the user is running
// a model they did not choose and must be told; repairable reports that the
// substitution came from a CONFIRMED absence, so the stale configuration may also
// be rewritten (see Orchestrator.deferPathCorrection). A configured path that is
// merely unreadable right now (an external volume that has not finished mounting)
// still falls back so the model can run, and is reported as substituted, but is
// NOT repairable, so the transient state is never persisted to config.yaml.
//
// A configured path that points at a file which does not exist is the state
// GitHub issues #4201 and #4204 describe: a gallery variant switch or a change
// of container HOME leaves settings pointing at a file that is no longer there,
// and before this fallback the model simply never loaded again.
func (o *Orchestrator) resolveFamilyPaths(registryID string, configured modelFileSet, needEmbeddings bool) pathResolution {
	presence := configured.nonEmptyMembersPresence(needEmbeddings)

	// An empty configured member is not a stale member: leaving a path empty is
	// the supported way to let the gallery own it. When every NON-EMPTY
	// configured member exists on disk, keep the configured set and fill only the
	// empty members from the gallery (main's behaviour). Neither flag is set:
	// nothing configured went stale, so there is nothing to repair and nothing the
	// user chose was replaced.
	if presence == presenceComplete {
		if configured.complete(needEmbeddings) {
			return pathResolution{resolved: configured}
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
		return pathResolution{resolved: filled}
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
		return pathResolution{resolved: sameVariant, substituted: true, repairable: repairable, unreadable: presence == presenceIndeterminate}
	}

	m, l, e := o.resolveInstalledPaths(registryID)
	fallback := modelFileSet{model: m, labels: l, embeddings: e}

	// Nothing installed to fall back to. Return the configured set unchanged so
	// the caller reports its existing "not installed or configured" error, and so
	// a set that is merely unreadable right now (an unmounted volume) is not
	// replaced by an empty one.
	if !fallback.complete(needEmbeddings) {
		return pathResolution{resolved: configured}
	}

	// Debug, not Info: this fires once per build, and the noteworthy outcomes
	// already log at Info from planPathCorrection (either the repair itself, or
	// the decision to leave a non-gallery path alone). Falling back for an empty
	// configured path is the normal, uninteresting case.
	GetLogger().Debug("configured model path is missing on disk, falling back to the installed model",
		logger.String("registry_id", registryID),
		logger.String("configured_model_path", configured.model),
		logger.String("resolved_model_path", fallback.model))

	return pathResolution{resolved: fallback, substituted: true, repairable: repairable, unreadable: presence == presenceIndeterminate}
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
	// The read is safe today because o.modelsDir is effectively immutable once the
	// pipeline is running: the constructor writes it before o is published, and
	// SetModelsDir (its only other writer, under o.mu.Lock()) is called at most
	// once, from NewModelManager, before the pipeline starts; cmd/benchmark and
	// cmd/rangefilter construct an Orchestrator and never call it at all. The
	// loader path additionally holds o.mu, but is not the only reader: the reload
	// path (ReloadSecondaryModels, which calls the builders after releasing o.mu)
	// reads it here and via resolveInstalledPaths with no lock, and the correction
	// drainer reads it via isGalleryManagedPath with no lock. Making the models
	// directory dynamic would therefore require revisiting all three lock-free
	// readers, not just adding a lock here.
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
// Takes no Orchestrator lock. Besides its arguments it reads o.modelsDir (in the
// o.modelsDir == "" guard and in filepath.Base(o.modelsDir) in the grandparent
// check) and the ActiveCatalog snapshot (whose accessor takes its own unrelated
// catalogMu). It is called only from planPathCorrection, which the drainer
// reaches after the loaders have released o.mu, so this o.modelsDir read holds no
// orchestrator lock; it is safe for the reason resolveSiblingSet documents.
//
// Calling THIS function under a loader's o.mu would be safe, since it takes no
// orchestrator lock. What must not move inside a loader is its caller chain's
// entry point, runPendingPathCorrections: that drainer takes o.mu itself to
// snapshot and clear the queue, and o.mu is not reentrant, so draining under the
// loaders' write lock self-deadlocks the load. Keep the drain where it is.
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

// primaryPathResolver resolves the configured primary classifier model path to
// the file the instance should actually load from. It is injected into NewBirdNET
// rather than called from it because the resolution needs the models directory
// and the catalog, which live on the Orchestrator: NewOrchestrator assigns
// o.modelsDir before it constructs the primary, while bn.modelsDir is still empty
// at that point (ModelManager sets it later, via SetModelsDir).
//
// A nil resolver means "use the configured path verbatim", which is what every
// direct construction (tests, and any caller with no orchestrator) keeps.
type primaryPathResolver func(configured string) pathResolution

// resolvePrimaryModelPath gives the primary BirdNET v2.4 slot the stale-path
// recovery the three secondary families got for GitHub #4201 and #4204. Without
// it, a configured primary path that no longer exists (a gallery variant switch,
// or a container HOME change that invalidates a stored absolute path) makes
// NewBirdNET fail outright, so there is no analysis at all rather than one
// missing optional model.
//
// The primary is deliberately NOT a modelFileSet family. Its label set is
// embedded and identical across v2.4 variants, which is why
// applyConfigForPrimarySwap writes BirdNET.ModelPath alone and documents that it
// never touches BirdNET.LabelPath: a user-configured custom label path must
// survive a variant swap. So there is exactly one path to resolve and no
// cross-variant pairing hazard to protect against.
func (o *Orchestrator) resolvePrimaryModelPath(configured string) pathResolution {
	if configured == "" {
		// No configured path at all: the Tier-4 default (the embedded model, or the
		// standard-path INT8 ONNX build on arm64). Nothing to resolve, nothing to
		// repair, and nothing was substituted.
		return pathResolution{}
	}

	keep := pathResolution{resolved: modelFileSet{model: configured}}

	// Stat the EXPANDED path: loadModel and initializeONNXModel both expand before
	// opening, so statting the raw string would classify every configured "$VAR/..."
	// or "~/..." path as missing and substitute a model out from under the user.
	expanded, err := conf.ExpandTildePath(os.ExpandEnv(configured))
	if err != nil {
		// A path that cannot even be expanded is not one we can reason about. Keep
		// it and let the existing load error report it.
		return keep
	}

	if _, statErr := os.Stat(expanded); statErr == nil {
		return keep
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		// Unreadable for a reason OTHER than absence: a permissions change, an I/O
		// error, a half-initialised mount. This is the ONE place the primary
		// deliberately behaves differently from the secondaries, which do fall back
		// on this signal (see resolveFamilyPaths).
		//
		// A secondary that fails to load costs one optional overlay for the session.
		// Swapping the PRIMARY on an ambiguous signal would silently run the stock
		// baseline in place of the regional or custom model the user chose, and every
		// detection written while that lasted would be attributed to the wrong model.
		// A transient unreadable file is far more likely to clear on its own than a
		// polluted detection history is to be noticed, so the primary keeps the
		// configured path and lets the existing load error surface.
		GetLogger().Warn("configured primary model path is unreadable; keeping it rather than substituting a different model",
			logger.String("model_path", configured),
			logger.Error(statErr))
		return keep
	}

	// CONFIRMED absent. Recover to the installed gallery variant when there is one
	// AND that variant can actually run on this host.
	if installed, _, _ := o.resolveInstalledPaths(permanentRegistryID); installed != "" && o.primaryVariantUsable(installed) {
		GetLogger().Info("configured primary model path is missing on disk, recovering the installed variant",
			logger.String("configured_model_path", configured),
			logger.String("resolved_model_path", installed))
		return pathResolution{
			resolved: modelFileSet{model: installed},
			// Repairable only when the configured string is literally what it
			// resolves to. A configured "$HOME/..." or "~/..." can still match the
			// gallery layout, and rewriting it would flatten the variable into the
			// absolute path it happens to expand to today, so the path would stop
			// following HOME on the next container start. That is the exact
			// fragility this recovery exists to undo, so the runtime substitution
			// stands but the config rewrite does not.
			//
			// The guard lives here rather than in isGalleryManagedPath because that
			// helper serves all four families from ONE call site. Putting it there
			// would change the three secondaries' behaviour, which this change has no
			// business touching: they would stop repairing a $VAR path and instead
			// warn about it on every single start, forever. Note the runtime
			// substitution is identical either way, so refusing the rewrite does not
			// give those users their own model back; it only trades a one-time repair
			// for a permanent notification.
			substituted: true,
			repairable:  configured == expanded,
		}
	}

	// Nothing installed to recover to. Resolve to EMPTY so the identity tiers
	// behave exactly as if the user had never configured a path: Tier 3 stops
	// firing, Tier 4 resolves the default, and remapV24ToONNXOnARM64 (which returns
	// early on a non-empty CustomPath) is free to remap arm64 to the standard-path
	// INT8 ONNX model. Before this the primary simply failed to construct and took
	// the whole pipeline down with it.
	//
	// substituted, but NOT repairable: there is no correct path to write. Writing
	// the empty string would discard the user's own setting, so re-mounting the
	// volume or reinstalling the variant restores their model on the next start.
	// The user is told through the built-in variant of the substituted
	// notification.
	GetLogger().Warn("configured primary model path is missing and no installed variant is available; using the built-in model",
		logger.String("configured_model_path", configured))
	return pathResolution{substituted: true}
}

// primaryVariantUsable reports whether an installed primary variant can actually
// be loaded on this host.
//
// The BuiltIn baseline declares no files, so resolveInstalledPaths can only ever
// return a DFT-truncated ONNX build for this family. Recovering onto one that
// cannot load would turn a recoverable stale path into a hard startup failure,
// which is precisely the outcome this recovery exists to prevent. Reporting false
// makes the caller fall through to the built-in baseline, which always loads.
//
// "Can load" is deliberately NOT "ONNX Runtime is available". initializeModel
// tries OPENVINO FIRST for the v2.4 identity and only falls through to ONNX
// Runtime when OpenVINO declines, so an openvino-tagged build on an A76/Pi5 or an
// Intel iGPU runs these variants with no ORT installed at all. Gating on ORT alone
// would refuse a variant that would have loaded, silently dropping such a host to
// the embedded model and telling the user no installed model was available, which
// is false.
//
// CheckORTAvailability is used rather than checkORTOrFail because the latter logs
// and raises a user notification, which would be wrong for a path we are choosing
// NOT to take. Note the OpenVINO leg below is a real load rather than a pure
// probe: InitOpenVINO memoizes success, and there is no way to answer "would this
// load" without trying.
func (o *Orchestrator) primaryVariantUsable(modelPath string) bool {
	if !isONNXModel(modelPath) {
		return true
	}

	// An empty configured runtime path is the normal "use the system default"
	// value, so a missing settings snapshot degrades to that rather than to a
	// refusal.
	settings := o.currentSettings()
	if settings == nil {
		settings = &conf.Settings{}
	}

	// OpenVINO first, mirroring initializeModel's own order. Eligibility alone is
	// NOT enough: initializeModel also falls through to ONNX Runtime when OpenVINO
	// is eligible but FAILS TO LOAD, and openVINOPlanFor's CPU branch answers yes
	// from the CPU's f16 support without ever opening the library. Accepting a
	// variant on eligibility would therefore hand a host with a broken or missing
	// OpenVINO library straight to the ONNX path it has no runtime for, which is
	// the hard startup failure this gate exists to prevent, reached from the other
	// side. So the plan must be usable AND the library must actually load.
	if _, ok, _ := openVINOPlanFor(
		settings.BirdNET.Backend,
		settings.BirdNET.OpenVINODevice,
		DefaultModelVersion,
		settings.BirdNET.OpenVINOPath,
		birdnetLogitsOutputIndex,
	); ok && o.openVINOLoads(settings.BirdNET.OpenVINOPath) {
		return true
	}

	available := o.ortAvailable
	if available == nil {
		available = func(path string) bool { return inference.CheckORTAvailability(path).Available }
	}
	if available(settings.BirdNET.ONNXRuntimePath) {
		return true
	}

	GetLogger().Warn("installed primary variant cannot load on this host (no OpenVINO plan and no ONNX Runtime); using the built-in model instead",
		logger.String("model_path", modelPath))
	return false
}

// openVINOLoads reports whether the OpenVINO runtime can actually be opened.
// Split out for the ovLoadable test seam; see Orchestrator.ovLoadable.
func (o *Orchestrator) openVINOLoads(libraryPath string) bool {
	if o.ovLoadable != nil {
		return o.ovLoadable(libraryPath)
	}
	return inference.InitOpenVINO(libraryPath) == nil
}

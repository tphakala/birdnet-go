package classifier

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// pendingPathCorrection records that a model was loaded from the gallery
// fallback rather than from the paths configured in settings, so the stale
// configuration can be repaired once o.mu is released.
type pendingPathCorrection struct {
	registryID string
	resolved   modelFileSet
}

// deferPathCorrection queues a configuration repair for a model that loaded via
// the gallery fallback. Called by the secondary model loaders while they hold
// o.mu; the repair itself runs in runPendingPathCorrections after the lock is
// released.
//
// Queued only AFTER the model has built successfully. The constructors open the
// ONNX session and validate the label count against the model's output tensor,
// so a set that builds is a set that genuinely belongs together. Queueing before
// the build could persist paths that turn out to be unusable.
//
// Must be called with o.mu held.
func (o *Orchestrator) deferPathCorrection(registryID string, resolved modelFileSet) {
	o.pendingPathCorrections = append(o.pendingPathCorrections, pendingPathCorrection{
		registryID: registryID,
		resolved:   resolved,
	})
}

// queuePathCorrectionIfFallback resolves the family's paths and, when the
// gallery fallback was used, queues the configuration repair. The loaders call
// this after a successful build.
//
// It re-runs the resolution the builder already performed rather than threading
// the result out through build*, because build* is shared with
// ReloadSecondaryModels, where a settings write is explicitly unwanted: a
// backend or device swap must not rewrite the user's paths. The re-resolution
// costs at most three os.Stat calls and cannot disagree with the build, since
// nothing between them touches the filesystem.
//
// Must be called with o.mu held. It only resolves paths and appends to the
// pending queue; neither takes o.mu, so it is safe under the loaders' write
// lock. The settings write itself happens later, in the drainer, after o.mu is
// released.
func (o *Orchestrator) queuePathCorrectionIfFallback(registryID string, configured modelFileSet, needEmbeddings bool) {
	resolved, usedFallback := o.resolveFamilyPaths(registryID, configured, needEmbeddings)
	if !usedFallback {
		return
	}
	o.deferPathCorrection(registryID, resolved)
}

// runPendingPathCorrections drains the queued configuration repairs, rewriting
// stale gallery paths in settings so the next start loads directly instead of
// falling back again.
//
// This is the self-heal for GitHub issues #4201 and #4204: a user who upgrades
// to a build containing this fix gets their config.yaml corrected on the first
// start, with no manual edit and no reinstall from the gallery. Without it the
// bad path would survive every restart, and it would keep feeding
// installedModelBasenameHint, which ScanInstalled uses to decide which variant
// is installed when more than one variant's files are present.
//
// Must be called with o.mu NOT held: the snapshot-and-clear below takes o.mu
// itself, and o.mu is not reentrant. (isGalleryManagedPath, reached from
// applyPathCorrection, does NOT take o.mu; it is the drain that does.)
func (o *Orchestrator) runPendingPathCorrections() {
	o.mu.Lock()
	pending := o.pendingPathCorrections
	o.pendingPathCorrections = nil
	o.mu.Unlock()

	for i := range pending {
		o.applyPathCorrection(&pending[i])
	}
}

// applyPathCorrection rewrites one family's stale gallery paths in settings and
// persists them, using the clone-mutate-publish protocol every other settings
// writer follows. It serializes against ModelManager's install/uninstall config
// writers through the package-level settingsWriteMu.
func (o *Orchestrator) applyPathCorrection(pc *pendingPathCorrection) {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()

	// Re-read INSIDE the critical section. Reading conf.GetSettings() before
	// taking the lock is the load-bearing half of the bug: a concurrent
	// ModelManager writer could publish between that read and our StoreSettings,
	// and our clone (built from the stale snapshot) would then clobber its write.
	current := conf.GetSettings()
	if current == nil {
		return
	}

	updated, changed := o.planPathCorrection(current, pc)
	if !changed {
		return
	}

	conf.StoreSettings(updated)
	if err := conf.SaveSettings(); err != nil {
		// The in-memory snapshot still carries the corrected paths, so the running
		// process is consistent; only the repair's persistence is lost, and the
		// next start repeats the fallback and tries again.
		GetLogger().Warn("failed to persist repaired model paths",
			logger.String("registry_id", pc.registryID),
			logger.Error(err))
		return
	}

	emitPathReconciledNotification(pc.registryID, pc.resolved.model)
}

// planPathCorrection produces the corrected settings snapshot for one family and
// reports whether anything actually changed. It is separated from the persisting
// half of applyPathCorrection so the decision (which fields may be rewritten and
// which must be left alone) is testable without touching the filesystem or the
// developer's own configuration file.
func (o *Orchestrator) planPathCorrection(current *conf.Settings, pc *pendingPathCorrection) (updated *conf.Settings, changed bool) {
	log := GetLogger()
	updated = conf.CloneSettings(current)

	// repair decides whether one field may be rewritten. An empty configured
	// value is deliberately left alone: the fallback already handles it and has
	// always been the supported way to let the gallery own a path, so filling it
	// in would only add another absolute path to go stale later. A non-empty
	// value is rewritten only when it is one the gallery itself wrote, so a
	// user's hand-configured custom model is never taken over.
	repair := func(field *string, resolved string) {
		if resolved == "" || *field == "" || *field == resolved {
			return
		}
		if !o.isGalleryManagedPath(pc.registryID, *field) {
			log.Info("configured model path is missing but is not gallery-managed, leaving it untouched",
				logger.String("registry_id", pc.registryID),
				logger.String("configured_path", *field))
			return
		}
		log.Info("repairing stale gallery model path in configuration",
			logger.String("registry_id", pc.registryID),
			logger.String("old_path", *field),
			logger.String("new_path", resolved))
		*field = resolved
		changed = true
	}

	switch pc.registryID {
	case RegistryIDPerchV2:
		repair(&updated.Perch.ModelPath, pc.resolved.model)
		repair(&updated.Perch.LabelPath, pc.resolved.labels)
	case RegistryIDBirdNETV3:
		repair(&updated.BirdNETV3.ModelPath, pc.resolved.model)
		repair(&updated.BirdNETV3.LabelPath, pc.resolved.labels)
	case RegistryIDBat:
		// The bat family carries three paths; the shared embedding extractor is
		// part of the set and goes stale with the rest.
		repair(&updated.Bat.ClassifierModel, pc.resolved.model)
		repair(&updated.Bat.LabelPath, pc.resolved.labels)
		repair(&updated.Bat.EmbeddingModel, pc.resolved.embeddings)
	default:
		return current, false
	}

	return updated, changed
}

// emitPathReconciledNotification tells the user that a broken model path was
// repaired. A silent repair would leave someone who lost detections for days
// with no explanation, and the notification is the signal a support dump needs
// to show that this state occurred at all.
func emitPathReconciledNotification(registryID, modelPath string) {
	svc := notification.GetService()
	if svc == nil {
		return
	}

	modelName := registryID
	if info, ok := ModelRegistry[registryID]; ok && info.Name != "" {
		modelName = info.Name
	}

	notif := notification.NewNotification(
		notification.TypeInfo,
		notification.PriorityMedium,
		fmt.Sprintf("Model file paths repaired for %s", modelName),
		// Worded to cover both branches: the model path itself may have gone stale,
		// or (in sibling recovery) the model exists and it was the labels or shared
		// embeddings path that changed. modelPath names the installed model the set
		// was reconciled against.
		fmt.Sprintf("Configured file paths for %s were out of date and have been repaired to match "+
			"the installed model at %s.", modelName, modelPath),
	).
		WithComponent("classifier").
		WithTitleKey(notification.MsgModelPathReconciledTitle, map[string]any{
			"modelName": modelName,
		}).
		WithMessageKey(notification.MsgModelPathReconciledMessage, map[string]any{
			"modelName": modelName,
			"modelPath": modelPath,
		}).
		WithDeliveryTarget("bell")

	_ = svc.CreateWithMetadata(notif)
}

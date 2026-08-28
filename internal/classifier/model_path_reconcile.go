package classifier

import (
	"fmt"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// pathCorrectionPersistenceDisabled, when true, suppresses the config.yaml write
// (and the reconciled notification) in applyPathCorrection process-wide. The
// runtime fallback still resolves stale paths so analysis can run; only the
// PERSISTENCE of the correction is skipped.
//
// It is a process-level switch rather than an Orchestrator field on purpose: the
// first correction drain runs INSIDE NewOrchestrator (loadAdditionalModels ->
// runPendingPathCorrections), so a per-instance flag set after construction would
// be too late to stop that write. Read-only diagnostic commands (benchmark,
// rangefilter print) set it BEFORE constructing the Orchestrator so they never
// rewrite the user's configuration. The default (false) preserves the self-heal
// on the real serve path.
var pathCorrectionPersistenceDisabled atomic.Bool

// SetPathCorrectionPersistenceDisabled toggles whether stale model-path
// corrections are persisted to config.yaml. Diagnostic commands call it with true
// before NewOrchestrator so a read-only command leaves the user's config
// untouched; the runtime fallback stays active either way. Passing false restores
// the default self-heal behaviour (also used by tests to reset the switch).
func SetPathCorrectionPersistenceDisabled(disabled bool) {
	pathCorrectionPersistenceDisabled.Store(disabled)
}

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
// backend or device swap must not rewrite the user's paths. The re-resolution is
// a handful of read-only stats (presence classification, sibling recovery and
// the installed-paths probe together) and cannot disagree with the build, since
// nothing between them writes to the filesystem: the model constructor in
// between opens the model and reads the label file, but writes nothing.
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

	if pathCorrectionPersistenceDisabled.Load() {
		// A read-only diagnostic command (benchmark, rangefilter print) is running.
		// The model already loaded from the runtime fallback, so analysis works; we
		// only skip rewriting config.yaml and any user-facing notification, so the
		// command leaves no side effect on the user's configuration.
		GetLogger().Info("stale model path resolved via fallback; persistence disabled, leaving config.yaml untouched",
			logger.String("registry_id", pc.registryID),
			logger.String("resolved_model_path", pc.resolved.model))
		return
	}

	updated, changed := o.planPathCorrection(current, pc)

	if !changed {
		// planPathCorrection returns a nil snapshot only when it ABANDONED a real
		// correction: applyPathCorrection is only called for families that used the
		// gallery fallback, so a non-empty configured path was substituted at
		// runtime, but the path is user-owned and must not be rewritten. The model
		// is now running a different (installed) model than the user configured;
		// that would otherwise be entirely silent, since emitPathReconciledNotification
		// fires only when config was rewritten. Tell the user instead. A non-nil
		// snapshot with changed==false is a genuine no-op (the paths already
		// matched), so stay quiet there.
		if updated == nil {
			emitPathSubstitutedNotification(pc.registryID, pc.resolved.model)
		}
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

	// fieldCorrection pairs one settings field (a pointer into the clone above)
	// with the resolved value the runtime actually built the model from.
	type fieldCorrection struct {
		field    *string
		resolved string
	}

	var fields []fieldCorrection
	switch pc.registryID {
	case RegistryIDPerchV2:
		fields = []fieldCorrection{
			{&updated.Perch.ModelPath, pc.resolved.model},
			{&updated.Perch.LabelPath, pc.resolved.labels},
		}
	case RegistryIDBirdNETV3:
		fields = []fieldCorrection{
			{&updated.BirdNETV3.ModelPath, pc.resolved.model},
			{&updated.BirdNETV3.LabelPath, pc.resolved.labels},
		}
	case RegistryIDBat:
		// The bat family carries three paths; the shared embedding extractor is
		// part of the set and goes stale with the rest.
		fields = []fieldCorrection{
			{&updated.Bat.ClassifierModel, pc.resolved.model},
			{&updated.Bat.LabelPath, pc.resolved.labels},
			{&updated.Bat.EmbeddingModel, pc.resolved.embeddings},
		}
	default:
		// Unknown registry: nothing to plan. Return nil rather than current so no
		// caller can mutate the live published snapshot through the result.
		return nil, false
	}

	// A resolved set is atomic: model, labels and (for bat) the shared embedding
	// extractor all come from ONE installed variant. The correction must be
	// all-or-nothing, so decide every field's verdict FIRST, then write. Writing
	// only the gallery-managed fields while leaving a user-owned field alone would
	// persist a cross-variant hybrid (a gallery model paired with the user's own
	// labels); the next start would see every member present, classify the set
	// presenceComplete, and use that hybrid verbatim with no fallback and no
	// repair. That is exactly the outcome resolveFamilyPaths' design prevents.
	//
	// A field is a candidate for rewriting only when it actually DIFFERS: a
	// non-empty configured value that is not already the resolved value. An empty
	// configured value is deliberately left alone (the fallback already handles it
	// and it has always been the supported way to let the gallery own a path), and
	// a value already equal to the resolved one needs no change.
	var toWrite []fieldCorrection
	for _, fc := range fields {
		if fc.resolved == "" || *fc.field == "" || *fc.field == fc.resolved {
			continue
		}
		if !o.isGalleryManagedPath(pc.registryID, *fc.field) {
			// One user-owned differing field vetoes the WHOLE correction. Because the
			// resolved set is atomic, writing the gallery-managed fields while leaving
			// this one alone is exactly the cross-variant hybrid described above. Leave
			// config untouched and let the runtime fallback keep handling it.
			log.Info("configured model path is user-owned, abandoning the whole correction to avoid a cross-variant config",
				logger.String("registry_id", pc.registryID),
				logger.String("user_owned_path", *fc.field))
			return nil, false
		}
		toWrite = append(toWrite, fc)
	}

	for _, fc := range toWrite {
		log.Info("repairing stale gallery model path in configuration",
			logger.String("registry_id", pc.registryID),
			logger.String("old_path", *fc.field),
			logger.String("new_path", fc.resolved))
		*fc.field = fc.resolved
		changed = true
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

	// CreateWithMetadata is rate limited and returns an error when the notification
	// is dropped. Log it rather than discarding: a batch of family repairs could
	// otherwise silently swallow the only user-visible signal that a repair
	// happened, which is the whole point of this notification. Mirrors
	// notifyModelsNotRegistered's handling of the same call.
	if err := svc.CreateWithMetadata(notif); err != nil {
		GetLogger().Warn("failed to create model-path-reconciled notification",
			logger.String("registry_id", registryID),
			logger.String("model_path", modelPath),
			logger.Error(err))
	}
}

// emitPathSubstitutedNotification tells the user that a configured model file was
// not found and the installed gallery model is being used at runtime instead,
// while their configuration was deliberately left unchanged (the configured path
// is user-owned, so the self-heal must not take it over). Without this the
// substitution is entirely silent: the user keeps getting detections, but from a
// model they never chose, and the reconciled notification never fires because
// config was not rewritten.
func emitPathSubstitutedNotification(registryID, modelPath string) {
	svc := notification.GetService()
	if svc == nil {
		return
	}

	modelName := registryID
	if info, ok := ModelRegistry[registryID]; ok && info.Name != "" {
		modelName = info.Name
	}

	notif := notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityMedium,
		fmt.Sprintf("Configured model file for %s was not found", modelName),
		fmt.Sprintf("The model file configured for %s was not found on disk, so the installed model at %s "+
			"is being used instead. Your configuration was left unchanged.", modelName, modelPath),
	).
		WithComponent("classifier").
		WithTitleKey(notification.MsgModelPathSubstitutedTitle, map[string]any{
			"modelName": modelName,
		}).
		WithMessageKey(notification.MsgModelPathSubstitutedMessage, map[string]any{
			"modelName": modelName,
			"modelPath": modelPath,
		}).
		WithDeliveryTarget("bell")

	// CreateWithMetadata is rate limited and returns an error when the notification
	// is dropped. Log it rather than discarding, matching emitPathReconciledNotification.
	if err := svc.CreateWithMetadata(notif); err != nil {
		GetLogger().Warn("failed to create model-path-substituted notification",
			logger.String("registry_id", registryID),
			logger.String("model_path", modelPath),
			logger.Error(err))
	}
}

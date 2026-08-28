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

// pendingPathCorrection records that a model was loaded from a different file set
// than the one configured (the gallery fallback, or for the primary the built-in
// baseline), so the user can be told and, where it is safe, the stale
// configuration repaired once o.mu is released.
type pendingPathCorrection struct {
	registryID string
	resolved   modelFileSet
	// repairable carries pathResolution.repairable through the queue: true only
	// when the configured path was CONFIRMED absent, so config.yaml may be
	// rewritten. When false the model still runs from a substitute, but the
	// configuration is left exactly as the user wrote it and only the
	// substituted notification fires.
	repairable bool
	// unreadable carries pathResolution.unreadable so the notification can tell a
	// present-but-unreadable file apart from an absent one.
	unreadable bool
}

// deferPathCorrection queues a configuration repair for a model that loaded from
// a different file set than the one configured. Called by the secondary model
// loaders while they hold o.mu, and by NewOrchestrator for the primary before the
// orchestrator is published; the repair itself runs in runPendingPathCorrections
// after the lock is released.
//
// Queued only AFTER the model has built successfully. The constructors open the
// ONNX session and validate the label count against the model's output tensor,
// so a set that builds is a set that genuinely belongs together. Queueing before
// the build could persist paths that turn out to be unusable.
//
// Must be called either with o.mu held, or before o is published (the
// NewOrchestrator case), since it appends to a slice no other goroutine can yet
// observe.
func (o *Orchestrator) deferPathCorrection(registryID string, res pathResolution) {
	o.pendingPathCorrections = append(o.pendingPathCorrections, pendingPathCorrection{
		registryID: registryID,
		resolved:   res.resolved,
		repairable: res.repairable,
		unreadable: res.unreadable,
	})
}

// queuePathCorrection queues the follow-up when a model resolved to a different
// file set than the one configured. The secondary loaders call it after a
// successful build, passing the resolution their builder already computed;
// NewOrchestrator calls it for the primary, whose substitute may be the built-in
// baseline rather than a gallery file.
//
// The resolution is threaded out of build* rather than recomputed here for two
// reasons. It avoids resolving the same file set twice per load, and, more
// importantly, it removes the window BETWEEN THE TWO RESOLUTIONS: recomputing
// here would resolve a second time after the model constructor (which takes real
// time), so an external writer (a concurrent gallery install, uninstall or
// variant switch) could otherwise make that second resolution differ from the set
// the model was actually built from, and this repair would persist those other
// paths. Threading the build's own resolution therefore guarantees the repair
// persists the set the instance was actually built from. It does NOT close the
// gap between resolving and persisting the paths (the drain still runs later, so
// the persisted set can lag a gallery change that lands after the build); it only
// removes the disagreement between two resolutions of the same load.
//
// ReloadSecondaryModels calls build* directly and never calls this, which is
// what keeps a backend or device swap from rewriting the user's paths: the
// reload path simply discards the resolution.
//
// Must be called either with o.mu held (the loaders) or before o is published
// (NewOrchestrator, for the primary). It only appends to the pending queue, which
// does not take o.mu, so it is safe under the loaders' write lock. The settings
// write itself happens later, in the drainer, after o.mu is released.
func (o *Orchestrator) queuePathCorrection(registryID string, res pathResolution) {
	// Queue on substituted, not on repairable. A substitution that may NOT rewrite
	// config (an unreadable configured path: a permissions change on a NAS mount,
	// a volume that is slow to come up) still means the user is running a model
	// they did not choose, and the drain is the only place that tells them. Before
	// this, nothing was queued for that case, so applyPathCorrection was never
	// reached and the substitution was visible only as a single Debug line.
	if !res.substituted {
		return
	}
	o.deferPathCorrection(registryID, res)
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
// planPathCorrection, does NOT take o.mu; it is the drain that does.)
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

	updated, outcome := o.planPathCorrection(current, pc)

	switch outcome {
	case correctionNoop:
		// The configured paths already match what the model was built from. Nothing
		// to write and nothing to say.
		return
	case correctionSubstituted:
		// The model is running a DIFFERENT (installed) model than the user
		// configured, and the configuration was deliberately left alone: either the
		// configured path is user-owned, or the failure to read it was not a
		// confirmed absence and must not be persisted. Either way this would
		// otherwise be entirely silent, since emitPathReconciledNotification fires
		// only when config was rewritten. Tell the user instead.
		emitPathSubstitutedNotification(pc)
		return
	case correctionUnknownFamily:
		// No settings mapping for this registry ID, so there is nothing to plan.
		// Distinct from correctionSubstituted on purpose: a substitution did happen,
		// but the thing at fault is the SELF-HEAL (no settings mapping for this
		// family), not the user's configuration, so this is a developer-facing log
		// and never a user-facing warning.
		GetLogger().Warn("no settings mapping for model family; skipping path correction",
			logger.String("registry_id", pc.registryID))
		return
	case correctionRewrite:
		// Fall through to the write below.
	default:
		// Not reachable today. Guarded anyway because the write below is the only
		// destructive action in this file: an outcome nobody enumerated must never
		// reach it by falling out of the switch.
		GetLogger().Warn("unrecognised path-correction outcome; not writing configuration",
			logger.String("registry_id", pc.registryID),
			logger.Int("outcome", int(outcome)))
		return
	}

	if updated == nil {
		// Defensive: correctionRewrite always carries a snapshot. StoreSettings(nil)
		// would publish a nil settings pointer process-wide.
		GetLogger().Warn("path-correction rewrite produced no settings snapshot; skipping write",
			logger.String("registry_id", pc.registryID))
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

// correctionOutcome is what applyPathCorrection should DO with a planned
// correction. It replaces an earlier (updated *conf.Settings, changed bool) pair
// in which a nil snapshot meant two different things (an abandoned user-owned
// correction, and an unknown registry ID) that shared one user-facing signal.
// That collision became reachable once the queue gate widened from repairable to
// substituted: adding a loader for a family with no settings mapping would then
// emit a user-facing warning for what is really a gap in the self-heal.
type correctionOutcome int

const (
	// correctionNoop: the configuration already matches what was built. Stay quiet.
	//
	// Deliberately the ZERO value. The alternative, correctionRewrite first, makes
	// "write the user's config.yaml" the outcome you get from a forgotten
	// assignment or a naked return on the named result below, which is the one
	// outcome that must never happen by accident.
	correctionNoop correctionOutcome = iota
	// correctionRewrite: the returned snapshot carries repaired paths and should be
	// stored, persisted, and reported with the reconciled notification.
	correctionRewrite
	// correctionSubstituted: the model is running a substitute, and config must be
	// left alone (the path is user-owned, or the read failure was not a confirmed
	// absence). Emit the substituted notification; write nothing.
	correctionSubstituted
	// correctionUnknownFamily: no settings mapping exists for the registry ID.
	// Developer-facing only; never a user-facing notification.
	correctionUnknownFamily
)

// planPathCorrection produces the corrected settings snapshot for one family and
// reports what should be done with it. It is separated from the persisting half
// of applyPathCorrection so the decision (which fields may be rewritten and which
// must be left alone) is testable without touching the filesystem or the
// developer's own configuration file.
//
// The returned snapshot is meaningful only for correctionRewrite; every other
// outcome returns nil so no caller can mutate the live published snapshot through
// the result.
func (o *Orchestrator) planPathCorrection(current *conf.Settings, pc *pendingPathCorrection) (updated *conf.Settings, outcome correctionOutcome) {
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
	case permanentRegistryID:
		// The primary BirdNET v2.4 slot carries ONE gallery-managed path. Its label
		// set is embedded and identical across variants, which is why
		// applyConfigForPrimarySwap sets BirdNET.ModelPath alone and documents that
		// it never touches BirdNET.LabelPath: a user-configured custom label path
		// must survive a variant swap. Repairing LabelPath here would break that
		// contract, so the primary family is deliberately model-only.
		fields = []fieldCorrection{
			{&updated.BirdNET.ModelPath, pc.resolved.model},
		}
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
		return nil, correctionUnknownFamily
	}

	// A substitution that is not repairable must never reach the field loop: the
	// configured path was not CONFIRMED absent (a permissions failure, an I/O
	// error, a half-initialised mount), so rewriting config.yaml would make a
	// transient condition permanent. The model is still running a substitute, so
	// the user is told; the configuration is left byte-for-byte as they wrote it.
	if !pc.repairable {
		return nil, correctionSubstituted
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
			return nil, correctionSubstituted
		}
		toWrite = append(toWrite, fc)
	}

	if len(toWrite) == 0 {
		return nil, correctionNoop
	}

	for _, fc := range toWrite {
		log.Info("repairing stale gallery model path in configuration",
			logger.String("registry_id", pc.registryID),
			logger.String("old_path", *fc.field),
			logger.String("new_path", fc.resolved))
		*fc.field = fc.resolved
	}

	return updated, correctionRewrite
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

// emitPathSubstitutedNotification tells the user that the model they configured
// is not the model that is running, while their configuration was deliberately
// left unchanged. Without this the substitution is entirely silent: the user
// keeps getting detections, but from a model they never chose, and the reconciled
// notification never fires because config was not rewritten. That is the same
// failure shape as a model that is assigned but never loaded, which cost one
// reporter days of detections before a missing dashboard panel gave it away.
func emitPathSubstitutedNotification(pc *pendingPathCorrection) {
	svc := notification.GetService()
	if svc == nil {
		return
	}

	registryID := pc.registryID
	modelPath := pc.resolved.model

	modelName := registryID
	if info, ok := ModelRegistry[registryID]; ok && info.Name != "" {
		modelName = info.Name
	}

	// Substitutions reaching here are NOT interchangeable to a user trying to fix
	// their install, so each gets its own wording. The unreadable case is carried as
	// an explicit flag rather than derived: "not repairable" has more than one
	// cause, and deriving from it told a user whose file was absent to go and check
	// its permissions.
	//
	//   default, non-empty resolved: the file is CONFIRMED absent and an installed
	//     model replaced it, but config was left alone, either because the
	//     configured path is user-owned or because it is not written in the form it
	//     resolves to. "Was not found" is exactly right for all of those.
	//   unreadable: the file could not be READ (a permissions change, an I/O error,
	//     a half-mounted volume). Telling this user it "was not found" sends them
	//     looking in the wrong place. Keyed on an explicit flag, because "not
	//     repairable" alone also covers a path we simply decline to rewrite.
	//   empty resolved: the primary classifier's configured file is absent and
	//     nothing is installed to replace it, so the BUILT-IN model is running.
	//     There is no installed path to name.
	title := fmt.Sprintf("Configured model file for %s was not found", modelName)
	titleKey := notification.MsgModelPathSubstitutedTitle
	body := fmt.Sprintf("The model file configured for %s was not found on disk, so the installed model at %s "+
		"is being used instead. Your configuration was left unchanged.", modelName, modelPath)
	bodyKey := notification.MsgModelPathSubstitutedMessage

	switch {
	case modelPath == "":
		body = fmt.Sprintf("The model file configured for %s was not found on disk and no installed model is "+
			"available to replace it, so the built-in model is being used instead. Your configuration was "+
			"left unchanged.", modelName)
		bodyKey = notification.MsgModelPathBuiltinMessage
	case pc.unreadable:
		title = fmt.Sprintf("Configured model file for %s could not be read", modelName)
		titleKey = notification.MsgModelPathUnreadableTitle
		body = fmt.Sprintf("The model file configured for %s exists but could not be read, so the installed "+
			"model at %s is being used instead. Your configuration was left unchanged. Check the file's "+
			"permissions and that its storage is mounted.", modelName, modelPath)
		bodyKey = notification.MsgModelPathUnreadableMessage
	}

	notif := notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityMedium,
		title,
		body,
	).
		WithComponent("classifier").
		WithTitleKey(titleKey, map[string]any{
			"modelName": modelName,
		}).
		WithMessageKey(bodyKey, map[string]any{
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

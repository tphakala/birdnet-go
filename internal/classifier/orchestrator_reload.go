// orchestrator_reload.go holds the single transactional model-reload path.
//
// reloadEntry rebuilds one loaded model from the current settings and swaps the fresh
// instance in under entry.mu (build-then-swap), replacing the in-place primary reload
// (BirdNET.reloadModelInternal, held under bn.mu) and unifying it with the per-entry
// secondary reload. Every reload is serialized by o.reloadMu across its whole
// build -> swap -> notify span, so the settings-monitor reload and the API variant-swap
// goroutine can never interleave their swap or their post-swap range-filter rebuild.
//
// The only observable difference from the old in-place path is that inference keeps
// flowing on the OLD instance while the new one builds, instead of blocking on
// bn.mu/entry.mu; a failed build or a refused reload simply never swaps (model
// de-privilege epic, Phase 3).
package classifier

import (
	"runtime/debug"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// reloadCheck lets a family refuse a reload after the new instance is built but before
// the swap (e.g. a v2.4 settings reload that changed the model file, which requires an
// orchestrator restart). Implementations type-assert to their concrete type, since
// ModelInstance carries no family state. A nil check accepts every build.
type reloadCheck func(old, next ModelInstance) error

// reloadOpts controls a single reloadEntry call.
type reloadOpts struct {
	// check, when non-nil, may refuse the reload after the build (the fresh instance is
	// closed and the old one keeps serving).
	check reloadCheck
	// settings, when non-nil, is the pinned snapshot the builder builds from, so a batch
	// caller (ReloadSecondaryModels) keeps every entry (and its batch-derived triplet and
	// thread budget) on ONE snapshot even if settings change concurrently mid-batch. When
	// nil, reloadEntry builds from a private clone of o.currentSettings(); the v2.4 anchor
	// path uses this because NewBirdNET's loadLabels writes BirdNET.Labels into the object it
	// receives and must not mutate the published snapshot other goroutines read.
	settings *conf.Settings
	// backend, when non-nil, is written onto the entry with the swap so the OV-capable
	// secondary triplet gate stays published atomically with the instance it describes.
	backend *secondaryBackendKey
	// skipSpeciesIndex suppresses the post-swap species-index rebuild so a batch caller
	// (ReloadSecondaryModels) can rebuild once after the whole batch.
	skipSpeciesIndex bool
	// threads is the CPU inference thread budget passed to the builder. The v2.4 builder
	// ignores it (NewBirdNET reads settings.BirdNET.Threads); secondary builders use it.
	// ReloadSecondaryModels computes the per-model budget once for the whole batch and
	// passes it here, so reloadEntry does not recompute the allocation per entry.
	threads int
}

// reloadEntry rebuilds one loaded model from the current settings and swaps it in
// transactionally. It returns swapped=true only when the entry's instance was actually
// replaced. Serialized by o.reloadMu for the whole operation.
//
// Lock order: o.reloadMu -> o.rebuildMu -> o.mu -> inferenceMu -> entry.mu -> bn.mu.
// reloadEntry holds o.reloadMu across build, swap and notify; it takes o.mu only to
// snapshot the entry, inferenceMu only for the warm-up, entry.mu only for the swap, and
// calls reloadAnchorRangeFilter (rfs.buildMu -> rfs.mu) and rebuildSpeciesIndex
// (o.rebuildMu -> o.mu) holding only o.reloadMu.
func (o *Orchestrator) reloadEntry(registryID string, build entryBuilder, opts reloadOpts) (swapped bool, err error) {
	o.reloadMu.Lock()
	defer o.reloadMu.Unlock()

	// 1. Snapshot the entry. Refuse if the orchestrator was deleted or the model is not
	//    loaded.
	o.mu.RLock()
	deleted := o.models == nil
	var entry *modelEntry
	if !deleted {
		entry = o.models[registryID]
	}
	o.mu.RUnlock()
	if deleted {
		return false, errors.Newf("orchestrator has been deleted, cannot reload model").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}
	if entry == nil {
		return false, errors.Newf("model %s is not loaded, cannot reload", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("registry_id", registryID).
			Build()
	}

	// 2. Determine the settings snapshot to build from. A batch caller passes one pinned
	//    snapshot for the whole batch via opts.settings, so every entry and the batch-derived
	//    triplet/thread budget stay consistent even if settings change concurrently mid-batch.
	//    The v2.4 anchor path passes none and gets a PRIVATE clone, because NewBirdNET's
	//    loadLabels writes BirdNET.Labels into the object it is handed and must never mutate
	//    the published snapshot other goroutines read.
	settings := opts.settings
	if settings == nil {
		settings = conf.CloneSettings(o.currentSettings())
	}

	// 3. Build holding only o.reloadMu (NOT o.mu / entry.mu / inferenceMu), so the build never
	//    blocks inference on other models. Use the caller-supplied thread budget (opts.threads):
	//    the v2.4 builder ignores it, secondary builders use it, and ReloadSecondaryModels
	//    computes it once for the batch. Capture RSS-before only when we will record it (step 5):
	//    the in-place v2.4 reload never recorded modelRSS, so the anchor skips it.
	recordRSS := registryID != RegistryIDBirdNETV24
	var before uint64
	if recordRSS {
		before = o.captureRSSBefore()
	}
	next, buildErr := build(o, settings, opts.threads)
	if buildErr != nil {
		o.logReloadFailure(registryID, buildErr) // old keeps serving
		return false, buildErr
	}

	// 4. Read the serving instance and run the family refusal check, both unlocked (old
	//    keeps serving). An orphaned entry (a concurrent Delete/Unload) closes next.
	entry.mu.Lock()
	old := entry.instance
	entry.mu.Unlock()
	if old == nil {
		o.closeDiscardedReloadInstance(registryID, next)
		return false, o.orphanReloadResult()
	}
	if opts.check != nil {
		if cerr := opts.check(old, next); cerr != nil {
			o.closeDiscardedReloadInstance(registryID, next)
			o.logReloadFailure(registryID, cerr) // refused; old keeps serving
			return false, cerr
		}
	}

	// 5. Warm up the private instance under inferenceMu, SECONDARIES ONLY. The in-place
	//    v2.4 reload never warmed up or re-recorded RSS (it served the fresh classifier
	//    cold and left modelRSS[v24] at its startup value), so PR3 skips warm-up+RSS for
	//    the v2.4 anchor to stay byte-identical. Secondaries warm up exactly as before,
	//    serialized behind live inference on inferenceMu (bounded by warmupTimeout).
	if recordRSS {
		func() {
			o.inferenceMu.Lock()
			defer o.inferenceMu.Unlock()
			o.warmupAndRecordRSS(registryID, before, next)
		}()
	}

	// 6. Swap under entry.mu. Re-check the orphan guard: a Delete/Unload may have raced
	//    the build/warm-up. Keeping the same *modelEntry preserves its mutex identity and
	//    the globalInferenceCounters keying.
	entry.mu.Lock()
	old = entry.instance
	if old == nil {
		entry.mu.Unlock()
		o.closeDiscardedReloadInstance(registryID, next)
		if recordRSS {
			// Drop the RSS entry recorded in step 5 for the instance we will not publish.
			o.rssMu.Lock()
			delete(o.modelRSS, registryID)
			o.rssMu.Unlock()
		}
		return false, o.orphanReloadResult()
	}
	entry.instance = next
	entry.generation++
	if opts.backend != nil {
		entry.backend = *opts.backend
	}
	dropInferenceFailureStreak(registryID) // fresh instance, fresh streak
	entry.mu.Unlock()

	// Close the replaced instance after releasing entry.mu: native teardown can be slow,
	// and no goroutine can reach the old instance once the swap is published (PredictModel
	// reads entry.instance under entry.mu on every call).
	if cerr := old.Close(); cerr != nil {
		GetLogger().Warn("failed to close old model instance after reload",
			logger.String("registry_id", registryID),
			logger.Error(cerr))
	}

	// 7. Family follow-ups, holding only o.reloadMu. The v2.4 anchor publishes its
	//    reloaded settings + labels, re-emits the missing-taxonomy diagnostics and hints
	//    the GC to release the closed model's native pages, exactly as
	//    reloadBirdNETV24InPlace + BirdNET.ReloadModel did; then the range filter is
	//    rebuilt against the anchor (non-fatal). Secondaries do none of this: a
	//    backend/device swap keeps the label set, and ReloadSecondaryModels reads the
	//    settings the anchor reload already published.
	if bn, ok := next.(*BirdNET); ok {
		o.updateSettings(bn.currentSettings())
		o.logMissingTaxonomyCodes(bn, bn.Labels())
		debug.FreeOSMemory()
	}
	if registryID == RegistryIDBirdNETV24 {
		o.reloadAnchorRangeFilter()
	}
	if !opts.skipSpeciesIndex {
		o.rebuildSpeciesIndex()
	}
	return true, nil
}

// closeDiscardedReloadInstance closes a freshly built instance that will not be
// published (a refusal or an orphaned entry), logging a close failure rather than
// leaking the native session.
func (o *Orchestrator) closeDiscardedReloadInstance(registryID string, inst ModelInstance) {
	if cerr := inst.Close(); cerr != nil {
		GetLogger().Warn("failed to close unpublished model instance after reload",
			logger.String("registry_id", registryID),
			logger.Error(cerr))
	}
}

// orphanReloadResult reports why an in-flight reload found its entry detached at swap time.
// If the whole orchestrator was torn down (o.models == nil, a Delete racing the build/warm-up),
// it returns the "orchestrator has been deleted" error the former in-place reload returned from
// its post-reload re-check, so a caller relying on the error (a settings save) is not told the
// reload succeeded when nothing was reloaded. A single model unloaded out from under the reload
// (o.models != nil) is benign: there is nothing to reload and no error.
func (o *Orchestrator) orphanReloadResult() error {
	o.mu.RLock()
	deleted := o.models == nil
	o.mu.RUnlock()
	if deleted {
		return errors.Newf("orchestrator has been deleted, cannot reload model").
			Component("classifier.orchestrator").
			Category(errors.CategorySystem).
			Build()
	}
	return nil
}

// logReloadFailure emits the operational warning that the in-place rollback logged, so a
// failed reload that left the previous model serving is not mistaken for a dead detector.
// Gated to the v2.4 anchor: ReloadSecondaryModels logs its own per-entry build failure, so
// logging here for a secondary would double up.
func (o *Orchestrator) logReloadFailure(registryID string, err error) {
	if registryID != RegistryIDBirdNETV24 {
		return
	}
	GetLogger().Warn("BirdNET model reload failed; keeping the previous model instance",
		logger.String("model_id", registryID),
		logger.Error(err))
}

// v24ReloadBuilder builds a fresh BirdNET v2.4 instance from the reload's private settings
// clone. It discards the pathResolution (a reload never rewrites paths, like the secondary
// builders) and ignores the thread count (NewBirdNET reads settings.BirdNET.Threads).
func v24ReloadBuilder(o *Orchestrator, settings *conf.Settings, _ int) (ModelInstance, error) {
	bn, _, err := o.buildBirdNETV24(settings)
	if err != nil {
		// Tag the failure operation=reload_model so a build error during a hot reload
		// (unreadable labels, a bad model file, an unknown birdnet.version) keeps the same
		// telemetry grouping the deleted in-place reload attached; NewBirdNET's own error
		// context does not carry it. The underlying message (e.g. "unknown BirdNET version:
		// X") is preserved.
		return nil, errors.New(err).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("operation", "reload_model").
			Build()
	}
	return bn, nil
}

// variantSwapBuilder returns the build-then-swap builder for a within-model variant
// swap of registryID: the v2.4 anchor builder for the permanent model, else the
// OV-capable secondary builder. ok=false for a family with no reload builder (Bat is
// single-variant so it never reaches a swap; BSG has no loader), so a swap of an
// unbuildable loaded family is refused before anything is swapped or unloaded.
func (o *Orchestrator) variantSwapBuilder(registryID string) (entryBuilder, bool) {
	if registryID == RegistryIDBirdNETV24 {
		return v24ReloadBuilder, true
	}
	build, ok := openvinoCapableSecondaryBuilders[registryID]
	return build, ok
}

// ReloadForVariantSwap rebuilds the loaded registryID model for a within-model variant
// swap (the gallery variant / "optimize" flow), accepting a changed or cleared model
// file that a settings hot-reload refuses: it passes no reloadCheck, so reloadEntry
// build-then-swaps the new variant and a failed build leaves the previous variant
// serving (gapless). Every family's within-model swap goes through this one path; it
// replaces the v2.4-only ReloadPrimaryForVariantSwap. reloadEntry clones the currently
// published settings, which ModelManager.replaceVariant has already updated with the new
// variant's paths, so the builder resolves the new file. The v2.4 builder ignores the
// thread budget (NewBirdNET reads settings.BirdNET.Threads); secondary builders use it.
func (o *Orchestrator) ReloadForVariantSwap(registryID string) error {
	build, ok := o.variantSwapBuilder(registryID)
	if !ok {
		return errors.Newf("model %s has no variant-swap reload builder", registryID).
			Component("classifier.orchestrator").
			Category(errors.CategoryValidation).
			Context("registry_id", registryID).
			Build()
	}
	_, err := o.reloadEntry(registryID, build, reloadOpts{
		threads: o.computeThreadAllocation(o.currentSettings())[registryID],
	})
	return err
}

// v24SettingsReloadCheck reproduces the reachable settings-reload refusals of the former
// reloadModelInternal(false) with byte-identical error texts and telemetry context. old and
// next are the serving and freshly built v2.4 instances.
//
// The switch mirrors reloadModelInternal's switch exactly, keyed on the RESOLVED model path
// (BirdNET.configuredModelPath() == primaryPath.resolved.model). Keying on the resolved path
// rather than a bare CustomPath comparison is load-bearing: clearing or losing the model file
// leaves next resolved onto the embedded baseline (np.resolved.model == "") with an empty
// CustomPath, which a bare CustomPath!=CustomPath case would misreport as "birdnet model file
// changed" instead of "no longer usable".
//
// One accepted deviation from strict byte-parity: with BOTH birdnet.version and a custom
// birdnet.modelpath set (an unusual dual config), editing the path emits "birdnet model file
// changed" here rather than the old version-branch's "model identity changed from BirdNET_V2.4
// to BirdNET_V2.4" (a clearer message). Both refuse identically and keep the old model serving,
// so the decision and the user outcome are unchanged.
func v24SettingsReloadCheck(old, next ModelInstance) error {
	ob, oOK := old.(*BirdNET)
	nb, nOK := next.(*BirdNET)
	if !oOK || !nOK {
		// Unreachable: the check is only wired to the v2.4 anchor. Accept rather than panic.
		return nil
	}
	oi, op := ob.identitySnapshot()
	ni, np := nb.identitySnapshot()

	switch {
	case ni.ID != oi.ID:
		// Unreachable in Phase 3: a fresh NewBirdNET only yields BirdNET_V2.4 (Tier 3/4) or
		// fails to build (Tier 2 other versions), so a successful build never changes the ID.
		// Kept to preserve reloadModelInternal's identity-change text for Phase 4, when the
		// range-filter anchor may be a different model.
		return errors.Newf("model identity changed from %s to %s: requires orchestrator restart", oi.ID, ni.ID).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("operation", "reload_model").
			Context("current_model", oi.ID).
			Context("requested_model", ni.ID).
			Build()
	case np.resolved.model != "":
		// A real model file is running: refuse only when the resolved CustomPath differs from
		// the serving one (a genuine file change). A recovered stale path resolves to the same
		// CustomPath and is accepted (the reload proceeds to the label/init steps).
		if ni.CustomPath != oi.CustomPath {
			return errors.Newf("birdnet model file changed from %q to %q: requires orchestrator restart", oi.CustomPath, ni.CustomPath).
				Component("birdnet").
				Category(errors.CategoryModelInit).
				Context("operation", "reload_model").
				Context("current_model_path", oi.CustomPath).
				Context("requested_model_path", ni.CustomPath).
				Build()
		}
	case op.resolved.model != "" && np.resolved.model == "":
		// The file this instance was RUNNING is gone from the configuration: the previous
		// resolution named a real file, this one resolves to the embedded baseline. Refuse so
		// the settings save fails loudly (the caller restarts the orchestrator) rather than
		// silently swapping the running detector onto the baseline.
		return errors.Newf("configured birdnet model file %q is no longer usable and no installed variant can replace it: requires orchestrator restart", nb.currentSettings().BirdNET.ModelPath).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("operation", "reload_model").
			Context("current_model_path", oi.CustomPath).
			Context("configured_model_path", nb.currentSettings().BirdNET.ModelPath).
			Build()
	}
	return nil
}

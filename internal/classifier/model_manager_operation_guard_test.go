package classifier

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// opWait bounds how long a guard test waits for a background operation or a held
// request to be reached.
const opWait = 10 * time.Second

// runAsync runs fn in a goroutine and returns a channel that yields its error.
func runAsync(fn func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	return done
}

// awaitSignal fails the test when ch is not closed within opWait.
func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(opWait):
		require.FailNow(t, "timed out waiting for "+what)
	}
}

// awaitResult returns the error an async operation finished with.
func awaitResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(opWait):
		require.FailNow(t, "timed out waiting for the background operation")
		return nil
	}
}

// requireBusy asserts err is an *OperationInProgressError for target while the
// operation op on running holds the slot.
func requireBusy(t *testing.T, err error, target string, op ModelOperation, running string) {
	t.Helper()
	busy, ok := errors.AsType[*OperationInProgressError](err)
	require.True(t, ok, "want *OperationInProgressError, got %v", err)
	assert.Equal(t, target, busy.CatalogID)
	assert.Equal(t, running, busy.Running.ID)
	assert.NotEmpty(t, busy.Running.Name)
	assert.Equal(t, op, busy.Operation)
}

// requireSlotFree asserts the operation slot is free, then releases it.
func requireSlotFree(t *testing.T, mm *ModelManager, msg string) {
	t.Helper()
	l, err := mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err, msg)
	l.Release()
}

// opStateSnapshot captures everything a refused operation must leave unchanged.
type opStateSnapshot struct {
	installed   map[string]InstalledModel
	downloading map[string]DownloadState
	files       map[string]int64
}

func (h *depHarness) opState() opStateSnapshot {
	h.t.Helper()
	snap := opStateSnapshot{
		installed:   installedSnapshot(h.mm),
		downloading: map[string]DownloadState{},
		files:       map[string]int64{},
	}
	h.mm.mu.RLock()
	for id, st := range h.mm.downloading {
		snap.downloading[id] = *st
	}
	h.mm.mu.RUnlock()
	require.NoError(h.t, filepath.WalkDir(h.modelsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		snap.files[path] = info.Size()
		return nil
	}))
	return snap
}

func TestOperationGuard_EveryEntryPointRefusesWhileSlotHeld(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))
	a, b, p := h.entry(depIDA), h.entry(depIDB), h.entry(depIDP)

	lease, err := h.mm.BeginOperation(OperationInstall, depIDB)
	require.NoError(t, err)
	defer lease.Release()
	ctx := t.Context()

	cases := map[string]func() error{
		"Install fresh":                  func() error { return h.mm.Install(ctx, &b, "", h.srv.URL, nil) },
		"InstallOrReplace fresh":         func() error { return h.mm.InstallOrReplace(ctx, &b, "", h.srv.URL, nil) },
		"InstallOrReplace swap":          func() error { return h.mm.InstallOrReplace(ctx, &p, depVariantDFT, h.srv.URL, nil) },
		"InstallOrReplace same variant":  func() error { return h.mm.InstallOrReplace(ctx, &p, depVariantBuiltin, h.srv.URL, nil) },
		"Reinstall":                      func() error { return h.mm.Reinstall(ctx, &a, h.srv.URL, nil) },
		"Uninstall":                      func() error { return h.mm.Uninstall(depIDA) },
		"Uninstall with a dependent":     func() error { return h.mm.Uninstall(depIDG) },
		"Uninstall of an uninstalled ID": func() error { return h.mm.Uninstall(depIDB) },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			before := h.opState()
			err := call()
			requireBusy(t, err, targetOf(name), OperationInstall, depIDB)
			assert.Equal(t, before, h.opState(), "a refused call changes nothing")
		})
	}
}

// targetOf maps a EveryEntryPointRefusesWhileSlotHeld case name to the catalog ID
// the case targets.
func targetOf(name string) string {
	switch name {
	case "Install fresh", "InstallOrReplace fresh", "Uninstall of an uninstalled ID":
		return depIDB
	case "InstallOrReplace swap", "InstallOrReplace same variant":
		return depIDP
	case "Uninstall with a dependent":
		return depIDG
	default:
		return depIDA
	}
}

func TestOperationGuard_DependentInstallDuringDirectDependencyInstall(t *testing.T) {
	h := newDepHarness(t)
	reached, release := h.srv.Hold("g-labels.txt")
	gDone := runAsync(func() error { return h.install(depIDG, "") })
	awaitSignal(t, reached, "the geomodel download")

	requireBusy(t, h.install(depIDA, ""), depIDA, OperationInstall, depIDG)
	assert.NoFileExists(t, h.own(depIDA, depIDA+"-model.onnx"), "the refused install wrote nothing")

	release(true)
	require.Error(t, awaitResult(t, gDone), "the geomodel install fails on its labels")

	require.NoError(t, h.install(depIDA, ""), "the failed install must not hold the slot for its retention window")
	got := installedSnapshot(h.mm)
	assert.Contains(t, got, depIDG)
	assert.Contains(t, got, depIDT)
	h.assertDependencyInvariants(t, "dependent after failed direct dependency install")
}

func TestOperationGuard_UninstallDuringInstallSeesNoTransientDependent(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	reached, release := h.srv.Hold(depIDA + "-model.onnx")
	aDone := runAsync(func() error { return h.install(depIDA, "") })
	awaitSignal(t, reached, "A's model download")

	requireBusy(t, h.mm.Uninstall(depIDG), depIDG, OperationInstall, depIDA)

	release(true)
	require.Error(t, awaitResult(t, aDone))
	require.NoError(t, h.mm.Uninstall(depIDG), "A never got installed, so G has no dependent")
	assert.NoFileExists(t, h.shared(depLocalGeoModel))
	assert.NoFileExists(t, h.shared(depLocalGeoLabels))
}

func TestOperationGuard_UninstallDuringSwapSeesNoTransientDependent(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	b, g := h.entry(depIDB), h.entry(depIDG)
	reached, release := h.srv.Hold("p-dft.onnx")
	swapDone := runAsync(func() error { return h.install(depIDP, depVariantDFT) })
	awaitSignal(t, reached, "the DFT variant download")

	before := h.opState()
	requireBusy(t, h.mm.Uninstall(depIDG), depIDG, OperationInstall, depIDP)
	requireBusy(t, h.mm.Install(t.Context(), &b, "", h.srv.URL, nil), depIDB, OperationInstall, depIDP)
	requireBusy(t, h.mm.Reinstall(t.Context(), &g, h.srv.URL, nil), depIDG, OperationInstall, depIDP)
	assert.Equal(t, before.installed, h.opState().installed, "refused calls change nothing")

	release(false)
	require.NoError(t, awaitResult(t, swapDone))
	assert.True(t, h.expectRefusal(depIDG), "P is now installed with a DFT record and needs G")
	requireDependents(t, h.mm.Uninstall(depIDG), depIDG, depIDP)
}

func TestOperationGuard_FailedOperationReleasesSlotImmediately(t *testing.T) {
	h := newDepHarness(t)
	h.srv.Fail("g-labels.txt")

	require.Error(t, h.install(depIDG, ""))

	requireSlotFree(t, h.mm, "a failed install frees the slot at once")
	state := h.mm.GetDownloadState(depIDG)
	require.NotNil(t, state, "the failed state is still retained for SSE pollers")
	assert.Equal(t, StatusFailed, state.Status)
}

func TestOperationGuard_ContextCancelReleasesSlot(t *testing.T) {
	h := newDepHarness(t)
	a := h.entry(depIDA)
	reached, _ := h.srv.Hold(depIDA + "-model.onnx")
	ctx, cancel := context.WithCancel(t.Context())
	done := runAsync(func() error { return h.mm.Install(ctx, &a, "", h.srv.URL, nil) })
	awaitSignal(t, reached, "A's model download")

	cancel()

	require.Error(t, awaitResult(t, done))
	requireSlotFree(t, h.mm, "a cancelled install frees the slot")
	state := h.mm.GetDownloadState(depIDA)
	require.NotNil(t, state)
	assert.False(t, state.IsActive(), "the cancelled install leaves no active state")
}

func TestOperationGuard_ReleaseAfterPanicFreesSlotAndFailsActiveState(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	lease, err := h.mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err)
	h.setDownloading(depIDA, StatusDownloading)

	func() {
		defer func() { _ = recover() }()
		defer lease.Release()
		panic("boom")
	}()

	requireSlotFree(t, h.mm, "the deferred release frees the slot after a panic")
	state := h.mm.GetDownloadState(depIDA)
	require.NotNil(t, state)
	assert.Equal(t, StatusFailed, state.Status)
	assert.False(t, state.IsActive())
	assert.NoError(t, h.mm.Uninstall(depIDG), "a ghost active state must not count as a dependent")
}

func TestOperationGuard_PanicInsideEntryPointFreesSlotAndFailsState(t *testing.T) {
	cases := map[string]func(h *depHarness, e *CatalogEntry) error{
		"Install": func(h *depHarness, e *CatalogEntry) error {
			return h.mm.Install(t.Context(), e, "", h.srv.URL, nil)
		},
		"InstallOrReplace": func(h *depHarness, e *CatalogEntry) error {
			return h.mm.InstallOrReplace(t.Context(), e, "", h.srv.URL, nil)
		},
		"lease InstallOrReplace": func(h *depHarness, e *CatalogEntry) error {
			lease, err := h.mm.BeginOperation(OperationInstall, e.ID)
			require.NoError(t, err)
			return lease.InstallOrReplace(t.Context(), e, "", h.srv.URL, nil)
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			h := newDepHarness(t)
			a := h.entry(depIDA)
			h.mm.freeSpaceFn = func(string) (uint64, error) { panic("boom") }

			assert.Panics(t, func() { _ = call(h, &a) })

			requireSlotFree(t, h.mm, "a panic inside "+name+" must not keep the slot")
			state := h.mm.GetDownloadState(depIDA)
			require.NotNil(t, state)
			assert.Equal(t, StatusFailed, state.Status)
			assert.Equal(t, operationEndedUnexpectedlyMsg, state.Error)
		})
	}
}

func TestOperationGuard_ReleaseIsIdempotentAndOwnerOnly(t *testing.T) {
	h := newDepHarness(t)
	l1, err := h.mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err)
	l1.Release()
	l2, err := h.mm.BeginOperation(OperationReinstall, depIDB)
	require.NoError(t, err)

	l1.Release()

	_, err = h.mm.BeginOperation(OperationInstall, depIDA)
	requireBusy(t, err, depIDA, OperationReinstall, depIDB)

	// A stale lease reaching releaseLocked directly (not through the Once) must not
	// free the slot a newer lease holds.
	h.mm.mu.Lock()
	h.mm.opMu.Lock()
	h.mm.releaseLocked(l1)
	h.mm.opMu.Unlock()
	h.mm.mu.Unlock()
	_, err = h.mm.BeginOperation(OperationInstall, depIDA)
	requireBusy(t, err, depIDA, OperationReinstall, depIDB)
	l2.Release()
	requireSlotFree(t, h.mm, "the owner's release frees the slot")
}

func TestOperationGuard_LeaseRejectsMismatchedOrReleasedUse(t *testing.T) {
	h := newDepHarness(t)
	a, b := h.entry(depIDA), h.entry(depIDB)
	ctx := t.Context()

	notRunnable := func(err error) {
		t.Helper()
		require.Error(t, err)
		_, isBusy := errors.AsType[*OperationInProgressError](err)
		assert.False(t, isBusy)
		assert.Empty(t, h.opState().downloading, "no download state registered")
		assert.Empty(t, installedSnapshot(h.mm))
	}

	wrongEntry, err := h.mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err)
	notRunnable(wrongEntry.InstallOrReplace(ctx, &b, "", h.srv.URL, nil))

	wrongOp, err := h.mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err)
	notRunnable(wrongOp.Reinstall(ctx, &a, h.srv.URL, nil))

	released, err := h.mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err)
	released.Release()
	notRunnable(released.InstallOrReplace(ctx, &a, "", h.srv.URL, nil))
	requireSlotFree(t, h.mm, "every lease method released its slot")
}

func TestOperationGuard_BeginOperationValidatesArguments(t *testing.T) {
	h := newDepHarness(t)

	_, err := h.mm.BeginOperation(OperationInstall, "")
	require.Error(t, err)
	_, err = h.mm.BeginOperation(ModelOperation("bogus"), depIDA)
	require.Error(t, err)
	requireSlotFree(t, h.mm, "a rejected begin takes no slot")
}

func TestOperationGuard_UninstallReleasesOnEveryPath(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))

	requireDependents(t, h.mm.Uninstall(depIDG), depIDG, depIDA)
	requireSlotFree(t, h.mm, "after a dependents refusal")

	require.Error(t, h.mm.Uninstall(depIDB), "B is not installed")
	requireSlotFree(t, h.mm, "after a not-installed refusal")

	require.NoError(t, h.mm.Uninstall(depIDA))
	requireSlotFree(t, h.mm, "after a successful uninstall")
}

func TestOperationGuard_ScanInstalledNotBlockedBySlot(t *testing.T) {
	h := newDepHarness(t)
	h.writeShared(depLocalTaxonomy, "t-taxonomy.csv")
	lease, err := h.mm.BeginOperation(OperationInstall, depIDA)
	require.NoError(t, err)
	defer lease.Release()

	done := runAsync(func() error { h.mm.ScanInstalled(); return nil })

	require.NoError(t, awaitResult(t, done))
	assert.Contains(t, installedSnapshot(h.mm), depIDT, "the scan recorded disk state while the slot was held")
	requireSlotFree(t, h.newManager(), "a fresh manager starts with a free slot")
}

func TestOperationGuard_OperationRunningFor(t *testing.T) {
	t.Parallel()
	mm := NewModelManager(t.TempDir(), nil, nil)
	assert.False(t, mm.OperationRunningFor("a"), "a free slot runs nothing")

	lease, err := mm.BeginOperation(OperationInstall, "a")
	require.NoError(t, err)
	assert.True(t, mm.OperationRunningFor("a"))
	assert.False(t, mm.OperationRunningFor("b"), "the slot is held for another entry")

	lease.Release()
	assert.False(t, mm.OperationRunningFor("a"), "a released slot runs nothing")
}

func TestOperationGuard_RefusesWhileManagerLockHeld(t *testing.T) {
	t.Parallel()
	mm := NewModelManager(t.TempDir(), nil, nil)
	lease, err := mm.BeginOperation(OperationUninstall, "a")
	require.NoError(t, err)
	t.Cleanup(lease.Release)

	// Uninstall holds mm.mu for its whole body. A request made meanwhile must be
	// refused at once, not wait on the lock and run after the uninstall.
	mm.mu.Lock()
	defer mm.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := mm.BeginOperation(OperationInstall, "b")
		done <- err
	}()
	select {
	case err := <-done:
		var busy *OperationInProgressError
		require.ErrorAs(t, err, &busy)
		assert.Equal(t, OperationUninstall, busy.Operation)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "BeginOperation blocked on the manager lock instead of refusing")
	}
	assert.True(t, mm.OperationRunningFor("a"), "the slot stays readable while the manager lock is held")
}

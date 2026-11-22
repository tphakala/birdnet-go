package classifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Synthetic entries for the cancel tests: a flat two-variant entry whose variants
// each own a model and a labels file, and a permanent entry with a BuiltIn baseline
// and one downloaded variant that depends on nothing.
const (
	cancelEntryID  = "cancel-multi"
	cancelVarA     = "va"
	cancelVarB     = "vb"
	cancelPermID   = "cancel-perm"
	cancelPermVar  = "vdl"
	cancelPermBase = "vbase"
)

func cancelCatalog() []CatalogEntry {
	variant := func(id string, isDefault bool) CatalogVariant {
		return CatalogVariant{
			ID: id, Default: isDefault, Precision: "fp32",
			Files: []CatalogFile{
				depFile(RoleModel, id+"-model.onnx", id+"-model.onnx"),
				depFile(RoleLabels, id+"-labels.txt", id+"-labels.txt"),
			},
		}
	}
	return []CatalogEntry{
		{
			ID: cancelEntryID, Name: "Cancel multi", Version: "1.0", Category: CategoryBird,
			RegistryID: RegistryIDPerchV2, HuggingFaceRepo: "t/cancel",
			Variants: []CatalogVariant{variant(cancelVarA, true), variant(cancelVarB, false)},
		},
		{
			ID: cancelPermID, Name: "Cancel permanent", Version: "2.4", Category: CategoryBird,
			RegistryID: RegistryIDBirdNETV24, HuggingFaceRepo: "t/perm",
			Variants: []CatalogVariant{
				{ID: cancelPermBase, BuiltIn: true, Default: true},
				{ID: cancelPermVar, Precision: "fp32", Files: []CatalogFile{depFile(RoleModel, "perm.onnx", "perm.onnx")}},
			},
		},
	}
}

// requireCancelled asserts the entry's retained state is cancelled with no error.
func requireCancelled(t *testing.T, mm *ModelManager, id string) {
	t.Helper()
	state := mm.GetDownloadState(id)
	require.NotNil(t, state, "the cancelled state is retained for SSE pollers")
	assert.Equal(t, StatusCancelled, state.Status)
	assert.Empty(t, state.Error, "a cancel is not a failure")
	assert.False(t, state.IsActive())
}

// requireNoTempFiles asserts no *.tmp file is left under dir.
func requireNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		assert.False(t, strings.HasSuffix(p, ".tmp"), "leftover temp file %s", p)
		return nil
	}))
}

// startHeldSwap installs variant from, then starts the swap to variant to in the
// background with the download of heldRemote parked. It returns the swap's result
// channel once the held request has arrived, and the release func of the hold.
func (h *depHarness) startHeldSwap(from, to, heldRemote string) (done <-chan error, release func(bool)) {
	h.t.Helper()
	require.NoError(h.t, h.install(cancelEntryID, from))
	e := h.entry(cancelEntryID)
	reached, release := h.srv.Hold(heldRemote)
	done = runAsync(func() error {
		return h.mm.InstallOrReplace(h.t.Context(), &e, to, h.srv.URL, nil)
	})
	awaitSignal(h.t, reached, heldRemote)
	return done, release
}

func TestCancelOperation_UpdateKeepsOldVariant(t *testing.T) {
	h := newDepHarnessWithCatalog(t, cancelCatalog())
	done, _ := h.startHeldSwap(cancelVarA, cancelVarB, cancelVarB+"-labels.txt")
	require.FileExists(t, h.own(cancelEntryID, cancelVarB+"-model.onnx"), "B's model finished before the held labels")

	require.NoError(t, h.mm.CancelOperation(cancelEntryID))

	err := awaitResult(t, done)
	require.ErrorIs(t, err, ErrOperationCancelled)
	requireCancelled(t, h.mm, cancelEntryID)
	requireSlotFree(t, h.mm, "a cancelled swap frees the slot")
	variant, _ := h.mm.InstalledVariantID(cancelEntryID)
	assert.Equal(t, cancelVarA, variant, "the old variant stays installed")
	assert.FileExists(t, h.own(cancelEntryID, cancelVarA+"-model.onnx"))
	assert.FileExists(t, h.own(cancelEntryID, cancelVarA+"-labels.txt"))
	assert.NoFileExists(t, h.own(cancelEntryID, cancelVarB+"-model.onnx"), "the cancelled call's finished file is removed")
	requireNoTempFiles(t, h.modelsDir)
}

func TestCancelOperation_RetryAndUninstallStartAtOnce(t *testing.T) {
	h := newDepHarnessWithCatalog(t, cancelCatalog())
	e := h.entry(cancelEntryID)

	// Cancel a fresh install, then install again at once.
	reached, _ := h.srv.Hold(cancelVarA + "-model.onnx")
	first := runAsync(func() error { return h.mm.Install(t.Context(), &e, cancelVarA, h.srv.URL, nil) })
	awaitSignal(t, reached, "the first install's model download")
	require.NoError(t, h.mm.CancelOperation(cancelEntryID))
	require.ErrorIs(t, awaitResult(t, first), ErrOperationCancelled)
	requireCancelled(t, h.mm, cancelEntryID)

	reached, release := h.srv.Hold(cancelVarA + "-model.onnx")
	second := runAsync(func() error { return h.mm.Install(t.Context(), &e, cancelVarA, h.srv.URL, nil) })
	awaitSignal(t, reached, "the retry's model download")
	state := h.mm.GetDownloadState(cancelEntryID)
	require.NotNil(t, state)
	assert.Equal(t, StatusDownloading, state.Status, "the retry's state replaced the retained cancelled one")
	release(false)
	require.NoError(t, awaitResult(t, second), "an install right after a cancel is not refused")

	// Cancel an update, then uninstall at once.
	reached, _ = h.srv.Hold(cancelVarB + "-labels.txt")
	swap := runAsync(func() error { return h.mm.InstallOrReplace(t.Context(), &e, cancelVarB, h.srv.URL, nil) })
	awaitSignal(t, reached, "the update's labels download")
	require.NoError(t, h.mm.CancelOperation(cancelEntryID))
	require.ErrorIs(t, awaitResult(t, swap), ErrOperationCancelled)
	assert.NoError(t, h.mm.Uninstall(cancelEntryID), "an uninstall right after a cancel is not refused")
}

// TestExpireDownloadState_KeepsNewerState pins the pointer check of the retention
// expiry: the timer retainTerminalState schedules calls expireDownloadState with the
// state it captured, and that call must leave a newer state alone.
func TestExpireDownloadState_KeepsNewerState(t *testing.T) {
	mm := NewModelManager(t.TempDir(), nil, nil)
	old := &DownloadState{CatalogID: "m", Status: StatusCancelled}
	newer := &DownloadState{CatalogID: "m", Status: StatusDownloading}

	mm.mu.Lock()
	mm.downloading["m"] = newer
	mm.mu.Unlock()
	mm.expireDownloadState("m", old)
	require.NotNil(t, mm.GetDownloadState("m"))
	assert.Equal(t, StatusDownloading, mm.GetDownloadState("m").Status, "an expiry for an old state leaves the newer one")

	mm.expireDownloadState("m", newer)
	assert.Nil(t, mm.GetDownloadState("m"), "an expiry for the current state removes it")
}

func TestCancelOperation_Refusals(t *testing.T) {
	cases := map[string]func(t *testing.T, mm *ModelManager) context.Context{
		"no operation": func(*testing.T, *ModelManager) context.Context { return t.Context() },
		"reinstall lease": func(t *testing.T, mm *ModelManager) context.Context {
			t.Helper()
			lease, err := mm.BeginOperation(OperationReinstall, cancelEntryID)
			require.NoError(t, err)
			t.Cleanup(lease.Release)
			return t.Context()
		},
		"install before the download state exists": func(t *testing.T, mm *ModelManager) context.Context {
			t.Helper()
			lease, err := mm.BeginOperation(OperationInstall, cancelEntryID)
			require.NoError(t, err)
			t.Cleanup(lease.Release)
			ctx, cancel := lease.cancellableContext(t.Context())
			t.Cleanup(func() { cancel(nil) })
			return ctx
		},
		"install after activation started": func(t *testing.T, mm *ModelManager) context.Context {
			t.Helper()
			lease, err := mm.BeginOperation(OperationInstall, cancelEntryID)
			require.NoError(t, err)
			t.Cleanup(lease.Release)
			ctx, cancel := lease.cancellableContext(t.Context())
			t.Cleanup(func() { cancel(nil) })
			mm.markCancellable(cancelEntryID)
			require.NoError(t, mm.enterActivation(ctx, cancelEntryID))
			return ctx
		},
		"other entry's install": func(t *testing.T, mm *ModelManager) context.Context {
			t.Helper()
			lease, err := mm.BeginOperation(OperationInstall, cancelPermID)
			require.NoError(t, err)
			t.Cleanup(lease.Release)
			ctx, cancel := lease.cancellableContext(t.Context())
			t.Cleanup(func() { cancel(nil) })
			mm.markCancellable(cancelPermID)
			return ctx
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newDepHarnessWithCatalog(t, cancelCatalog())
			ctx := setup(t, h.mm)

			err := h.mm.CancelOperation(cancelEntryID)

			_, refused := errors.AsType[*OperationNotCancellableError](err)
			assert.True(t, refused, "want *OperationNotCancellableError, got %v", err)
			require.NoError(t, ctx.Err(), "a refused cancel leaves the operation untouched")
		})
	}

	t.Run("accepted once the download state exists", func(t *testing.T) {
		h := newDepHarnessWithCatalog(t, cancelCatalog())
		lease, err := h.mm.BeginOperation(OperationInstall, cancelEntryID)
		require.NoError(t, err)
		t.Cleanup(lease.Release)
		ctx, cancel := lease.cancellableContext(t.Context())
		t.Cleanup(func() { cancel(nil) })
		h.mm.markCancellable(cancelEntryID)

		require.NoError(t, h.mm.CancelOperation(cancelEntryID))

		require.ErrorIs(t, context.Cause(ctx), ErrOperationCancelled)
		assert.ErrorIs(t, h.mm.enterActivation(ctx, cancelEntryID), ErrOperationCancelled, "activation refuses to start after an accepted cancel")
	})
}

func TestCancelOperation_BuiltInSwap(t *testing.T) {
	h := newDepHarnessWithCatalog(t, cancelCatalog())
	require.NoError(t, h.install(cancelPermID, cancelPermVar))
	e := h.entry(cancelPermID)
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(ErrOperationCancelled)

	err := h.mm.InstallOrReplace(ctx, &e, cancelPermBase, h.srv.URL, nil)

	require.ErrorIs(t, err, ErrOperationCancelled)
	state := h.mm.GetDownloadState(cancelPermID)
	require.NotNil(t, state)
	assert.Equal(t, StatusCancelled, state.Status, "a cancelled BuiltIn swap is cancelled, not ended unexpectedly")
	assert.Empty(t, state.Error)
	variant, _ := h.mm.InstalledVariantID(cancelPermID)
	assert.Equal(t, cancelPermVar, variant, "the old variant stays installed")
	requireSlotFree(t, h.mm, "the slot is free")
}

// TestCancelledDownloadErrorsAreSuppressible pins that the errors built while a
// download is interrupted by its context are expected interruptions for telemetry:
// every EnhancedError is reported when built, so the inner errors, not only the one
// the install returns, must be suppressible.
func TestCancelledDownloadErrorsAreSuppressible(t *testing.T) {
	requireSuppressible := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		ee, ok := errors.AsType[*errors.EnhancedError](err)
		require.True(t, ok, "want an EnhancedError in the chain of %v", err)
		assert.True(t, errors.IsSuppressibleOperationalError(ee), "a user cancel must not reach telemetry: %v", err)
		assert.ErrorIs(t, err, ErrOperationCancelled)
	}

	t.Run("getHuggingFace with a cancelled context", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
		t.Cleanup(srv.Close)
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(ErrOperationCancelled)

		resp, err := getHuggingFace(ctx, downloadHTTPClient, srv.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}

		requireSuppressible(t, err)
		assert.False(t, shouldFailover(err))
	})

	t.Run("downloadFile cancelled mid-body", func(t *testing.T) {
		url, reached, _ := heldBodyServer(t)
		mm, _ := newFailoverManager(t, nil)
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		done := runAsync(func() error {
			return mm.downloadFile(ctx, "m", url, filepath.Join(t.TempDir(), "model.onnx"), "", 0)
		})
		awaitSignal(t, reached, "the body to start streaming")

		cancel(ErrOperationCancelled)

		err := awaitResult(t, done)
		requireSuppressible(t, err)
		assert.False(t, shouldFailover(err))
	})

	t.Run("the error an install returns", func(t *testing.T) {
		h := newDepHarnessWithCatalog(t, cancelCatalog())
		done, _ := h.startHeldSwap(cancelVarA, cancelVarB, cancelVarB+"-labels.txt")
		require.NoError(t, h.mm.CancelOperation(cancelEntryID))

		requireSuppressible(t, awaitResult(t, done))
	})
}

// TestCancelOperation_RefusedAfterFailure pins that the failure path closes the
// cancel window: a cancel that arrives after the download already failed is refused
// (409) instead of being accepted for an operation that ends failed.
func TestCancelOperation_RefusedAfterFailure(t *testing.T) {
	h := newDepHarnessWithCatalog(t, cancelCatalog())
	lease, err := h.mm.BeginOperation(OperationInstall, cancelEntryID)
	require.NoError(t, err)
	t.Cleanup(lease.Release)
	ctx, cancel := lease.cancellableContext(t.Context())
	t.Cleanup(func() { cancel(nil) })
	h.setDownloading(cancelEntryID, StatusDownloading)
	h.mm.markCancellable(cancelEntryID)

	settled := h.mm.settleDownloadFailure(ctx, cancelEntryID, errors.NewStd("disk full"), nil)

	require.Error(t, settled)
	require.NotErrorIs(t, settled, ErrOperationCancelled, "an ordinary failure is not a cancel")
	state := h.mm.GetDownloadState(cancelEntryID)
	require.NotNil(t, state)
	assert.Equal(t, StatusFailed, state.Status)
	cancelErr := h.mm.CancelOperation(cancelEntryID)
	_, refused := errors.AsType[*OperationNotCancellableError](cancelErr)
	assert.True(t, refused, "a cancel after the failure is refused, got %v", cancelErr)
	require.NoError(t, ctx.Err(), "the refused cancel leaves the context alone")
}

// TestDownloadVariantFiles_ActivationBoundary pins the point of no return inside
// the download: with every file already on disk the loop downloads nothing, so only
// the boundary after it decides. A cancel before it ends the call cancelled; without
// one the state moves to loading.
func TestDownloadVariantFiles_ActivationBoundary(t *testing.T) {
	h := newDepHarnessWithCatalog(t, cancelCatalog())
	require.NoError(t, h.install(cancelEntryID, cancelVarA))
	e := h.entry(cancelEntryID)

	t.Run("cancelled before the boundary", func(t *testing.T) {
		h.setDownloading(cancelEntryID, StatusDownloading)
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(ErrOperationCancelled)

		_, _, _, _, err := h.mm.downloadVariantFiles(ctx, &e, cancelVarA, h.srv.URL, nil, true)

		require.ErrorIs(t, err, ErrOperationCancelled)
		requireCancelled(t, h.mm, cancelEntryID)
		assert.FileExists(t, h.own(cancelEntryID, cancelVarA+"-model.onnx"), "files this call did not write stay")
	})

	t.Run("not cancelled", func(t *testing.T) {
		h.setDownloading(cancelEntryID, StatusDownloading)

		_, _, _, _, err := h.mm.downloadVariantFiles(t.Context(), &e, cancelVarA, h.srv.URL, nil, true)

		require.NoError(t, err)
		state := h.mm.GetDownloadState(cancelEntryID)
		require.NotNil(t, state)
		assert.Equal(t, StatusLoading, state.Status, "activation starts once every file is on disk")
	})
}

// TestGetHuggingFace_StatusRacingCancelIsSuppressible pins that an HTTP status error
// built while the context is cancelled is the suppressible interruption error, not
// a network-category error: every EnhancedError is reported when built.
func TestGetHuggingFace_StatusRacingCancelIsSuppressible(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var attempts atomic.Int64
	client := &http.Client{Transport: cancelThenGatewayTransport{cancel: cancel, calls: &attempts}}

	resp, err := getHuggingFace(ctx, client, "http://primary.invalid/model.onnx")
	if resp != nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err)
	ee, ok := errors.AsType[*errors.EnhancedError](err)
	require.True(t, ok, "want an EnhancedError in the chain of %v", err)
	assert.True(t, errors.IsSuppressibleOperationalError(ee), "a status racing a user cancel must not reach telemetry: %v", err)
	require.ErrorIs(t, err, ErrOperationCancelled)
	assert.False(t, shouldFailover(err))
}

// retainCancelledSwap installs variant A of the cancel entry, then cancels a swap to
// B, leaving the cancelled state retained for the retention window.
func (h *depHarness) retainCancelledSwap() {
	h.t.Helper()
	done, _ := h.startHeldSwap(cancelVarA, cancelVarB, cancelVarB+"-labels.txt")
	require.NoError(h.t, h.mm.CancelOperation(cancelEntryID))
	require.ErrorIs(h.t, awaitResult(h.t, done), ErrOperationCancelled)
	requireCancelled(h.t, h.mm, cancelEntryID)
}

// TestNewAttemptDropsRetainedState pins that an attempt that ends without writing a
// state of its own does not leave the previous attempt's retained state to be
// replayed to its progress stream: a same-variant no-op leaves none, and a reinstall
// that gives up early records a failure of its own.
func TestNewAttemptDropsRetainedState(t *testing.T) {
	t.Run("same-variant install", func(t *testing.T) {
		h := newDepHarnessWithCatalog(t, cancelCatalog())
		h.retainCancelledSwap()
		e := h.entry(cancelEntryID)

		require.NoError(t, h.mm.InstallOrReplace(t.Context(), &e, cancelVarA, h.srv.URL, nil))

		assert.Nil(t, h.mm.GetDownloadState(cancelEntryID), "the no-op leaves no state to replay")
	})

	t.Run("reinstall of a variant that left the catalog", func(t *testing.T) {
		h := newDepHarnessWithCatalog(t, cancelCatalog())
		h.retainCancelledSwap()
		e := h.entry(cancelEntryID)
		e.Variants = e.Variants[1:] // drop the installed variant A

		err := h.mm.Reinstall(t.Context(), &e, h.srv.URL, nil)

		require.Error(t, err)
		state := h.mm.GetDownloadState(cancelEntryID)
		require.NotNil(t, state)
		assert.Equal(t, StatusFailed, state.Status, "the reinstall failure replaces the retained cancel")
		assert.NotEmpty(t, state.Error)
		requireSlotFree(t, h.mm, "the early failure frees the slot")
	})
}

// TestDownloadStateAndRunning pins the snapshot the progress stream relies on: the
// entry's state and whether an operation holds its slot, read together, so a
// retained terminal state is told apart from the outcome of the operation running now.
func TestDownloadStateAndRunning(t *testing.T) {
	h := newDepHarnessWithCatalog(t, cancelCatalog())
	h.setDownloading(cancelEntryID, StatusCancelled)

	state, running := h.mm.DownloadStateAndRunning(cancelEntryID)
	require.NotNil(t, state)
	assert.Equal(t, StatusCancelled, state.Status)
	assert.False(t, running, "no operation holds the slot")

	lease, err := h.mm.BeginOperation(OperationInstall, cancelEntryID)
	require.NoError(t, err)
	t.Cleanup(lease.Release)
	_, running = h.mm.DownloadStateAndRunning(cancelEntryID)
	assert.True(t, running, "a held lease for the entry is running")
	_, running = h.mm.DownloadStateAndRunning(cancelPermID)
	assert.False(t, running, "another entry's operation does not count")
	lease.Release()

	state, running = h.mm.DownloadStateAndRunning("no-such-entry")
	assert.Nil(t, state)
	assert.False(t, running)
}

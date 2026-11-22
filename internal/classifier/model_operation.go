package classifier

import (
	"context"
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// operationComponent names this file's errors for telemetry.
const operationComponent = "classifier.model_manager"

// operationEndedUnexpectedlyMsg is the error recorded on an entry's download state
// when its operation ended without settling the state (a recovered panic).
const operationEndedUnexpectedlyMsg = "operation ended unexpectedly"

// ErrOperationCancelled is the cause a user's cancel request gives the running
// install's context. It wraps context.Canceled on purpose: net/http returns the
// context's cause instead of context.Canceled, and a cause that does not wrap it
// would make conf.IsUnreachable read the cancel as an unreachable host, so the
// download would fail over to the mirror endpoint.
var ErrOperationCancelled = fmt.Errorf("model operation cancelled: %w", context.Canceled)

// OperationNotCancellableError is returned by CancelOperation when the operation
// for the entry cannot be cancelled now. Nothing is changed when it is returned.
type OperationNotCancellableError struct {
	// CatalogID is the entry the refused cancel targeted.
	CatalogID string
	// Reason says why the cancel was refused.
	Reason string
}

// Error implements the error interface.
func (e *OperationNotCancellableError) Error() string {
	return "cannot cancel the model operation for " + e.CatalogID + ": " + e.Reason
}

// Reasons CancelOperation refuses a cancel.
const (
	cancelRefusedNotRunning     = "no operation is running for it"
	cancelRefusedNotCancellable = "the running operation cannot be cancelled"
	cancelRefusedNotStarted     = "the download has not started, has ended or activation has begun"
)

// ModelOperation names a model gallery operation that holds the ModelManager's
// single operation slot.
type ModelOperation string

const (
	// OperationInstall is an install or a variant swap (Install, InstallOrReplace).
	OperationInstall ModelOperation = "install"
	// OperationReinstall repairs an installed model's files (Reinstall).
	OperationReinstall ModelOperation = "reinstall"
	// OperationUninstall removes an installed model (Uninstall).
	OperationUninstall ModelOperation = "uninstall"
)

// valid reports whether op is one of the defined operations.
func (op ModelOperation) valid() bool {
	switch op {
	case OperationInstall, OperationReinstall, OperationUninstall:
		return true
	}
	return false
}

// OperationInProgressError is returned by BeginOperation, Install, InstallOrReplace,
// Reinstall and Uninstall when another model operation holds the slot. Nothing is
// changed when it is returned.
type OperationInProgressError struct {
	// CatalogID is the entry the refused call targeted.
	CatalogID string
	// Running identifies the entry whose operation holds the slot.
	Running CatalogRef
	// Operation is the running operation.
	Operation ModelOperation
}

// Error implements the error interface.
func (e *OperationInProgressError) Error() string {
	return "cannot start a model operation for " + e.CatalogID + ": " +
		string(e.Operation) + " of " + e.Running.ID + " is in progress"
}

// OperationLease is the ModelManager's operation slot, held by one caller until
// Release. It is single-use: after Release it can never be taken again.
type OperationLease struct {
	mm        *ModelManager
	catalogID string
	op        ModelOperation
	name      string // display name of catalogID at begin time
	once      sync.Once

	// cancel ends the operation's context with ErrOperationCancelled as the cause.
	// It is set by cancellableContext. cancellable is true only between the
	// operation registering its download state and the first of: the start of
	// activation, or the download failing (settleDownloadFailure). Both are
	// guarded by mm.opMu.
	cancel      context.CancelCauseFunc
	cancellable bool
}

// cancellableContext derives the context an install runs under and stores its
// cancel function on the lease, so CancelOperation can end it. The lease is not
// cancellable yet: the operation calls markCancellable once its download state
// exists. The returned function releases the context's resources; call it when
// the operation ends.
func (l *OperationLease) cancellableContext(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	l.mm.opMu.Lock()
	l.cancel = cancel
	l.mm.opMu.Unlock()
	return ctx, cancel
}

// BeginOperation reserves the manager's single operation slot for op on catalogID,
// or returns an *OperationInProgressError, changing nothing, while another
// operation holds it. The caller must Release the lease on every exit path. The
// install and reinstall lease methods run the operation under the lease. It takes
// only the slot's own mutex, never mm.mu, so it answers at once even while an
// uninstall holds mm.mu.
func (mm *ModelManager) BeginOperation(op ModelOperation, catalogID string) (*OperationLease, error) {
	if catalogID == "" || !op.valid() {
		return nil, errors.Newf("invalid model operation %q for catalog ID %q", string(op), catalogID).
			Component(operationComponent).
			Category(errors.CategoryValidation).
			Context("catalog_id", catalogID).
			Build()
	}
	mm.opMu.Lock()
	defer mm.opMu.Unlock()
	if err := mm.busyErrorLocked(catalogID); err != nil {
		return nil, err
	}
	return mm.acquireLocked(op, catalogID), nil
}

// busyErrorLocked returns nil when the operation slot is free, else the refusal for
// a call targeting catalogID. The caller must hold mm.opMu.
func (mm *ModelManager) busyErrorLocked(catalogID string) error {
	l := mm.activeOp
	if l == nil {
		return nil
	}
	return &OperationInProgressError{
		CatalogID: catalogID,
		Running:   CatalogRef{ID: l.catalogID, Name: l.name},
		Operation: l.op,
	}
}

// acquireLocked takes the free operation slot. The caller must hold mm.opMu and
// have checked busyErrorLocked.
func (mm *ModelManager) acquireLocked(op ModelOperation, catalogID string) *OperationLease {
	name := catalogID
	if entry, ok := GetCatalogEntry(catalogID); ok && entry.Name != "" {
		name = entry.Name
	}
	l := &OperationLease{mm: mm, catalogID: catalogID, op: op, name: name}
	mm.activeOp = l
	return l
}

// releaseLocked frees the slot when l still owns it. If the lease's entry is still
// in an active download state, the operation ended without settling it (a recovered
// panic), so the state is marked failed and removed after failedStateRetention (if it
// is still that state) like every other failure path. The caller must hold mm.mu and mm.opMu, in that order.
func (mm *ModelManager) releaseLocked(l *OperationLease) {
	if mm.activeOp != l {
		return
	}
	if state := mm.downloading[l.catalogID]; state.IsActive() {
		state.Status = StatusFailed
		state.Error = operationEndedUnexpectedlyMsg
		mm.retainTerminalStateLocked(l.catalogID)
	}
	mm.activeOp = nil
}

// Release frees the operation slot. It is safe to call more than once, and never
// frees a slot a newer lease holds.
func (l *OperationLease) Release() {
	l.once.Do(func() {
		l.mm.mu.Lock()
		defer l.mm.mu.Unlock()
		l.mm.opMu.Lock()
		defer l.mm.opMu.Unlock()
		l.mm.releaseLocked(l)
	})
}

// check verifies that l is still the held lease and was begun for op and entry.
func (l *OperationLease) check(op ModelOperation, entry *CatalogEntry) error {
	l.mm.opMu.Lock()
	held := l.mm.activeOp == l
	l.mm.opMu.Unlock()
	if !held || l.op != op || entry.ID != l.catalogID {
		return errors.Newf("operation lease for %s %s cannot run %s of %s", string(l.op), l.catalogID, string(op), entry.ID).
			Component(operationComponent).
			Category(errors.CategoryValidation).
			Context("catalog_id", entry.ID).
			Build()
	}
	return nil
}

// InstallOrReplace runs ModelManager.InstallOrReplace under an OperationInstall
// lease for the same catalog entry, then releases the lease whatever the outcome.
//
// It checks no document agreement. That check belongs to the user-facing install
// handlers (internal/api/v2/models), which refuse a request while a document of
// the requested entry's own repo has no recorded agreement. Keeping it out of
// here means an automatic install, such as the Geomodel files that arrive with
// another model, can never be blocked by a terms agreement. The install is
// cancellable through ModelManager.CancelOperation once its download is
// registered, until activation starts.
func (l *OperationLease) InstallOrReplace(ctx context.Context, entry *CatalogEntry, variantID, baseURL string, progress chan<- DownloadState) error {
	defer l.Release()
	if err := l.check(OperationInstall, entry); err != nil {
		return err
	}
	ctx, cancel := l.cancellableContext(ctx)
	defer cancel(nil)
	return l.mm.installOrReplace(ctx, entry, variantID, baseURL, progress)
}

// Reinstall runs ModelManager.Reinstall under an OperationReinstall lease for the
// same catalog entry, then releases the lease whatever the outcome. Like
// InstallOrReplace it checks no document agreement; the user-facing reinstall
// handler owns that check.
func (l *OperationLease) Reinstall(ctx context.Context, entry *CatalogEntry, baseURL string, progress chan<- DownloadState) error {
	defer l.Release()
	if err := l.check(OperationReinstall, entry); err != nil {
		return err
	}
	return l.mm.reinstall(ctx, entry, baseURL, progress)
}

// OperationRunningFor reports whether the operation slot is held by an operation on
// catalogID. An install records the entry before its hot-load ends, so a progress
// reader uses this to report completion only once the slot is free again. Like
// BeginOperation it never waits on mm.mu.
func (mm *ModelManager) OperationRunningFor(catalogID string) bool {
	mm.opMu.Lock()
	defer mm.opMu.Unlock()
	return mm.operationRunningForLocked(catalogID)
}

// operationRunningForLocked reports whether the operation slot is held by an
// operation on catalogID. The caller must hold mm.opMu.
func (mm *ModelManager) operationRunningForLocked(catalogID string) bool {
	return mm.activeOp != nil && mm.activeOp.catalogID == catalogID
}

// markCancellable makes the running install for catalogID cancellable. Operations
// call it in the critical section that registers their download state (holding
// mm.mu; opMu is taken after it, as releaseLocked does), so no reader sees the state
// before a cancel is accepted, and no early exit (a validation refusal, an
// already-installed no-op) can follow an accepted cancel.
// It does nothing for another entry, another operation or a lease without a
// cancel function.
func (mm *ModelManager) markCancellable(catalogID string) {
	mm.opMu.Lock()
	defer mm.opMu.Unlock()
	if l := mm.activeOp; l != nil && l.catalogID == catalogID && l.op == OperationInstall && l.cancel != nil {
		l.cancellable = true
	}
}

// CancelOperation cancels the running install or variant swap for catalogID. It
// returns an *OperationNotCancellableError, changing nothing, when no install
// runs for the entry, when the operation is not an install (a reinstall unloads
// the model first and is not cancellable), or when it has not registered its
// download yet, its download has failed or it has started activation. A nil
// return means the operation will end cancelled; its final state is reported
// through the download state. Like BeginOperation it takes only mm.opMu, so it
// answers at once.
func (mm *ModelManager) CancelOperation(catalogID string) error {
	mm.opMu.Lock()
	defer mm.opMu.Unlock()
	l := mm.activeOp
	reason := ""
	switch {
	case l == nil || l.catalogID != catalogID:
		reason = cancelRefusedNotRunning
	case l.op != OperationInstall || l.cancel == nil:
		reason = cancelRefusedNotCancellable
	case !l.cancellable:
		reason = cancelRefusedNotStarted
	}
	if reason != "" {
		return &OperationNotCancellableError{CatalogID: catalogID, Reason: reason}
	}
	l.cancel(ErrOperationCancelled)
	return nil
}

// enterActivation is the point of no return of an install. Under mm.opMu it
// refuses when the context was cancelled by the user, and otherwise makes the
// lease uncancellable, so a cancel the API accepted always ends the operation
// cancelled and one it refused never changes it. It then marks the entry's
// download state loading. The lease may be absent (tests call replaceVariant
// without one).
func (mm *ModelManager) enterActivation(ctx context.Context, catalogID string) error {
	mm.opMu.Lock()
	if errors.Is(context.Cause(ctx), ErrOperationCancelled) {
		mm.opMu.Unlock()
		return newCancelledError(catalogID)
	}
	if l := mm.activeOp; l != nil && l.catalogID == catalogID {
		l.cancellable = false
	}
	mm.opMu.Unlock()

	mm.mu.Lock()
	if state, ok := mm.downloading[catalogID]; ok {
		state.Status = StatusLoading
	}
	mm.mu.Unlock()
	return nil
}

// newCancelledError builds the error an install returns when the user cancelled
// it. It wraps ErrOperationCancelled, so errors.Is matches both that and
// context.Canceled, and carries CategorySystem so telemetry treats it as an
// expected interruption, never a failure.
func newCancelledError(catalogID string) error {
	return errors.New(ErrOperationCancelled).
		Component(operationComponent).
		Category(errors.CategorySystem).
		Context("operation", "model_download_cancelled").
		Context("catalog_id", catalogID).
		Build()
}

// DownloadStateAndRunning returns a copy of the entry's download state (nil when
// there is none) and whether an operation holds the operation slot for the entry,
// read as one snapshot. Slot release and state replacement both need mm.mu, which
// the snapshot holds while it reads, so a retained terminal state of an earlier
// attempt cannot be paired with a slot that the next attempt has already released:
// a progress reader uses it to send a terminal state only when it is the outcome of
// the operation that just ended. mm.opMu is taken after mm.mu, the usual order.
func (mm *ModelManager) DownloadStateAndRunning(catalogID string) (state *DownloadState, running bool) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	mm.opMu.Lock()
	defer mm.opMu.Unlock()
	return mm.downloadStateLocked(catalogID), mm.operationRunningForLocked(catalogID)
}

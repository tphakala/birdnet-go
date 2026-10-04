package classifier

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// operationComponent names this file's errors for telemetry.
const operationComponent = "classifier.model_manager"

// operationEndedUnexpectedlyMsg is the error recorded on an entry's download state
// when its operation ended without settling the state (a recovered panic).
const operationEndedUnexpectedlyMsg = "operation ended unexpectedly"

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
// panic), so the state is marked failed and removed after failedStateRetention like
// every other failure path. The caller must hold mm.mu and mm.opMu, in that order.
func (mm *ModelManager) releaseLocked(l *OperationLease) {
	if mm.activeOp != l {
		return
	}
	if state := mm.downloading[l.catalogID]; state.IsActive() {
		state.Status = StatusFailed
		state.Error = operationEndedUnexpectedlyMsg
		id := l.catalogID
		time.AfterFunc(failedStateRetention, func() {
			mm.removeDownloading(id)
		})
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
func (l *OperationLease) InstallOrReplace(ctx context.Context, entry *CatalogEntry, variantID, baseURL string, progress chan<- DownloadState) error {
	defer l.Release()
	if err := l.check(OperationInstall, entry); err != nil {
		return err
	}
	return l.mm.installOrReplace(ctx, entry, variantID, baseURL, progress)
}

// Reinstall runs ModelManager.Reinstall under an OperationReinstall lease for the
// same catalog entry, then releases the lease whatever the outcome.
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
	return mm.activeOp != nil && mm.activeOp.catalogID == catalogID
}

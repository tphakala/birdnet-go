// jobqueue_adapter.go provides adapter functions for using the jobqueue package
//
// This file contains only the essential ActionAdapter component for interfacing
// with the jobqueue package.
package processor

import "context"

// ActionAdapter adapts the processor.Action interface to the jobqueue.Action interface
type ActionAdapter struct {
	action Action
}

// Execute implements the jobqueue.Action interface
func (a *ActionAdapter) Execute(ctx context.Context, data any) error {
	return a.action.Execute(ctx, data)
}

// GetDescription returns a human-readable description of the action
func (a *ActionAdapter) GetDescription() string {
	return a.action.GetDescription()
}

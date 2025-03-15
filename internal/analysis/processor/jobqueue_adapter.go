// jobqueue_adapter.go provides adapter functions for using the jobqueue package
//
// This file contains only the essential ActionAdapter component for interfacing
// with the jobqueue package.
package processor

// ActionAdapter adapts the processor.Action interface to the jobqueue.Action interface
type ActionAdapter struct {
	action Action
}

// Execute implements the jobqueue.Action interface
func (a *ActionAdapter) Execute(data interface{}) error {
	return a.action.Execute(data)
}

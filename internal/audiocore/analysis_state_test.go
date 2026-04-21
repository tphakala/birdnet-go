package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalysisStateSnapshotReturnsCopy(t *testing.T) {
	t.Parallel()

	SetAnalysisSuspended("source-copy", true)
	t.Cleanup(func() {
		RemoveAnalysisState("source-copy")
	})

	snapshot := GetAnalysisSuspendedSnapshot()
	snapshot["source-copy"] = false

	refreshed := GetAnalysisSuspendedSnapshot()
	assert.True(t, refreshed["source-copy"], "snapshot mutation must not alter store state")
}

func TestAnalysisStateRemoveClearsSource(t *testing.T) {
	t.Parallel()

	SetAnalysisSuspended("source-remove", true)
	RemoveAnalysisState("source-remove")

	snapshot := GetAnalysisSuspendedSnapshot()
	_, exists := snapshot["source-remove"]
	assert.False(t, exists)
}

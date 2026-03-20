package restart

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestedDefault(t *testing.T) {
	t.Cleanup(Reset)
	assert.Equal(t, RestartNone, Requested())
}

func TestSetBinaryRestart(t *testing.T) {
	t.Cleanup(Reset)
	assert.True(t, SetBinaryRestart(), "first call should succeed")
	assert.Equal(t, RestartBinary, Requested())
}

func TestSetContainerRestart(t *testing.T) {
	t.Cleanup(Reset)
	assert.True(t, SetContainerRestart(), "first call should succeed")
	assert.Equal(t, RestartContainer, Requested())
}

func TestCASPreventsDoubleSet(t *testing.T) {
	t.Cleanup(Reset)
	assert.True(t, SetBinaryRestart())
	assert.False(t, SetContainerRestart(), "second call should fail")
	assert.Equal(t, RestartBinary, Requested(), "original type preserved")
}

func TestMarkRestartRequired(t *testing.T) {
	t.Cleanup(Reset)
	assert.False(t, IsRestartRequired())
	assert.Empty(t, GetRestartReasons())

	MarkRestartRequired("Web server port changed")
	assert.True(t, IsRestartRequired())
	assert.Equal(t, []string{"Web server port changed"}, GetRestartReasons())
}

func TestMarkRestartRequiredDedup(t *testing.T) {
	t.Cleanup(Reset)
	MarkRestartRequired("Port changed")
	MarkRestartRequired("Port changed")
	assert.Equal(t, []string{"Port changed"}, GetRestartReasons())
}

func TestMarkRestartRequiredMultiple(t *testing.T) {
	t.Cleanup(Reset)
	MarkRestartRequired("Port changed")
	MarkRestartRequired("Database path changed")
	assert.Equal(t, []string{"Port changed", "Database path changed"}, GetRestartReasons())
}

func TestResetClearsReasons(t *testing.T) {
	t.Cleanup(Reset)
	MarkRestartRequired("Something")
	Reset()
	assert.False(t, IsRestartRequired())
	assert.Empty(t, GetRestartReasons())
}

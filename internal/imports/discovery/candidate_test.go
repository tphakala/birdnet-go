package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceCandidate_ZeroValueIsInvalidWithNoReason(t *testing.T) {
	t.Parallel()
	var c SourceCandidate
	assert.False(t, c.Valid)
	assert.Empty(t, c.Reason)
	assert.Equal(t, Kind(""), c.Kind)
}

func TestKindConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, KindLocal, Kind("local"))
	assert.Equal(t, KindRemovable, Kind("removable"))
	assert.Equal(t, KindNetwork, Kind("network"))
}

func TestReasonConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ReasonPermissionDenied, "permission_denied")
	assert.Equal(t, ReasonInvalidSchema, "invalid_schema")
	assert.Equal(t, ReasonOpenFailed, "open_failed")
}

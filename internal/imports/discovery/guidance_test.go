package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetGuidance_KnownReason(t *testing.T) {
	t.Parallel()
	g := GetGuidance(ReasonPermissionDenied)
	assert.Equal(t, ReasonPermissionDenied, g.Key)
	assert.Contains(t, g.Message, "permission")
}

func TestGetGuidance_UnknownReason(t *testing.T) {
	t.Parallel()
	g := GetGuidance("this_is_not_a_real_reason")
	assert.Equal(t, "this_is_not_a_real_reason", g.Key)
	assert.Contains(t, g.Message, "unknown")
}

func TestRegisterGuidance(t *testing.T) {
	RegisterGuidance("test_custom", "My custom message")
	g := GetGuidance("test_custom")
	assert.Equal(t, "test_custom", g.Key)
	assert.Equal(t, "My custom message", g.Message)
}

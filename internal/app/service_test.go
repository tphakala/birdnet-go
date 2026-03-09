package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShutdownTier_Ordering(t *testing.T) {
	t.Parallel()
	// TierNetwork must be lower than TierCore so network stops before data services
	assert.Less(t, int(TierNetwork), int(TierCore))
}

package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpeciesGuideEnableSupplementaryLinksDefaultsFalse(t *testing.T) {
	var cfg SpeciesGuideConfig
	assert.False(t, cfg.EnableSupplementaryLinks, "supplementary links must default to off")
}

func TestSpeciesGuideEnableSupplementaryLinksSettable(t *testing.T) {
	cfg := SpeciesGuideConfig{EnableSupplementaryLinks: true}
	assert.True(t, cfg.EnableSupplementaryLinks)
}

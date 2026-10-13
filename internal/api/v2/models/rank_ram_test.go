package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestRankCatalogUsesRAMUpperBound pins that a host whose exact RAM is unknown
// (cgroup limit unreadable) still gets the ram.insufficient blocker when its
// upper bound is below the variant's floor.
func TestRankCatalogUsesRAMUpperBound(t *testing.T) {
	t.Parallel()

	const (
		catalogID    = "ram-upper-bound"
		variantID    = "needs-4g"
		minRAMMB     = 4096
		upperBoundGB = 1
	)
	entry := classifier.CatalogEntry{
		ID: catalogID,
		Variants: []classifier.CatalogVariant{{
			ID:           variantID,
			Requirements: classifier.VariantRequirements{MinRAMMB: minRAMMB},
			Backends:     map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
		}},
	}

	h := New(apitest.NewCore(t), nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		return hwprofile.Profile{
			Arch:               "amd64",
			RAMUpperBoundBytes: upperBoundGB * 1024 * 1024 * 1024,
			Backends:           hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
		}
	}

	byVariant, _, _ := h.rankCatalog([]classifier.CatalogEntry{entry}, inference.ORTStatus{})
	rec, ok := byVariant[catalogID][variantID]
	require.True(t, ok)
	require.NotEmpty(t, rec.Blockers)
	codes := make([]string, 0, len(rec.Blockers))
	for _, b := range rec.Blockers {
		codes = append(codes, b.Code)
	}
	assert.Contains(t, codes, recommend.BlockerRAMInsufficient)
}

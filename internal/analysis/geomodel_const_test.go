package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestGeomodelConstantsMatchCatalog verifies that the geomodel filenames defined
// in internal/conf match the actual file local names configured in the embedded
// model catalog in internal/classifier. This prevents silent drift if one package is updated
// without updating the other.
func TestGeomodelConstantsMatchCatalog(t *testing.T) {
	t.Parallel()

	var checkedModel, checkedLabels bool

	for _, entry := range classifier.EmbeddedCatalog {
		for _, file := range entry.Files {
			switch file.Role {
			case classifier.RoleGeomodelModel:
				assert.Equal(t, conf.GeomodelONNXLocalName, file.LocalName,
					"catalog entry %s contains geomodel model file local name that drifts from conf.GeomodelONNXLocalName", entry.ID)
				checkedModel = true
			case classifier.RoleGeomodelLabels:
				assert.Equal(t, conf.GeomodelLabelsLocalName, file.LocalName,
					"catalog entry %s contains geomodel labels file local name that drifts from conf.GeomodelLabelsLocalName", entry.ID)
				checkedLabels = true
			}
		}
	}

	assert.True(t, checkedModel, "expected to find and check at least one geomodel model file in the catalog")
	assert.True(t, checkedLabels, "expected to find and check at least one geomodel labels file in the catalog")
}

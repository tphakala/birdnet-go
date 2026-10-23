package detections

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestAddSpeciesToIgnoredList_AlreadyExcludedIsNotAnError pins that marking a
// species as a false positive when it is already on the exclude list succeeds
// without publishing a new snapshot, instead of surfacing the "nothing to
// change" signal from the settings writer as a failure.
func TestAddSpeciesToIgnoredList_AlreadyExcludedIsNotAnError(t *testing.T) {
	const species = "Parus major"

	core := apitest.NewCore(t, apitest.WithEcho(echo.New()))
	seeded := conf.CloneSettings(core.Settings.Load())
	seeded.Realtime.Species.Exclude = []string{species}
	core.Settings.Store(seeded)
	h := buildTestHandler(t, core, map[string]string{}, map[string]string{})

	require.NoError(t, h.addSpeciesToIgnoredList(species))
	assert.Same(t, seeded, core.Settings.Load(), "an already excluded species must not publish a new snapshot")

	require.NoError(t, h.addSpeciesToIgnoredList("Turdus merula"))
	assert.Equal(t, []string{species, "Turdus merula"}, core.Settings.Load().Realtime.Species.Exclude,
		"a species that is not excluded yet is appended")
}

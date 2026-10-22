package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"gorm.io/gorm"
)

const (
	rateTestClip = "2026/01/bat.flac"
	rateTestRate = 384000
	rateTestID   = uint(77)
)

// newRateTestDeps builds conversion dependencies over real repositories.
func newRateTestDeps(t *testing.T) (*ConversionDeps, *gorm.DB) {
	t.Helper()
	db := setupDetectionTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&entities.AIModel{}, &entities.Label{}, &entities.LabelType{},
		&entities.TaxonomicClass{}, &entities.AudioSource{},
	))
	lt := &entities.LabelType{Name: "species"}
	require.NoError(t, db.Create(lt).Error)
	return &ConversionDeps{
		LabelRepo:          NewLabelRepository(db, nil, false, false),
		ModelRepo:          NewModelRepository(db, nil, false, false),
		SourceRepo:         NewAudioSourceRepository(db, nil, false, false),
		SpeciesLabelTypeID: lt.ID,
	}, db
}

func rateTestResult(clip string, rate int) *detection.Result {
	return &detection.Result{
		ID:             rateTestID,
		Species:        detection.Species{ScientificName: "Myotis daubentonii"},
		Confidence:     0.9,
		ClipName:       clip,
		ClipSampleRate: rate,
	}
}

func TestConvertToV2Detection_ClipSampleRate(t *testing.T) {
	deps, _ := newRateTestDeps(t)

	det, err := ConvertToV2Detection(t.Context(), rateTestResult(rateTestClip, rateTestRate), deps)
	require.NoError(t, err)
	require.NotNil(t, det.ClipSampleRate)
	assert.Equal(t, rateTestRate, *det.ClipSampleRate)

	// No rate, or no clip, stays NULL.
	det, err = ConvertToV2Detection(t.Context(), rateTestResult(rateTestClip, 0), deps)
	require.NoError(t, err)
	assert.Nil(t, det.ClipSampleRate)
	det, err = ConvertToV2Detection(t.Context(), rateTestResult("", rateTestRate), deps)
	require.NoError(t, err)
	assert.Nil(t, det.ClipSampleRate)
}

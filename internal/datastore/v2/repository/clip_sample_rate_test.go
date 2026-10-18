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

func TestConvertV2Detection_ClipSampleRateRoundTrip(t *testing.T) {
	deps, _ := newRateTestDeps(t)

	det, err := ConvertToV2Detection(t.Context(), rateTestResult(rateTestClip, rateTestRate), deps)
	require.NoError(t, err)
	require.NotNil(t, det.ClipSampleRate)
	assert.Equal(t, rateTestRate, *det.ClipSampleRate)
	assert.Equal(t, rateTestRate, ConvertFromV2Detection(det).ClipSampleRate)

	// No rate, or no clip, stays NULL.
	det, err = ConvertToV2Detection(t.Context(), rateTestResult(rateTestClip, 0), deps)
	require.NoError(t, err)
	assert.Nil(t, det.ClipSampleRate)
	det, err = ConvertToV2Detection(t.Context(), rateTestResult("", rateTestRate), deps)
	require.NoError(t, err)
	assert.Nil(t, det.ClipSampleRate)
	assert.Zero(t, ConvertFromV2Detection(det).ClipSampleRate)
}

// TestSyncToV2_NeverClearsClipSampleRate verifies that syncing a legacy-derived
// result without a rate (the dirty-ID reconcile path) leaves a stored rate alone,
// while a result that carries a rate updates it.
func TestSyncToV2_NeverClearsClipSampleRate(t *testing.T) {
	deps, db := newRateTestDeps(t)
	v2Repo := NewDetectionRepository(db, nil, false, false)
	dw := &DualWriteRepository{
		v2:                 v2Repo,
		labelRepo:          deps.LabelRepo,
		modelRepo:          deps.ModelRepo,
		sourceRepo:         deps.SourceRepo,
		logger:             testLogger(),
		speciesLabelTypeID: deps.SpeciesLabelTypeID,
	}
	rateOf := func() *int {
		var det entities.Detection
		require.NoError(t, db.First(&det, rateTestID).Error)
		return det.ClipSampleRate
	}

	// First save stores the rate.
	require.NoError(t, dw.syncToV2(t.Context(), rateTestResult(rateTestClip, rateTestRate), nil))
	require.NotNil(t, rateOf())
	assert.Equal(t, rateTestRate, *rateOf())

	// A sync from legacy data without a rate must not clear it.
	require.NoError(t, dw.syncToV2(t.Context(), rateTestResult(rateTestClip, 0), nil))
	require.NotNil(t, rateOf(), "an absent rate must not overwrite the stored value")
	assert.Equal(t, rateTestRate, *rateOf())

	// A sync that carries a rate updates it.
	const newRate = 192000
	require.NoError(t, dw.syncToV2(t.Context(), rateTestResult(rateTestClip, newRate), nil))
	assert.Equal(t, newRate, *rateOf())
}

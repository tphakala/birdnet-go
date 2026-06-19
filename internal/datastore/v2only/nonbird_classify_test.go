package v2only

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// perchModelInfo returns a ModelInfo for a Perch/multi-taxa model.
// ResolveModelType("Perch", "1.0") returns ModelTypeMulti, so taxonomicClassForModel
// returns nil, matching the expected behavior for non-bird sound classes.
func perchModelInfo() detection.ModelInfo {
	return detection.ModelInfo{
		Name:    "Perch",
		Version: "1.0",
		Variant: "default",
	}
}

// birdModelInfo returns a ModelInfo that resolves to ModelTypeBird.
func birdModelInfo() detection.ModelInfo {
	return detection.ModelInfo{
		Name:    "BirdNET",
		Version: "2.4",
		Variant: "default",
	}
}

// TestSave_PerchSoundClass_GetsNonBirdLabelType verifies that a Perch detection with a
// non-bird raw label (e.g. "power_tool") is stored with the correct non-bird label type
// and a nil taxonomic class, rather than the species label type and Aves class.
func TestSave_PerchSoundClass_GetsNonBirdLabelType(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "10:00:00",
		ScientificName: "power",
		CommonName:     "Power Tool",
		Confidence:     0.92,
		Model:          perchModelInfo(),
		RawLabel:       "power_tool",
	}

	require.NoError(t, ds.Save(note, nil))

	// Fetch the created label directly via the label repository.
	ctx := t.Context()
	perchModel, err := ds.model.GetOrCreate(ctx, "Perch", "1.0", "default", "multi", nil)
	require.NoError(t, err)

	label, err := ds.label.GetOrCreate(ctx, "power", perchModel.ID, ds.speciesLabelTypeID, nil)
	require.NoError(t, err)

	wantTypeID := ds.nonBirdLabelTypeIDs[nonbird.CategoryMechanical]
	require.NotZero(t, wantTypeID, "CategoryMechanical must be registered in nonBirdLabelTypeIDs")

	assert.Equal(t, wantTypeID, label.LabelTypeID,
		"Perch sound class 'power_tool' must use the Mechanical non-bird label type")
	assert.Nil(t, label.TaxonomicClassID,
		"non-bird sound class must have nil taxonomic class, not Aves")
}

// TestSave_BirdLabel_GetsSpeciesLabelType verifies that a standard bird detection stored
// under a bird model gets the species label type (backward-compatible).
func TestSave_BirdLabel_GetsSpeciesLabelType(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "11:00:00",
		ScientificName: "Turdus merula",
		CommonName:     "Common Blackbird",
		Confidence:     0.88,
		Model:          birdModelInfo(),
		RawLabel:       "Turdus merula_Common Blackbird",
	}

	require.NoError(t, ds.Save(note, nil))

	ctx := t.Context()
	birdModel, err := ds.model.GetOrCreate(ctx, "BirdNET", "2.4", "default", "bird", nil)
	require.NoError(t, err)

	label, err := ds.label.GetOrCreate(ctx, "Turdus merula", birdModel.ID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)

	assert.Equal(t, ds.speciesLabelTypeID, label.LabelTypeID,
		"bird detection must keep the species label type")
	// For a bird model taxonomicClassForModel returns avesClassID.
	require.NotNil(t, label.TaxonomicClassID,
		"bird detection must have a non-nil taxonomic class")
	assert.Equal(t, *ds.avesClassID, *label.TaxonomicClassID,
		"bird detection taxonomic class must be Aves")
}

// TestSave_EmptyRawLabel_FallsBackToSpecies verifies backward compatibility: when RawLabel
// is empty (non-processor paths or older callers), Save must behave exactly as before
// (species label type, model's taxonomic class). nonbird.CategoryOf("") returns false so the
// empty string naturally takes the species branch.
func TestSave_EmptyRawLabel_FallsBackToSpecies(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "12:00:00",
		ScientificName: "Passer domesticus",
		CommonName:     "House Sparrow",
		Confidence:     0.75,
		// RawLabel intentionally left empty - simulates older caller.
	}

	require.NoError(t, ds.Save(note, nil))

	// The note was saved with the default BirdNET model (no Model field set).
	// Fetch the created label.
	ctx := t.Context()
	defaultModel, err := ds.model.GetOrCreate(ctx, "BirdNET", "2.4", "default", "bird", nil)
	require.NoError(t, err)

	label, err := ds.label.GetOrCreate(ctx, "Passer domesticus", defaultModel.ID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)

	assert.Equal(t, ds.speciesLabelTypeID, label.LabelTypeID,
		"empty RawLabel must fall back to species label type")
}

// TestSave_BatchMixed_CorrectLabelTypesPerPrediction verifies that when a Note has
// both a bird prediction and a non-bird sound-class prediction in results, each
// resolved label gets the correct label type: species for the bird, and the
// appropriate non-bird category for the sound class.
func TestSave_BatchMixed_CorrectLabelTypesPerPrediction(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	// Primary detection is a non-bird sound class.
	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "13:00:00",
		ScientificName: "speech",
		CommonName:     "Speech",
		Confidence:     0.80,
		Model:          perchModelInfo(),
		RawLabel:       "speech",
	}

	// Predictions contain a mix: one bird label (no category match) and one sound class.
	results := []datastore.Results{
		{
			// Bird prediction from Perch (should stay species type since no nonbird category).
			Species:    "Turdus merula_Common Blackbird",
			Confidence: 0.15,
			RawLabel:   "Turdus merula_Common Blackbird",
		},
		{
			// Non-bird sound class prediction.
			Species:    "speech",
			Confidence: 0.80,
			RawLabel:   "speech",
		},
	}

	require.NoError(t, ds.Save(note, results))

	ctx := t.Context()
	perchModel, err := ds.model.GetOrCreate(ctx, "Perch", "1.0", "default", "multi", nil)
	require.NoError(t, err)

	// Check bird prediction label: should be species type.
	birdLabel, err := ds.label.GetOrCreate(ctx, "Turdus merula", perchModel.ID, ds.speciesLabelTypeID, nil)
	require.NoError(t, err)
	assert.Equal(t, ds.speciesLabelTypeID, birdLabel.LabelTypeID,
		"bird prediction must keep species label type")

	// Check sound-class prediction label: should be human type.
	wantHumanTypeID := ds.nonBirdLabelTypeIDs[nonbird.CategoryHuman]
	require.NotZero(t, wantHumanTypeID)

	speechLabel, err := ds.label.GetOrCreate(ctx, "speech", perchModel.ID, wantHumanTypeID, nil)
	require.NoError(t, err)
	assert.Equal(t, wantHumanTypeID, speechLabel.LabelTypeID,
		"'speech' prediction must use the Human non-bird label type")
	assert.Nil(t, speechLabel.TaxonomicClassID,
		"sound-class prediction must have nil taxonomic class")
}

// TestSave_Relabel_CorrectsMisclassifiedSpeciesRow verifies the relabel path: if a label
// row for a non-bird sound class already exists with the wrong (species) label type - as
// would be the case for data written before the fix - Save must update its label type to
// the correct non-bird category type.
func TestSave_Relabel_CorrectsMisclassifiedSpeciesRow(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	ctx := t.Context()

	// Pre-create a label for "power" with the WRONG species type (simulating legacy data).
	perchModel, err := ds.model.GetOrCreate(ctx, "Perch", "1.0", "default", "multi", nil)
	require.NoError(t, err)

	preLabel, err := ds.label.GetOrCreate(ctx, "power", perchModel.ID, ds.speciesLabelTypeID, ds.avesClassID)
	require.NoError(t, err)
	require.Equal(t, ds.speciesLabelTypeID, preLabel.LabelTypeID,
		"pre-condition: label must start with species type")

	// Now Save a Note with the correct RawLabel. This should trigger the relabel.
	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "14:00:00",
		ScientificName: "power",
		CommonName:     "Power Tool",
		Confidence:     0.91,
		Model:          perchModelInfo(),
		RawLabel:       "power_tool",
	}

	require.NoError(t, ds.Save(note, nil))

	// Fetch the label again and verify it was relabeled.
	wantTypeID := ds.nonBirdLabelTypeIDs[nonbird.CategoryMechanical]
	require.NotZero(t, wantTypeID)

	// GetOrCreate with the mechanical type will return the now-updated row.
	updatedLabel, err := ds.label.GetOrCreate(ctx, "power", perchModel.ID, wantTypeID, nil)
	require.NoError(t, err)

	assert.Equal(t, wantTypeID, updatedLabel.LabelTypeID,
		"Save must relabel a pre-existing species row to the correct non-bird type")
	// NOTE: UpdateLabelType only fixes label_type_id (TYPE-ONLY scope). The TaxonomicClassID of a
	// pre-existing mis-labeled row is not retroactively changed; only rows created fresh after the
	// fix get nil TaxonomicClassID. This is acceptable: the ID field drives detection queries, not
	// TaxonomicClassID.
}

// TestSave_NonBirdDetection_AppearsInGetAllNotes proves that non-bird Perch detections
// are not hidden from general detection queries. The detection system queries by label_id,
// not by label_type, so non-species labels must still surface in GetAllNotes.
func TestSave_NonBirdDetection_AppearsInGetAllNotes(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "15:00:00",
		ScientificName: "power",
		CommonName:     "Power Tool",
		Confidence:     0.95,
		Model:          perchModelInfo(),
		RawLabel:       "power_tool",
	}

	require.NoError(t, ds.Save(note, nil))

	// The detection must appear in the general notes list.
	notes, err := ds.GetAllNotes()
	require.NoError(t, err)

	require.NotEmpty(t, notes, "non-bird detection must appear in GetAllNotes")

	found := false
	for _, n := range notes {
		if n.ScientificName == "power" {
			found = true
			break
		}
	}
	assert.True(t, found, "GetAllNotes must include the non-bird detection with scientific_name='power'")

	// Verify the detection was saved (note.ID should be populated after Save).
	// The note pointer is updated in-place, so note.ID > 0 confirms Save propagated the ID.
	assert.Positive(t, note.ID,
		"Save must propagate the detection ID back into note.ID")

	// Also verify via GetLastDetections.
	last, err := ds.GetLastDetections(10)
	require.NoError(t, err)
	require.NotEmpty(t, last)

	found = false
	for i := range last {
		if last[i].ScientificName == "power" {
			found = true
			break
		}
	}
	assert.True(t, found, "GetLastDetections must include the non-bird detection")

	// Finally check the label type is correct after this end-to-end flow.
	ctx := t.Context()
	perchModel, err := ds.model.GetOrCreate(ctx, "Perch", "1.0", "default", "multi", nil)
	require.NoError(t, err)
	wantTypeID := ds.nonBirdLabelTypeIDs[nonbird.CategoryMechanical]
	label, err := ds.label.GetOrCreate(ctx, "power", perchModel.ID, wantTypeID, nil)
	require.NoError(t, err)
	assert.Equal(t, wantTypeID, label.LabelTypeID,
		"end-to-end: label type must be Mechanical after saving via GetAllNotes flow")

	// Check detection count matches expectations.
	assert.Len(t, notes, 1, "exactly one detection was saved")

	// Check that time parsing (June 19 is valid) was used.
	assert.Equal(t, "2024-06-01", notes[0].Date, "detection date must round-trip through Save")
}

// TestSave_LabelTypeForRawLabel_Helper verifies the labelTypeForRawLabel helper directly,
// covering each case: non-bird category, empty rawLabel, and bird (unrecognized) label.
func TestSave_LabelTypeForRawLabel_Helper(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	speciesTaxID := ds.avesClassID

	t.Run("non-bird rawLabel returns category type and nil taxClass", func(t *testing.T) {
		typeID, taxID, isNonBird := ds.labelTypeForRawLabel("power_tool", speciesTaxID)
		assert.Equal(t, ds.nonBirdLabelTypeIDs[nonbird.CategoryMechanical], typeID)
		assert.Nil(t, taxID)
		assert.True(t, isNonBird)
	})

	t.Run("empty rawLabel returns species type and species taxClass", func(t *testing.T) {
		typeID, taxID, isNonBird := ds.labelTypeForRawLabel("", speciesTaxID)
		assert.Equal(t, ds.speciesLabelTypeID, typeID)
		assert.Equal(t, speciesTaxID, taxID)
		assert.False(t, isNonBird)
	})

	t.Run("bird rawLabel returns species type and species taxClass", func(t *testing.T) {
		typeID, taxID, isNonBird := ds.labelTypeForRawLabel("Turdus merula_Common Blackbird", speciesTaxID)
		assert.Equal(t, ds.speciesLabelTypeID, typeID)
		assert.Equal(t, speciesTaxID, taxID)
		assert.False(t, isNonBird)
	})

	t.Run("human rawLabel returns human category type", func(t *testing.T) {
		typeID, taxID, isNonBird := ds.labelTypeForRawLabel("speech", speciesTaxID)
		assert.Equal(t, ds.nonBirdLabelTypeIDs[nonbird.CategoryHuman], typeID)
		assert.Nil(t, taxID)
		assert.True(t, isNonBird)
	})
}

// TestSave_PerchSoundClass_NoteIDPopulated verifies that note.ID is populated after
// a successful Save of a non-bird Perch detection, preserving the existing contract.
func TestSave_PerchSoundClass_NoteIDPopulated(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	note := &datastore.Note{
		Date:           "2024-06-01",
		Time:           "16:00:00",
		ScientificName: "power",
		Confidence:     0.90,
		Model:          perchModelInfo(),
		RawLabel:       "power_tool",
	}

	require.NoError(t, ds.Save(note, nil))
	assert.Positive(t, note.ID, "note.ID must be populated after Save")
}

// TestSave_MultipleNonBirdCategories verifies that different sound classes map to
// their respective non-bird category types correctly.
func TestSave_MultipleNonBirdCategories(t *testing.T) {
	tests := []struct {
		rawLabel string
		sciName  string
		wantCat  nonbird.Category
	}{
		{"speech", "speech", nonbird.CategoryHuman},
		{"power_tool", "power", nonbird.CategoryMechanical},
		{"dog", "dog", nonbird.CategoryAnimal},
		{"music", "music", nonbird.CategoryMusic},
		{"rain", "rain", nonbird.CategoryEnvironment},
	}

	for _, tt := range tests {
		t.Run(string(tt.wantCat), func(t *testing.T) {
			// Each subtest needs its own datastore to avoid label conflicts.
			ds, cleanup := setupTestDatastore(t)
			defer cleanup()

			note := &datastore.Note{
				Date:           "2024-06-01",
				Time:           "17:00:00",
				ScientificName: tt.sciName,
				Confidence:     0.85,
				Model:          perchModelInfo(),
				RawLabel:       tt.rawLabel,
			}

			require.NoError(t, ds.Save(note, nil))

			ctx := t.Context()
			perchModel, err := ds.model.GetOrCreate(ctx, "Perch", "1.0", "default", "multi", nil)
			require.NoError(t, err)

			wantTypeID := ds.nonBirdLabelTypeIDs[tt.wantCat]
			require.NotZero(t, wantTypeID, "category %q must be in nonBirdLabelTypeIDs", tt.wantCat)

			label, err := ds.label.GetOrCreate(ctx, tt.sciName, perchModel.ID, wantTypeID, nil)
			require.NoError(t, err)

			assert.Equal(t, wantTypeID, label.LabelTypeID,
				"rawLabel=%q must yield category %q type", tt.rawLabel, tt.wantCat)
			assert.Nil(t, label.TaxonomicClassID,
				"non-bird label must have nil taxonomic class")
		})
	}

}

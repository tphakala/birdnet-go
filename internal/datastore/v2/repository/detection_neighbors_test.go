package repository

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

const (
	neighborsSpeciesA        = "Parus major"
	neighborsSpeciesB        = "Cyanistes caeruleus"
	neighborsLegacyLabelA    = neighborsSpeciesA + "_Great Tit"
	neighborsNamelessLabelA  = "_Odd"
	neighborsNamelessLabelB  = "_Other"
	neighborsMissingLabelID  = uint(9999)
	neighborsNeverUsedID     = uint(1000000)
	neighborsCaptureCallback = "test:capture_neighbor_statements"
	neighborsMinStatements   = 3
	neighborsTableName       = "detections"
	neighborsIndexName       = "idx_detection_model_label"
	neighborsSearchPrefix    = "SEARCH"
	neighborsTempBTree       = "TEMP B-TREE"
	neighborsRowidBound      = "rowid"
)

// neighborsFixture is a detections table with a species of two models, a second
// species and a way to change the rows, with an oracle of what each row's
// neighbors must be.
type neighborsFixture struct {
	t       *testing.T
	db      *gorm.DB
	repo    *detectionRepository
	species map[uint]string // detection id to the species its label stands for
}

func newNeighborsFixture(t *testing.T) *neighborsFixture {
	t.Helper()
	db := setupDetectionTestDBWithLabels(t)
	return &neighborsFixture{t: t, db: db, repo: &detectionRepository{db: db}, species: map[uint]string{}}
}

// add stores a detection under label and records the species it stands for.
func (f *neighborsFixture) add(label *entities.Label, species string) uint {
	f.t.Helper()
	det := &entities.Detection{LabelID: label.ID, ModelID: label.ModelID, Confidence: 0.9, DetectedAt: 1000}
	require.NoError(f.t, f.db.Table(tableDetections).Create(det).Error)
	f.species[det.ID] = species
	return det.ID
}

// remove deletes a detection.
func (f *neighborsFixture) remove(id uint) {
	f.t.Helper()
	require.NoError(f.t, f.db.Table(tableDetections).Delete(&entities.Detection{}, id).Error)
	delete(f.species, id)
}

// relabel moves a detection to another label the way ChangeSpecies does: only
// label_id changes.
func (f *neighborsFixture) relabel(id uint, label *entities.Label, species string) {
	f.t.Helper()
	require.NoError(f.t, f.db.Table(tableDetections).Where("id = ?", id).Update("label_id", label.ID).Error)
	f.species[id] = species
}

// oracle returns the neighbors of id computed from the species map alone.
func (f *neighborsFixture) oracle(id uint) DetectionNeighbors {
	var want DetectionNeighbors
	for other, species := range f.species {
		same := species == f.species[id]
		switch {
		case other < id:
			want.Previous = max(want.Previous, other)
			if same {
				want.PreviousSameSpecies = max(want.PreviousSameSpecies, other)
			}
		case other > id:
			if want.Next == 0 || other < want.Next {
				want.Next = other
			}
			if same && (want.NextSameSpecies == 0 || other < want.NextSameSpecies) {
				want.NextSameSpecies = other
			}
		}
	}
	return want
}

// assertChains checks every row against the oracle, then walks each chain
// forward from its lowest row and backward from its highest and requires the two
// walks to visit the same rows in opposite orders.
func (f *neighborsFixture) assertChains(step string) {
	f.t.Helper()
	ids := slices.Sorted(maps.Keys(f.species))
	require.NotEmpty(f.t, ids)

	got := make(map[uint]*DetectionNeighbors, len(ids))
	for _, id := range ids {
		n, err := f.repo.GetNeighborIDs(f.t.Context(), id)
		require.NoError(f.t, err, "%s: id %d", step, id)
		want := f.oracle(id)
		assert.Equal(f.t, want, *n, "%s: neighbors of id %d", step, id)
		got[id] = n
	}

	walk := func(start uint, pick func(*DetectionNeighbors) uint) []uint {
		var chain []uint
		// A chain longer than the table is a loop; stop so the assertion reports it.
		for id := start; id != 0 && len(chain) <= len(ids); id = pick(got[id]) {
			chain = append(chain, id)
		}
		return chain
	}
	forward := walk(ids[0], func(n *DetectionNeighbors) uint { return n.Next })
	backward := walk(ids[len(ids)-1], func(n *DetectionNeighbors) uint { return n.Previous })
	assert.Equal(f.t, ids, forward, "%s: forward chain", step)
	slices.Reverse(backward)
	assert.Equal(f.t, ids, backward, "%s: backward chain is the reverse", step)

	for _, species := range []string{neighborsSpeciesA, neighborsSpeciesB} {
		var same []uint
		for _, id := range ids {
			if f.species[id] == species {
				same = append(same, id)
			}
		}
		if len(same) == 0 {
			continue
		}
		forwardSame := walk(same[0], func(n *DetectionNeighbors) uint { return n.NextSameSpecies })
		backwardSame := walk(same[len(same)-1], func(n *DetectionNeighbors) uint { return n.PreviousSameSpecies })
		slices.Reverse(backwardSame)
		assert.Equal(f.t, same, forwardSame, "%s: forward chain of %s", step, species)
		assert.Equal(f.t, same, backwardSame, "%s: backward chain of %s", step, species)
	}
}

func TestDetectionRepository_GetNeighborIDs_StepsBothWays(t *testing.T) {
	f := newNeighborsFixture(t)
	parusM1 := createTestLabel(t, f.db, neighborsSpeciesA, 1)
	parusM2 := createTestLabel(t, f.db, neighborsSpeciesA, 2)
	blueM1 := createTestLabel(t, f.db, neighborsSpeciesB, 1)

	ids := []uint{
		f.add(parusM1, neighborsSpeciesA),
		f.add(blueM1, neighborsSpeciesB),
		f.add(parusM2, neighborsSpeciesA),
		f.add(parusM1, neighborsSpeciesA),
		f.add(blueM1, neighborsSpeciesB),
		f.add(parusM2, neighborsSpeciesA),
		f.add(parusM1, neighborsSpeciesA),
	}
	f.assertChains("seeded")

	steps := []struct {
		name string
		do   func()
	}{
		{"a middle row deleted", func() { f.remove(ids[3]) }},
		{"a row added", func() { f.add(blueM1, neighborsSpeciesB) }},
		{"a row moved to the other species", func() {
			// ChangeSpecies picks the new label of the detection's own model.
			f.relabel(ids[2], createTestLabel(t, f.db, neighborsSpeciesB, 2), neighborsSpeciesB)
		}},
		{"a third model with the species", func() {
			parusM3 := createTestLabel(t, f.db, neighborsSpeciesA, 3)
			f.add(parusM3, neighborsSpeciesA)
		}},
		{"a legacy concatenated label of the species", func() {
			legacy := createTestLabel(t, f.db, neighborsLegacyLabelA, 1)
			f.add(legacy, neighborsSpeciesA)
		}},
	}
	for _, step := range steps {
		step.do()
		f.assertChains(step.name)
	}
}

func TestDetectionRepository_GetNeighborIDs_Edges(t *testing.T) {
	t.Run("the only row has no neighbors", func(t *testing.T) {
		f := newNeighborsFixture(t)
		id := f.add(createTestLabel(t, f.db, neighborsSpeciesA, 1), neighborsSpeciesA)
		n, err := f.repo.GetNeighborIDs(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, DetectionNeighbors{}, *n)
	})

	t.Run("a row whose label is gone has overall neighbors only", func(t *testing.T) {
		f := newNeighborsFixture(t)
		label := createTestLabel(t, f.db, neighborsSpeciesA, 1)
		before := f.add(label, neighborsSpeciesA)
		orphan := f.add(&entities.Label{ID: neighborsMissingLabelID, ModelID: 1}, neighborsSpeciesA)
		after := f.add(label, neighborsSpeciesA)
		n, err := f.repo.GetNeighborIDs(t.Context(), orphan)
		require.NoError(t, err)
		assert.Equal(t, DetectionNeighbors{Previous: before, Next: after}, *n)
	})

	t.Run("a label without a scientific name has overall neighbors only", func(t *testing.T) {
		f := newNeighborsFixture(t)
		odd := createTestLabel(t, f.db, neighborsNamelessLabelA, 1)
		other := createTestLabel(t, f.db, neighborsNamelessLabelB, 1)
		before := f.add(odd, neighborsNamelessLabelA)
		anchor := f.add(odd, neighborsNamelessLabelA)
		after := f.add(other, neighborsNamelessLabelB)
		n, err := f.repo.GetNeighborIDs(t.Context(), anchor)
		require.NoError(t, err)
		assert.Equal(t, DetectionNeighbors{Previous: before, Next: after}, *n,
			"a name with no species part must not match every label that starts with an underscore")
	})

	t.Run("a deleted and a never used id are not found", func(t *testing.T) {
		f := newNeighborsFixture(t)
		label := createTestLabel(t, f.db, neighborsSpeciesA, 1)
		f.add(label, neighborsSpeciesA)
		deleted := f.add(label, neighborsSpeciesA)
		f.remove(deleted)
		for _, id := range []uint{deleted, neighborsNeverUsedID} {
			_, err := f.repo.GetNeighborIDs(t.Context(), id)
			require.ErrorIs(t, err, ErrDetectionNotFound, "id %d", id)
		}
	})
}

// TestDetectionRepository_GetNeighborIDs_UsesIndexSeeks pins that every
// statement on the detections table is an index seek. The slower forms return the
// same rows, so only the plan tells them apart.
func TestDetectionRepository_GetNeighborIDs_UsesIndexSeeks(t *testing.T) {
	f := newNeighborsFixture(t)
	parusM1 := createTestLabel(t, f.db, neighborsSpeciesA, 1)
	parusM2 := createTestLabel(t, f.db, neighborsSpeciesA, 2)
	f.add(parusM1, neighborsSpeciesA)
	anchor := f.add(parusM2, neighborsSpeciesA)
	f.add(parusM1, neighborsSpeciesA)

	var statements []string
	require.NoError(t, f.db.Callback().Row().After("gorm:row").Register(neighborsCaptureCallback, func(tx *gorm.DB) {
		statements = append(statements, tx.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
	}))
	_, err := f.repo.GetNeighborIDs(t.Context(), anchor)
	// Remove the capture before any EXPLAIN, which goes through the same chain.
	require.NoError(t, f.db.Callback().Row().Remove(neighborsCaptureCallback))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(statements), neighborsMinStatements, "the overall pair and one pair per label")

	indexed := 0
	for _, statement := range statements {
		var plan []struct{ Detail string }
		require.NoError(t, f.db.Raw("EXPLAIN QUERY PLAN "+statement).Scan(&plan).Error, statement)
		for _, row := range plan {
			assert.NotContains(t, row.Detail, neighborsTempBTree, statement)
			if !strings.Contains(row.Detail, neighborsTableName) {
				continue
			}
			assert.True(t, strings.HasPrefix(row.Detail, neighborsSearchPrefix), "not a seek: %s (%s)", row.Detail, statement)
			if strings.Contains(row.Detail, neighborsIndexName) && strings.Contains(row.Detail, neighborsRowidBound) {
				indexed++
			}
		}
	}
	assert.GreaterOrEqual(t, indexed, 2*(neighborsMinStatements-1), "both subqueries of every label statement seek idx_detection_model_label with a rowid bound")
}

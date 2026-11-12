package noisebank

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	spaceA = "space-a"
	spaceB = "space-b"

	// similarityTolerance covers float32 accumulation over a few components.
	similarityTolerance = 1e-5
)

// TestBank_Match pins what a query can and cannot match: only vectors of its own
// space and dimension, scored by cosine similarity whatever the vector length.
func TestBank_Match(t *testing.T) {
	stored := []float32{1, 0, 0}
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))

	tests := []struct {
		name   string
		space  string
		query  []float32
		want   float32
		wantOK bool
	}{
		{"identical vector", spaceA, []float32{1, 0, 0}, 1, true},
		{"scaled copy", spaceA, []float32{7, 0, 0}, 1, true},
		{"orthogonal vector", spaceA, []float32{0, 1, 0}, 0, true},
		{"opposite vector", spaceA, []float32{-1, 0, 0}, -1, true},
		{"diagonal vector", spaceA, []float32{1, 1, 0}, float32(math.Sqrt2 / 2), true},
		{"other space", spaceB, []float32{1, 0, 0}, 0, false},
		{"dimension mismatch", spaceA, []float32{1, 0}, 0, false},
		{"zero vector", spaceA, []float32{0, 0, 0}, 0, false},
		{"empty vector", spaceA, nil, 0, false},
		{"NaN component", spaceA, []float32{nan, 0, 0}, 0, false},
		{"infinite component", spaceA, []float32{inf, 0, 0}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			b.Add(spaceA, [][]float32{stored})
			got, ok := b.Match(tt.space, tt.query)
			assert.Equal(t, tt.wantOK, ok)
			assert.InDelta(t, tt.want, got, similarityTolerance)
		})
	}

	t.Run("empty bank", func(t *testing.T) {
		got, ok := New().Match(spaceA, stored)
		assert.False(t, ok)
		assert.Zero(t, got)
	})

	t.Run("best of several", func(t *testing.T) {
		b := New()
		b.Add(spaceA, [][]float32{{0, 1, 0}, {1, 1, 0}, {0, 0, 1}})
		got, ok := b.Match(spaceA, []float32{1, 1, 0})
		require.True(t, ok)
		assert.InDelta(t, 1, got, similarityTolerance)
	})
}

// TestBank_RejectsUnusableVectorsOnTheWayIn pins that a zero or non-finite vector
// and a vector of the wrong dimension are never stored, so they cannot match.
func TestBank_RejectsUnusableVectorsOnTheWayIn(t *testing.T) {
	nan := float32(math.NaN())
	b := New()
	b.Add(spaceA, [][]float32{{1, 0}, {0, 0}, {nan, 1}, {1, 0, 0}, nil})
	assert.Equal(t, 1, b.Len(spaceA), "only the first vector is usable")

	b.Replace(1, spaceB, [][]float32{{0, 0}})
	assert.Equal(t, 0, b.Len(spaceB))

	b.Load([]Example{
		{Space: spaceB, Vector: []float32{0, 1}},
		{Space: spaceB, Vector: []float32{nan, 1}},
		{Space: spaceB, Vector: []float32{1, 0, 0}},
	})
	assert.Equal(t, 1, b.Len(spaceB))
	assert.Equal(t, 0, b.Len(spaceA), "Load replaces the whole bank")
}

// TestBank_MatchSeesNormalisedExamples pins that a stored example is compared by
// direction: a long stored vector must not outscore a short one.
func TestBank_MatchSeesNormalisedExamples(t *testing.T) {
	b := New()
	b.Add(spaceA, [][]float32{{100, 0}})
	got, ok := b.Match(spaceA, []float32{1, 1})
	require.True(t, ok)
	assert.InDelta(t, math.Sqrt2/2, got, similarityTolerance)
}

// bankRow is one row of the naive model the transition table checks the bank
// against.
type bankRow struct {
	owner uint
	space string
	vec   []float32
}

// naiveBank recomputes every answer from the plain list of rows it should hold.
type naiveBank []bankRow

func (n naiveBank) dim(space string) int {
	for _, r := range n {
		if r.space == space {
			return len(r.vec)
		}
	}
	return 0
}

func (n naiveBank) len(space string) int {
	count := 0
	for _, r := range n {
		if r.space == space {
			count++
		}
	}
	return count
}

func (n naiveBank) match(space string, q []float32) (float32, bool) {
	best, found := float32(-2), false
	for _, r := range n {
		if r.space != space || len(r.vec) != len(q) {
			continue
		}
		var dot, nr, nq float64
		for i := range q {
			dot += float64(r.vec[i]) * float64(q[i])
			nr += float64(r.vec[i]) * float64(r.vec[i])
			nq += float64(q[i]) * float64(q[i])
		}
		if s := float32(dot / math.Sqrt(nr*nq)); s > best {
			best, found = s, true
		}
	}
	return best, found
}

func (n naiveBank) withoutOwner(owner uint) naiveBank {
	var out naiveBank
	for _, r := range n {
		if r.owner != owner {
			out = append(out, r)
		}
	}
	return out
}

// TestBank_Transitions pins that after every step of a sequence the bank holds
// exactly the rows a plain list says it should: attached and detached examples,
// two spaces, a relearn of the same detection, removal and a reload.
func TestBank_Transitions(t *testing.T) {
	const (
		detOne = uint(11)
		detTwo = uint(22)
	)
	var (
		e1 = []float32{1, 0, 0}
		e2 = []float32{0, 1, 0}
		e3 = []float32{0, 0, 1}
		e4 = []float32{1, 1, 0}
		f1 = []float32{1, 0}
		f2 = []float32{0, 1}
	)
	probes := []struct {
		space string
		q     []float32
	}{
		{spaceA, e1}, {spaceA, e2}, {spaceA, e3}, {spaceA, e4}, {spaceA, []float32{1, 1, 1}},
		{spaceB, f1}, {spaceB, f2}, {spaceB, []float32{1, 2}},
	}

	type step struct {
		name  string
		apply func(b *Bank, n naiveBank) naiveBank
	}
	steps := []step{
		{"add detached", func(b *Bank, n naiveBank) naiveBank {
			b.Add(spaceA, [][]float32{e1})
			return append(n, bankRow{0, spaceA, e1})
		}},
		{"replace detection one", func(b *Bank, n naiveBank) naiveBank {
			b.Replace(detOne, spaceA, [][]float32{e2, e3})
			return append(n, bankRow{detOne, spaceA, e2}, bankRow{detOne, spaceA, e3})
		}},
		{"replace detection two in another space", func(b *Bank, n naiveBank) naiveBank {
			b.Replace(detTwo, spaceB, [][]float32{f1})
			return append(n, bankRow{detTwo, spaceB, f1})
		}},
		{"replace detection one again", func(b *Bank, n naiveBank) naiveBank {
			b.Replace(detOne, spaceA, [][]float32{e4})
			return append(n.withoutOwner(detOne), bankRow{detOne, spaceA, e4})
		}},
		{"relearn detection one in another space", func(b *Bank, n naiveBank) naiveBank {
			b.Replace(detOne, spaceB, [][]float32{f2})
			return append(n.withoutOwner(detOne), bankRow{detOne, spaceB, f2})
		}},
		{"remove detection two", func(b *Bank, n naiveBank) naiveBank {
			b.Remove(detTwo)
			return n.withoutOwner(detTwo)
		}},
		{"remove a detection that owns nothing", func(b *Bank, n naiveBank) naiveBank {
			b.Remove(99)
			return n
		}},
		{"remove detection zero changes nothing", func(b *Bank, n naiveBank) naiveBank {
			b.Remove(0)
			return n
		}},
		{"replace with no vectors clears the detection", func(b *Bank, n naiveBank) naiveBank {
			b.Replace(detOne, spaceB, nil)
			return n.withoutOwner(detOne)
		}},
		{"replace with detection zero changes nothing", func(b *Bank, n naiveBank) naiveBank {
			b.Replace(0, spaceA, [][]float32{e2})
			return n
		}},
		{"load", func(b *Bank, n naiveBank) naiveBank {
			b.Load([]Example{
				{DetectionID: detOne, Space: spaceA, Vector: e3},
				{Space: spaceA, Vector: e1},
				{DetectionID: detTwo, Space: spaceB, Vector: f2},
			})
			return naiveBank{{detOne, spaceA, e3}, {0, spaceA, e1}, {detTwo, spaceB, f2}}
		}},
		{"remove after load", func(b *Bank, n naiveBank) naiveBank {
			b.Remove(detOne)
			return n.withoutOwner(detOne)
		}},
		{"load nothing", func(b *Bank, _ naiveBank) naiveBank {
			b.Load(nil)
			return nil
		}},
	}

	b := New()
	var model naiveBank
	for _, s := range steps {
		model = s.apply(b, model)
		for _, space := range []string{spaceA, spaceB} {
			assert.Equal(t, model.len(space), b.Len(space), "%s: Len(%s)", s.name, space)
		}
		for _, p := range probes {
			if model.dim(p.space) != len(p.q) && model.len(p.space) > 0 {
				continue
			}
			want, wantOK := model.match(p.space, p.q)
			got, ok := b.Match(p.space, p.q)
			assert.Equal(t, wantOK, ok, "%s: Match(%s, %v) ok", s.name, p.space, p.q)
			if wantOK {
				assert.InDelta(t, want, got, similarityTolerance, "%s: Match(%s, %v)", s.name, p.space, p.q)
			}
		}
	}
}

// TestBank_WritersDoNotDisturbReaders pins the copy on write: a snapshot a reader
// is scanning is never modified, so a writer cannot corrupt a Match in flight.
func TestBank_WritersDoNotDisturbReaders(t *testing.T) {
	b := New()
	b.Replace(1, spaceA, [][]float32{{1, 0}})
	before := b.snap.Load()

	b.Replace(1, spaceA, [][]float32{{0, 1}})
	b.Add(spaceA, [][]float32{{1, 1}})
	b.Remove(1)

	sp := before.spaces[spaceA]
	require.NotNil(t, sp)
	assert.Equal(t, []uint{1}, sp.owners)
	assert.Equal(t, []float32{1, 0}, sp.data)
}

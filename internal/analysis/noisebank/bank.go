// Package noisebank holds the noise examples a user has marked and compares the
// embedding of an analysis window with them.
//
// An example is the embedding of an audio window that held no animal. A window
// whose embedding is close to an example of the same embedding space is judged to
// hold no call either. The bank lives in memory so the comparison costs one scan
// of the examples per window and never touches the database.
package noisebank

import (
	"maps"
	"math"
	"sync"
	"sync/atomic"
)

const (
	// MatchSimilarity is the cosine similarity at or above which a window counts as
	// matching a noise example.
	MatchSimilarity = 0.80

	// MaxExamplesPerSpace is the most examples one embedding space may hold. It
	// bounds the scan a window pays (a few milliseconds on a Raspberry Pi 4) and the
	// memory of the bank. The bank itself never evicts: whoever adds examples
	// refuses to go past the cap, because removing an example is an action the user
	// takes, not one the bank takes on their behalf.
	MaxExamplesPerSpace = 2000
)

// Example is one stored noise example.
type Example struct {
	// DetectionID is the detection the example was learned from, or zero for an
	// example whose detection no longer exists.
	DetectionID uint
	// Space names the embedding space the vector belongs to.
	Space string
	// Vector is the window embedding.
	Vector []float32
}

// Bank is a set of noise examples grouped by embedding space. Readers never block:
// they scan an immutable snapshot that writers replace whole, so a Match in
// progress is not affected by a concurrent write. The zero value is not ready for
// use; create a Bank with New.
type Bank struct {
	mu   sync.Mutex // serialises writers
	snap atomic.Pointer[snapshot]
}

// snapshot is the content of a Bank at one moment. It is never modified after it
// is published.
type snapshot struct {
	spaces map[string]*space
}

// space holds the examples of one embedding space in one contiguous slice.
type space struct {
	dim    int       // length of every vector
	data   []float32 // len(owners)*dim values, each vector scaled to unit length
	owners []uint    // detection ID of each vector, zero when detached
}

// New returns an empty Bank.
func New() *Bank {
	b := &Bank{}
	b.snap.Store(&snapshot{})
	return b
}

// Match compares v with the examples of space and returns the highest cosine
// similarity. ok is false when the space holds no example of v's dimension or v
// cannot be compared (empty, all zero or not finite); similarity is then zero.
// It allocates nothing and is safe to call while examples are being written.
func (b *Bank) Match(spaceName string, v []float32) (similarity float32, ok bool) {
	sp := b.snap.Load().spaces[spaceName]
	if sp == nil || len(v) != sp.dim {
		return 0, false
	}
	norm, usable := vectorNorm(v)
	if !usable {
		return 0, false
	}

	best := float32(math.Inf(-1))
	for off := 0; off < len(sp.data); off += sp.dim {
		var dot float32
		for i, x := range sp.data[off : off+sp.dim] {
			dot += x * v[i]
		}
		best = max(best, dot)
	}
	return best / norm, true
}

// Len returns the number of examples in space.
func (b *Bank) Len(spaceName string) int {
	if sp := b.snap.Load().spaces[spaceName]; sp != nil {
		return len(sp.owners)
	}
	return 0
}

// Replace makes vectors the examples of detectionID in the named space, dropping
// every example that detection owned before, in any space. A vector that is empty,
// all zero, not finite or of another dimension than the space is skipped. A
// detectionID of zero is ignored: zero marks a detached example, and replacing
// those wholesale would drop examples nobody asked to drop.
func (b *Bank) Replace(detectionID uint, spaceName string, vectors [][]float32) {
	if detectionID == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	next := b.snap.Load().without(detectionID).with(spaceName, owned(detectionID, vectors))
	b.snap.Store(next)
}

// Add stores vectors as detached examples in the named space, skipping the
// unusable ones as Replace does.
func (b *Bank) Add(spaceName string, vectors [][]float32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.Store(b.snap.Load().with(spaceName, owned(0, vectors)))
}

// Remove drops every example detectionID owns, in every space. Detached examples
// are not touched.
func (b *Bank) Remove(detectionID uint) {
	if detectionID == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.Store(b.snap.Load().without(detectionID))
}

// Load replaces the whole content of the bank with examples, skipping the
// unusable ones as Replace does.
func (b *Bank) Load(examples []Example) {
	bySpace := make(map[string][]Example)
	for i := range examples {
		bySpace[examples[i].Space] = append(bySpace[examples[i].Space], examples[i])
	}
	next := &snapshot{}
	for name, group := range bySpace {
		next = next.with(name, group)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap.Store(next)
}

// owned wraps vectors as examples of one owner.
func owned(owner uint, vectors [][]float32) []Example {
	examples := make([]Example, len(vectors))
	for i, v := range vectors {
		examples[i] = Example{DetectionID: owner, Vector: v}
	}
	return examples
}

// without returns a snapshot lacking the examples detectionID owns. Spaces that
// owner does not appear in are shared with s, not copied.
func (s *snapshot) without(detectionID uint) *snapshot {
	next := &snapshot{spaces: make(map[string]*space, len(s.spaces))}
	for name, sp := range s.spaces {
		if kept := sp.without(detectionID); kept != nil {
			next.spaces[name] = kept
		}
	}
	return next
}

// with returns a snapshot that also holds the usable examples in the named space;
// the Space field of an example is not read. A space that is not in s is created
// with the dimension of the first usable vector, and an existing space keeps its
// dimension.
func (s *snapshot) with(spaceName string, examples []Example) *snapshot {
	next := &snapshot{spaces: make(map[string]*space, len(s.spaces)+1)}
	maps.Copy(next.spaces, s.spaces)

	var grown *space
	for i := range examples {
		v := examples[i].Vector
		norm, usable := vectorNorm(v)
		if !usable {
			continue
		}
		if grown == nil {
			grown = next.spaces[spaceName].clone(len(v), len(examples))
		}
		if len(v) != grown.dim {
			continue
		}
		for _, x := range v {
			grown.data = append(grown.data, x/norm)
		}
		grown.owners = append(grown.owners, examples[i].DetectionID)
	}
	if grown != nil && len(grown.owners) > 0 {
		next.spaces[spaceName] = grown
	}
	return next
}

// without returns the space minus the vectors owner holds: sp itself when owner
// holds none, nil when nothing is left.
func (sp *space) without(owner uint) *space {
	held := 0
	for _, o := range sp.owners {
		if o == owner {
			held++
		}
	}
	switch held {
	case 0:
		return sp
	case len(sp.owners):
		return nil
	}
	kept := &space{
		dim:    sp.dim,
		data:   make([]float32, 0, len(sp.data)-held*sp.dim),
		owners: make([]uint, 0, len(sp.owners)-held),
	}
	for i, o := range sp.owners {
		if o != owner {
			kept.data = append(kept.data, sp.data[i*sp.dim:(i+1)*sp.dim]...)
			kept.owners = append(kept.owners, o)
		}
	}
	return kept
}

// clone returns a private copy of sp with room for extra more vectors; a nil sp
// gives an empty space of dimension dim. The copy keeps sp's dimension.
func (sp *space) clone(dim, extra int) *space {
	if sp == nil {
		sp = &space{dim: dim}
	}
	grown := &space{
		dim:    sp.dim,
		data:   make([]float32, len(sp.data), len(sp.data)+extra*sp.dim),
		owners: make([]uint, len(sp.owners), len(sp.owners)+extra),
	}
	copy(grown.data, sp.data)
	copy(grown.owners, sp.owners)
	return grown
}

// vectorNorm returns the Euclidean length of v as a float32. usable is false for
// an empty vector, a zero vector, or one with a NaN or infinite component or a
// length that does not fit a float32.
func vectorNorm(v []float32) (norm float32, usable bool) {
	if len(v) == 0 {
		return 0, false
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	if n == 0 || math.IsNaN(n) || math.IsInf(n, 0) || n > math.MaxFloat32 {
		return 0, false
	}
	return float32(n), true
}

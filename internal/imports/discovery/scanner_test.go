package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedProvider struct{ roots []Root }

func (p fixedProvider) Roots() []Root { return p.roots }

func TestScanner_FindsBirdsDbUnderRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "pi", "BirdNET-Pi")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	dbPath := filepath.Join(nested, "birds.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("x"), 0o600))

	probed := map[string]bool{}
	s := NewScanner(
		fixedProvider{roots: []Root{{Path: root, Kind: KindLocal}}},
		WithProber(func(_ context.Context, p string, k Kind) SourceCandidate {
			probed[p] = true
			return SourceCandidate{Path: p, Kind: k, Valid: true}
		}),
	)

	got := s.Scan(t.Context())
	require.Len(t, got, 1)
	assert.Equal(t, dbPath, got[0].Path)
	assert.True(t, probed[dbPath])
}

func TestScanner_RespectsMaxDepth(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "birds.db"), []byte("x"), 0o600))

	s := NewScanner(
		fixedProvider{roots: []Root{{Path: root, Kind: KindLocal}}},
		WithMaxDepth(2),
		WithProber(func(_ context.Context, p string, k Kind) SourceCandidate {
			return SourceCandidate{Path: p, Valid: true}
		}),
	)
	assert.Empty(t, s.Scan(t.Context()))
}

func TestScanner_SkipsNetworkPrefixedRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "birds.db"), []byte("x"), 0o600))

	s := NewScanner(
		fixedProvider{roots: []Root{{Path: root, Kind: KindRemovable}}},
		WithNetworkPrefixes([]string{root}),
		WithProber(func(_ context.Context, p string, k Kind) SourceCandidate {
			return SourceCandidate{Path: p, Valid: true}
		}),
	)
	assert.Empty(t, s.Scan(t.Context()))
}

func TestScanner_StopsAtMaxCandidates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, sub := range []string{"one", "two", "three"} {
		d := filepath.Join(root, sub)
		require.NoError(t, os.MkdirAll(d, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, "birds.db"), []byte("x"), 0o600))
	}
	s := NewScanner(
		fixedProvider{roots: []Root{{Path: root, Kind: KindLocal}}},
		WithMaxCandidates(2),
		WithProber(func(_ context.Context, p string, k Kind) SourceCandidate {
			return SourceCandidate{Path: p, Valid: true}
		}),
	)
	assert.Len(t, s.Scan(t.Context()), 2)
}

func TestScanner_CancelledContextReturnsEarly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "birds.db"), []byte("x"), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := NewScanner(
		fixedProvider{roots: []Root{{Path: root, Kind: KindLocal}}},
		WithTimeout(time.Second),
		WithProber(func(_ context.Context, p string, k Kind) SourceCandidate {
			return SourceCandidate{Path: p, Valid: true}
		}),
	)
	assert.Empty(t, s.Scan(ctx))
}

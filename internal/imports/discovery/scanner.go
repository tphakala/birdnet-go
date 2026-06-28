package discovery

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMaxDepth      = 4
	defaultScanTimeout   = 3 * time.Second
	defaultMaxCandidates = 25
	birdsDBFilename      = "birds.db"
)

// proberFunc inspects a birds.db path and returns a candidate.
type proberFunc func(ctx context.Context, dbPath string, kind Kind) SourceCandidate

// Scanner walks LocationProvider roots for birds.db files within bounded
// depth/time and probes each one.
type Scanner struct {
	provider        LocationProvider
	prober          proberFunc
	maxDepth        int
	timeout         time.Duration
	maxCandidates   int
	networkPrefixes []string
}

// Option configures a Scanner.
type Option func(*Scanner)

// WithMaxDepth bounds how many directory levels below a root are searched.
func WithMaxDepth(d int) Option { return func(s *Scanner) { s.maxDepth = d } }

// WithTimeout bounds the total wall-clock scan time.
func WithTimeout(d time.Duration) Option { return func(s *Scanner) { s.timeout = d } }

// WithMaxCandidates caps how many candidates are returned.
func WithMaxCandidates(n int) Option { return func(s *Scanner) { s.maxCandidates = n } }

// WithProber overrides the per-candidate probe (used in tests).
func WithProber(p proberFunc) Option { return func(s *Scanner) { s.prober = p } }

// WithNetworkPrefixes overrides the network mount prefixes to skip (used in tests).
func WithNetworkPrefixes(p []string) Option { return func(s *Scanner) { s.networkPrefixes = p } }

// NewScanner builds a Scanner with production defaults, overridable via options.
func NewScanner(provider LocationProvider, opts ...Option) *Scanner {
	s := &Scanner{
		provider:        provider,
		prober:          probeCandidate,
		maxDepth:        defaultMaxDepth,
		timeout:         defaultScanTimeout,
		maxCandidates:   defaultMaxCandidates,
		networkPrefixes: defaultNetworkMountPrefixes(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Scan searches all provider roots and returns the discovered candidates.
func (s *Scanner) Scan(ctx context.Context) []SourceCandidate {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	seen := make(map[string]struct{})
	var out []SourceCandidate

	for _, root := range s.provider.Roots() {
		if ctx.Err() != nil {
			return out
		}
		if s.underNetworkMount(root.Path) {
			continue
		}
		out = s.walkRoot(ctx, root, seen, out)
		if len(out) >= s.maxCandidates {
			return out[:s.maxCandidates]
		}
	}
	return out
}

func (s *Scanner) walkRoot(ctx context.Context, root Root, seen map[string]struct{}, out []SourceCandidate) []SourceCandidate {
	rootClean := filepath.Clean(root.Path)
	_ = filepath.WalkDir(rootClean, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			// Unreadable dir or vanished entry: skip this subtree, never abort.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if depthBelow(rootClean, path) > s.maxDepth {
				return filepath.SkipDir
			}
			if s.underNetworkMount(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != birdsDBFilename {
			return nil
		}
		clean := filepath.Clean(path)
		if _, dup := seen[clean]; dup {
			return nil
		}
		seen[clean] = struct{}{}
		out = append(out, s.prober(ctx, clean, root.Kind))
		if len(out) >= s.maxCandidates {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// depthBelow returns how many path separators deep path is below root.
func depthBelow(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// underNetworkMount reports whether path is at or below any network mount prefix.
func (s *Scanner) underNetworkMount(path string) bool {
	clean := filepath.Clean(path)
	for _, prefix := range s.networkPrefixes {
		p := filepath.Clean(prefix)
		if clean == p || strings.HasPrefix(clean, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

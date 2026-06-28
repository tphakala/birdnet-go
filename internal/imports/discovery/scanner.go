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
// Non-positive depth, timeout, or candidate limits are clamped back to the
// defaults so a bad option value cannot disable a bound (a non-positive
// maxCandidates would otherwise make every scan return nothing).
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
	if s.maxDepth <= 0 {
		s.maxDepth = defaultMaxDepth
	}
	if s.timeout <= 0 {
		s.timeout = defaultScanTimeout
	}
	if s.maxCandidates <= 0 {
		s.maxCandidates = defaultMaxCandidates
	}
	return s
}

// Scan searches all provider roots and returns the discovered candidates. The
// walk runs in a separate goroutine so a blocking syscall on a hung mount (a
// dead USB, or a stale network mount whose fstype is not in the skip list)
// cannot block the caller past the timeout. On timeout or cancellation Scan
// returns the candidates collected so far; the walk goroutine is then abandoned
// and unwinds once the context is cancelled (the deferred cancel below) or the
// stuck syscall returns, without holding up the caller.
func (s *Scanner) Scan(ctx context.Context) []SourceCandidate {
	if s.provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	results := make(chan SourceCandidate)
	go s.walk(ctx, results)

	out := make([]SourceCandidate, 0, s.maxCandidates)
	for {
		select {
		case <-ctx.Done():
			return out
		case c, ok := <-results:
			if !ok {
				return out
			}
			out = append(out, c)
			if len(out) >= s.maxCandidates {
				return out
			}
		}
	}
}

// walk visits every provider root, sending each discovered candidate on results,
// and closes results when finished or when ctx is cancelled.
func (s *Scanner) walk(ctx context.Context, results chan<- SourceCandidate) {
	defer close(results)
	seen := make(map[string]struct{})
	for _, root := range s.provider.Roots() {
		if ctx.Err() != nil {
			return
		}
		if s.underNetworkMount(root.Path) {
			continue
		}
		if !s.walkRoot(ctx, root, seen, results) {
			return
		}
	}
}

// walkRoot walks a single root, sending birds.db candidates on results. It
// returns false when the caller should stop (context cancelled), true otherwise.
func (s *Scanner) walkRoot(ctx context.Context, root Root, seen map[string]struct{}, results chan<- SourceCandidate) bool {
	rootClean := filepath.Clean(root.Path)
	cont := true
	_ = filepath.WalkDir(rootClean, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			cont = false
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
		// Only probe regular files. This skips a symlink (whose target could
		// escape the scanned root) and, importantly, a FIFO, socket, or device
		// node named birds.db, on which the probe's os.Open would block the
		// scanner goroutine indefinitely.
		if !d.Type().IsRegular() {
			return nil
		}
		clean := filepath.Clean(path)
		if _, dup := seen[clean]; dup {
			return nil
		}
		seen[clean] = struct{}{}
		cand := s.prober(ctx, clean, root.Kind)
		select {
		case results <- cand:
		case <-ctx.Done():
			cont = false
			return filepath.SkipAll
		}
		return nil
	})
	return cont
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
	sep := string(filepath.Separator)
	for _, prefix := range s.networkPrefixes {
		p := filepath.Clean(prefix)
		// p == sep means the whole filesystem is a network mount; everything is
		// under it (and p+sep would be "//", which HasPrefix would never match).
		if clean == p || p == sep || strings.HasPrefix(clean, p+sep) {
			return true
		}
	}
	return false
}

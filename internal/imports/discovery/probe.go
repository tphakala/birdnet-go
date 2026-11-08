package discovery

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imports/birdnetpi"
)

// BirdNET-Pi audio tree directory names.
const (
	// birdSongsDirName is the BirdNET-Pi audio output directory. It holds the
	// "Extracted" clip tree next to the BirdNET-Pi installation.
	birdSongsDirName = "BirdSongs"
	// extractedDirName is the directory inside an audio root that holds the
	// clips, organised as Extracted/By_Date/<date>/<common name>/<file>.
	extractedDirName = "Extracted"
	// byDateDirName is the directory inside "Extracted" that groups the clips
	// by capture date.
	byDateDirName = "By_Date"
	// audioRootSearchLevels is the number of directory levels that are searched
	// for the audio tree: the database directory plus two parent levels.
	// BirdNET-Pi keeps its database in ~/BirdNET-Pi/scripts and writes its
	// clips to ~/BirdSongs, two levels above the database directory.
	audioRootSearchLevels = 3
	// audioRootCandidatesPerLevel is the number of candidates every searched
	// level contributes: the level itself and a "BirdSongs" directory next to
	// it.
	audioRootCandidatesPerLevel = 2
)

// audioDirNames are sibling directory names that indicate a BirdNET-Pi audio
// tree next to a birds.db.
var audioDirNames = []string{birdSongsDirName, extractedDirName}

// Probe inspects a single birds.db path and returns its candidate metadata. It
// is the manual-entry counterpart to a Scan hit: the wizard's "validate this
// location" endpoint uses it so a typed path is described with the same fields
// (counts, latest date, audio-dir guess, validity reason, owner) as an
// auto-detected card. The kind is KindLocal because a manually entered path has
// no removable/network classification from a scan root.
//
// Unlike the scanner (which only ever probes regular dir entries), Probe takes an
// arbitrary user-supplied path, so it MUST reject non-regular files itself:
// probeCandidate's sqlite open blocks indefinitely on a FIFO or device node,
// which would hang the HTTP handler goroutine. os.Stat (not Lstat) is used so
// a symlink to a valid birds.db is accepted; a symlink to a FIFO or device is
// still rejected by the IsRegular check after the symlink is followed.
//
// Residual TOCTOU: a regular file swapped to a FIFO between Stat and the sqlite
// open is handled by classifyOpenError's O_NONBLOCK open on unix. The import
// path re-validates the source before committing.
func Probe(ctx context.Context, path string) SourceCandidate {
	info, err := os.Stat(path)
	if err != nil {
		// Absent or inaccessible. A permission error on the path itself is
		// surfaced as ReasonPermissionDenied; anything else (not-found, broken
		// symlink) maps to an empty Reason so the API layer labels it "not_found".
		if os.IsPermission(err) {
			return SourceCandidate{Path: path, Kind: KindLocal, Valid: false, Reason: ReasonPermissionDenied, OwnerUID: -1}
		}
		return SourceCandidate{Path: path, Kind: KindLocal, OwnerUID: -1}
	}
	if !info.Mode().IsRegular() {
		// A FIFO, socket, device, or directory (including a symlink to one) is
		// never a valid source and must never be opened.
		return SourceCandidate{Path: path, Kind: KindLocal, Valid: false, Reason: ReasonOpenFailed, OwnerUID: -1}
	}
	return probeCandidate(ctx, path, KindLocal)
}

// probeCandidate inspects a birds.db at dbPath and returns display metadata.
// It never returns an error: failures are encoded as Valid=false plus a Reason.
func probeCandidate(ctx context.Context, dbPath string, kind Kind) SourceCandidate {
	c := SourceCandidate{Path: dbPath, Kind: kind, OwnerUID: -1}

	info, statErr := os.Stat(dbPath)
	if statErr == nil {
		c.Size = info.Size()
		c.OwnerUID, c.OwnerName = fileOwner(info)
	}

	src, err := birdnetpi.New(dbPath)
	if err != nil {
		c.Reason = classifyOpenError(dbPath)
		return c
	}
	defer func() { _ = src.Close() }()

	if err := src.Validate(ctx); err != nil {
		// A cancelled or expired scan context surfaces here as a Validate error;
		// do not mislabel an otherwise valid database as having a bad schema.
		if ctx.Err() != nil {
			return c
		}
		c.Reason = ReasonInvalidSchema
		return c
	}
	if count, err := src.Count(ctx); err == nil {
		c.DetectionCount = count
	}
	if latest, err := src.LatestDate(ctx); err == nil {
		c.LatestDate = latest
	}
	c.AudioDirGuess = guessAudioDir(dbPath)
	c.Valid = true
	return c
}

// guessAudioDir returns the first sibling audio directory next to dbPath, or "".
func guessAudioDir(dbPath string) string {
	parent := filepath.Dir(dbPath)
	for _, name := range audioDirNames {
		candidate := filepath.Join(parent, name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// ResolveAudioRoot returns the directory that holds the BirdNET-Pi audio tree
// of the database at dbPath, and whether such a directory was found.
//
// The returned directory is the one that CONTAINS the "Extracted" directory,
// because a clip is looked up as
// "Extracted/By_Date/<date>/<common name>/<file>" relative to that root.
//
// The search starts at the database's own directory and walks up at most
// audioRootSearchLevels levels. At every level the level itself and a
// "BirdSongs" directory next to it are candidates, because BirdNET-Pi keeps
// its database in ~/BirdNET-Pi/scripts and writes its clips to ~/BirdSongs.
// The first candidate that holds an "Extracted/By_Date" tree wins.
//
// A candidate only counts when the tree is readable inside it, which is
// checked with the same os.Root sandbox the importer opens the audio root
// with: a tree that is only reachable through a symlink leaving the
// candidate does not win, and the search continues with the next candidate.
//
// bound, when not empty, is the directory the caller allows the import to
// read from: the import source root of a container install, or the staging
// directory of a staged import. The search then never leaves it: the walk
// stops at the first level outside bound, and a candidate that resolves
// outside bound (for example a "BirdSongs" symlink pointing elsewhere) is
// skipped. Both checks compare symlink-resolved paths. When bound cannot be
// resolved, no candidate is trusted and nothing is found. An empty bound
// leaves the search unbounded, which is the native install case where the
// user may import from any path the service user can read.
//
// Symlinks are not resolved in the result, so the returned root is the path
// that was found and not a copy of it. The importer opens that path as an
// os.Root, which contains every lookup inside it.
func ResolveAudioRoot(dbPath, bound string) (string, bool) {
	resolvedBound := ""
	if bound != "" {
		var err error
		resolvedBound, err = filepath.EvalSymlinks(bound)
		if err != nil {
			return "", false
		}
	}
	for _, candidate := range audioRootCandidates(dbPath, resolvedBound) {
		if resolvedBound != "" && !resolvesWithin(resolvedBound, candidate) {
			continue
		}
		if !hasExtractedTree(candidate) {
			continue
		}
		return candidate, true
	}
	return "", false
}

// audioRootCandidates returns the directories that may hold the BirdNET-Pi
// audio tree of dbPath, in search order. Every candidate is a directory that is
// expected to contain the "Extracted" directory.
//
// When resolvedBound is not empty (it must already be symlink-resolved), the
// walk stops at the first level that does not resolve inside it, so no
// candidate is taken from above the bound.
func audioRootCandidates(dbPath, resolvedBound string) []string {
	candidates := make([]string, 0, audioRootSearchLevels*audioRootCandidatesPerLevel)
	level := filepath.Dir(dbPath)
	for range audioRootSearchLevels {
		if resolvedBound != "" && !resolvesWithin(resolvedBound, level) {
			break
		}
		// A "BirdSongs" directory holds the "Extracted" tree itself, while an
		// "Extracted" directory next to the database is the tree itself, so the
		// level that holds it is the root.
		candidates = append(candidates, filepath.Join(level, birdSongsDirName), level)
		parent := filepath.Dir(level)
		if parent == level {
			break
		}
		level = parent
	}
	return candidates
}

// resolvesWithin reports whether path, after its symlinks are resolved, lies
// inside resolvedBound (which must already be symlink-resolved). A path that
// does not exist or cannot be resolved is not inside.
func resolvesWithin(resolvedBound, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedBound, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hasExtractedTree reports whether dir holds a BirdNET-Pi "Extracted/By_Date"
// clip tree.
//
// The tree is looked up through an os.Root, the same sandbox the importer
// opens the audio root with, so the answer is the answer the importer gets: a
// tree that only exists through a symlink leaving dir is not found, and the
// search continues with the next candidate.
//
// The tree must also be usable, not only present: the By_Date directory must
// be searchable (isTraversable), so a tree the service user can see but not
// enter is rejected and the search continues with the next candidate instead
// of selecting a root whose every clip would then be a miss.
func hasExtractedTree(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	byDate := filepath.Join(extractedDirName, byDateDirName)
	info, err := root.Stat(byDate)
	if err != nil || !info.IsDir() {
		return false
	}
	return isTraversable(root, byDate) && hasReachableDateDir(root, byDate)
}

// dateDirProbeBatch is how many By_Date entries are read at a time while
// looking for a reachable date directory.
const dateDirProbeBatch = 64

// hasReachableDateDir reports whether the By_Date directory rel inside root is
// empty or holds at least one date directory the importer can enter. Clips
// live one level further down (By_Date/<date>/<species>/<clip>), so a tree
// whose date directories are all closed to the service user, such as a copy
// that kept another user's 0700 modes, would make every clip a miss; such a
// tree is rejected and the search continues with the next candidate.
//
// One reachable date directory is enough. A single closed directory in an
// otherwise readable tree costs only the clips inside it, which the import
// reports as misses, while rejecting the whole tree for it would lose every
// clip. Entries that are not directories (a stray file, a broken symlink) are
// ignored, and the scan stops at the first reachable date directory.
func hasReachableDateDir(root *os.Root, rel string) bool {
	dir, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer func() { _ = dir.Close() }()
	empty := true
	for {
		entries, readErr := dir.ReadDir(dateDirProbeBatch)
		for _, entry := range entries {
			empty = false
			dateRel := filepath.Join(rel, entry.Name())
			info, statErr := root.Stat(dateRel)
			if statErr == nil && info.IsDir() && isTraversable(root, dateRel) {
				return true
			}
		}
		if readErr != nil {
			// io.EOF ends the listing. Any other error means the importer
			// could not list the tree either.
			return empty && errors.Is(readErr, io.EOF)
		}
	}
}

// traversalProbeName is a child name no clip tree contains. Looking it up
// tells whether a directory can be searched without listing it or touching
// any of its real entries.
const traversalProbeName = ".birdnet-go-traversal-probe"

// isTraversable reports whether the clips under the directory rel inside root
// can be reached. It looks up a child name that does not exist through root,
// the same os.Root the importer opens clips with: "not found" means the lookup
// got inside the directory, anything else (permission denied) means the
// importer could not open a clip there either. os.Root opens every directory
// on a path, so this needs the same read and search permission the clip reads
// need, no more. The result does not depend on any real entry, so a broken
// symlink among the clips cannot reject the tree.
func isTraversable(root *os.Root, rel string) bool {
	_, err := root.Lstat(filepath.Join(rel, traversalProbeName))
	return errors.Is(err, fs.ErrNotExist)
}

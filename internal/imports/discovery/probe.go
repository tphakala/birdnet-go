package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"

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

// guessAudioDir returns the first "BirdSongs" or "Extracted" directory in the
// directory of dbPath, or "". The guess is the audio directory itself and is
// only looked for beside the database: it becomes the --audio argument of
// import-stage, which accepts only a sibling of the database. It is not the
// audio root of an import. ResolveAudioRoot finds that: the directory that
// contains "Extracted", searched up to audioRootSearchLevels levels.
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
// of the database at dbPath, and whether one was found. The result is the
// directory that contains "Extracted", because the importer looks a clip up
// as "Extracted/By_Date/<date>/<common name>/<file>" under it.
//
// The search covers audioRootSearchLevels levels, nearest first, starting at
// the database directory. Each level contributes two candidates: its
// "BirdSongs" child, then the level itself. The first candidate with a
// non-empty "Extracted/By_Date" directory wins (see hasExtractedTree).
//
// bound, when not empty, is the directory the import may read from. A
// candidate is skipped unless its symlink-resolved path lies inside the
// symlink-resolved bound, and nothing is found when bound cannot be
// resolved. An empty bound leaves the search unbounded.
//
// dbPath must be absolute; a relative path finds nothing. The result is the
// candidate path as built, with its symlinks unresolved.
func ResolveAudioRoot(dbPath, bound string) (string, bool) {
	if !filepath.IsAbs(dbPath) {
		return "", false
	}
	if bound != "" {
		resolved, err := filepath.EvalSymlinks(bound)
		if err != nil {
			return "", false
		}
		bound = resolved
	}
	level := filepath.Dir(dbPath)
	for range audioRootSearchLevels {
		for _, candidate := range []string{filepath.Join(level, birdSongsDirName), level} {
			if (bound == "" || resolvesWithin(bound, candidate)) && hasExtractedTree(candidate) {
				return candidate, true
			}
		}
		// At the filesystem root Dir returns the root again, so the remaining
		// levels repeat the same two candidates.
		level = filepath.Dir(level)
	}
	return "", false
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

// hasExtractedTree reports whether dir holds a non-empty "Extracted/By_Date"
// directory. The lookup goes through an os.Root, as the importer's clip
// lookups do, so a tree that is only reachable through a symlink leaving dir
// does not count. One entry is read: an empty skeleton must not win over a
// populated tree further up, and a large By_Date is never listed. Whether
// the clips below are readable is left to the import, which counts a clip it
// cannot read as a miss.
func hasExtractedTree(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	byDatePath := filepath.Join(extractedDirName, byDateDirName)
	// Opening a FIFO without a writer blocks, so only a directory is opened.
	if info, err := root.Stat(byDatePath); err != nil || !info.IsDir() {
		return false
	}
	byDate, err := root.Open(byDatePath)
	if err != nil {
		return false
	}
	defer func() { _ = byDate.Close() }()
	names, err := byDate.Readdirnames(1)
	return err == nil && len(names) > 0
}

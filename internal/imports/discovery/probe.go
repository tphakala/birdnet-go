package discovery

import (
	"context"
	"os"
	"path/filepath"

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
// Symlinks are not resolved, so the returned root is the path that was found
// and not a copy of it. The importer opens that path as an os.Root, which
// contains every lookup inside it.
func ResolveAudioRoot(dbPath string) (string, bool) {
	for _, candidate := range audioRootCandidates(dbPath) {
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
func audioRootCandidates(dbPath string) []string {
	candidates := make([]string, 0, audioRootSearchLevels*audioRootCandidatesPerLevel)
	level := filepath.Dir(dbPath)
	for range audioRootSearchLevels {
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

// hasExtractedTree reports whether dir holds a BirdNET-Pi "Extracted/By_Date"
// clip tree.
//
// The tree is looked up through an os.Root, the same sandbox the importer
// opens the audio root with, so the answer is the answer the importer gets: a
// tree that only exists through a symlink leaving dir is not found, and the
// search continues with the next candidate.
func hasExtractedTree(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(filepath.Join(extractedDirName, byDateDirName))
	return err == nil && info.IsDir()
}

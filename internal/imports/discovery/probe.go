package discovery

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/imports/birdnetpi"
)

// audioDirNames are sibling directory names that indicate a BirdNET-Pi audio
// tree next to a birds.db.
var audioDirNames = []string{"BirdSongs", "Extracted"}

// Probe inspects a single birds.db path and returns its candidate metadata. It
// is the manual-entry counterpart to a Scan hit: the wizard's "validate this
// location" endpoint uses it so a typed path is described with the same fields
// (counts, latest date, audio-dir guess, validity reason, owner) as an
// auto-detected card. The kind is KindLocal because a manually entered path has
// no removable/network classification from a scan root.
//
// Unlike the scanner (which only ever probes regular dir entries), Probe takes an
// arbitrary user-supplied path, so it MUST reject non-regular files itself:
// probeCandidate's os.Open blocks indefinitely on a FIFO or device node, which
// would hang the HTTP handler goroutine. Lstat neither follows a final symlink
// nor opens the file.
func Probe(ctx context.Context, path string) SourceCandidate {
	info, err := os.Lstat(path)
	if err != nil {
		// Absent or unstattable: invalid with an empty Reason. The API layer maps
		// the empty Reason to "not_found".
		return SourceCandidate{Path: path, Kind: KindLocal, OwnerUID: -1}
	}
	if !info.Mode().IsRegular() {
		// A symlink, FIFO, socket, device, or directory is never a valid source and
		// must never be opened.
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

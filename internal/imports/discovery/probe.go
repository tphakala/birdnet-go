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
		c.Reason = classifyOpenError(err, dbPath)
		return c
	}
	defer func() { _ = src.Close() }()

	if err := src.Validate(ctx); err != nil {
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

// classifyOpenError maps an open failure to a candidate Reason. A real read
// permission problem on the file is reported as permission-denied; anything else
// is an open failure.
func classifyOpenError(_ error, dbPath string) string {
	if f, err := os.Open(dbPath); err != nil {
		if os.IsPermission(err) {
			return ReasonPermissionDenied
		}
	} else {
		_ = f.Close()
	}
	return ReasonOpenFailed
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

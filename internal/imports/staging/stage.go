// Package staging performs the privileged BirdNET-Pi import staging copy. The
// Stage operation is what the `birdnet-go import-stage` subcommand runs, often
// as root via sudo, so it re-validates the source AFTER opening the file
// descriptor (O_NOFOLLOW + fstat + SQLite magic) to close the TOCTOU window that
// the unprivileged API-side path check cannot.
package staging

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3" // SQLite driver for integrity check
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// stagedDBName is the fixed filename the staged database is written as.
	stagedDBName = "birds.db"
	// sqliteHeaderLen is the length of the SQLite file magic.
	sqliteHeaderLen = 16
	// integrityOK is the expected result of PRAGMA integrity_check.
	integrityOK = "ok"
)

// sqliteMagic is the 16-byte header every SQLite 3 database starts with.
var sqliteMagic = append([]byte("SQLite format 3"), 0x00)

var (
	// ErrUnsupportedPlatform is returned on non-Linux platforms.
	ErrUnsupportedPlatform = errors.NewStd("import staging is only supported on Linux")
	// ErrNotSQLite is returned when the source is not a SQLite database.
	ErrNotSQLite = errors.NewStd("source is not a SQLite database")
	// ErrDstNotEmpty is returned when the destination is missing or not empty.
	ErrDstNotEmpty = errors.NewStd("destination must be an existing empty directory")
	// ErrInvalidOptions is returned for malformed inputs.
	ErrInvalidOptions = errors.NewStd("invalid staging options")
)

// Options configures a staging copy.
type Options struct {
	Src   string // absolute path to the source birds.db
	Audio string // optional absolute path to the source audio directory
	Dst   string // absolute path to an existing empty staging directory we own
	UID   int    // service-user uid to chown staged files to
	GID   int    // service-user gid to chown staged files to
}

// Result reports the staged paths.
type Result struct {
	StagedDB    string
	StagedAudio string
}

// Stage validates and copies the source database (and optional audio) into Dst,
// chowns the copies to UID:GID, and verifies SQLite integrity. On integrity
// failure it rolls back the staged contents.
func Stage(ctx context.Context, opts Options) (Result, error) {
	if err := validateOptions(opts); err != nil {
		return Result{}, err
	}

	srcFile, err := openNoFollow(opts.Src)
	if err != nil {
		return Result{}, errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "open-src").Build()
	}
	defer func() { _ = srcFile.Close() }()

	info, err := srcFile.Stat()
	if err != nil {
		return Result{}, errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "fstat-src").Build()
	}
	if !info.Mode().IsRegular() {
		return Result{}, ErrInvalidOptions
	}

	if err := verifySQLiteMagic(srcFile); err != nil {
		return Result{}, err
	}

	stagedDB := filepath.Join(opts.Dst, stagedDBName)
	if err := copyOpenFileTo(srcFile, stagedDB); err != nil {
		return Result{}, rollback(opts.Dst, err)
	}

	res := Result{StagedDB: stagedDB}
	if opts.Audio != "" {
		dstAudio := filepath.Join(opts.Dst, filepath.Base(opts.Audio))
		if err := copyTree(opts.Audio, dstAudio); err != nil {
			return Result{}, rollback(opts.Dst, err)
		}
		res.StagedAudio = dstAudio
	}

	if err := chownTree(opts.Dst, opts.UID, opts.GID); err != nil {
		return Result{}, rollback(opts.Dst, err)
	}

	if err := verifyIntegrity(ctx, stagedDB); err != nil {
		return Result{}, rollback(opts.Dst, err)
	}

	return res, nil
}

// validateOptions checks that the Options fields are well-formed before any
// privileged operation begins.
func validateOptions(opts Options) error {
	if opts.Src == "" || !filepath.IsAbs(opts.Src) {
		return ErrInvalidOptions
	}
	if opts.Dst == "" || !filepath.IsAbs(opts.Dst) {
		return ErrInvalidOptions
	}

	// Dst must exist, be a directory, and be empty.
	entries, err := os.ReadDir(opts.Dst)
	if err != nil {
		return ErrDstNotEmpty
	}
	if len(entries) > 0 {
		return ErrDstNotEmpty
	}

	// Audio, if set, must be absolute and a strict sibling of Src (same parent
	// directory). This blocks the arbitrary-directory-copy attack where an attacker
	// provides a valid birds.db but --audio=/root/.ssh to extract privileged data.
	if opts.Audio != "" {
		if !filepath.IsAbs(opts.Audio) {
			return ErrInvalidOptions
		}
		srcDir := filepath.Dir(filepath.Clean(opts.Src))
		audioDir := filepath.Dir(filepath.Clean(opts.Audio))
		if audioDir != srcDir {
			return ErrInvalidOptions
		}
	}

	return nil
}

// verifySQLiteMagic reads the first sqliteHeaderLen bytes from the open fd and
// confirms the SQLite format 3 magic. This is the authoritative TOCTOU gate: the
// check runs on the already-open fd, so the file on disk cannot be swapped after
// this point. It seeks back to the start so the copy reads from the beginning.
func verifySQLiteMagic(f *os.File) error {
	hdr := make([]byte, sqliteHeaderLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return ErrNotSQLite
	}
	if !bytes.Equal(hdr, sqliteMagic) {
		return ErrNotSQLite
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "seek-src").Build()
	}
	return nil
}

// copyOpenFileTo copies the content of f to dst. dst is created exclusively and
// without following symlinks (createNoFollow): the staging directory is owned by
// the unprivileged service user, so a plain create could be redirected through a
// pre-planted symlink and have this root process write to an arbitrary path.
func copyOpenFileTo(f *os.File, dst string) error {
	out, err := createNoFollow(dst)
	if err != nil {
		return errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "create-dst").Build()
	}
	if _, err := io.Copy(out, f); err != nil {
		_ = out.Close()
		return errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "copy-db").Build()
	}
	if err := out.Close(); err != nil {
		return errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "close-dst").Build()
	}
	return nil
}

// copyTree copies the directory tree rooted at src into dst, creating dst.
// Every leaf file is opened with openNoFollow and fstat-checked for regularity
// before copying, so a symlink swapped in mid-walk cannot be followed. Symlinked
// leaf entries and non-regular files (devices, FIFOs, sockets) are rejected.
// filepath.WalkDir does not descend into symlinked directories.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "mkdir-dst-audio").Build()
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return errors.New(err).Component("imports").
				Category(errors.CategoryFileIO).Context("op", "walk-rel").Build()
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		// Not a dir: open with O_NOFOLLOW to reject symlinks.
		f, err := openNoFollow(path)
		if err != nil {
			return errors.New(err).Component("imports").
				Category(errors.CategoryFileIO).Context("op", "open-audio-leaf").
				Context("path", path).Build()
		}
		defer func() { _ = f.Close() }()

		info, err := f.Stat()
		if err != nil {
			return errors.New(err).Component("imports").
				Category(errors.CategoryFileIO).Context("op", "fstat-audio-leaf").
				Context("path", path).Build()
		}
		if !info.Mode().IsRegular() {
			// Skip non-regular files (devices, FIFOs, sockets). Symlinks are
			// already rejected by openNoFollow returning ELOOP.
			return nil
		}
		return copyOpenFileTo(f, target)
	})
}

// chownTree walks root and calls chownTo on every entry.
func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return chownTo(path, uid, gid)
	})
}

// verifyIntegrity opens the staged db read-only and runs PRAGMA integrity_check.
// The database must report "ok" for the stage to be considered valid.
func verifyIntegrity(ctx context.Context, dbPath string) error {
	dsn := fmt.Sprintf("file:%s?mode=ro", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return errors.New(err).Component("imports").
			Category(errors.CategoryDatabase).Context("op", "open-integrity").Build()
	}
	defer func() { _ = db.Close() }()

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return errors.New(err).Component("imports").
			Category(errors.CategoryDatabase).Context("op", "integrity-check").Build()
	}
	if result != integrityOK {
		return errors.Newf("SQLite integrity check failed: %s", result).
			Component("imports").Category(errors.CategoryDatabase).Build()
	}
	return nil
}

// rollback removes all contents staged under dst (but not dst itself, which we
// did not create) and returns cause wrapped with rollback context.
func rollback(dst string, cause error) error {
	entries, err := os.ReadDir(dst)
	if err != nil {
		return errors.New(cause).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "rollback").Build()
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dst, e.Name()))
	}
	return errors.New(cause).Component("imports").
		Category(errors.CategoryFileIO).Context("op", "rollback").Build()
}

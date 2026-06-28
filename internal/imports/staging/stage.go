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
	"io"
	"io/fs"
	"net/url"
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
	// ErrDstExists is returned when the destination already exists. Stage creates
	// the staging directory itself, so it must not pre-exist.
	ErrDstExists = errors.NewStd("destination must not already exist")
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

// Stage validates the source, then copies the source database (and optional
// audio) into a staging directory that this process CREATES and owns, verifies
// SQLite integrity, and finally chowns the staged copies to UID:GID. On any
// failure it rolls back by removing the staging directory.
//
// Stage creates opts.Dst itself with mode 0700 (it must not already exist). This
// is essential: when invoked as root via sudo, a staging directory pre-created
// by the unprivileged service user would be writable by that user during the
// copy, letting them swap a parent component for a symlink and redirect root's
// writes/chowns to an arbitrary path. A root-owned 0700 directory the service
// user cannot write into closes that whole class of races; the per-file
// O_NOFOLLOW/O_EXCL/Lchown guards remain as defense in depth on the source side,
// which is owned by the service user and therefore still attacker-controllable.
func Stage(ctx context.Context, opts Options) (Result, error) {
	if err := validateOptions(opts); err != nil {
		return Result{}, err
	}

	// Create the staging directory as this process (root under sudo), 0700, so
	// the service user cannot tamper with its contents during staging. os.Mkdir
	// fails if opts.Dst already exists (including a pre-planted symlink, EEXIST).
	if err := os.Mkdir(opts.Dst, 0o700); err != nil {
		return Result{}, errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "mkdir-staging").Build()
	}

	srcFile, err := openNoFollow(opts.Src)
	if err != nil {
		return Result{}, rollback(opts.Dst, errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "open-src").Build())
	}
	defer func() { _ = srcFile.Close() }()

	info, err := srcFile.Stat()
	if err != nil {
		return Result{}, rollback(opts.Dst, errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "fstat-src").Build())
	}
	if !info.Mode().IsRegular() {
		return Result{}, rollback(opts.Dst, ErrInvalidOptions)
	}

	if err := verifySQLiteMagic(srcFile); err != nil {
		return Result{}, rollback(opts.Dst, err)
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

	// Verify integrity BEFORE handing ownership to the service user, so the user
	// only ever receives a verified copy.
	if err := verifyIntegrity(ctx, stagedDB); err != nil {
		return Result{}, rollback(opts.Dst, err)
	}

	if err := chownTree(opts.Dst, opts.UID, opts.GID); err != nil {
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
	// UID/GID must be real ids: lchown(2) treats -1 as "leave unchanged", so a
	// negative value would silently leave staged files owned by root and unreadable
	// by the service user, while Stage still reports success.
	if opts.UID < 0 || opts.GID < 0 {
		return ErrInvalidOptions
	}

	// Dst must NOT already exist; Stage creates it itself (root-owned 0700) so the
	// service user cannot tamper with it during staging. The parent must exist and
	// be a directory so the create can succeed.
	if _, err := os.Lstat(opts.Dst); err == nil {
		return ErrDstExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errors.New(err).Component("imports").
			Category(errors.CategoryFileIO).Context("op", "lstat-dst").Build()
	}
	parent := filepath.Dir(opts.Dst)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return ErrInvalidOptions
	}

	// Audio, if set, must be absolute and a strict sibling of Src (same parent
	// directory). This blocks the arbitrary-directory-copy attack where an attacker
	// provides a valid birds.db but --audio=/root/.ssh to extract privileged data.
	// It must also differ from Src, else the audio tree copy would collide with the
	// staged birds.db path.
	if opts.Audio != "" {
		if !filepath.IsAbs(opts.Audio) {
			return ErrInvalidOptions
		}
		cleanSrc := filepath.Clean(opts.Src)
		cleanAudio := filepath.Clean(opts.Audio)
		if cleanAudio == cleanSrc {
			return ErrInvalidOptions
		}
		if filepath.Dir(cleanAudio) != filepath.Dir(cleanSrc) {
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
		// Skip non-regular entries (symlinks, FIFOs, sockets, devices) by their
		// Lstat-based type: a symlink must not be copied (and would otherwise abort
		// the whole import on ELOOP), and a FIFO/device would block openNoFollow.
		// The O_NOFOLLOW + fstat-regular re-check below still guards the TOCTOU case
		// where a regular entry is swapped for a symlink after this Lstat.
		if !d.Type().IsRegular() {
			return nil
		}
		// Open with O_NOFOLLOW to reject a symlink raced in after the Lstat above.
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
	// Build the DSN via url.URL so a path containing a URI-special character
	// (space, ?, #, %) is percent-encoded and cannot corrupt the query string.
	// Mirrors internal/imports/birdnetpi/source.go (which cannot be imported here
	// without pulling in GORM + cgo).
	dsn := (&url.URL{Scheme: "file", OmitHost: true, Path: dbPath, RawQuery: "mode=ro"}).String()
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

// rollback removes the staging directory we created and returns cause wrapped
// with rollback context. If the removal itself fails, that is recorded in the
// returned error context so the leftover directory (which would make the next
// attempt fail with ErrDstExists) is not silently swallowed.
func rollback(dst string, cause error) error {
	b := errors.New(cause).Component("imports").
		Category(errors.CategoryFileIO).Context("op", "rollback")
	if rmErr := os.RemoveAll(dst); rmErr != nil {
		b = b.Context("rollback_cleanup_error", rmErr.Error())
	}
	return b.Build()
}

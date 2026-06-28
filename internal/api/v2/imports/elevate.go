package importsapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imports/discovery"
	"github.com/tphakala/birdnet-go/internal/imports/elevation"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// stagingParentDir is the root-owned, sticky system directory under which each
	// per-import staging directory is created. /var/tmp (mode 1777) cannot be
	// renamed or replaced by an unprivileged local user, so handing
	// <stagingParentDir>/<random> to the root import-stage subcommand cannot be
	// redirected via a parent-path symlink swap.
	stagingParentDir = "/var/tmp"
	// stagingDirPrefix names the per-import staging subdirectory.
	stagingDirPrefix = "birdnet-go-import-"
	// stagingTokenBytes is the length of the random staging-subdir name suffix.
	stagingTokenBytes = 16
)

const (
	// responseMethodDirect is returned when the source is directly readable;
	// an import job is launched from the original path.
	responseMethodDirect = "direct"
	// responseMethodSudo is returned when staging succeeded via sudo (passwordless
	// or password); an import job is launched from the staged copy.
	responseMethodSudo = "sudo"
	// responseMethodPasswordRequired is returned when passwordless elevation failed,
	// AllowInAppElevation is enabled, and no password was supplied this call. The UI
	// must prompt for the sudo password and re-POST with it. No job is launched.
	responseMethodPasswordRequired = "password_required"
	// responseMethodFallback is returned when elevation is unavailable or all rungs
	// failed; the response includes copy-paste remediation commands.
	responseMethodFallback = "fallback"
)

var (
	// ErrDiskCheckUnsupported is returned when free-space checks are unavailable.
	ErrDiskCheckUnsupported = errors.NewStd("disk space check unsupported on this platform")
	// ErrInsufficientSpace is returned when the staging base lacks room for the copy.
	ErrInsufficientSpace = errors.NewStd("insufficient disk space for staging copy")
	// ErrStagingBaseUnavailable is returned when no trusted base directory is available.
	ErrStagingBaseUnavailable = errors.NewStd("trusted staging base directory unavailable")
)

// resolveStagingBase returns the trusted parent directory for per-import staging
// dirs. It does NOT create the directory: the parent is a pre-existing root-owned
// sticky system dir, and import-stage creates the random child. Tests override
// via c.stagingBase.
func (c *Handler) resolveStagingBase() (string, error) {
	if c.stagingBase != "" {
		return c.stagingBase, nil
	}
	return stagingParentDir, nil
}

// newStagingDst verifies the base is a trusted (root-owned, sticky) directory,
// then returns a fresh, non-existent <base>/birdnet-go-import-<random> path for
// import-stage to create root-owned 0700 and chown to the service user. The
// random terminal name means a local attacker cannot pre-plant a symlink at the
// path (and even a lucky pre-plant fails import-stage's os.Mkdir with EEXIST).
func (c *Handler) newStagingDst(base string) (string, error) {
	if err := c.verifyTrustedBase(base); err != nil {
		return "", err
	}
	buf := make([]byte, stagingTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New(err).Component("imports").Category(errors.CategoryGeneric).
			Context("op", "staging-token").Build()
	}
	return filepath.Join(base, stagingDirPrefix+hex.EncodeToString(buf)), nil
}

// preflightDiskSpace fails when the staging base filesystem cannot hold the copy.
// free and freeErr are the results of a prior freeBytesFn call; passing them here
// avoids a redundant Statfs syscall when the caller already fetched the value.
func (c *Handler) preflightDiskSpace(free uint64, freeErr error, requiredBytes uint64) error {
	if freeErr != nil {
		return freeErr
	}
	if free < requiredBytes {
		return ErrInsufficientSpace
	}
	return nil
}

// elevateRequest carries the parameters for the /import/elevate endpoint.
type elevateRequest struct {
	// SourcePath is the absolute path to the BirdNET-Pi birds.db.
	SourcePath string `json:"source_path"`
	// Mode selects which data to copy: "db-only" or "db-audio".
	Mode string `json:"mode"`
	// Password is the optional sudo password. Memory-only; never logged or serialized.
	Password elevation.Password `json:"password"`
}

// elevateResponse is returned by POST /import/elevate.
type elevateResponse struct {
	// Method reports how the file was made accessible: "direct", "sudo",
	// "password_required", or "fallback".
	Method string `json:"method"`
	// JobID is set when an import was launched (Method "direct" or "sudo").
	JobID string `json:"job_id,omitempty"`
	// Status mirrors the started import status when JobID is set.
	Status string `json:"status,omitempty"`
	// FallbackCommands are copy-paste shell commands for the "fallback" method.
	FallbackCommands []string `json:"fallback_commands,omitempty"`
}

// ElevateImport attempts to make an unreadable BirdNET-Pi database accessible via
// the elevation ladder (direct read -> passwordless sudo -> in-app password ->
// fallback). On a successful direct or sudo staging it launches the import; on
// password_required it asks the UI to re-POST with a sudo password; on fallback it
// returns copy-paste remediation commands.
//
// Only available for native installs: container installs have no elevation need
// because /external is already mounted at the right place.
func (c *Handler) ElevateImport(ctx echo.Context) error {
	// Belt-and-suspenders: clear the password in the decoded request on return so
	// it is zeroed even if something returns before Ladder.Stage (which also
	// clears its own copy).
	var req elevateRequest
	defer req.Password.Clear()

	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "invalid request body", http.StatusBadRequest)
	}
	// Capture whether the caller supplied a password before the ladder's deferred
	// req.Password.Clear() runs (Stage clears the password, so len(req.Password)
	// is 0 after Stage regardless).
	hadPassword := len(req.Password) > 0

	// Elevation is a native-install-only code path.
	if c.isContainerEnv != nil && c.isContainerEnv() {
		return c.HandleError(ctx, nil, "elevation is not available in container environments", http.StatusBadRequest)
	}

	// Verify the datastore is available before running any staging.
	if c.DS == nil || c.Repo == nil {
		return c.HandleError(ctx, nil, "datastore is not available", http.StatusServiceUnavailable)
	}

	// Validate import mode.
	switch req.Mode {
	case importModeDBOnly:
		// accepted
	case importModeDBaudio:
		// accepted
	default:
		return c.HandleError(ctx, nil, "unsupported import mode", http.StatusBadRequest)
	}

	// Validate the source path: must be absolute and exist (may be unreadable).
	if req.SourcePath == "" || !filepath.IsAbs(req.SourcePath) {
		return c.HandleError(ctx, nil, "source_path must be an absolute path", http.StatusBadRequest)
	}
	cleanSrc := filepath.Clean(req.SourcePath)
	if _, err := os.Lstat(cleanSrc); err != nil {
		return c.HandleError(ctx, err, "source path does not exist", http.StatusBadRequest)
	}

	// Probe the source for size, audio-dir guess, and ownership details.
	cand := discovery.Probe(ctx.Request().Context(), cleanSrc)

	// Determine the audio directory for db-audio mode.
	audioDir := ""
	if req.Mode == importModeDBaudio {
		audioDir = cand.AudioDirGuess
	}

	// Resolve the trusted staging base.
	base, err := c.resolveStagingBase()
	if err != nil {
		return c.HandleError(ctx, err, "staging base unavailable", http.StatusServiceUnavailable)
	}

	// Fetch disk space once; shared by the audio-size estimate and the preflight
	// check to avoid a redundant Statfs syscall.
	free, freeErr := c.freeBytesFn(base)

	// Estimate required bytes: db size + (for db-audio) audio tree size * 2 transient factor.
	requiredBytes := uint64(cand.Size) //nolint:gosec // Size is a non-negative file-size field
	if audioDir != "" {
		audioBytes := dirSizeBounded(ctx.Request().Context(), audioDir, free, dirSizeBoundedNodeCap)
		requiredBytes += audioBytes * 2 // transient factor for copy
		if freeErr != nil {
			c.LogDebugIfEnabled("import: could not check free disk space for audio estimate",
				logger.String("error", freeErr.Error()))
		}
	}
	if pfErr := c.preflightDiskSpace(free, freeErr, requiredBytes); pfErr != nil {
		if errors.Is(pfErr, ErrInsufficientSpace) {
			return c.HandleError(ctx, pfErr, "insufficient disk space for staging", http.StatusInsufficientStorage)
		}
		// ErrDiskCheckUnsupported or other: skip the check rather than blocking
		// (non-linux platforms cannot check; disk checks are best-effort).
	}

	// Pick a random staging destination. If the base is not trusted (returns
	// ErrStagingBaseUnavailable), fall through to the ladder so it can return
	// fallback commands; the ladder never reaches staging for that case anyway.
	dst, dstErr := c.newStagingDst(base)
	if dstErr != nil && !errors.Is(dstErr, ErrStagingBaseUnavailable) {
		return c.HandleError(ctx, dstErr, "could not allocate staging directory", http.StatusInternalServerError)
	}
	// dstErr may be ErrStagingBaseUnavailable here; the ladder will fall through
	// to fallback commands, which is the correct UX.

	// Read settings live per request (hot-reload compliance).
	var allowElevation bool
	if settings := c.CurrentSettings(); settings != nil {
		allowElevation = settings.Import.AllowInAppElevation
	}

	stageReq := elevation.StageRequest{
		Src:            cleanSrc,
		Audio:          audioDir,
		Dst:            dst,
		UID:            os.Getuid(),
		GID:            os.Getgid(),
		Password:       req.Password,
		AllowElevation: allowElevation,
		Owner:          cand.OwnerName,
	}

	ladder, ladderErr := c.newLadder()
	if ladderErr != nil {
		return c.HandleError(ctx, ladderErr, "could not initialize elevation ladder", http.StatusInternalServerError)
	}

	out, stageErr := ladder.Stage(ctx.Request().Context(), &stageReq)
	if stageErr != nil {
		// A ladder error means the ladder itself failed, not a fallback outcome.
		// Best-effort cleanup: import-stage may have partially created dst.
		if dst != "" {
			c.cleanupStagingDir(dst)
		}
		return c.HandleError(ctx, stageErr, "elevation failed", http.StatusInternalServerError)
	}

	// Audit log: method + src + dst. Never log the password.
	c.LogInfoIfEnabled("import: elevation outcome",
		logger.String("method", string(out.Method)),
		logger.String("src", cleanSrc),
		logger.String("dst", dst),
	)

	switch out.Method {
	case elevation.MethodDirect:
		// Source is directly readable; no staging needed. Import the original.
		id, launchErr := c.launchImport(ctx, cleanSrc, req.Mode, nil, "")
		if launchErr != nil {
			return launchErr
		}
		return ctx.JSON(http.StatusAccepted, elevateResponse{
			Method: responseMethodDirect,
			JobID:  id,
			Status: importStatusStarted,
		})

	case elevation.MethodSudoNonInteractive, elevation.MethodSudoPassword:
		// Staging succeeded; import from the staged copy. launchImport's deferred
		// cleanup owns the staging dir lifetime on every non-transferred return.
		id, launchErr := c.launchImport(ctx, out.StagedDB, req.Mode, nil, dst)
		if launchErr != nil {
			return launchErr
		}
		return ctx.JSON(http.StatusAccepted, elevateResponse{
			Method: responseMethodSudo,
			JobID:  id,
			Status: importStatusStarted,
		})

	default: // elevation.MethodFallback
		// A fallback from the ladder means passwordless elevation did not work.
		// If in-app elevation is enabled and the caller supplied no password yet,
		// ask for one rather than dropping straight to copy-paste commands: the
		// password rung is only reachable once the user provides a password.
		if allowElevation && !hadPassword {
			return ctx.JSON(http.StatusOK, elevateResponse{Method: responseMethodPasswordRequired})
		}
		// Return the copy-paste remediation commands. Do NOT remove dst: for a
		// fallback outcome import-stage never ran and dst was never created.
		return ctx.JSON(http.StatusOK, elevateResponse{
			Method:           responseMethodFallback,
			FallbackCommands: out.FallbackCommands,
		})
	}
}

// dirSizeBoundedNodeCap is the maximum number of directory entries visited
// during the audio-tree disk estimate. It prevents a hostile or enormous tree
// from stalling the HTTP handler goroutine.
const dirSizeBoundedNodeCap = 50_000

// dirSizeBounded walks dir summing regular-file sizes, returning early when the
// running total exceeds capBytes, the node count reaches nodeCap, or ctx is
// cancelled. It returns 0 when dir is empty or absent. The bounds prevent a large
// or hostile audio tree from stalling the HTTP handler goroutine.
func dirSizeBounded(ctx context.Context, dir string, capBytes uint64, nodeCap int) uint64 {
	if dir == "" {
		return 0
	}
	var total uint64
	nodes := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if walkErr != nil {
			return nil //nolint:nilerr // skip unreadable entries; we are estimating disk usage, not auditing
		}
		nodes++
		if nodes > nodeCap {
			return fs.SkipAll
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil //nolint:nilerr // skip entries whose info cannot be retrieved during estimation
		}
		total += uint64(info.Size()) //nolint:gosec // file size is non-negative
		if capBytes > 0 && total >= capBytes {
			return fs.SkipAll
		}
		return nil
	})
	return total
}

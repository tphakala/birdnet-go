package importsapi

import (
	"net/http"
	"path/filepath"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/imports/discovery"
)

const (
	// reasonNotFound marks a manual path that does not exist.
	reasonNotFound = "not_found"
	// reasonInvalidPath marks a manual path that is empty or not absolute.
	reasonInvalidPath = "invalid_path"
)

// sourcesResponse describes the import-source discovery result for the wizard.
type sourcesResponse struct {
	Environment   string                      `json:"environment"`
	Containerized bool                        `json:"containerized"`
	RunAsUser     string                      `json:"run_as_user"`
	RunAsUID      int                         `json:"run_as_uid"`
	Candidates    []discovery.SourceCandidate `json:"candidates"`
	Guidance      *discovery.Guidance         `json:"guidance"`
}

// validateRequest is the JSON body for POST /import/validate.
type validateRequest struct {
	SourcePath string `json:"source_path"`
}

// validateResponse is the reply body for POST /import/validate.
type validateResponse struct {
	Valid          bool   `json:"valid"`
	Reason         string `json:"reason"`
	DetectionCount int    `json:"detection_count"`
	LatestDate     string `json:"latest_date"`
	AudioDirGuess  string `json:"audio_dir_guess"`
	OwnerName      string `json:"owner_name"`
}

// GetImportSources scans the per-environment allowlist for importable BirdNET-Pi
// databases and returns the candidates, the run-as identity, and (only when no
// candidate was found) setup guidance.
func (c *Handler) GetImportSources(ctx echo.Context) error {
	info := c.importEnvInfo()
	provider := discovery.SelectProvider(info.envType, info.home)
	candidates := c.scanCandidates(ctx.Request().Context(), provider)
	if candidates == nil {
		candidates = []discovery.SourceCandidate{}
	}

	resp := sourcesResponse{
		Environment:   info.envType,
		Containerized: info.containerized,
		RunAsUser:     info.username,
		RunAsUID:      info.uid,
		Candidates:    candidates,
	}
	if len(candidates) == 0 {
		resp.Guidance = discovery.BuildGuidance(info.envType, info.username)
	}
	return ctx.JSON(http.StatusOK, resp)
}

// ValidateImportSource probes a single manually entered absolute path and reports
// whether it is a readable, valid BirdNET-Pi database, distinguishing not-found,
// unreadable (permission_denied), and invalid-schema cases for the wizard.
func (c *Handler) ValidateImportSource(ctx echo.Context) error {
	var req validateRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "invalid request body", http.StatusBadRequest)
	}
	if req.SourcePath == "" || !filepath.IsAbs(req.SourcePath) {
		return ctx.JSON(http.StatusOK, validateResponse{Valid: false, Reason: reasonInvalidPath})
	}
	cand := discovery.Probe(ctx.Request().Context(), filepath.Clean(req.SourcePath))
	resp := validateResponse{
		Valid:          cand.Valid,
		Reason:         cand.Reason,
		DetectionCount: cand.DetectionCount,
		LatestDate:     cand.LatestDate,
		AudioDirGuess:  cand.AudioDirGuess,
		OwnerName:      cand.OwnerName,
	}
	// Probe returns Reason "" + Valid false only when the file is absent (open
	// failed at stat level). Surface that as not_found so the UI can say "we
	// couldn't find a file there" rather than a generic error.
	if !cand.Valid && cand.Reason == "" {
		resp.Reason = reasonNotFound
	}
	return ctx.JSON(http.StatusOK, resp)
}

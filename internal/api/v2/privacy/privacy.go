// Package privacy is the api/v2 privacy filter domain handler. It serves the
// read-only /api/v2/privacy-filter/* endpoints: the hourly rollup of privacy
// filter activity and the list of discarded detections with the hits behind
// them.
//
// Every route runs the auth middleware before any handler code, and answers 409
// unless the enhanced (v2) database is active. Audio sources are returned as a
// numeric id plus a display name with stream URL credentials removed; the stored
// source URI, node name and configuration are never returned, and neither table
// holds transcripts or audio paths.
package privacy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	// GroupPath is the route prefix of the privacy filter endpoints.
	GroupPath = "/privacy-filter"
	// ActivityPath is the hourly rollup route under GroupPath.
	ActivityPath = "/activity"
	// DiscardsPath is the discarded detections route under GroupPath.
	DiscardsPath = "/discards"

	// queryTimeout bounds the datastore work of one request.
	queryTimeout = 30 * time.Second

	// v2RequiredMessage is the English text of the 409 response sent when the
	// enhanced database is not in use.
	v2RequiredMessage = "Privacy filter activity requires the enhanced (v2) database"

	// milliPerUnit converts the thousandths the tables store to a plain value.
	milliPerUnit = 1000

	// Query parameter names and the request error texts.
	paramStartDate = "start_date"
	paramEndDate   = "end_date"

	msgMissingStartDate = "Missing required parameter: start_date"
	msgInvalidDate      = "Invalid date parameters"
	msgRangeTooLong     = "Date range too long. Maximum: 31 days"
	msgQueryTimeout     = "Request timed out"
	msgQueryCanceled    = "Request canceled by client"
	msgQueryFailed      = "Failed to load privacy filter data"
)

// Handler serves the privacy filter domain endpoints. It embeds *apicore.Core by
// pointer so the shared Core members promote onto it. The repositories are nil
// until RegisterRoutes builds them (only when a v2 manager is set).
type Handler struct {
	*apicore.Core

	hourly   repository.PrivacyFilterHourlyRepository
	discards repository.PrivacyFilterDiscardRepository
	sources  repository.AudioSourceRepository
}

// New creates the privacy filter handler on core.
func New(core *apicore.Core) *Handler {
	return &Handler{Core: core}
}

// SourceRef identifies an audio source without exposing where it reads from.
type SourceRef struct {
	// ID is the numeric audio source id.
	ID uint `json:"id"`
	// Name is the display name with stream URL credentials removed, or
	// source-<id> when the source has no display name.
	Name string `json:"name"`
}

// RegisterRoutes registers the privacy filter activity routes behind the auth
// middleware. They register unconditionally and answer 409 unless the enhanced
// v2 database is active; the repositories are built here when a v2 manager is
// set.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	if c.V2Manager != nil {
		db := c.V2Manager.DB()
		isMySQL := c.V2Manager.IsMySQL()
		useV2Prefix := c.V2Manager.TablePrefix() != ""
		c.hourly = repository.NewPrivacyFilterHourlyRepository(db, nil, useV2Prefix, isMySQL)
		c.discards = repository.NewPrivacyFilterDiscardRepository(db, nil, useV2Prefix)
		c.sources = repository.NewAudioSourceRepository(db, nil, useV2Prefix, isMySQL)
	}

	grp := g.Group(GroupPath, c.AuthMiddleware)
	grp.GET(ActivityPath, c.GetActivity)
	grp.GET(DiscardsPath, c.ListDiscards)
}

// available reports whether the enhanced database is active and the
// repositories were built.
func (c *Handler) available() bool {
	return datastoreV2.IsEnhancedDatabase() && c.hourly != nil && c.discards != nil && c.sources != nil
}

// requireV2 writes the 409 Conflict response sent when the enhanced database is
// not in use.
func (c *Handler) requireV2(ctx echo.Context) error {
	return c.HandleErrorWithKey(ctx, nil,
		v2RequiredMessage, http.StatusConflict, notification.MsgErrPrivacyFilterV2Required, nil)
}

// parseRange reads start_date and end_date and writes the 400 response itself
// when they are missing or invalid. ok is false when a response was written; err
// is then the handler's return value.
func (c *Handler) parseRange(ctx echo.Context) (start, end time.Time, endDate string, ok bool, err error) {
	startDate := ctx.QueryParam(paramStartDate)
	if startDate == "" {
		return time.Time{}, time.Time{}, "", false,
			c.HandleError(ctx, nil, msgMissingStartDate, http.StatusBadRequest)
	}
	start, end, endDate, rangeErr := localDayRange(startDate, ctx.QueryParam(paramEndDate), time.Local)
	switch {
	case errors.Is(rangeErr, ErrRangeTooLong):
		return time.Time{}, time.Time{}, "", false,
			c.HandleError(ctx, rangeErr, msgRangeTooLong, http.StatusBadRequest)
	case rangeErr != nil:
		return time.Time{}, time.Time{}, "", false,
			c.HandleError(ctx, rangeErr, msgInvalidDate, http.StatusBadRequest)
	}
	return start, end, endDate, true, nil
}

// queryError maps a datastore error to an HTTP response: a canceled request to
// 499, a deadline to 408, anything else to 500.
func (c *Handler) queryError(ctx echo.Context, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return c.HandleError(ctx, err, msgQueryCanceled, apicore.StatusClientClosedRequest)
	case errors.Is(err, context.DeadlineExceeded):
		return c.HandleError(ctx, err, msgQueryTimeout, http.StatusRequestTimeout)
	default:
		return c.HandleError(ctx, err, msgQueryFailed, http.StatusInternalServerError)
	}
}

// loadSources loads the audio sources with the given ids in one batch.
func (c *Handler) loadSources(ctx context.Context, ids []uint) (map[uint]*entities.AudioSource, error) {
	return c.sources.GetByIDs(ctx, ids)
}

// sourceRef renders the source with the given id. A source missing from loaded
// (for example deleted between the two queries) renders as source-<id>.
func sourceRef(id uint, loaded map[uint]*entities.AudioSource) *SourceRef {
	return &SourceRef{ID: id, Name: sourceName(id, loaded[id])}
}

// sourceName returns the display name with stream URL credentials removed, or
// source-<id> when there is no usable display name. The stored URI and node name
// are never used.
func sourceName(id uint, src *entities.AudioSource) string {
	if src != nil && src.DisplayName != nil {
		if name := strings.TrimSpace(*src.DisplayName); name != "" {
			return privacy.SanitizeStreamUrls(name)
		}
	}
	return fmt.Sprintf("source-%d", id)
}

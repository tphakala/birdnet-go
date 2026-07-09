// Package alerts is the api/v2 alerts domain handler. It owns the
// /api/v2/alerts/* endpoints (alert-rule CRUD, import/export, history, and the
// schema/test-fire helpers). The Handler embeds *apicore.Core by pointer so the
// shared dependencies and helpers (HandleError, HandleErrorWithKey, the logging
// helpers, the V2Manager and auth middleware) promote onto it.
//
// Unlike the Core-only leaf domains, alerts owns two domain-specific
// dependencies: the alert-rule repository and the alerting engine. Both are
// referenced only by this domain (plus the facade Shutdown), so the Handler owns
// them outright: they are constructed lazily in RegisterRoutes (mirroring the
// old initAlertRoutes) and torn down in Shutdown, which the facade calls.
package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

const maxHistoryLimit = 200

// queryValueTrue is the canonical "true" query-parameter value used when parsing
// optional boolean filters (enabled, built_in). Kept local to the alerts domain
// to avoid rippling the shared constant in package api into unrelated domains.
const queryValueTrue = "true"

// Handler serves the alerts domain endpoints. It embeds *apicore.Core BY POINTER
// so the shared Core members promote onto it without re-wiring; Core carries
// atomic/lock-bearing fields and must never be copied by value. alertRuleRepo and
// alertEngine are owned by this handler: they are nil until RegisterRoutes
// constructs them (only when the enhanced v2 database schema is active), and the
// facade calls Shutdown to stop them on teardown.
//
// alertEngineErr records a non-nil error ONLY when alerting.Initialize was
// attempted (the enhanced v2 schema is active) and failed. It stays nil both when
// the engine initialized successfully and when initialization was deliberately
// skipped (alerting disabled / legacy / migration DB). Engine-dependent handlers
// gate a 503 response on alertEngineErr != nil so an init failure surfaces instead
// of silently no-opping; the "alerting disabled" case is handled separately by the
// 409 group middleware.
type Handler struct {
	*apicore.Core

	alertRuleRepo  repository.AlertRuleRepository
	// alertEngine is accessed from concurrent request goroutines (read) and from
	// ensureEngineReady (write, under initMu). Using atomic.Pointer makes every
	// load/store a single pointer-width atomic op, eliminating the data race
	// without requiring callers to acquire initMu on the read path.
	alertEngine    atomic.Pointer[alerting.Engine]
	alertEngineErr error
	initMu         sync.Mutex
}

// New builds an alerts Handler around the shared core. The alert-rule repository
// and alerting engine are constructed lazily in RegisterRoutes.
func New(core *apicore.Core) *Handler {
	return &Handler{Core: core}
}

// RegisterRoutes registers alert rule API endpoints and starts the alerting engine.
//
// The routes register UNCONDITIONALLY so the documented behavior holds on every
// deployment: when the enhanced (v2) database is unavailable the alert endpoints
// answer 409 Conflict (not 404). The requireV2 group middleware enforces that 409
// for every alert route, and because it keys on alertsAvailable() - the exact
// condition under which alertRuleRepo/alertEngine get built - no registered route
// can ever reach a handler that dereferences a nil repository or manager.
//
// The alerting engine is started only when alertsAvailable() is true, preventing
// background operations (rule seeding, history cleanup) against missing tables on
// legacy/migration databases. The routes, per-route middleware, and order match
// the facade's old initAlertRoutes exactly.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	// Gate every alert route on v2 availability via group middleware so the routes
	// register even when the enhanced DB is absent (answering 409, never 404).
	alerts := g.Group("/alerts", c.requireV2Middleware)

	// Public read endpoints
	alerts.GET("/schema", c.GetAlertSchema)
	alerts.GET("/rules", c.ListAlertRules)
	alerts.GET("/rules/:id", c.GetAlertRule)
	alerts.GET("/history", c.ListAlertHistory)

	// Protected endpoints. The protected subgroup inherits requireV2Middleware from
	// its parent, so v2-unavailable requests get 409 before AuthMiddleware runs.
	protected := alerts.Group("", c.AuthMiddleware)
	protected.GET("/rules/export", c.ExportAlertRules)
	protected.POST("/rules", c.CreateAlertRule)
	protected.PUT("/rules/:id", c.UpdateAlertRule)
	protected.PATCH("/rules/:id/toggle", c.ToggleAlertRule)
	protected.DELETE("/rules/:id", c.DeleteAlertRule)
	protected.POST("/rules/:id/test", c.TestAlertRule)
	protected.POST("/rules/reset-defaults", c.ResetDefaultAlertRules)
	protected.POST("/rules/import", c.ImportAlertRules)
	protected.DELETE("/history", c.ClearAlertHistory)

	// Only initialize the alerting engine when the enhanced v2 schema is active and
	// the v2 manager is present. On legacy/migration databases the alert tables do
	// not exist, so starting the engine would fail during rule seeding and history
	// cleanup. Keying on alertsAvailable() (rather than IsEnhancedDatabase() alone)
	// also guarantees c.V2Manager is non-nil before the c.V2Manager.DB() deref below.
	if !c.alertsAvailable() {
		apicore.GetLogger().Info("alerting engine skipped: v2 database schema not active")
		return
	}

	// Initialize repository lazily from V2Manager
	c.alertRuleRepo = repository.NewAlertRuleRepository(c.V2Manager.DB(), nil)
	speciesListRepo := repository.NewSpeciesListRepository(c.V2Manager.DB(), nil)

	// Initialize the alerting engine - seeds default rules and starts event processing
	alertTelemetry := alerting.NewAlertingTelemetry()
	eventBus := alerting.NewAlertEventBus(alertTelemetry)
	engine, err := alerting.Initialize(c.alertRuleRepo, speciesListRepo, eventBus, apicore.GetLogger(), alertTelemetry)
	if err != nil {
		apicore.GetLogger().Error("failed to initialize alerting engine", logger.Error(err))
		eventBus.Stop() // Stop the bus goroutine since Initialize didn't set it as global
		// Record the failure so engine-dependent handlers surface 503 instead of
		// silently no-opping. CRUD reads still work, but engine actions cannot.
		c.alertEngineErr = err
	} else {
		c.alertEngine.Store(engine)
	}
}

// RefreshSpeciesLists reloads the species lists cache in the alerting engine.
func (c *Handler) RefreshSpeciesLists(ctx context.Context) error {
	if engine := c.alertEngine.Load(); engine != nil {
		return engine.RefreshSpeciesLists(ctx)
	}
	return nil
}

// alertsAvailable reports whether the enhanced (v2) database backing the
// alert-rule repository and engine is available. It is the single source of truth
// for "alerts can do real work": the requireV2 group middleware and the lazy
// repo/engine initialization both consult it, so no registered alert route can
// reach a handler whose repository was never built, and the c.V2Manager.DB() deref
// in RegisterRoutes can never nil-panic. It is stricter than IsEnhancedDatabase()
// alone (which can be true with a nil V2Manager only in pathological test states);
// in every real deployment the two agree because Start sets the enhanced-mode flag
// and the v2 manager together.
func (c *Handler) alertsAvailable() bool {
	return c.V2Manager != nil && datastoreV2.IsEnhancedDatabase()
}

// requireV2Middleware returns 409 Conflict for every alert route when the enhanced
// (v2) database is unavailable, allowing the routes to register unconditionally
// (answering 409, never 404) while guaranteeing no handler runs against a nil
// alert-rule repository.
func (c *Handler) requireV2Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		if !c.alertsAvailable() {
			return c.requireV2(ctx)
		}
		return next(ctx)
	}
}

// Shutdown stops the alerting engine's background goroutines and its global event
// bus. The facade Controller.Shutdown calls this in the same order the monolith
// used (after closing SSE clients, before cancelling the controller context). It
// is safe to call when the engine was never initialized: alertEngine is nil and
// GetGlobalBus returns nil when Initialize failed or was skipped.
func (c *Handler) Shutdown() {
	if engine := c.alertEngine.Load(); engine != nil {
		engine.Stop()
	}
	if bus := alerting.GetGlobalBus(); bus != nil {
		bus.Stop()
	}
}

// validateEscalationSteps checks that escalation steps (when provided) contain no
// empty slices, no negative values, and no duplicates. Valid steps are sorted ascending
// in place for consistent display in the UI.
func validateEscalationSteps(steps []float64) error {
	if steps == nil {
		return nil // nil means "no escalation"
	}
	if len(steps) == 0 {
		return fmt.Errorf("escalation_steps must be nil (no escalation) or a non-empty array")
	}
	seen := make(map[float64]struct{}, len(steps))
	for _, v := range steps {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("escalation_steps must contain finite numbers, got %g", v)
		}
		if v < 0 {
			return fmt.Errorf("escalation_steps must not contain negative values, got %g", v)
		}
		if _, dup := seen[v]; dup {
			return fmt.Errorf("escalation_steps must not contain duplicates, got %g twice", v)
		}
		seen[v] = struct{}{}
	}
	slices.Sort(steps)
	return nil
}

// bindAndValidateAlertRule binds and validates the alert rule from the request body.
// On validation failure, it writes the error response and returns nil with the written error.
// Callers should check: if rule == nil { return err }
func (c *Handler) bindAndValidateAlertRule(ctx echo.Context) (*entities.AlertRule, error) {
	var rule entities.AlertRule
	if err := ctx.Bind(&rule); err != nil {
		return nil, c.HandleErrorWithKey(ctx, err, "Invalid request body", http.StatusBadRequest, notification.MsgErrAlertInvalidBody, nil)
	}
	if rule.Name == "" {
		return nil, c.HandleErrorWithKey(ctx, nil, "Rule name is required", http.StatusBadRequest, notification.MsgErrAlertNameRequired, nil)
	}
	if rule.ObjectType == "" || rule.TriggerType == "" {
		return nil, c.HandleErrorWithKey(ctx, nil, "Object type and trigger type are required", http.StatusBadRequest, notification.MsgErrAlertTypesRequired, nil)
	}
	if err := validateEscalationSteps(rule.EscalationSteps); err != nil {
		return nil, c.HandleErrorWithKey(ctx, err, err.Error(), http.StatusBadRequest, notification.MsgErrAlertInvalidEscalation, nil)
	}
	return &rule, nil
}

// requireV2 writes the 409 Conflict response used when the enhanced (v2) database
// is unavailable. It is the body of requireV2Middleware, which gates every alert
// route on alertsAvailable().
func (c *Handler) requireV2(ctx echo.Context) error {
	return c.HandleErrorWithKey(ctx, nil,
		"Alert rules require the enhanced (v2) database", http.StatusConflict, notification.MsgErrAlertV2Required, nil)
}

// engineUnavailableLocked writes a 503 Service Unavailable response carrying the
// recorded alerting-engine initialization error when one is present.
// Callers must hold c.initMu before calling this.
func (c *Handler) engineUnavailableLocked(ctx echo.Context) error {
	return c.HandleErrorWithKey(ctx, c.alertEngineErr,
		"Alerting engine is unavailable", http.StatusServiceUnavailable, notification.MsgErrAlertEngineUnavailable, nil)
}

// engineUnavailable writes a 503 Service Unavailable response carrying the recorded
// alerting-engine initialization error when one is present. It acquires the mutex
// to prevent data races on alertEngineErr.
func (c *Handler) engineUnavailable(ctx echo.Context) error {
	c.initMu.Lock()
	err := c.alertEngineErr
	c.initMu.Unlock()
	return c.HandleErrorWithKey(ctx, err,
		"Alerting engine is unavailable", http.StatusServiceUnavailable, notification.MsgErrAlertEngineUnavailable, nil)
}

// ensureEngineReady checks if the alerting engine is available and, if not,
// attempts to re-initialize it to recover from transient startup failures.
// It returns true if the engine is ready, and false if it is unavailable (in which
// case the error response has already been written to the client).
func (c *Handler) ensureEngineReady(ctx echo.Context) bool {
	// Fast path: engine already running — atomic load, no lock needed.
	if c.alertEngine.Load() != nil {
		return true
	}

	// Slow path: attempt re-initialization under the mutex so only one goroutine
	// retries at a time. A second goroutine that races here will wait, then see
	// the engine non-nil on its post-lock check and return true immediately.
	c.initMu.Lock()
	defer c.initMu.Unlock()

	// Double-check after acquiring the lock.
	if c.alertEngine.Load() != nil {
		return true
	}

	if !c.alertsAvailable() {
		_ = c.engineUnavailableLocked(ctx)
		return false
	}

	apicore.GetLogger().Info("attempting to re-initialize alerting engine after previous failure")

	speciesListRepo := repository.NewSpeciesListRepository(c.V2Manager.DB(), nil)
	alertTelemetry := alerting.NewAlertingTelemetry()
	eventBus := alerting.NewAlertEventBus(alertTelemetry)
	engine, err := alerting.Initialize(c.alertRuleRepo, speciesListRepo, eventBus, apicore.GetLogger(), alertTelemetry)
	if err != nil {
		apicore.GetLogger().Error("failed to re-initialize alerting engine", logger.Error(err))
		eventBus.Stop()
		c.alertEngineErr = err
		_ = c.engineUnavailableLocked(ctx)
		return false
	}

	c.alertEngine.Store(engine)
	c.alertEngineErr = nil
	return true
}

// GetAlertSchema returns the alerting schema for the UI. v2 availability is
// enforced by requireV2Middleware on the alert route group.
func (c *Handler) GetAlertSchema(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, alerting.GetSchema())
}

// ListAlertRules returns all alert rules, optionally filtered.
func (c *Handler) ListAlertRules(ctx echo.Context) error {
	filter := repository.AlertRuleFilter{
		ObjectType: ctx.QueryParam("object_type"),
	}
	if enabledParam := ctx.QueryParam("enabled"); enabledParam != "" {
		v := enabledParam == queryValueTrue
		filter.Enabled = &v
	}
	if builtInParam := ctx.QueryParam("built_in"); builtInParam != "" {
		v := builtInParam == queryValueTrue
		filter.BuiltIn = &v
	}

	rules, err := c.alertRuleRepo.ListRules(ctx.Request().Context(), filter)
	if err != nil {
		c.LogErrorIfEnabled("failed to list alert rules", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to list alert rules", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"rules": rules,
		"count": len(rules),
	})
}

// GetAlertRule returns a single alert rule by ID.
func (c *Handler) GetAlertRule(ctx echo.Context) error {
	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid rule ID", http.StatusBadRequest, notification.MsgErrAlertInvalidID, nil)
	}

	rule, err := c.alertRuleRepo.GetRule(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return c.HandleErrorWithKey(ctx, err, "Alert rule not found", http.StatusNotFound, notification.MsgErrAlertNotFound, nil)
		}
		c.LogErrorIfEnabled("failed to get alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get alert rule", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, rule)
}

// CreateAlertRule creates a new alert rule.
func (c *Handler) CreateAlertRule(ctx echo.Context) error {
	// A created rule must become active via the engine; refuse with 503 if the
	// engine failed to initialize rather than persisting an inert rule.
	if !c.ensureEngineReady(ctx) {
		return nil
	}

	rule, err := c.bindAndValidateAlertRule(ctx)
	if rule == nil {
		return err
	}

	// Prevent duplicate names
	count, err := c.alertRuleRepo.CountRulesByName(ctx.Request().Context(), rule.Name)
	if err != nil {
		c.LogErrorIfEnabled("failed to check rule name uniqueness", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to create alert rule", http.StatusInternalServerError)
	}
	if count > 0 {
		return c.HandleErrorWithKey(ctx, nil, "A rule with this name already exists", http.StatusConflict, notification.MsgErrAlertDuplicateName, nil)
	}

	if err := c.alertRuleRepo.CreateRule(ctx.Request().Context(), rule); err != nil {
		c.LogErrorIfEnabled("failed to create alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to create alert rule", http.StatusInternalServerError)
	}

	// Refresh engine cache if available
	c.refreshAlertEngine(ctx)

	c.LogInfoIfEnabled("alert rule created",
		logger.String("name", rule.Name),
		logger.Uint64("id", uint64(rule.ID)))

	return ctx.JSON(http.StatusCreated, rule)
}

// UpdateAlertRule replaces an existing alert rule.
func (c *Handler) UpdateAlertRule(ctx echo.Context) error {
	// The updated rule must be refreshed into the engine; refuse with 503 if the
	// engine failed to initialize rather than silently persisting an inert change.
	if !c.ensureEngineReady(ctx) {
		return nil
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid rule ID", http.StatusBadRequest, notification.MsgErrAlertInvalidID, nil)
	}

	// Verify rule exists
	existing, err := c.alertRuleRepo.GetRule(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return c.HandleErrorWithKey(ctx, err, "Alert rule not found", http.StatusNotFound, notification.MsgErrAlertNotFound, nil)
		}
		return c.HandleError(ctx, err, "Failed to get alert rule", http.StatusInternalServerError)
	}

	rule, err := c.bindAndValidateAlertRule(ctx)
	if rule == nil {
		return err
	}

	rule.ID = existing.ID
	rule.CreatedAt = existing.CreatedAt

	if err := c.alertRuleRepo.UpdateRule(ctx.Request().Context(), rule); err != nil {
		c.LogErrorIfEnabled("failed to update alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to update alert rule", http.StatusInternalServerError)
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, rule)
}

// ToggleAlertRule enables or disables an alert rule.
func (c *Handler) ToggleAlertRule(ctx echo.Context) error {
	// Toggling enabled state must be refreshed into the engine; refuse with 503 if
	// the engine failed to initialize rather than silently no-opping the refresh.
	if !c.ensureEngineReady(ctx) {
		return nil
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid rule ID", http.StatusBadRequest, notification.MsgErrAlertInvalidID, nil)
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := ctx.Bind(&body); err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid request body", http.StatusBadRequest, notification.MsgErrAlertInvalidBody, nil)
	}

	if err := c.alertRuleRepo.ToggleRule(ctx.Request().Context(), id, body.Enabled); err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return c.HandleErrorWithKey(ctx, err, "Alert rule not found", http.StatusNotFound, notification.MsgErrAlertNotFound, nil)
		}
		c.LogErrorIfEnabled("failed to toggle alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to toggle alert rule", http.StatusInternalServerError)
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, map[string]any{"id": id, "enabled": body.Enabled})
}

// DeleteAlertRule deletes an alert rule.
func (c *Handler) DeleteAlertRule(ctx echo.Context) error {
	// The deletion must be refreshed out of the engine cache; refuse with 503 if
	// the engine failed to initialize rather than leaving a deleted rule live.
	if !c.ensureEngineReady(ctx) {
		return nil
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid rule ID", http.StatusBadRequest, notification.MsgErrAlertInvalidID, nil)
	}

	if err := c.alertRuleRepo.DeleteRule(ctx.Request().Context(), id); err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return c.HandleErrorWithKey(ctx, err, "Alert rule not found", http.StatusNotFound, notification.MsgErrAlertNotFound, nil)
		}
		c.LogErrorIfEnabled("failed to delete alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to delete alert rule", http.StatusInternalServerError)
	}

	c.refreshAlertEngine(ctx)

	return ctx.NoContent(http.StatusNoContent)
}

// TestAlertRule simulates firing a rule for testing purposes.
func (c *Handler) TestAlertRule(ctx echo.Context) error {
	// Test-firing dereferences the engine directly, so return 503 up front whenever
	// it is absent for ANY reason: init was attempted and failed (alertEngineErr
	// set), or init never ran (engine nil with err nil, e.g. a direct call that
	// bypasses the requireV2 middleware). Gating on the engine pointer rather than
	// only alertEngineErr prevents both a nil-deref of TestFireRule below and a false
	// "test fired" success. Checked before parsing/fetching the rule so unavailability
	// is reported regardless of input; engineUnavailable carries the recorded init
	// error when set and a generic message otherwise.
	if !c.ensureEngineReady(ctx) {
		return nil
	}
	// ensureEngineReady guarantees the engine is non-nil when it returns true;
	// load it once and use the local variable throughout this handler.
	engine := c.alertEngine.Load()
	if engine == nil {
		return c.engineUnavailable(ctx)
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid rule ID", http.StatusBadRequest, notification.MsgErrAlertInvalidID, nil)
	}

	rule, err := c.alertRuleRepo.GetRule(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return c.HandleErrorWithKey(ctx, err, "Alert rule not found", http.StatusNotFound, notification.MsgErrAlertNotFound, nil)
		}
		return c.HandleError(ctx, err, "Failed to get alert rule", http.StatusInternalServerError)
	}

	// Fire the rule's actions directly, bypassing condition evaluation.
	engine.TestFireRule(rule)

	return ctx.JSON(http.StatusOK, map[string]string{"status": "test fired"})
}

// ResetDefaultAlertRules deletes all built-in rules and re-seeds them.
func (c *Handler) ResetDefaultAlertRules(ctx echo.Context) error {
	// Re-seeded defaults must be refreshed into the engine; refuse with 503 if the
	// engine failed to initialize rather than leaving the engine cache stale.
	if !c.ensureEngineReady(ctx) {
		return nil
	}

	reqCtx := ctx.Request().Context()

	_, err := c.alertRuleRepo.DeleteBuiltInRules(reqCtx)
	if err != nil {
		c.LogErrorIfEnabled("failed to delete built-in rules", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to reset default rules", http.StatusInternalServerError)
	}

	// Re-seed defaults
	defaults := alerting.DefaultRules()
	for i := range defaults {
		if err := c.alertRuleRepo.CreateRule(reqCtx, &defaults[i]); err != nil {
			c.LogErrorIfEnabled("failed to seed default rule",
				logger.String("name", defaults[i].Name), logger.Error(err))
		}
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, map[string]string{"status": "defaults reset"})
}

// ListAlertHistory returns paginated alert firing history.
func (c *Handler) ListAlertHistory(ctx echo.Context) error {
	filter := repository.AlertHistoryFilter{}

	if ruleIDParam := ctx.QueryParam("rule_id"); ruleIDParam != "" {
		v, err := strconv.ParseUint(ruleIDParam, 10, 64)
		if err != nil {
			return c.HandleErrorWithKey(ctx, err, "Invalid rule_id", http.StatusBadRequest, notification.MsgErrAlertInvalidID, nil)
		}
		filter.RuleID = uint(v)
	}
	if limitParam := ctx.QueryParam("limit"); limitParam != "" {
		v, err := strconv.Atoi(limitParam)
		if err == nil && v > 0 {
			if v > maxHistoryLimit {
				v = maxHistoryLimit
			}
			filter.Limit = v
		}
	} else {
		filter.Limit = 50
	}
	if offsetParam := ctx.QueryParam("offset"); offsetParam != "" {
		v, err := strconv.Atoi(offsetParam)
		if err == nil && v >= 0 {
			filter.Offset = v
		}
	}

	items, total, err := c.alertRuleRepo.ListHistory(ctx.Request().Context(), filter)
	if err != nil {
		c.LogErrorIfEnabled("failed to list alert history", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to list alert history", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"history": items,
		"total":   total,
		"limit":   filter.Limit,
		"offset":  filter.Offset,
	})
}

// ClearAlertHistory deletes all alert history records.
func (c *Handler) ClearAlertHistory(ctx echo.Context) error {
	deleted, err := c.alertRuleRepo.DeleteHistory(ctx.Request().Context())
	if err != nil {
		c.LogErrorIfEnabled("failed to clear alert history", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to clear alert history", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{"deleted": deleted})
}

// ExportAlertRules exports all rules as JSON.
func (c *Handler) ExportAlertRules(ctx echo.Context) error {
	rules, err := c.alertRuleRepo.ListRules(ctx.Request().Context(), repository.AlertRuleFilter{})
	if err != nil {
		c.LogErrorIfEnabled("failed to export alert rules", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to export alert rules", http.StatusInternalServerError)
	}

	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=alert-rules.json")
	return ctx.JSON(http.StatusOK, map[string]any{
		"rules":   rules,
		"version": 1,
	})
}

// ImportAlertRules imports rules from JSON.
func (c *Handler) ImportAlertRules(ctx echo.Context) error {
	// Imported rules must be refreshed into the engine; refuse with 503 if the
	// engine failed to initialize rather than importing inert rules.
	if !c.ensureEngineReady(ctx) {
		return nil
	}

	var payload struct {
		Rules   []entities.AlertRule `json:"rules"`
		Version int                  `json:"version"`
	}
	if err := json.NewDecoder(ctx.Request().Body).Decode(&payload); err != nil {
		return c.HandleErrorWithKey(ctx, err, "Invalid JSON", http.StatusBadRequest, notification.MsgErrAlertInvalidJSON, nil)
	}

	reqCtx := ctx.Request().Context()
	var imported int
	for i := range payload.Rules {
		rule := &payload.Rules[i]
		// Reset IDs for import
		rule.ID = 0
		for j := range rule.Conditions {
			rule.Conditions[j].ID = 0
			rule.Conditions[j].RuleID = 0
		}
		for j := range rule.Actions {
			rule.Actions[j].ID = 0
			rule.Actions[j].RuleID = 0
		}

		if err := validateEscalationSteps(rule.EscalationSteps); err != nil {
			c.LogErrorIfEnabled("skipping imported rule with invalid escalation steps",
				logger.String("name", rule.Name), logger.Error(err))
			continue
		}

		if err := c.alertRuleRepo.CreateRule(reqCtx, rule); err != nil {
			c.LogErrorIfEnabled("failed to import rule",
				logger.String("name", rule.Name), logger.Error(err))
			continue
		}
		imported++
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, map[string]any{
		"imported": imported,
		"total":    len(payload.Rules),
	})
}

// refreshAlertEngine refreshes the engine's rule cache if the engine is set.
func (c *Handler) refreshAlertEngine(ctx echo.Context) {
	if engine := c.alertEngine.Load(); engine != nil {
		if err := engine.RefreshRules(ctx.Request().Context()); err != nil {
			c.LogErrorIfEnabled("failed to refresh alert engine rules", logger.Error(err))
		}
	}
}

// parseUintParam parses a uint route parameter.
func parseUintParam(ctx echo.Context, name string) (uint, error) {
	v, err := strconv.ParseUint(ctx.Param(name), 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(v), nil
}

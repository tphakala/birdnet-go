package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/alerting"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const maxHistoryLimit = 200

// initAlertRoutes registers alert rule API endpoints.
func (c *Controller) initAlertRoutes() {
	if c.V2Manager == nil {
		return
	}

	// Initialize repository lazily from V2Manager
	c.alertRuleRepo = repository.NewAlertRuleRepository(c.V2Manager.DB())

	alerts := c.Group.Group("/alerts")

	// Public read endpoints
	alerts.GET("/schema", c.GetAlertSchema)
	alerts.GET("/rules", c.ListAlertRules)
	alerts.GET("/rules/:id", c.GetAlertRule)
	alerts.GET("/history", c.ListAlertHistory)

	// Protected endpoints
	protected := alerts.Group("", c.authMiddleware)
	protected.GET("/rules/export", c.ExportAlertRules)
	protected.POST("/rules", c.CreateAlertRule)
	protected.PUT("/rules/:id", c.UpdateAlertRule)
	protected.PATCH("/rules/:id/toggle", c.ToggleAlertRule)
	protected.DELETE("/rules/:id", c.DeleteAlertRule)
	protected.POST("/rules/:id/test", c.TestAlertRule)
	protected.POST("/rules/reset-defaults", c.ResetDefaultAlertRules)
	protected.POST("/rules/import", c.ImportAlertRules)
	protected.DELETE("/history", c.ClearAlertHistory)
}

// requireV2 checks that the enhanced database is available and returns an error response if not.
func (c *Controller) requireV2(ctx echo.Context) error {
	return c.HandleError(ctx, fmt.Errorf("enhanced database not enabled"),
		"Alert rules require the enhanced (v2) database", http.StatusConflict)
}

// GetAlertSchema returns the alerting schema for the UI.
func (c *Controller) GetAlertSchema(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}
	return ctx.JSON(http.StatusOK, alerting.GetSchema())
}

// ListAlertRules returns all alert rules, optionally filtered.
func (c *Controller) ListAlertRules(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	filter := repository.AlertRuleFilter{
		ObjectType: ctx.QueryParam("object_type"),
	}
	if enabledParam := ctx.QueryParam("enabled"); enabledParam != "" {
		v := enabledParam == QueryValueTrue
		filter.Enabled = &v
	}
	if builtInParam := ctx.QueryParam("built_in"); builtInParam != "" {
		v := builtInParam == QueryValueTrue
		filter.BuiltIn = &v
	}

	rules, err := c.alertRuleRepo.ListRules(ctx.Request().Context(), filter)
	if err != nil {
		c.logErrorIfEnabled("failed to list alert rules", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to list alert rules", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"rules": rules,
		"count": len(rules),
	})
}

// GetAlertRule returns a single alert rule by ID.
func (c *Controller) GetAlertRule(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid rule ID"})
	}

	rule, err := c.alertRuleRepo.GetRule(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Alert rule not found"})
		}
		c.logErrorIfEnabled("failed to get alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get alert rule", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, rule)
}

// CreateAlertRule creates a new alert rule.
func (c *Controller) CreateAlertRule(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	var rule entities.AlertRule
	if err := ctx.Bind(&rule); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}

	if rule.Name == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Rule name is required"})
	}
	if rule.ObjectType == "" || rule.TriggerType == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Object type and trigger type are required"})
	}

	// Prevent duplicate names
	count, err := c.alertRuleRepo.CountRulesByName(ctx.Request().Context(), rule.Name)
	if err != nil {
		c.logErrorIfEnabled("failed to check rule name uniqueness", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to create alert rule", http.StatusInternalServerError)
	}
	if count > 0 {
		return ctx.JSON(http.StatusConflict, map[string]string{"error": "A rule with this name already exists"})
	}

	if err := c.alertRuleRepo.CreateRule(ctx.Request().Context(), &rule); err != nil {
		c.logErrorIfEnabled("failed to create alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to create alert rule", http.StatusInternalServerError)
	}

	// Refresh engine cache if available
	c.refreshAlertEngine(ctx)

	c.logInfoIfEnabled("alert rule created",
		logger.String("name", rule.Name),
		logger.Uint64("id", uint64(rule.ID)))

	return ctx.JSON(http.StatusCreated, rule)
}

// UpdateAlertRule replaces an existing alert rule.
func (c *Controller) UpdateAlertRule(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid rule ID"})
	}

	// Verify rule exists
	existing, err := c.alertRuleRepo.GetRule(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Alert rule not found"})
		}
		return c.HandleError(ctx, err, "Failed to get alert rule", http.StatusInternalServerError)
	}

	var rule entities.AlertRule
	if err := ctx.Bind(&rule); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}

	if rule.Name == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Rule name is required"})
	}
	if rule.ObjectType == "" || rule.TriggerType == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Object type and trigger type are required"})
	}

	rule.ID = existing.ID
	rule.CreatedAt = existing.CreatedAt

	if err := c.alertRuleRepo.UpdateRule(ctx.Request().Context(), &rule); err != nil {
		c.logErrorIfEnabled("failed to update alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to update alert rule", http.StatusInternalServerError)
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, rule)
}

// ToggleAlertRule enables or disables an alert rule.
func (c *Controller) ToggleAlertRule(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid rule ID"})
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := ctx.Bind(&body); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}

	if err := c.alertRuleRepo.ToggleRule(ctx.Request().Context(), id, body.Enabled); err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Alert rule not found"})
		}
		c.logErrorIfEnabled("failed to toggle alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to toggle alert rule", http.StatusInternalServerError)
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, map[string]any{"id": id, "enabled": body.Enabled})
}

// DeleteAlertRule deletes an alert rule.
func (c *Controller) DeleteAlertRule(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid rule ID"})
	}

	if err := c.alertRuleRepo.DeleteRule(ctx.Request().Context(), id); err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Alert rule not found"})
		}
		c.logErrorIfEnabled("failed to delete alert rule", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to delete alert rule", http.StatusInternalServerError)
	}

	c.refreshAlertEngine(ctx)

	return ctx.NoContent(http.StatusNoContent)
}

// TestAlertRule simulates firing a rule for testing purposes.
func (c *Controller) TestAlertRule(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	id, err := parseUintParam(ctx, "id")
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid rule ID"})
	}

	rule, err := c.alertRuleRepo.GetRule(ctx.Request().Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrAlertRuleNotFound) {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Alert rule not found"})
		}
		return c.HandleError(ctx, err, "Failed to get alert rule", http.StatusInternalServerError)
	}

	// Fire the rule's actions directly, bypassing condition evaluation
	if c.alertEngine != nil {
		c.alertEngine.TestFireRule(rule)
	}

	return ctx.JSON(http.StatusOK, map[string]string{"status": "test fired"})
}

// ResetDefaultAlertRules deletes all built-in rules and re-seeds them.
func (c *Controller) ResetDefaultAlertRules(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	reqCtx := ctx.Request().Context()

	_, err := c.alertRuleRepo.DeleteBuiltInRules(reqCtx)
	if err != nil {
		c.logErrorIfEnabled("failed to delete built-in rules", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to reset default rules", http.StatusInternalServerError)
	}

	// Re-seed defaults
	defaults := alerting.DefaultRules()
	for i := range defaults {
		if err := c.alertRuleRepo.CreateRule(reqCtx, &defaults[i]); err != nil {
			c.logErrorIfEnabled("failed to seed default rule",
				logger.String("name", defaults[i].Name), logger.Error(err))
		}
	}

	c.refreshAlertEngine(ctx)

	return ctx.JSON(http.StatusOK, map[string]string{"status": "defaults reset"})
}

// ListAlertHistory returns paginated alert firing history.
func (c *Controller) ListAlertHistory(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	filter := repository.AlertHistoryFilter{}

	if ruleIDParam := ctx.QueryParam("rule_id"); ruleIDParam != "" {
		v, err := strconv.ParseUint(ruleIDParam, 10, 64)
		if err != nil {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid rule_id"})
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
		c.logErrorIfEnabled("failed to list alert history", logger.Error(err))
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
func (c *Controller) ClearAlertHistory(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	deleted, err := c.alertRuleRepo.DeleteHistory(ctx.Request().Context())
	if err != nil {
		c.logErrorIfEnabled("failed to clear alert history", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to clear alert history", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]any{"deleted": deleted})
}

// ExportAlertRules exports all rules as JSON.
func (c *Controller) ExportAlertRules(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	rules, err := c.alertRuleRepo.ListRules(ctx.Request().Context(), repository.AlertRuleFilter{})
	if err != nil {
		c.logErrorIfEnabled("failed to export alert rules", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to export alert rules", http.StatusInternalServerError)
	}

	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=alert-rules.json")
	return ctx.JSON(http.StatusOK, map[string]any{
		"rules":   rules,
		"version": 1,
	})
}

// ImportAlertRules imports rules from JSON.
func (c *Controller) ImportAlertRules(ctx echo.Context) error {
	if !datastoreV2.IsEnhancedDatabase() {
		return c.requireV2(ctx)
	}

	var payload struct {
		Rules   []entities.AlertRule `json:"rules"`
		Version int                  `json:"version"`
	}
	if err := json.NewDecoder(ctx.Request().Body).Decode(&payload); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
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

		if err := c.alertRuleRepo.CreateRule(reqCtx, rule); err != nil {
			c.logErrorIfEnabled("failed to import rule",
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
func (c *Controller) refreshAlertEngine(ctx echo.Context) {
	if c.alertEngine != nil {
		if err := c.alertEngine.RefreshRules(ctx.Request().Context()); err != nil {
			c.logErrorIfEnabled("failed to refresh alert engine rules", logger.Error(err))
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

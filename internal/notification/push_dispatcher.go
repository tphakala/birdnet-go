package notification

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// pushDispatcher routes notifications to enabled providers based on filters
// It subscribes to the notification service and forwards notifications asynchronously.
type pushDispatcher struct {
	providers      []registeredProvider
	log            *slog.Logger
	enabled        bool
	maxRetries     int
	retryDelay     time.Duration
	defaultTimeout time.Duration
	cancel         context.CancelFunc
	mu             sync.RWMutex
}

type registeredProvider struct {
	prov   Provider
	filter conf.PushFilterConfig
	name   string
}

var (
	globalPushDispatcher *pushDispatcher
	dispatcherOnce       sync.Once
)

// InitializePushFromConfig builds and starts the push dispatcher using app settings.
func InitializePushFromConfig(settings *conf.Settings) error {
	var initErr error
	dispatcherOnce.Do(func() {
		if settings == nil || !settings.Notification.Push.Enabled {
			return
		}

		pd := &pushDispatcher{
			log:            getFileLogger(settings.Debug),
			enabled:        settings.Notification.Push.Enabled,
			maxRetries:     settings.Notification.Push.MaxRetries,
			retryDelay:     settings.Notification.Push.RetryDelay,
			defaultTimeout: settings.Notification.Push.DefaultTimeout,
		}

		// Build providers
		for _, pc := range settings.Notification.Push.Providers {
			prov := buildProvider(pc)
			if prov == nil {
				continue
			}
			if err := prov.ValidateConfig(); err != nil {
				pd.log.Error("push provider config invalid", "name", pc.Name, "type", pc.Type, "error", err)
				continue
			}
			if prov.IsEnabled() {
				r := registeredProvider{prov: prov, filter: pc.Filter, name: prov.GetName()}
				if pd.log != nil {
					pd.log.Debug("registered push provider", "name", r.name, "types", r.filter.Types, "priorities", r.filter.Priorities, "metadata_filters", r.filter.MetadataFilters, "metadata_count", len(r.filter.MetadataFilters))
				}
				pd.providers = append(pd.providers, r)
			}
		}

		globalPushDispatcher = pd

		// Move start() inside Once to prevent race conditions
		if pd.enabled && len(pd.providers) > 0 {
			if err := pd.start(); err != nil {
				pd.log.Error("failed to start push dispatcher", "error", err)
				initErr = err
			}
		}
	})

	return initErr
}

// GetPushDispatcher returns the dispatcher if initialized
func GetPushDispatcher() *pushDispatcher { return globalPushDispatcher }

func (d *pushDispatcher) start() error {
	if !d.enabled {
		return nil
	}
	if d.cancel != nil {
		return nil // already started
	}
	if len(d.providers) == 0 {
		d.log.Info("push notifications enabled but no providers configured")
		return nil
	}

	service := GetService()
	if service == nil {
		return fmt.Errorf("notification service not initialized")
	}

	ch, ctx := service.Subscribe()
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	go func() {
		defer service.Unsubscribe(ch)
		for {
			select {
			case notif, ok := <-ch:
				if !ok || notif == nil {
					return
				}
				// Skip ephemeral toast notifications
				if isToastNotification(notif) {
					continue
				}
				// Dispatch in background
				go d.dispatch(ctx, notif)
			case <-ctx.Done():
				return
			}
		}
	}()

	d.log.Info("push dispatcher started", "providers", len(d.providers))
	return nil
}

func (d *pushDispatcher) dispatch(ctx context.Context, notif *Notification) {
	for _, rp := range d.providers {
		rp := rp // capture
		if !rp.prov.IsEnabled() || !rp.prov.SupportsType(notif.Type) {
			continue
		}
		// Apply filter
		if !MatchesProviderFilter(&rp.filter, notif, d.log, rp.name) {
			continue
		}

		// Run each provider in its own goroutine to avoid head-of-line blocking
		go func() {
			attempts := 0
			for {
				attempts++
				// Set timeout per attempt
				attemptCtx := ctx
				var cancel context.CancelFunc
				if deadline := d.defaultTimeout; deadline > 0 {
					attemptCtx, cancel = context.WithTimeout(ctx, deadline)
				}

				start := time.Now()
				err := rp.prov.Send(attemptCtx, notif)
				if cancel != nil {
					cancel() // release timer resources immediately per attempt
				}
				if err == nil {
					d.log.Info("push sent", "provider", rp.prov.GetName(), "id", notif.ID, "type", string(notif.Type), "priority", string(notif.Priority), "attempt", attempts, "elapsed", time.Since(start))
					return
				}

				// Classify error for retry based on sentinel prefix
				// Providers can return any error; treat as retryable unless explicitly marked otherwise
				var perr *providerError
				retryable := false
				if errors.As(err, &perr) {
					retryable = perr.Retryable
				} else {
					retryable = true
				}
				if !retryable || attempts > d.maxRetries {
					d.log.Error("push send failed", "provider", rp.prov.GetName(), "attempts", attempts, "error", err)
					return
				}

				// Wait for retry delay with context cancellation check
				select {
				case <-ctx.Done():
					d.log.Debug("retry cancelled due to context cancellation", "provider", rp.prov.GetName(), "attempts", attempts)
					return
				case <-time.After(d.retryDelay):
					// Continue to next retry
				}
			}
		}()
	}
}

// ----------------- Provider construction -----------------

func buildProvider(pc conf.PushProviderConfig) Provider {
	ptype := strings.ToLower(pc.Type)
	types := effectiveTypes(pc.Filter.Types)
	switch ptype {
	case "script":
		return NewScriptProvider(orDefault(pc.Name, "script"), pc.Enabled, pc.Command, pc.Args, pc.Environment, pc.InputFormat, types)
	case "shoutrrr":
		return NewShoutrrrProvider(orDefault(pc.Name, "shoutrrr"), pc.Enabled, pc.URLs, types, pc.Timeout)
	default:
		return nil
	}
}

func effectiveTypes(cfg []string) []string {
	if len(cfg) == 0 {
		return []string{"error", "warning", "info", "detection", "system"}
	}
	return append([]string{}, cfg...)
}

// ----------------- helpers -----------------

func orDefault[T ~string](v T, d T) T {
	if strings.TrimSpace(string(v)) == "" {
		return d
	}
	return v
}

// MatchesProviderFilter applies basic filtering based on type/priority/component and simple metadata rules.
// This function is exported for testing purposes.
func MatchesProviderFilter(f *conf.PushFilterConfig, n *Notification, log *slog.Logger, providerName string) bool {
	if f == nil {
		if log != nil {
			log.Debug("no filter configured, allowing notification", "provider", providerName, "notification_id", n.ID)
		}
		return true
	}

	// Types
	if len(f.Types) > 0 {
		if log != nil {
			log.Debug("checking type filter", "provider", providerName, "allowed_types", f.Types, "notification_type", string(n.Type), "notification_id", n.ID)
		}
		if !slices.Contains(f.Types, string(n.Type)) {
			if log != nil {
				log.Debug("filter failed: type mismatch", "provider", providerName, "allowed_types", f.Types, "notification_type", string(n.Type), "notification_id", n.ID)
			}
			return false
		}
	}
	// Priorities
	if len(f.Priorities) > 0 {
		if !slices.Contains(f.Priorities, string(n.Priority)) {
			if log != nil {
				log.Debug("filter failed: priority mismatch", "provider", providerName, "allowed_priorities", f.Priorities, "notification_priority", string(n.Priority), "notification_id", n.ID)
			}
			return false
		}
	}
	// Component
	if len(f.Components) > 0 {
		if !slices.Contains(f.Components, n.Component) {
			if log != nil {
				log.Debug("filter failed: component mismatch", "provider", providerName, "allowed_components", f.Components, "notification_component", n.Component, "notification_id", n.ID)
			}
			return false
		}
	}
	// Metadata filters: support confidence > >= < <= = == and equality matches for bools/strings
	for key, val := range f.MetadataFilters {
		if log != nil {
			log.Debug("processing metadata filter", "provider", providerName, "key", key, "filter_value", val, "notification_id", n.ID)
		}
		// confidence threshold
		if key == "confidence" {
			cond, ok := val.(string)
			if !ok {
				if log != nil {
					log.Debug("filter failed: confidence filter misconfigured", "provider", providerName, "filter_value", val, "notification_id", n.ID)
				}
				return false // misconfigured filter
			}
			cond = strings.TrimSpace(cond)
			if len(cond) == 0 {
				if log != nil {
					log.Debug("filter failed: empty confidence condition", "provider", providerName, "notification_id", n.ID)
				}
				return false
			}

			// Parse operator and value
			var op string
			var valStr string
			if len(cond) >= 2 && (cond[:2] == ">=" || cond[:2] == "<=" || cond[:2] == "==") {
				op = cond[:2]
				valStr = strings.TrimSpace(cond[2:])
			} else if len(cond) >= 1 && (cond[0] == '>' || cond[0] == '<' || cond[0] == '=') {
				op = string(cond[0])
				valStr = strings.TrimSpace(cond[1:])
			} else {
				if log != nil {
					log.Debug("filter failed: unknown confidence operator", "provider", providerName, "condition", cond, "notification_id", n.ID)
				}
				return false // unknown operator format
			}

			threshold, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				if log != nil {
					log.Debug("filter failed: invalid confidence threshold", "provider", providerName, "threshold_str", valStr, "error", err, "notification_id", n.ID)
				}
				return false
			}
			raw, exists := n.Metadata["confidence"]
			if !exists {
				if log != nil {
					log.Debug("filter failed: confidence metadata missing", "provider", providerName, "available_metadata", n.Metadata, "notification_id", n.ID)
				}
				return false // require presence
			}
			cv, ok := toFloat(raw)
			if !ok {
				if log != nil {
					log.Debug("filter failed: confidence value not parseable", "provider", providerName, "confidence_value", raw, "notification_id", n.ID)
				}
				return false // require parse success
			}
			switch op {
			case ">":
				if !(cv > threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too low", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("> %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case ">=":
				if !(cv >= threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too low", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf(">= %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case "<":
				if !(cv < threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too high", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("< %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case "<=":
				if !(cv <= threshold) {
					if log != nil {
						log.Debug("filter failed: confidence too high", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("<= %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			case "=", "==":
				if !(cv == threshold) {
					if log != nil {
						log.Debug("filter failed: confidence mismatch", "provider", providerName, "condition", cond, "actual_confidence", cv, "required", fmt.Sprintf("== %v", threshold), "notification_id", n.ID)
					}
					return false
				}
			default:
				if log != nil {
					log.Debug("filter failed: unknown confidence operator", "provider", providerName, "operator", op, "notification_id", n.ID)
				}
				return false // unknown operator
			}
			continue
		}
		// exact match requires key presence
		mv, ok := n.Metadata[key]
		if !ok {
			if log != nil {
				log.Debug("filter failed: metadata key missing", "provider", providerName, "required_key", key, "available_metadata", n.Metadata, "notification_id", n.ID)
			}
			return false
		}
		if fmt.Sprint(mv) != fmt.Sprint(val) {
			if log != nil {
				log.Debug("filter failed: metadata value mismatch", "provider", providerName, "key", key, "expected", val, "actual", mv, "notification_id", n.ID)
			}
			return false
		}
	}
	return true
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float32:
		return float64(t), true
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// providerError allows providers to mark errors as retryable/non-retryable
// (kept for backward compatibility with dispatcher retry logic)
type providerError struct {
	Err       error
	Retryable bool
}

func (e *providerError) Error() string { return e.Err.Error() }
func (e *providerError) Unwrap() error { return e.Err }

package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification/pushproviders"
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
	prov   pushproviders.Provider
	filter conf.PushFilterConfig
	name   string
}

var (
	globalPushDispatcher *pushDispatcher
	dispatcherOnce       sync.Once
)

// InitializePushFromConfig builds and starts the push dispatcher using app settings.
func InitializePushFromConfig(settings *conf.Settings) error {
	dispatcherOnce.Do(func() {
		// Default to disabled if no settings
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
				pd.providers = append(pd.providers, r)
			}
		}

		globalPushDispatcher = pd
		if err := pd.start(); err != nil {
			pd.log.Error("failed to start push dispatcher", "error", err)
		}
	})
	return nil
}

// GetPushDispatcher returns the dispatcher if initialized
func GetPushDispatcher() *pushDispatcher { return globalPushDispatcher }

func (d *pushDispatcher) start() error {
	if !d.enabled {
		return nil
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
		if !rp.prov.IsEnabled() || !rp.prov.SupportsType(string(notif.Type)) {
			continue
		}
		// Apply filter
		if !matchesProviderFilter(&rp.filter, notif) {
			continue
		}

		// Run each provider in its own goroutine to avoid head-of-line blocking
		go func() {
			attempts := 0
			for {
				attempts++
				// Set timeout per attempt
				attemptCtx := ctx
				if deadline := d.defaultTimeout; deadline > 0 {
					var cancel context.CancelFunc
					attemptCtx, cancel = context.WithTimeout(ctx, deadline)
					defer cancel()
				}

				payload := &pushproviders.Payload{
					ID:        notif.ID,
					Type:      string(notif.Type),
					Priority:  string(notif.Priority),
					Title:     notif.Title,
					Message:   notif.Message,
					Component: notif.Component,
					Timestamp: notif.Timestamp,
					Metadata:  notif.Metadata,
				}

				err := rp.prov.Send(attemptCtx, payload)
				if err == nil {
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
				time.Sleep(d.retryDelay)
			}
		}()
	}
}

// ----------------- Provider construction -----------------

func buildProvider(pc conf.PushProviderConfig) pushproviders.Provider {
	ptype := strings.ToLower(pc.Type)
	types := effectiveTypes(pc.Filter.Types)
	switch ptype {
	case "script":
		return pushproviders.NewScriptProvider(orDefault(pc.Name, "script"), pc.Enabled, pc.Command, pc.Args, pc.Environment, pc.InputFormat, types)
	case "shoutrrr":
		return pushproviders.NewShoutrrrProvider(orDefault(pc.Name, "shoutrrr"), pc.Enabled, pc.URLs, types, pc.Timeout)
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

// matchesProviderFilter applies basic filtering based on type/priority/component and simple metadata rules.
func matchesProviderFilter(f *conf.PushFilterConfig, n *Notification) bool {
	if f == nil {
		return true
	}
	// Types
	if len(f.Types) > 0 {
		ok := false
		for _, t := range f.Types {
			if string(n.Type) == t {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// Priorities
	if len(f.Priorities) > 0 {
		ok := false
		for _, p := range f.Priorities {
			if string(n.Priority) == p {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// Component
	if len(f.Components) > 0 {
		ok := false
		for _, c := range f.Components {
			if n.Component == c {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// Minimal metadata filters: support confidence ">x" or "<x" and equality matches for bools/strings
	for key, val := range f.MetadataFilters {
		// confidence threshold
		if key == "confidence" {
			cond, ok := val.(string)
			if !ok {
				continue
			}
			cv, _ := toFloat(n.Metadata["confidence"])
			cond = strings.TrimSpace(cond)
			if strings.HasPrefix(cond, ">") {
				thr, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(cond, ">")), 64)
				if !(cv > thr) {
					return false
				}
			} else if strings.HasPrefix(cond, "<") {
				thr, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(cond, "<")), 64)
				if !(cv < thr) {
					return false
				}
			}
			continue
		}
		// exact match
		if mv, ok := n.Metadata[key]; ok {
			if fmt.Sprint(mv) != fmt.Sprint(val) {
				return false
			}
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

package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	shoutrrr "github.com/containrrr/shoutrrr"
	router "github.com/containrrr/shoutrrr/pkg/router"
	stypes "github.com/containrrr/shoutrrr/pkg/types"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// PushProvider defines an external push delivery backend
// Providers should be safe for concurrent use.
type PushProvider interface {
	GetName() string
	ValidateConfig() error
	Send(ctx context.Context, notification *Notification) error
	SupportsType(notifType Type) bool
	IsEnabled() bool
}

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
	prov   PushProvider
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
			prov := buildProvider(pc, pd)
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
		if !rp.prov.IsEnabled() || !rp.prov.SupportsType(notif.Type) {
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

				err := rp.prov.Send(attemptCtx, notif)
				if err == nil {
					return
				}

				// Classify error for retry based on sentinel prefix
				var perr *providerError
				retryable := false
				if errors.As(err, &perr) {
					retryable = perr.Retryable
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

// providerError allows providers to mark errors as retryable/non-retryable
type providerError struct {
	Err       error
	Retryable bool
}

func (e *providerError) Error() string { return e.Err.Error() }
func (e *providerError) Unwrap() error { return e.Err }

// ----------------- Providers -----------------

// shoutrrrProvider sends via containrrr/shoutrrr
// Creates a single sender for multiple URLs.
type shoutrrrProvider struct {
	name    string
	enabled bool
	urls    []string
	types   map[Type]bool
	sender  *router.ServiceRouter
	timeout time.Duration
}

func newShoutrrrProvider(pc conf.PushProviderConfig) PushProvider {
	sp := &shoutrrrProvider{
		name:    orDefault(pc.Name, "shoutrrr"),
		enabled: pc.Enabled,
		urls:    append([]string{}, pc.URLs...),
		types:   map[Type]bool{},
		timeout: pc.Timeout,
	}
	if len(pc.Filter.Types) == 0 {
		sp.types[TypeError] = true
		sp.types[TypeWarning] = true
		sp.types[TypeInfo] = true
		sp.types[TypeDetection] = true
		sp.types[TypeSystem] = true
	} else {
		for _, t := range pc.Filter.Types {
			sp.types[Type(t)] = true
		}
	}
	return sp
}

func (s *shoutrrrProvider) GetName() string          { return s.name }
func (s *shoutrrrProvider) IsEnabled() bool          { return s.enabled }
func (s *shoutrrrProvider) SupportsType(t Type) bool { return s.types[t] }

func (s *shoutrrrProvider) ValidateConfig() error {
	if !s.enabled {
		return nil
	}
	if len(s.urls) == 0 {
		return fmt.Errorf("at least one URL is required")
	}
	// Build sender to validate URLs
	sender, err := shoutrrr.CreateSender(s.urls...)
	if err != nil {
		return err
	}
	s.sender = sender
	// Apply configured timeout and quiet logger
	if s.timeout > 0 {
		s.sender.Timeout = s.timeout
	}
	s.sender.SetLogger(log.New(io.Discard, "", 0))
	return nil
}

func (s *shoutrrrProvider) Send(ctx context.Context, n *Notification) error {
	if s.sender == nil {
		return &providerError{Err: fmt.Errorf("shoutrrr sender not initialized"), Retryable: false}
	}
	_ = ctx // router handles its own timeouts

	body := n.Message
	p := stypes.Params{}
	if n.Title != "" {
		p.SetTitle(n.Title)
	}
	errs := s.sender.Send(body, &p)
	if len(errs) > 0 {
		for _, e := range errs {
			if e != nil {
				return &providerError{Err: e, Retryable: true}
			}
		}
	}
	return nil
}

// scriptProvider executes external scripts/binaries
// It supports passing notification data via env vars and/or JSON to stdin.
type scriptProvider struct {
	name        string
	enabled     bool
	command     string
	args        []string
	env         map[string]string
	inputFormat string // "json", "env", "both"
	types       map[Type]bool
}

func buildProvider(pc conf.PushProviderConfig, d *pushDispatcher) PushProvider {
	switch strings.ToLower(pc.Type) {
	case "script":
		return newScriptProvider(pc)
	case "shoutrrr":
		return newShoutrrrProvider(pc)
	default:
		return nil
	}
}

func newScriptProvider(pc conf.PushProviderConfig) PushProvider {
	sp := &scriptProvider{
		name:        orDefault(pc.Name, "script"),
		enabled:     pc.Enabled,
		command:     pc.Command,
		args:        append([]string{}, pc.Args...),
		env:         map[string]string{},
		inputFormat: strings.ToLower(orDefault(pc.InputFormat, "env")),
		types:       map[Type]bool{},
	}
	for k, v := range pc.Environment {
		sp.env[k] = v
	}
	if len(pc.Filter.Types) == 0 {
		// default: support all types
		sp.types[TypeError] = true
		sp.types[TypeWarning] = true
		sp.types[TypeInfo] = true
		sp.types[TypeDetection] = true
		sp.types[TypeSystem] = true
	} else {
		for _, t := range pc.Filter.Types {
			sp.types[Type(t)] = true
		}
	}
	return sp
}

func (s *scriptProvider) GetName() string          { return s.name }
func (s *scriptProvider) IsEnabled() bool          { return s.enabled }
func (s *scriptProvider) SupportsType(t Type) bool { return s.types[t] }

func (s *scriptProvider) ValidateConfig() error {
	if !s.enabled {
		return nil
	}
	if strings.TrimSpace(s.command) == "" {
		return fmt.Errorf("script command is required")
	}
	return nil
}

func (s *scriptProvider) Send(ctx context.Context, n *Notification) error {
	cmd := exec.CommandContext(ctx, s.command, s.args...)

	// Environment variables
	env := os.Environ()
	env = append(env,
		"NOTIFICATION_ID="+n.ID,
		"NOTIFICATION_TYPE="+string(n.Type),
		"NOTIFICATION_PRIORITY="+string(n.Priority),
		"NOTIFICATION_TITLE="+n.Title,
		"NOTIFICATION_MESSAGE="+n.Message,
		"NOTIFICATION_COMPONENT="+n.Component,
		"NOTIFICATION_TIMESTAMP="+n.Timestamp.UTC().Format(time.RFC3339),
	)
	if len(n.Metadata) > 0 {
		b, _ := json.Marshal(n.Metadata)
		env = append(env, "NOTIFICATION_METADATA_JSON="+string(b))
	}
	// Provider-specific env
	for k, v := range s.env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	// Optional JSON on stdin
	if s.inputFormat == "json" || s.inputFormat == "both" {
		payload := map[string]any{
			"id":        n.ID,
			"type":      n.Type,
			"priority":  n.Priority,
			"title":     n.Title,
			"message":   n.Message,
			"component": n.Component,
			"timestamp": n.Timestamp.UTC().Format(time.RFC3339),
			"metadata":  n.Metadata,
		}
		b, _ := json.Marshal(payload)
		cmd.Stdin = strings.NewReader(string(b))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Determine retryability from exit code
		var exitErr *exec.ExitError
		retry := true
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			// 0 success already handled, 1 retryable, 2 permanent, >=3 treat as permanent
			if code == 2 {
				retry = false
			}
		}
		perr := &providerError{Err: fmt.Errorf("script '%s' failed: %v, output: %s", s.name, err, truncate(string(out), 512)), Retryable: retry}
		return perr
	}
	return nil
}

// -------------- helpers --------------

func orDefault[T ~string](v T, d T) T {
	if strings.TrimSpace(string(v)) == "" {
		return d
	}
	return v
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
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

// parseBool returns boolean value from interface{} supporting strings and numbers
func parseBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(t)
		if err == nil {
			return b, true
		}
	}
	return false, false
}

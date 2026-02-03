package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

type ScriptProvider struct {
	name        string
	enabled     bool
	command     string
	args        []string
	env         map[string]string
	inputFormat string // "json", "env", "both"
	types       map[string]bool
}

func NewScriptProvider(name string, enabled bool, command string, args []string, env map[string]string, inputFormat string, supportedTypes []string) *ScriptProvider {
	sp := &ScriptProvider{
		name:        strings.TrimSpace(name),
		enabled:     enabled,
		command:     command,
		args:        slices.Clone(args),
		env:         map[string]string{},
		inputFormat: strings.ToLower(strings.TrimSpace(inputFormat)),
		types:       map[string]bool{},
	}
	if sp.name == "" {
		sp.name = "script"
	}
	maps.Copy(sp.env, env)
	if sp.inputFormat == "" {
		sp.inputFormat = "env"
	}
	if len(supportedTypes) == 0 {
		sp.types["error"] = true
		sp.types["warning"] = true
		sp.types["info"] = true
		sp.types["detection"] = true
		sp.types["system"] = true
	} else {
		for _, t := range supportedTypes {
			sp.types[t] = true
		}
	}
	return sp
}

func (s *ScriptProvider) GetName() string          { return s.name }
func (s *ScriptProvider) IsEnabled() bool          { return s.enabled }
func (s *ScriptProvider) SupportsType(t Type) bool { return s.types[string(t)] }
func (s *ScriptProvider) ValidateConfig() error {
	if !s.enabled {
		return nil
	}
	if strings.TrimSpace(s.command) == "" {
		return fmt.Errorf("script command is required")
	}
	return nil
}

func (s *ScriptProvider) Send(ctx context.Context, n *Notification) error {
	// G204: Command and args come from validated configuration, not user input.
	// This is intentional design to allow administrators to configure custom notification scripts.
	cmd := exec.CommandContext(ctx, s.command, s.args...) //nolint:gosec // Configuration-sourced command execution is intentional

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
			"type":      string(n.Type),
			"priority":  string(n.Priority),
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
		if errors.As(err, &exitErr) {
			_ = exitErr // caller decides retry policy; we just return the error
		}
		return fmt.Errorf("script '%s' failed: %w, output: %s", s.name, err, truncate(string(out), DefaultScriptOutputTruncateLength))
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

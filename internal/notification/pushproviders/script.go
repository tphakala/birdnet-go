package pushproviders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
		args:        append([]string{}, args...),
		env:         map[string]string{},
		inputFormat: strings.ToLower(strings.TrimSpace(inputFormat)),
		types:       map[string]bool{},
	}
	if sp.name == "" {
		sp.name = "script"
	}
	for k, v := range env {
		sp.env[k] = v
	}
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

func (s *ScriptProvider) GetName() string            { return s.name }
func (s *ScriptProvider) IsEnabled() bool            { return s.enabled }
func (s *ScriptProvider) SupportsType(t string) bool { return s.types[t] }
func (s *ScriptProvider) ValidateConfig() error {
	if !s.enabled {
		return nil
	}
	if strings.TrimSpace(s.command) == "" {
		return fmt.Errorf("script command is required")
	}
	return nil
}

func (s *ScriptProvider) Send(ctx context.Context, p *Payload) error {
	cmd := exec.CommandContext(ctx, s.command, s.args...)

	// Environment variables
	env := os.Environ()
	env = append(env,
		"NOTIFICATION_ID="+p.ID,
		"NOTIFICATION_TYPE="+p.Type,
		"NOTIFICATION_PRIORITY="+p.Priority,
		"NOTIFICATION_TITLE="+p.Title,
		"NOTIFICATION_MESSAGE="+p.Message,
		"NOTIFICATION_COMPONENT="+p.Component,
		"NOTIFICATION_TIMESTAMP="+p.Timestamp.UTC().Format(time.RFC3339),
	)
	if len(p.Metadata) > 0 {
		b, _ := json.Marshal(p.Metadata)
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
			"id":        p.ID,
			"type":      p.Type,
			"priority":  p.Priority,
			"title":     p.Title,
			"message":   p.Message,
			"component": p.Component,
			"timestamp": p.Timestamp.UTC().Format(time.RFC3339),
			"metadata":  p.Metadata,
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
		return fmt.Errorf("script '%s' failed: %v, output: %s", s.name, err, truncate(string(out), 512))
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

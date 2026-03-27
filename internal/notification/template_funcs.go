package notification

import (
	"encoding/json"
	"text/template"
	"time"

	"github.com/tphakala/birdnet-go/internal/templatefuncs"
)

// TemplateFuncs re-exports the shared template function map for use within the
// notification package. All template creation sites should use this.
var TemplateFuncs = templatefuncs.Funcs

// newTemplateWithFuncs creates a new template with the shared functions registered.
func newTemplateWithFuncs(name string) *template.Template {
	return template.New(name).Funcs(TemplateFuncs)
}

// jsonEscapeString escapes a string so it can be safely embedded inside a JSON
// string literal. It marshals the value with encoding/json (which handles
// quotes, backslashes, newlines, control characters, and unicode) then strips
// the surrounding double-quote characters added by Marshal.
func jsonEscapeString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a plain string should never fail, but be safe.
		return s
	}
	// Strip the leading and trailing '"' added by Marshal.
	return string(b[1 : len(b)-1])
}

// jsonEscapeTemplateMap returns a shallow copy of the map with every string
// value (including strings nested one level inside map[string]any) replaced
// by its JSON-escaped equivalent. This ensures that when a text/template
// interpolates {{.Title}} into a JSON payload, special characters such as
// quotes, newlines, and backslashes do not break the JSON syntax.
// NOTE: Only escapes strings up to one level of map nesting. This is
// sufficient for the current ToTemplateMap() shape where Metadata is flat.
func jsonEscapeTemplateMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case string:
			out[k] = jsonEscapeString(val)
		case map[string]any:
			out[k] = jsonEscapeNestedMap(val)
		default:
			// Non-string scalars (int, float64, bool, time.Time, etc.)
			// are safe to interpolate as-is; their fmt representation
			// does not contain JSON-breaking characters.
			out[k] = v
		}
	}
	return out
}

// jsonEscapeNestedMap escapes string values one level deep inside a nested map.
// This handles metadata maps like Metadata["bg_common_name"] = "Vögel \"Test\"".
func jsonEscapeNestedMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = jsonEscapeString(s)
		} else {
			out[k] = v
		}
	}
	return out
}

// ToTemplateMap converts a Notification into a map for template execution.
// This provides both Go-style (PascalCase) and JSON-style (camelCase) field names,
// so users can write {{.Timestamp}} or {{.timestamp}} interchangeably.
// The Timestamp field is pre-formatted as RFC3339 for the lowercase alias,
// while the PascalCase version remains a time.Time for use with formatTime.
func (n *Notification) ToTemplateMap() map[string]any {
	m := map[string]any{
		// PascalCase fields (Go convention, documented)
		"ID":        n.ID,
		"Type":      string(n.Type),
		"Priority":  string(n.Priority),
		"Status":    string(n.Status),
		"Title":     n.Title,
		"Message":   n.Message,
		"Component": n.Component,
		"Timestamp": n.Timestamp,
		"Metadata":  n.Metadata,

		// camelCase/lowercase aliases (matching JSON tags, user-friendly)
		"id":        n.ID,
		"type":      string(n.Type),
		"priority":  string(n.Priority),
		"status":    string(n.Status),
		"title":     n.Title,
		"message":   n.Message,
		"component": n.Component,
		"timestamp": n.Timestamp.Format(time.RFC3339),
		"metadata":  n.Metadata,
	}

	// Flatten metadata into the map so users can access bg_* keys directly,
	// without overwriting core notification fields.
	for k, v := range n.Metadata {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}

	return m
}

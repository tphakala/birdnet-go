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

// jsonEscapeTemplateMap returns a deep copy of the map with every string
// value recursively replaced by its JSON-escaped equivalent. This ensures
// that when a text/template interpolates {{.Title}} into a JSON payload,
// special characters such as quotes, newlines, and backslashes do not
// break the JSON syntax.
func jsonEscapeTemplateMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = jsonEscapeValue(v)
	}
	return out
}

// jsonEscapeValue recursively escapes string values within maps, slices, and
// plain strings. Non-string scalars (int, float64, bool, time.Time, etc.)
// are returned as-is since their fmt representation cannot break JSON syntax.
func jsonEscapeValue(v any) any {
	switch val := v.(type) {
	case string:
		return jsonEscapeString(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, nested := range val {
			out[k] = jsonEscapeValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, nested := range val {
			out[i] = jsonEscapeValue(nested)
		}
		return out
	default:
		return v
	}
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

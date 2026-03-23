package notification

import (
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

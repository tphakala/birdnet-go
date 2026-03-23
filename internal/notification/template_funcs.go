package notification

import (
	"strings"
	"text/template"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TemplateFuncs provides common template functions available to all notification templates.
// This includes string manipulation functions that users expect from template systems
// like Hugo/Helm but are not part of Go's text/template builtins.
var TemplateFuncs = template.FuncMap{
	"title":      cases.Title(language.English).String,
	"upper":      strings.ToUpper,
	"lower":      strings.ToLower,
	"trim":       strings.TrimSpace,
	"contains":   strings.Contains,
	"replace":    strings.ReplaceAll,
	"hasPrefix":  strings.HasPrefix,
	"hasSuffix":  strings.HasSuffix,
	"formatTime": formatTime,
}

// formatTime formats a time value using the given Go layout string.
// Accepts both time.Time and string (attempts to parse RFC3339).
// Example usage in templates: {{formatTime .Timestamp "2006-01-02"}}
func formatTime(t any, layout string) string {
	switch v := t.(type) {
	case time.Time:
		return v.Format(layout)
	case string:
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return v // Return the original string if parsing fails
		}
		return parsed.Format(layout)
	default:
		return ""
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

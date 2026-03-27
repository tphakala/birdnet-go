// Package templatefuncs provides a shared FuncMap for Go text/template rendering.
// Both notification and conf packages import this to avoid circular dependencies
// while keeping the function registry in a single place.
package templatefuncs

import (
	"strings"
	"text/template"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Funcs provides common template functions available to all notification templates.
// This includes string manipulation functions that users expect from template systems
// like Hugo/Helm but are not part of Go's text/template builtins.
//
// Note: jsonEscape is intentionally NOT included here. Webhook templates have
// their data pre-escaped by jsonEscapeTemplateMap before template execution,
// so exposing jsonEscape in templates would cause double-escaping.
var Funcs = template.FuncMap{
	"title":      cases.Title(language.English).String,
	"upper":      strings.ToUpper,
	"lower":      strings.ToLower,
	"trim":       strings.TrimSpace,
	"contains":   strings.Contains,
	"replace":    strings.ReplaceAll,
	"hasPrefix":  strings.HasPrefix,
	"hasSuffix":  strings.HasSuffix,
	"formatTime": FormatTime,
}

// FormatTime formats a time value using the given Go layout string.
// Accepts both time.Time and string (attempts to parse RFC3339).
// Returns an empty string for unsupported types (int, bool, etc.).
// Example usage in templates: {{formatTime .Timestamp "2006-01-02"}}
func FormatTime(t any, layout string) string {
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

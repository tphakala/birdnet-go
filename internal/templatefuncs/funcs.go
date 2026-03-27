// Package templatefuncs provides a shared FuncMap for Go text/template rendering.
// Both notification and conf packages import this to avoid circular dependencies
// while keeping the function registry in a single place.
package templatefuncs

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Funcs provides common template functions available to all notification templates.
// This includes string manipulation functions that users expect from template systems
// like Hugo/Helm but are not part of Go's text/template builtins.
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
	"jsonEscape": JSONEscape,
}

// JSONEscape escapes a value so it is safe to embed inside a JSON string literal.
// Strings are marshalled via encoding/json (handling quotes, backslashes,
// newlines, control characters, and unicode) with the surrounding quotes
// stripped. Non-string types are converted to their default string form first.
// Example usage in templates: {{jsonEscape .Title}}
func JSONEscape(v any) string {
	var s string
	switch val := v.(type) {
	case string:
		s = val
	default:
		s = fmt.Sprintf("%v", val)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// Strip the leading and trailing '"' added by Marshal.
	return string(b[1 : len(b)-1])
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

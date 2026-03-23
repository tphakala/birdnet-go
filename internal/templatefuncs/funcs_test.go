package templatefuncs

import (
	"bytes"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFuncs_RegisteredFunctions(t *testing.T) {
	t.Parallel()

	expectedFuncs := []string{
		"title", "upper", "lower", "trim",
		"contains", "replace", "hasPrefix", "hasSuffix",
		"formatTime",
	}

	for _, name := range expectedFuncs {
		assert.Contains(t, Funcs, name, "Funcs should contain %q", name)
	}
}

func TestFuncs_TemplateExecution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tmplStr  string
		data     map[string]any
		expected string
	}{
		{
			name:     "title function",
			tmplStr:  `{{title .text}}`,
			data:     map[string]any{"text": "hello world"},
			expected: "Hello World",
		},
		{
			name:     "upper function",
			tmplStr:  `{{upper .text}}`,
			data:     map[string]any{"text": "hello"},
			expected: "HELLO",
		},
		{
			name:     "lower function",
			tmplStr:  `{{lower .text}}`,
			data:     map[string]any{"text": "HELLO"},
			expected: "hello",
		},
		{
			name:     "trim function",
			tmplStr:  `{{trim .text}}`,
			data:     map[string]any{"text": "  hello  "},
			expected: "hello",
		},
		{
			name:     "contains function",
			tmplStr:  `{{if contains .text "world"}}yes{{else}}no{{end}}`,
			data:     map[string]any{"text": "hello world"},
			expected: "yes",
		},
		{
			name:     "replace function",
			tmplStr:  `{{replace .text "world" "Go"}}`,
			data:     map[string]any{"text": "hello world"},
			expected: "hello Go",
		},
		{
			name:     "hasPrefix function",
			tmplStr:  `{{if hasPrefix .text "hello"}}yes{{else}}no{{end}}`,
			data:     map[string]any{"text": "hello world"},
			expected: "yes",
		},
		{
			name:     "hasSuffix function",
			tmplStr:  `{{if hasSuffix .text "world"}}yes{{else}}no{{end}}`,
			data:     map[string]any{"text": "hello world"},
			expected: "yes",
		},
		{
			name:     "formatTime with time.Time",
			tmplStr:  `{{formatTime .ts "2006-01-02"}}`,
			data:     map[string]any{"ts": time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)},
			expected: "2026-03-23",
		},
		{
			name:     "formatTime with RFC3339 string",
			tmplStr:  `{{formatTime .ts "Jan 2, 2006"}}`,
			data:     map[string]any{"ts": "2026-03-23T10:30:00Z"},
			expected: "Mar 23, 2026",
		},
		{
			name:     "formatTime with unparseable string returns original",
			tmplStr:  `{{formatTime .ts "2006-01-02"}}`,
			data:     map[string]any{"ts": "not-a-date"},
			expected: "not-a-date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := template.New("test").Funcs(Funcs).Parse(tt.tmplStr)
			require.NoError(t, err)

			var buf bytes.Buffer
			err = tmpl.Execute(&buf, tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

package notification

import (
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateFuncs_RegisteredFunctions(t *testing.T) {
	t.Parallel()

	// Verify all expected functions are registered
	expectedFuncs := []string{
		"title", "upper", "lower", "trim",
		"contains", "replace", "hasPrefix", "hasSuffix",
		"formatTime",
	}

	for _, name := range expectedFuncs {
		assert.Contains(t, TemplateFuncs, name, "TemplateFuncs should contain %q", name)
	}
}

func TestTemplateFuncs_TemplateExecution(t *testing.T) {
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
			name:     "formatTime function",
			tmplStr:  `{{formatTime .ts "2006-01-02"}}`,
			data:     map[string]any{"ts": time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)},
			expected: "2026-03-23",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := template.New("test").Funcs(TemplateFuncs).Parse(tt.tmplStr)
			require.NoError(t, err)

			var buf []byte
			w := &writerHelper{buf: &buf}
			err = tmpl.Execute(w, tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, string(buf))
		})
	}
}

// writerHelper collects template output into a byte slice.
type writerHelper struct {
	buf *[]byte
}

func (w *writerHelper) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

func TestNotification_ToTemplateMap(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 23, 10, 30, 0, 0, time.UTC)
	n := &Notification{
		ID:        "test-123",
		Type:      TypeDetection,
		Priority:  PriorityMedium,
		Status:    StatusUnread,
		Title:     "Test Bird",
		Message:   "Detected a test bird",
		Component: "analysis",
		Timestamp: ts,
		Metadata: map[string]any{
			"bg_confidence_percent": "95",
			"bg_detection_time":     "10:30:00",
			"species":               "Test Species",
		},
	}

	m := n.ToTemplateMap()

	// PascalCase fields
	assert.Equal(t, "test-123", m["ID"])
	assert.Equal(t, "Test Bird", m["Title"])
	assert.Equal(t, "Detected a test bird", m["Message"])
	assert.Equal(t, "analysis", m["Component"])
	assert.Equal(t, ts, m["Timestamp"])
	assert.Equal(t, string(TypeDetection), m["Type"])
	assert.Equal(t, string(PriorityMedium), m["Priority"])
	assert.Equal(t, string(StatusUnread), m["Status"])

	// camelCase aliases
	assert.Equal(t, "test-123", m["id"])
	assert.Equal(t, "Detected a test bird", m["message"])
	assert.Equal(t, "analysis", m["component"])
	assert.Equal(t, ts.Format(time.RFC3339), m["timestamp"])
	assert.Equal(t, string(TypeDetection), m["type"])
	assert.Equal(t, string(PriorityMedium), m["priority"])
	assert.Equal(t, string(StatusUnread), m["status"])

	// Flattened metadata
	assert.Equal(t, "95", m["bg_confidence_percent"])
	assert.Equal(t, "10:30:00", m["bg_detection_time"])
	assert.Equal(t, "Test Species", m["species"])
}

func TestNotification_ToTemplateMap_UsableInTemplate(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 23, 10, 30, 0, 0, time.UTC)
	n := &Notification{
		ID:        "test-123",
		Type:      TypeDetection,
		Priority:  PriorityMedium,
		Status:    StatusUnread,
		Title:     "american robin",
		Message:   "Detected",
		Component: "analysis",
		Timestamp: ts,
		Metadata: map[string]any{
			"bg_confidence_percent": "95",
		},
	}

	tests := []struct {
		name     string
		tmplStr  string
		expected string
	}{
		{
			name:     "lowercase timestamp alias",
			tmplStr:  `{{.timestamp}}`,
			expected: "2026-03-23T10:30:00Z",
		},
		{
			name:     "PascalCase with formatTime",
			tmplStr:  `{{formatTime .Timestamp "Jan 2, 2006"}}`,
			expected: "Mar 23, 2026",
		},
		{
			name:     "title function on field",
			tmplStr:  `{{title .Title}}`,
			expected: "American Robin",
		},
		{
			name:     "flattened metadata access",
			tmplStr:  `{{.bg_confidence_percent}}%`,
			expected: "95%",
		},
		{
			name:     "JSON webhook payload",
			tmplStr:  `{"title":"{{.Title}}","time":"{{.timestamp}}"}`,
			expected: `{"title":"american robin","time":"2026-03-23T10:30:00Z"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := template.New("test").Funcs(TemplateFuncs).Parse(tt.tmplStr)
			require.NoError(t, err)

			var buf []byte
			w := &writerHelper{buf: &buf}
			err = tmpl.Execute(w, n.ToTemplateMap())
			require.NoError(t, err)
			assert.Equal(t, tt.expected, string(buf))
		})
	}
}

func TestNotification_ToTemplateMap_NilMetadata(t *testing.T) {
	t.Parallel()

	n := &Notification{
		ID:        "test",
		Type:      TypeInfo,
		Priority:  PriorityLow,
		Status:    StatusUnread,
		Title:     "Test",
		Message:   "Test",
		Timestamp: time.Now(),
	}

	m := n.ToTemplateMap()
	assert.NotNil(t, m)
	assert.Equal(t, "test", m["ID"])
	assert.Equal(t, "test", m["id"])
}

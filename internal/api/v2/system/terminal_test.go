package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Compile-time interface check.
var _ ptyHandle = (*mockPTY)(nil)

// mockPTY implements ptyHandle for testing.
type mockPTY struct {
	resizeCols uint16
	resizeRows uint16
	resizeErr  error
	resized    bool
}

func (m *mockPTY) Read([]byte) (int, error) { return 0, nil }

func (m *mockPTY) Write(p []byte) (int, error) { return len(p), nil }

func (m *mockPTY) Close() error { return nil }

func (m *mockPTY) Resize(cols, rows uint16) error {
	m.resized = true
	m.resizeCols = cols
	m.resizeRows = rows
	return m.resizeErr
}

func TestHandleResizeMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		msg        string
		wantCols   uint16
		wantRows   uint16
		wantResize bool
		handled    bool
	}{
		{
			name:       "valid resize",
			msg:        `{"type":"resize","cols":120,"rows":40}`,
			wantCols:   120,
			wantRows:   40,
			wantResize: true,
			handled:    true,
		},
		{
			name:    "not a resize message",
			msg:     `{"type":"input","data":"hello"}`,
			handled: false,
		},
		{
			name:    "invalid JSON",
			msg:     `not json`,
			handled: false,
		},
		{
			name:    "zero cols ignored",
			msg:     `{"type":"resize","cols":0,"rows":40}`,
			handled: true,
		},
		{
			name:    "zero rows ignored",
			msg:     `{"type":"resize","cols":120,"rows":0}`,
			handled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockPTY{}
			result := handleResizeMessage(mock, []byte(tt.msg))
			assert.Equal(t, tt.handled, result)
			assert.Equal(t, tt.wantResize, mock.resized, "Resize call")
			if tt.wantResize {
				assert.Equal(t, tt.wantCols, mock.resizeCols)
				assert.Equal(t, tt.wantRows, mock.resizeRows)
			}
		})
	}
}

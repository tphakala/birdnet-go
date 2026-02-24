package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface check.
var _ ptyHandle = (*mockPTY)(nil)

// mockPTY implements ptyHandle for testing.
type mockPTY struct {
	resizeCols uint16
	resizeRows uint16
	resizeErr  error
	written    []byte
	readData   []byte
	closed     bool
}

func (m *mockPTY) Read(p []byte) (int, error) {
	n := copy(p, m.readData)
	return n, nil
}

func (m *mockPTY) Write(p []byte) (int, error) {
	m.written = append(m.written, p...)
	return len(p), nil
}

func (m *mockPTY) Close() error {
	m.closed = true
	return nil
}

func (m *mockPTY) Resize(cols, rows uint16) error {
	m.resizeCols = cols
	m.resizeRows = rows
	return m.resizeErr
}

func TestHandleResizeMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		msg      string
		wantCols uint16
		wantRows uint16
		handled  bool
	}{
		{
			name:     "valid resize",
			msg:      `{"type":"resize","cols":120,"rows":40}`,
			wantCols: 120,
			wantRows: 40,
			handled:  true,
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
			name:    "zero dimensions ignored",
			msg:     `{"type":"resize","cols":0,"rows":0}`,
			handled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockPTY{}
			result := handleResizeMessage(mock, []byte(tt.msg))
			assert.Equal(t, tt.handled, result)
			if tt.wantCols > 0 {
				assert.Equal(t, tt.wantCols, mock.resizeCols)
				assert.Equal(t, tt.wantRows, mock.resizeRows)
			}
		})
	}
}

func TestPtyHandleInterface(t *testing.T) {
	t.Parallel()

	mock := &mockPTY{readData: []byte("hello")}

	n, err := mock.Write([]byte("test"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, []byte("test"), mock.written)

	buf := make([]byte, 10)
	n, err = mock.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", string(buf[:n]))

	err = mock.Resize(80, 24)
	require.NoError(t, err)
	assert.Equal(t, uint16(80), mock.resizeCols)
	assert.Equal(t, uint16(24), mock.resizeRows)

	err = mock.Close()
	require.NoError(t, err)
	assert.True(t, mock.closed)
}

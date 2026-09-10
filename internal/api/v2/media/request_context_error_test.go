package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

func TestHandleRequestContextError(t *testing.T) {
	tests := []struct {
		name        string
		requestCtx  func(*testing.T) context.Context
		wantHandled bool
		wantStatus  int
	}{
		{
			name:        "active request",
			requestCtx:  (*testing.T).Context,
			wantHandled: false,
			wantStatus:  http.StatusOK,
		},
		{
			name: "client canceled",
			requestCtx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			wantHandled: true,
			wantStatus:  http.StatusOK,
		},
		{
			name: "deadline exceeded",
			requestCtx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			wantHandled: true,
			wantStatus:  http.StatusRequestTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			h := New(apitest.NewCore(t, apitest.WithEcho(e)))
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/test", http.NoBody).
				WithContext(tt.requestCtx(t))
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			handled, err := h.handleRequestContextError(ctx)

			require.NoError(t, err)
			assert.Equal(t, tt.wantHandled, handled)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

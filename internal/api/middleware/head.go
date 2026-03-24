package middleware

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// NewHeadToGet returns a pre-middleware that rewrites HEAD requests to GET
// before Echo's router runs. This ensures HEAD requests match GET routes and
// receive the same status code, satisfying RFC 9110 section 9.3.2.
//
// Go's net/http.Server automatically suppresses the response body for HEAD
// requests, so only the method needs rewriting for correct behavior.
//
// Must be registered with e.Pre() so it executes before routing:
//
//	e.Pre(middleware.NewHeadToGet())
func NewHeadToGet() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Method == http.MethodHead {
				c.Request().Method = http.MethodGet
			}
			return next(c)
		}
	}
}

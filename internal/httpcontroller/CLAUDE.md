# HTTP Controller CSRF Protection

## Middleware Configuration

```go
// middleware.go - CSRF setup
config := middleware.CSRFConfig{
    TokenLookup:    "header:X-CSRF-Token,form:_csrf",
    CookieName:     "csrf",
    CookiePath:     "/",
    CookieHTTPOnly: false,  // Allow JS access for hobby/LAN use
    CookieSecure:   false,  // Allow HTTP for LAN deployments
    CookieSameSite: http.SameSiteLaxMode,
    CookieMaxAge:   1800,   // 30 minutes
    ContextKey:     CSRFContextKey,
}
```

## Token Distribution

1. **Page Rendering**: Token injected via `CSRFContextKey`

   ```go
   data.CSRFToken = c.Get(CSRFContextKey).(string)
   ```

2. **Template Usage**:
   ```html
   <meta name="csrf-token" content="{{.CSRFToken}}" />
   ```

## Exempted Paths

CSRF validation skipped for:

- `/assets/*` - Static files
- `/api/v1/media/*` - Media streaming
- `/api/v1/sse` - Server-sent events
- `/api/v1/auth/*` - Auth endpoints
- `/api/v1/oauth2/*` - OAuth flows

## Error Handling

Invalid CSRF returns `403 Forbidden` with structured logging.

# API v2 CSRF Protection

## Overview

All v2 API endpoints require CSRF protection except public read-only endpoints.

## CSRF Token Validation

The CSRF middleware validates tokens from:
1. `X-CSRF-Token` header (primary)
2. `_csrf` form field (fallback)

## Implementation

```go
// CSRF tokens are validated by middleware before reaching handlers
// No additional CSRF handling needed in individual endpoints

// Public endpoints that skip CSRF (defined in middleware.go):
var publicV2ApiPrefixes = map[string]struct{}{
    "/api/v2/detections":          {}, // GET only
    "/api/v2/analytics":           {}, // GET only
    "/api/v2/media/species-image": {},
    "/api/v2/media/audio":         {},
    "/api/v2/spectrogram":         {},
    "/api/v2/audio":               {},
    "/api/v2/health":              {},
}
```

## Client Requirements

- Include `X-CSRF-Token` header with all POST/PUT/DELETE requests
- Token available from meta tag: `<meta name="csrf-token">`
- Cookie fallback: `csrf` (when not HttpOnly)
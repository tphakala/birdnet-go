# API v2 Development Guidelines

## Essential Reference

**ALWAYS read `internal/api/v2/README.md` first** - it contains:
- Complete list of all 75 API endpoints 
- Route registration patterns
- Authentication requirements
- Best practices and security guidelines

## Quick Development Rules

### Finding Existing Endpoints
1. Check `internal/api/v2/README.md` for complete endpoint list
2. Search by category (auth, analytics, detections, etc.)
3. Look for similar patterns before creating new endpoints

### Adding New Endpoints

1. **Find the right file**: Group by functionality (detections.go, analytics.go, etc.)
2. **Follow registration pattern**:
   ```go
   // Public endpoint
   c.Group.GET("/path", c.HandlerName)
   
   // Protected endpoint
   c.Group.POST("/path", c.HandlerName, c.getEffectiveAuthMiddleware())
   ```
3. **Update README.md** with new endpoint documentation
4. **Use error handling**: `return c.HandleError(ctx, err, "message", statusCode)`

### Authentication Patterns
- Public endpoints: No middleware
- Protected endpoints: `c.getEffectiveAuthMiddleware()`
- Rate-limited streams: `middleware.RateLimiterWithConfig(config)`

### Critical Rules
- **Never duplicate existing endpoints** - check README.md first
- **Always validate input** - prevent injection attacks
- **Use structured logging** - `c.logAPIRequest(ctx, level, msg, args...)`
- **Follow error format** - Use `c.HandleError()` consistently
- **Document in README.md** - Update endpoint table immediately

### Code Quality
- Input validation is mandatory
- Use SecureFS for file operations
- Parameterized database queries only
- Follow existing naming conventions
- Include authentication where needed

## CSRF Protection (Legacy Info)

CSRF middleware validates tokens from:
1. `X-CSRF-Token` header (primary)  
2. `_csrf` form field (fallback)

Public read-only endpoints skip CSRF validation.
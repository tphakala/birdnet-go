# API v2 Development Guidelines

## Essential Reference

**ALWAYS read `internal/api/v2/README.md` first** - it is the endpoint catalog:

- Complete list of all API endpoints, grouped by domain
- Per-route authentication requirements
- Request/response shapes and best practices

## Architecture (post-split)

`internal/api/v2` is a thin composition root (package `api`) plus one subpackage
per domain. The dependency direction is strictly acyclic:

```
internal/api (parent server.go)
        |  api.New, *api.Controller
        v
internal/api/v2            package api  (the facade)
  Controller{ *apicore.Core; <domain>.Handler fields }
  New / NewWithOptions / InitializeAPI, ordered initRoutes()
        |                         |                      |
        | imports                 | imports              | imports
        v                         v                      v
internal/api/v2/analytics   internal/api/v2/weather  ...  internal/api/v2/<domain>
        |  type Handler struct{ *apicore.Core }; New; RegisterRoutes
        +------------------+------------------+
                 | imports          | imports
                 v                  v
       internal/api/v2/apicore   internal/api/v2/dto
       (Core: shared state,        (cross-domain request/
        helpers, middleware,        response DTOs)
        SSE hub, broadcasters)
                 |
                 v
   leaf pkgs: conf, datastore, logger, securefs, observability, ...
```

Rule, stated once: **domains depend on `apicore` and `dto`; the facade depends on
`apicore` and every domain; `apicore` depends on neither a domain nor the
facade.** There is no path back up, so no import cycle is possible. This is
enforced mechanically by `internal/api/v2/apicore/import_guard_test.go` (a
`go list -deps` check that fails if `apicore` or `apitest` ever imports a domain
or the facade); it runs inside the normal `go test ./...` unit-test jobs.

### What lives where

- **`apicore`** (`internal/api/v2/apicore`): the shared `Core` struct (deps,
  settings accessors, error/log helpers, telemetry reporting, `RequireDatastore`),
  the shared group middleware (`TunnelDetectionMiddleware`, `LoggingMiddleware`,
  `PrivateModeAuth`, the trusted-proxy IP extractor, `GetAuthMiddleware`), the SSE
  hub (`SSEManager`, `SSEClient`) and the broadcasters (`BroadcastDetection`,
  `BroadcastSoundLevel`, `BroadcastPending`). Everything a domain handler touches
  is **exported** on `Core` (cross-package field/method promotion does not bypass
  Go's export rules).
- **`<domain>`** (e.g. `internal/api/v2/weather`): `type Handler struct{ *apicore.Core }`,
  `New(core *apicore.Core) *Handler`, and `RegisterRoutes(g *echo.Group)`. The
  handler embeds `*apicore.Core` **by pointer** so the shared members promote onto
  it. Domain-only types and helpers stay unexported in the domain package.
- **`dto`** (`internal/api/v2/dto`): request/response structs shared by 2+ domains
  (e.g. `SourceInfo`, `WeatherInfo`, `RangeFilterSpecies`). Domain-only structs
  stay in their domain package.
- **`apitest`** (`internal/api/v2/apitest`): importable test scaffolding built
  around `*apicore.Core`. Domain tests build their own handler:
  `h := weather.New(apitest.NewCore(t))`. `apitest` imports only `apicore`/`dto`.
- **`api` (facade, directory root)**: `Controller` embeds `*apicore.Core` and holds
  one field per domain Handler. `New`/`NewWithOptions`/`InitializeAPI` build the
  single `Core`, construct each domain Handler around it, and `initRoutes` calls
  every `RegisterRoutes` in a deterministic ordered list. `settings.go` (and its
  large test suite) intentionally still lives here, as does the facade-owned
  name-map plumbing (`name_maps.go`) and the cross-domain `/system` wiring
  (`system_routes.go`).

> **Embed `*apicore.Core` by pointer only, never by value.** `Core` holds
> `atomic.Pointer`, `sync.RWMutex` and `sync.WaitGroup` fields; copying it by value
> desyncs the atomics and trips `go vet` copylocks. A single `Core` is built once
> in `NewWithOptions` and shared by pointer to every handler and the facade.

> **Broadcaster-payload rule.** Because the SSE broadcasters live in `apicore` and
> are called from domains, their parameter/return types must stay in leaf packages,
> `dto`, or `apicore` itself - **never a domain type** - or `apicore` would have to
> import a domain and close a cycle. Use a leaf type, `any` + serialize, or push the
> payload struct to `dto`.

## Adding endpoints

### Recipe A - new endpoint in an EXISTING domain (no facade change)

1. Add the handler method on that domain's `*Handler` in
   `internal/api/v2/<domain>/`, receiver named `c`:

   ```go
   func (c *Handler) GetThing(ctx echo.Context) error {
       if c.DS == nil {
           return c.HandleError(ctx, nil, "datastore unavailable", http.StatusServiceUnavailable)
       }
       // ... use c.CurrentSettings(), c.HandleError(...), dto.Thing, etc.
       return ctx.JSON(http.StatusOK, resp)
   }
   ```

2. Add the route line to that domain's `RegisterRoutes(g *echo.Group)`:

   ```go
   g.GET("/things", c.GetThing)                       // public
   g.POST("/things", c.CreateThing, c.AuthMiddleware) // protected
   ```

3. Update `README.md` with the new endpoint. No facade edit is needed.

### Recipe B - new domain

1. Create `internal/api/v2/<domain>/<domain>.go` with
   `type Handler struct{ *apicore.Core }`, `New(core *apicore.Core) *Handler`, and
   `RegisterRoutes(g *echo.Group)`.
2. In the facade (`api.go`): add one `*<domain>.Handler` field to `Controller`, one
   `c.<domain> = <domain>.New(c.Core)` line in `NewWithOptions`, and one ordered
   entry `{"<domain> routes", func() { c.<domain>.RegisterRoutes(c.Group) }}` in
   `initRoutes`. Registration is explicit (not `init()`-based) so order stays
   deterministic; preserve the existing ordering.
3. New domains are subpackages of `api/v2`, so the "all new endpoints stay under
   api/v2" rule holds.

A single `RegisterRoutes` is the norm, but a larger domain may expose several named
registrars (e.g. `detections` has `RegisterSearchRoutes` + `RegisterDetectionRoutes`;
`analytics` has `RegisterAnalyticsRoutes` + `RegisterHeatmapRoutes` +
`RegisterInsightsRoutes` + `RegisterDatabaseOverviewRoutes`; `audio` and `system`
similarly). Each is wired as its own ordered `initRoutes` entry, which keeps the
original per-route registration order across the split.

### Recipe C - new shared dependency or middleware

- Shared dependency: add an exported field (+ functional option) on `apicore.Core`
  and thread it through `NewCore`; domains read it via promotion.
- Shared middleware: add it on `apicore` and apply it at the facade group level in
  `NewWithOptions` (preserving order), or as a shared per-route middleware
  constructor that domains call.

## Authentication patterns

- Public endpoints: no middleware.
- Protected endpoints/groups: apply the promoted `c.AuthMiddleware` field directly,
  e.g. `g.Group("/control", c.AuthMiddleware)` or
  `g.POST("/path", c.Handler, c.AuthMiddleware)`. (`c.GetAuthMiddleware()` returns
  the same value for callers that prefer an accessor.) The middleware is injected
  from the parent server via the `WithAuthMiddleware` functional option.
- Rate-limited streams: `middleware.RateLimiterWithConfig(config)`.
- PrivateMode: all endpoints are gated by `apicore.PrivateModeAuth` group
  middleware; the bootstrap/login/live-audio carve-outs are listed in the facade's
  `isPrivateModeExempt` allow-list (keyed on method + path, fail-closed).

## Route namespace guide

The API uses distinct namespaces. Adding endpoints to the wrong namespace causes
route collisions.

| Namespace | Purpose | Registration | Example |
|---|---|---|---|
| `/audio/:id` | Detection audio clips by numeric note ID | `c.Echo.GET(...)` (media domain) | `ServeAudioByID` |
| `/system/audio/*` | Audio device/source management (protected) | `protectedGroup.Group("/audio")` (audio domain) | `GetAudioDevices`, `ListAudioSources` |
| `/streams/*` | Live streaming, SSE, source listing (public) | `g.GET("/streams/...")` (audio/sse domains) | `StreamAudioLevel`, `ListStreamSources` |
| `/media/*` | Static media files (images, spectrograms) | `g.GET("/media/...")` (media domain) | `ServeSpectrogram` |

**WARNING:** `GET /api/v2/audio/:id` is registered directly on `c.Echo` (not the
`/api/v2` group) and catches ALL paths under `/api/v2/audio/*`. Any non-numeric
path like `/api/v2/audio/sources` returns 400. Never add new endpoints under
`/api/v2/audio/` unless they use a numeric `:id` parameter.

**Public endpoints that expose source metadata** must anonymize display names for
unauthenticated clients (the audio domain does this with its local
`getAnonymizedSourceName`, matching `StreamAudioLevel`).

## Critical rules

- **Never duplicate existing endpoints** - check `README.md` first.
- **Always validate input** - prevent injection attacks; use SecureFS (`c.SFS`) for
  file operations and parameterized queries only.
- **Use structured logging** - `c.LogAPIRequest(ctx, level, msg, args...)` and the
  `c.LogInfoIfEnabled`/`LogWarnIfEnabled`/`LogErrorIfEnabled`/`LogDebugIfEnabled`
  family.
- **Follow the error format** - `return c.HandleError(ctx, err, "message", statusCode)`
  (or `c.HandleErrorWithKey(...)` for an i18n key). The `ErrorResponse` shape and
  correlation-id behavior live in `apicore`.
- **Hot-reload** - read settings per request via `c.CurrentSettings()` /
  `c.ControllerSettings()` (the atomic snapshot on `Core`); never branch on settings
  captured at startup.
- **Document in README.md** - update the endpoint table immediately.

## Future api/v3

A future `internal/api/v3` lives as a sibling facade with its own `Core` (do not
re-monolith). It can reuse these patterns but should not couple to `apicore` so the
two versions evolve independently.

## CSRF Protection (legacy info)

CSRF middleware validates tokens from the `X-CSRF-Token` header (primary) or the
`_csrf` form field (fallback); it is wired globally in the parent `server.go`. The
token is issued by the `/app/config` endpoint via `middleware.EnsureCSRFToken()`.
Public read-only endpoints skip CSRF validation.

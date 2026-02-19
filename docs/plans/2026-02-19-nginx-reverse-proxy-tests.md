# Nginx Reverse Proxy Integration Tests

**Date:** 2026-02-19
**Status:** Implementation Complete
**Branch:** `feature/testcontainers-nginx-proxy`
**Related:** `docs/plans/2026-02-14-testcontainers-integration-plan.md` (Phase 5)

## Summary

Adds integration tests that validate all application routes work correctly when
accessed through an nginx reverse proxy. Tests use real nginx Docker containers
to verify that no valid route returns a 404 error in two proxy configurations:

1. **Root proxy** — nginx at `/` proxies to the backend (no path prefix)
2. **Subpath proxy** — nginx at `/birdnet/` proxies to the backend (with `X-Forwarded-Prefix`)

## Motivation

Users commonly deploy BirdNET-Go behind reverse proxies (nginx, Caddy, Traefik,
Home Assistant Ingress). This creates routing challenges:

- Path prefix stripping and rewriting
- `X-Forwarded-Prefix` header propagation
- Asset path rewriting via `sub_filter`
- SSE/WebSocket connection proxying
- Redirect URL construction

Without automated tests, reverse proxy regressions are only caught by users
after deployment.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│ Vitest (Node.js)                                        │
│                                                          │
│  Global Setup:                                           │
│   1. Start BirdNET-Go backend on :8080                   │
│   2. Start nginx root-proxy container on :8180           │
│   3. Start nginx subpath-proxy container on :8181        │
│                                                          │
│  Tests: HTTP fetch through each nginx → verify status    │
│                                                          │
│  Global Teardown:                                        │
│   1. Stop nginx containers (docker stop)                 │
│   2. Stop backend (SIGTERM)                              │
└──────────┬────────────────────────┬─────────────────────┘
           │                        │
    ┌──────▼──────┐          ┌──────▼──────┐
    │ nginx:8180  │          │ nginx:8181  │
    │ Root Proxy  │          │ Subpath     │
    │ / → :8080   │          │ /birdnet/   │
    │             │          │  → :8080    │
    └──────┬──────┘          └──────┬──────┘
           │                        │
           └────────┬───────────────┘
                    │
            ┌───────▼───────┐
            │ BirdNET-Go    │
            │ Backend :8080 │
            └───────────────┘
```

## Test Coverage

### Routes Tested (45+ unique routes)

**SPA UI Routes (21):**
- `/ui/dashboard`, `/ui/notifications`, `/ui/analytics`, `/ui/analytics/advanced`,
  `/ui/analytics/species`, `/ui/search`, `/ui/detections`, `/ui/settings`,
  `/ui/settings/main`, `/ui/settings/userinterface`, `/ui/settings/audio`,
  `/ui/settings/detectionfilters`, `/ui/settings/integrations`, `/ui/settings/security`,
  `/ui/settings/species`, `/ui/settings/notifications`, `/ui/settings/support`,
  `/ui/system`, `/ui/system/database`, `/ui/system/terminal`, `/ui/about`

**API v2 Routes (24):**
- Health, app config, settings, detections, analytics, notifications,
  dynamic thresholds, range filter, integrations, streams, SSE status,
  system info, database stats, resources, disks, audio devices, locales

**Special Routes:**
- Root redirect (`/` → `/ui/dashboard`)
- SSE stream endpoints (connection establishment)
- Invalid routes (verify 404 still works)

### Assertions

For each route, in both proxy configurations:
- HTTP status is **not 404**
- SPA routes return `text/html` content type
- API routes return valid responses
- SSE endpoints return `text/event-stream` content type
- Root redirects point to the correct location
- `X-Forwarded-Prefix` is reflected in `basePath` from `/api/v2/app/config`
- Invalid routes still correctly return 404

## Files Created

| File | Purpose |
|------|---------|
| `frontend/src/test/nginx/root-proxy.conf` | nginx config: root proxy |
| `frontend/src/test/nginx/subpath-proxy.conf` | nginx config: subpath `/birdnet` proxy |
| `frontend/src/test/reverse-proxy-global-setup.ts` | Global setup/teardown (backend + nginx) |
| `frontend/src/test/reverse-proxy.reverse-proxy.test.ts` | All route validation tests |
| `frontend/vitest.reverse-proxy.config.ts` | Vitest config for reverse proxy tests |

## Files Modified

| File | Change |
|------|--------|
| `frontend/package.json` | Added `test:reverse-proxy` and `test:reverse-proxy:auto` scripts |

## Usage

```bash
cd frontend

# Manual mode (start backend yourself first)
npm run test:reverse-proxy

# Auto mode (starts backend + nginx automatically)
npm run test:reverse-proxy:auto
```

## Prerequisites

- Docker running (for nginx containers)
- Backend available on port 8080 (or use auto mode)
- `air` CLI available (for auto backend start)

## Design Decisions

### Docker CLI vs testcontainers-node

Used Docker CLI (`child_process.execSync`) instead of testcontainers-node to:
- Avoid adding a heavy npm dependency
- Stay consistent with existing integration test patterns (spawn-based)
- Keep the setup simple and debuggable

### Node.js environment vs browser mode

Used Vitest in Node.js mode (`environment: 'node'`) because:
- Tests are HTTP-level route validation (fetch-based)
- No need for DOM rendering to verify proxy routing
- Faster execution than browser mode
- Still validates the full nginx → backend → response chain

### Two proxy configurations

Testing both root and subpath proxy catches different bug classes:
- Root proxy: basic proxy_pass correctness
- Subpath proxy: path stripping, `X-Forwarded-Prefix`, `sub_filter`,
  redirect rewriting, double-prefix prevention

### Templated nginx configs

The nginx config files use `BACKEND_HOST` / `BACKEND_PORT` placeholders
that are replaced at runtime. This allows the configs to work on both
Linux (bridge gateway IP) and Docker Desktop (host.docker.internal).

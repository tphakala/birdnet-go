# BirdNET-Go Architecture

This document is a map of how BirdNET-Go is put together: the major subsystems, how audio becomes a detection, and where each concern lives in the tree. It deliberately stays at the package level. File lists, struct definitions and tuning numbers change too often to be copied here, so each section points at the package or guide that owns the detail.

For coding rules read [`AGENTS.md`](AGENTS.md) and [`internal/AGENTS.md`](internal/AGENTS.md). For contributing see [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Table of Contents

- [System Overview](#system-overview)
- [Process Lifecycle](#process-lifecycle)
- [Audio Capture and Routing](#audio-capture-and-routing)
- [Inference](#inference)
- [Detection Pipeline](#detection-pipeline)
- [Datastore](#datastore)
- [Web Server and API](#web-server-and-api)
- [Security](#security)
- [Integrations and Supporting Services](#integrations-and-supporting-services)
- [Frontend](#frontend)
- [Build and Platforms](#build-and-platforms)
- [Testing and Development Tools](#testing-and-development-tools)
- [Further Reading](#further-reading)

---

## System Overview

BirdNET-Go is a single Go binary that listens to one or more audio sources, runs bird (and optionally bat) sound classification models over the audio in real time, stores detections, and serves a web interface and API for reviewing them.

Design principles:

- **Single binary.** The Svelte frontend is embedded in the Go binary. The default build also embeds the BirdNET v2.4 model.
- **Local first.** Detections, audio clips and settings live on the user's machine. Error reporting (Sentry) and the Prometheus endpoint are off by default. Audio clips are stored locally by default.
- **Runs on small hardware.** Raspberry Pi class devices up to desktop servers, with a low-memory mode for constrained systems.
- **Hot reload.** Settings changed in the UI take effect without a restart; code reads settings per request or per operation, never once at startup.
- **Hobbyist audience.** Defaults favour a working setup over tuning knobs.

Standing constraints that shape the code (see [`AGENTS.md`](AGENTS.md)):

- API v1 is gone and frozen. All endpoints are in `internal/api/v2/`.
- The legacy database is deprecated and will be removed. New persistence targets the v2 schema only.
- The frontend targets desktop and tablet; phone layouts are out of scope.
- Errors go through `internal/errors`, never the standard `errors` package.

### High-level picture

```
 Audio sources                                   Web UI (Svelte 5, embedded)
 local devices (malgo)                                      ▲
 RTSP / HTTP / HLS / RTMP / UDP (FFmpeg or native RTSP)     │ HTTP, SSE, HLS
        │                                                   │
        ▼                                          ┌────────┴────────┐
 ┌──────────────┐   frames   ┌───────────────┐    │  Echo server    │
 │ audiocore    │──────────▶│ AudioRouter   │    │  internal/api   │
 │ engine       │            │ (per-consumer │    │  /api/v2/*      │
 └──────────────┘            │  routes)      │    └────────▲────────┘
                             └───────┬───────┘             │
              ┌──────────────────────┼───────────────┐     │
              ▼                      ▼               ▼     │
       BufferConsumer         level / sound     live audio │
       analysis + capture     level consumers   (HLS)      │
       buffers                                             │
              │                                            │
              ▼                                            │
 ┌─────────────────────┐  results   ┌────────────────┐     │
 │ classifier          │───────────▶│ processor      │─────┤
 │ Orchestrator        │  queue     │ filters,       │     │
 │ TFLite/ONNX/OpenVINO│            │ thresholds,    │     │
 └─────────────────────┘            │ actions        │     │
                                    └───────┬────────┘     │
                                            ▼              │
                                     job queue (async)     │
                      ┌──────────┬───────────┼─────────┬────┘
                      ▼          ▼           ▼         ▼
                  datastore   clip export   MQTT   BirdWeather / SSE / ...
                                            │
                                            ▼
                                  event bus → notifications, alerting
```

---

## Process Lifecycle

**CLI** (`cmd/`, Cobra + Viper). The root command registers `serve` (with `realtime` kept as an alias), `authors`, `license`, `range` (species range filter inspection), `support` (support bundle collection, OpenVINO probe), `benchmark`, `notify` and `import-stage` (BirdNET-Pi import, runs privileged and skips normal daemon init). There is no file or directory analysis command. `cmd/` also holds developer tools that are not part of the CLI: `gen-schema` (configuration schema and reference generation), `wiki-export` and `perch-benchmark`.

**Configuration** (`internal/conf`). Settings are layered: command-line flags, environment variables, `config.yaml`, defaults. Validation and migrations run on load. `task generate-schema` regenerates `config.schema.json` and the configuration reference from the `Settings` struct.

**Service lifecycle** (`internal/app`). `cmd/serve` builds the audio engine, then registers four services with `app.App`, in start order:

1. `BirdNETAnalyzer`: loads the model orchestrator and model manager.
2. `DatabaseService`: opens or migrates the datastore, starts retention and housekeeping.
3. `APIServerService`: creates the processor, event consumers and the web server.
4. `AudioPipelineService`: wires audio sources into buffers, starts analysis monitors, sound level, quiet hours, weather polling and clip cleanup.

A service implements `Start` and `Stop` and may declare a shutdown tier. Shutdown has a 9 second total budget, sized for Docker's default 10 second stop timeout. The network tier (API server, monitors) stops first with 6 seconds. The core tier (the database, which must checkpoint its WAL) stops last with its own fresh 3 second budget. After a clean stop the process-global ONNX Runtime and OpenVINO cores are released.

`internal/analysis` holds these services and the glue around them: buffer management, control signals and restart handling, sound level, clip reconciliation, and the job queue and processor subpackages. `internal/mempolicy` applies the low-memory policy (soft `GOMEMLIMIT`, glibc arena cap) before any inference thread starts. `internal/diagnostics` keeps a local boot journal that survives database loss. `internal/restart` decides between re-exec and container exit when a restart is requested.

---

## Audio Capture and Routing

All audio infrastructure lives in `internal/audiocore`. (The older `myaudio` package no longer exists.)

| Area                  | Package                                                                   | Role                                                                                                                                                                 |
| --------------------- | ------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Engine                | `audiocore/engine`                                                        | `AudioEngine` orchestrates the source registry, router, stream manager, device manager, buffer manager and quiet-hours scheduler.                                    |
| Local devices         | `audiocore` (`capture.go`, `device.go`, `usb.go`)                         | Cross-platform capture through malgo (miniaudio): ALSA, CoreAudio, WASAPI.                                                                                           |
| Network streams       | `audiocore/ffmpeg`, `audiocore/stream`                                    | FFmpeg manages RTSP, HTTP, HLS, RTMP and UDP sources by default. Setting `BIRDNET_STREAM_INGEST=native` selects a native RTSP-only manager built on go-audio-stream. |
| Routing               | `audiocore` (`router.go`)                                                 | Non-blocking fan-out of frames to consumers.                                                                                                                         |
| Buffers               | `audiocore/buffer`                                                        | Analysis ring buffers, capture buffers, pooled frame memory.                                                                                                         |
| DSP                   | `audiocore/resample`, `equalizer`, `loudness`, `ultrasonic`, `soundlevel` | Resampling, per-route EQ and gain, loudness normalization, bat band filtering, 1/3-octave sound level measurement. Built on the go-audio-dsp family of libraries.    |
| Encoding and decoding | `audiocore/transcode`, `clipenc`, `wav`, `flac`, `mp3`, `aac`, `opus`     | Native encoders and decoders for clip export, on-demand downloads and processing. FFmpeg is not used for clips.                                                      |
| Scheduling            | `audiocore/schedule`                                                      | Quiet hours.                                                                                                                                                         |

**Frames and routing.** Sources produce `AudioFrame`s (PCM, source id and name, sample rate, timestamp). The frame data is read-only after creation and may be backed by pooled memory with reference counting. The router gives each consumer its own route with a bounded inbox; a slow consumer drops frames rather than stalling capture, and drops are counted, logged and reported. Per-route gain and EQ run in float32 with a single quantization back to 16-bit, and gain changes apply to live routes without rebuilding them.

**From frames to analysis windows.**

1. `analysis.BufferConsumer` is registered per source. It resamples to each enabled model's sample rate (one resampler per distinct target rate) and writes PCM16 to a per-source, per-model analysis buffer and to the source's capture buffer.
2. The analysis buffer is a ring sized to one model clip, in overwrite mode. A monitor goroutine per source and model polls it and reads a full window when one is available. The window is 3 seconds for BirdNET v2.4, Bat and BSG, and 5 seconds for BirdNET v3 and Perch. The `birdnet.overlap` setting (default none) is scaled proportionally for 5 second models.
3. The capture buffer holds recent audio (120 seconds by default, longer with extended capture) so that a clip can be cut with pre-roll when a detection is saved.

**Other consumers on a route** include audio level metering (the live level meter in the UI), sound level monitoring (`internal/analysis/sound_level*.go`), and live listening, which is served as HLS (see the API section). Sound level series exposed to Prometheus are labelled with the source id, never the display name, because the metrics listener has no authentication.

**Liveness.** A watchdog restarts sources that go silent, and stream health is tracked per stream and exposed through the API.

---

## Inference

`internal/classifier` owns models and prediction. `internal/inference` defines the backend abstraction and the runtime implementations.

**Orchestrator and model manager.** `classifier.Orchestrator` is the entry point for inference. It manages one or more loaded models with per-model locking, name resolution to a common species identity, hot reload and failover. `ModelManager` handles the model catalog, installation, variant and primary swaps, and uninstalling, driven from the settings UI and API.

**Models.** The registry knows these model families; which ones run is chosen by `models.enabled` and the per-model settings sections:

| Model        | Format | Clip | Notes                                                          |
| ------------ | ------ | ---- | -------------------------------------------------------------- |
| BirdNET v2.4 | TFLite | 3 s  | Default. FP32 model and range filter models are embedded.      |
| BirdNET v3   | ONNX   | 5 s  | Downloaded.                                                    |
| Perch v2     | ONNX   | 5 s  | Downloaded.                                                    |
| Bat          | ONNX   | 3 s  | Ultrasonic source; gated by a night-time schedule. Downloaded. |
| BSG Finland  | ONNX   | 3 s  | Regional model. Downloaded.                                    |

Regional BirdNET v2.4 variants and geographic range models exist in the catalog. Non-embedded models are fetched from Hugging Face by the model manager. Building with the `noembed` tag leaves the embedded models out for smaller binaries and images; the Docker image ships them separately.

**Backends** (`internal/inference`):

- **TFLite** through `go-tflite` (CGO), built unless the `notflite` tag is set. Used for BirdNET v2.4.
- **ONNX Runtime** through `onnxruntime_go`, loaded as a shared library (`task download-onnxruntime`).
- **OpenVINO** behind the `openvino` build tag, loaded at runtime, with ONNX Runtime as the fallback when the library is absent. Selected with `birdnet.backend` (`auto`, `onnx`, `openvino`). The default Linux amd64 and arm64 builds carry the tag.

`internal/hwprofile` decides which model builds a machine can run.

**Species range filter.** A geographic and seasonal model (`classifier/range_filter*.go`, the `range` command) limits detections to species plausible for the configured location. It applies only when a location is configured and a participating model is in use; Bat and BSG never participate. It is refreshed daily by `UpdateRangeFilterAction` and reloaded on settings or model changes.

**Species identity.** `internal/speciesindex` is the single owner of the process-wide species name lookup. `internal/openfauna` supplies embedded multilingual names and taxonomy, `internal/labels` the non-bird and vocalization label sets, and `internal/ebird` taxonomy and observation data.

**Voice activity detection.** An optional Silero VAD (`internal/inference/vad`, needs ONNX Runtime, off by default) acts as a second source of "speech present" for the privacy filter described below.

---

## Detection Pipeline

`classifier` pushes prediction results into a bounded results queue (non-blocking, a full queue drops the result and records it). `processor.Processor` (`internal/analysis/processor`) consumes it:

1. **Validate and classify results.** Parse species, record human-voice and dog-bark label hits for the filters, and drop human-sound labels from saved detections.
2. **Thresholds.** Per-species threshold if configured, otherwise the model's global threshold; with dynamic thresholds enabled (default), the threshold for a species is lowered in steps after high-confidence detections and resets when its timer expires. The state is tracked per species, not per model, and persisted.
3. **Species filters.** Exclude list, include list and range filter.
4. **Pending detections.** A candidate becomes a pending detection keyed by source and species. A flusher releases it after the detection window, so repeated hits merge and the clip covers the whole event.
5. **Discard filters.** Before actions run, a pending detection can be discarded by:
   - the false-positive filter (requires confirmations within the window; level 0 is off);
   - the **privacy filter** (on by default): human-voice labels above a confidence floor, plus optional VAD, mark moments of speech; a detection whose clip reaches a speech hit is dropped. Detections may wait briefly until a speech detector has scanned the whole clip;
   - the dog bark filter and the daylight filter (nocturnal species during daylight, needs a location).
6. **Actions.** Surviving detections get a set of actions enqueued on the job queue.

Note that `internal/privacy` is unrelated to the voice filter: it scrubs telemetry and logs (URL anonymization, RTSP sanitizing, system id).

**Actions** (`processor/actions_*.go`): log, database save, clip export, BirdWeather upload, MQTT publish, SSE broadcast, range filter update, external command, and `CompositeAction`, which runs several actions in sequence with a per-step timeout. When the database is enabled, the save and the SSE broadcast run in one composite so that SSE sees the database id assigned by the save. A step in between enqueues the MQTT publish as its own job after the save, so the payload carries the database id and the MQTT retry settings apply; the composite itself is never retried, since a retry would save the detection twice. Without a database, SSE and MQTT are queued as independent actions.

**Job queue** (`internal/analysis/jobqueue`). Actions run asynchronously off the audio path in a bounded queue with per-job state, optional retry with exponential backoff, and per-action statistics. Retry configuration is chosen in the processor's job queue adapter and currently applies to BirdWeather uploads, the MQTT publish job, and deferred clip saves under extended capture. A retry configuration with `Enabled` false means a single attempt. A job never has two attempts running at once: an attempt that overruns its timeout is waited for before the next one starts. See [`internal/analysis/jobqueue/README.md`](internal/analysis/jobqueue/README.md).

**Event bus** (`internal/events`). After a database save, the detection is published as an event. Consumers include the notification service (new species and detection notifications), the alerting bridge (rules on detection events) and the telemetry worker. Errors flow through the same bus: `internal/errors` builds structured errors, which reach notifications and telemetry asynchronously without blocking the caller.

**Species tracking** (`internal/analysis/species`) tracks lifetime, yearly and seasonal novelty for "new species" logic.

---

## Datastore

The datastore is in the middle of a long migration. Read [`internal/datastore/AGENTS.md`](internal/datastore/AGENTS.md) before touching it.

- **v2 schema** is the present and the future. Entities are in `datastore/v2/entities` (detections with predictions, model contributions, reviews, comments and locks; labels and taxonomic classes; AI models; audio sources; weather; dynamic thresholds; alert rules and history; notification history; app events and metadata; migration state). Persistence goes through repositories: `datastore.DetectionRepository` and the rest in `datastore/v2/repository`.
- **Legacy schema** (the `notes` table, the `Note` model, `DataStore`, `SQLiteStore`, `MySQLStore` and the large `datastore.Interface`) is frozen and will be removed. `datastore/v2only` adapts a v2 database to the old `Interface` so existing callers keep working until they move to repositories.
- **Startup decides the mode** (`datastore/v2/startup.go`): a fresh install starts directly in v2-only mode. An existing legacy database stays in legacy mode until the user starts a migration through the API. Migration runs as a state machine (initializing, dual write, migrating, validating, cutover, completed) in `datastore/v2/migration` and `state_manager.go`; afterwards data lives in `birdnet_v2.db` and may later be consolidated to the configured path.
- **Engines.** SQLite (default, single open connection) and MySQL or MariaDB (pooled), for both schemas.
- **Mocks.** `datastore.Interface` and other interfaces are mocked with mockery (`.mockery.yaml`, `go generate ./internal/datastore`). Never edit generated mocks by hand.

Related storage: `internal/diskmanager` enforces clip retention (age, usage and drain policies) and reconciles clips with the database; `internal/backup` provides scheduled, optionally encrypted backups to local, FTP, SFTP, rsync or Google Drive targets.

---

## Web Server and API

**Server** (`internal/api`, Echo). `server.go` builds the middleware chain and routes. Global middleware, in order: panic recovery, request logging, CORS, CSRF, body size limit, gzip (skipping SSE), security headers. Base-path handling for reverse proxies runs before routing and is hot-reloadable (`X-Ingress-Path`, then `X-Forwarded-Prefix`, then `webserver.basepath`). Authentication, rate limiting and cache control are applied per route or route group, not globally.

**Frontend serving.** The SPA shell and assets are served from the embedded `frontend.DistFS`, or from `./frontend/dist` on disk when it exists, which is how development works. `/ui/*` serves the same HTML shell and the client router takes over. HTML and text assets are rewritten for sub-path proxies.

**API v2** (`internal/api/v2`). A thin facade plus one subpackage per domain (analytics, detections, species, media, settings, system, weather, notifications, alerts, models, imports, support, and others) on shared `apicore` and `dto` packages. Each domain exposes a `Handler` with `RegisterRoutes`. The dependency direction is one-way: domains import `apicore`, `apicore` imports no domain, and a test enforces it. The endpoint catalog is [`internal/api/v2/README.md`](internal/api/v2/README.md); the recipe for adding endpoints is [`internal/api/v2/AGENTS.md`](internal/api/v2/AGENTS.md). Group-level middleware adds panic recovery, tunnel detection, a body limit, logging and private-mode gating.

**Real time.** Server-Sent Events carry detections, sound levels, notifications, audio levels, stream health, system metrics and long-running job progress (imports, model installs). Live listening is HLS (`go-hls`) with tokenized playlists. The only WebSocket is the optional browser terminal, off unless `webserver.enableterminal` is set.

**Other endpoints on the server:** `/health`, pprof (off unless profiling is enabled, see [`doc/PROFILING.md`](doc/PROFILING.md)), the PWA manifest and service worker, and the OAuth routes. A separate Prometheus listener is optional (`realtime.telemetry`).

---

## Security

Detail for the authentication package is in [`internal/security/README.md`](internal/security/README.md) and the user-facing [`doc/wiki/security.md`](doc/wiki/security.md).

**Model.** Authentication is binary: a request is authenticated or it is not. There are no roles. When no provider is configured the instance is open.

**Providers** (`internal/security`):

- Password login, implemented as an OAuth2 authorization-code flow against the server itself, issuing access tokens.
- Social login through goth: Google, GitHub, Microsoft, LINE, Kakao and a generic OIDC provider (always with PKCE), each restricted by a user id allowlist.
- **Local subnet bypass** (off by default) has two parts. The request gate admits loopback and the configured CIDRs only. A separate same-subnet-as-host check applies to `IsUserAuthenticated` and cookie handling.
- **Trusted proxies** (`security.trustedproxies`, with a Cloudflare preset): forwarded client IP headers are honoured only from configured proxies, through `internal/security/proxytrust`.
- Public access can be opened per feature, for example live audio. Private mode gates the whole v2 API.

**API v2 authentication** (`internal/api/auth`). A `Service` interface wraps the security package through `SecurityAdapter`; `Middleware.Authenticate` runs in this order: pass-through when authentication is not required (no provider, or subnet bypass); Bearer token if an `Authorization` header is present (an invalid token is an immediate 401, with no fall-through); browser session; otherwise a redirect to `/login` for HTML clients or a JSON 401 for API clients. `AuthMethodAPIKey` is declared but not implemented.

**Sessions and tokens.** Sessions use gorilla's filesystem store under the config directory, with keys derived from `security.sessionsecret`, a 7 day default lifetime, `HttpOnly`, `SameSite=Lax` and `Secure` over TLS. Tokens persist to `tokens.json` (mode 0600, atomic writes) and expired tokens are cleaned up hourly. CSRF protection is global middleware.

**Transport.** `security.tlsmode` selects plain HTTP, Let's Encrypt (autocert in `internal/api/server.go`) or a self-signed certificate (`internal/tls`). `internal/securefs` provides confined, cached file access for media.

**Privacy.** Sentry is opt-in; reports go through the scrubbing in `internal/privacy`. Secrets and PII must never be logged; typed helpers in `internal/logger` enforce this for credential field names, backed by a lint rule.

---

## Integrations and Supporting Services

| Package                  | Purpose                                                                                                                                           |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/mqtt`          | MQTT publishing and Home Assistant discovery.                                                                                                     |
| `internal/notification`  | Toasts, persistent notices and push delivery through shoutrrr, webhook and script providers, with rate limiting and circuit breakers.             |
| `internal/birdweather`   | Soundscape and detection upload to BirdWeather, loudness-normalized with the native `loudnorm`.                                                   |
| `internal/ebird`         | eBird API client for taxonomy and observations.                                                                                                   |
| `internal/weather`       | Weather providers: Yr.no (default), OpenWeather, Pirate Weather, Weather Underground. Plus `internal/suncalc` for sun and moon.                   |
| `internal/imageprovider` | Species images (Avicommons, Wikipedia) with a file cache.                                                                                         |
| `internal/alerting`      | User-defined alert rules, evaluator, dispatcher and defaults, fed by detections and metrics.                                                      |
| `internal/observability` | Prometheus metrics, health events and the metrics endpoint. `internal/health` runs the health checks; `internal/monitor` collects system metrics. |
| `internal/telemetry`     | Optional Sentry integration and the error-to-event-bus bridge.                                                                                    |
| `internal/support`       | Support bundle collection (`support collect`, the API and the UI).                                                                                |
| `internal/imports`       | BirdNET-Pi import.                                                                                                                                |
| `internal/spectrogram`   | Native Go spectrogram rendering, pre-rendering and size variants. No external binary is involved.                                                 |
| `internal/detection`     | The domain `Result` type that carries detections between layers.                                                                                  |
| `internal/update`        | Release manifest schema only; there is no in-app updater.                                                                                         |

Each package has its own README where one exists; prefer those for behaviour and configuration keys.

---

## Frontend

The UI is a Svelte 5 single-page app (not SvelteKit) with TypeScript, Tailwind CSS v4, Vite and Vitest, in `frontend/`. Component and style rules are in [`frontend/AGENTS.md`](frontend/AGENTS.md) and the nested guides it links.

```
frontend/src/
├── App.svelte          # path-based client router under /ui/, lazy-loaded views
├── main.js
└── lib/
    ├── api/            # typed API clients
    ├── auth/           # provider definitions
    ├── components/     # shared icons and primitives
    ├── desktop/        # features/, views/, layouts/, components/: the main UI
    ├── i18n/           # ICU message runtime and generated types
    ├── stores/         # Svelte 5 rune stores (navigation, settings, auth, models, ...)
    ├── telemetry/      # client-side error reporting
    ├── types/
    └── utils/          # API client, SSE helpers, settings and audio helpers
```

- **State.** Rune-based stores for cross-component state, `$state`, `$derived` and `$effect` inside components.
- **Routing.** `App.svelte` maps `/ui/...` paths to route configs and loads views with dynamic `import()`. The Go server returns the same HTML shell for every UI route.
- **Real time.** `ReconnectingEventSource` consumers for the SSE streams described above.
- **i18n.** 16 locales in `frontend/static/messages`, validated with `npm run i18n:validate`; see [`frontend/static/messages/AGENTS.md`](frontend/static/messages/AGENTS.md).
- **Scope.** Desktop and tablet. Phone viewports are out of scope.
- **Tests.** Vitest unit and component tests, integration and reverse-proxy suites, browser-mode tests, and Playwright end-to-end tests in `frontend/tests/e2e`.

---

## Build and Platforms

**Build system.** [Task](https://taskfile.dev) (`Taskfile.yml`); run `task --list` for everything. Run `task setup-dev` once to install the TensorFlow Lite headers and library that every Go build, lint and test needs.

| Task                                                                               | Purpose                                                             |
| ---------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| `task`                                                                             | Build for the current platform (frontend plus Go, models embedded). |
| `task linux_amd64`, `linux_arm64`, `darwin_amd64`, `darwin_arm64`, `windows_amd64` | Cross-platform builds.                                              |
| `task noembed` and the `noembed_*` variants                                        | Builds without the embedded models.                                 |
| `task frontend-build`                                                              | Production frontend build with strict type checking.                |
| `task lint`, `task test`                                                           | Go lint and tests with the right build tags and CGO flags.          |
| `task generate-schema`                                                             | Regenerate the config schema and reference.                         |

CGO is required for TensorFlow Lite. The frontend is embedded with `//go:embed all:dist` (`frontend/embed.go`); building with the `skipfrontend` tag substitutes a stub so lint and tests do not need a frontend build.

**Development workflows.**

- `task dev_server` runs the backend with Air hot reload. Build the frontend in watch mode with `task frontend-watch`; the server picks up `./frontend/dist` from disk when it exists.
- `task frontend-dev` runs the Vite dev server with HMR on port 5173, proxying `/api` to a running backend on 8080.

**Platforms.** Release binaries are built for Linux amd64 and arm64, Windows amd64 and macOS arm64. Docker images are published for Linux amd64 and arm64, based on Debian trixie-slim with FFmpeg and ALSA. There is no 32-bit ARM build. FFmpeg handles network stream ingest by default.

---

## Testing and Development Tools

- **Go tests** use testify and run through `task test` with the race detector. Read [`TESTING.md`](TESTING.md) before writing or changing tests, in particular the rules for asynchronous mock expectations.
- **Mocks** are generated with mockery; see `.mockery.yaml` and [`internal/datastore/mocks/README.md`](internal/datastore/mocks/README.md).
- **Lint.** `task lint` (golangci-lint with custom ruleguard rules in `rules/`), `cd frontend && npm run check:all` for the frontend. A Husky pre-commit hook formats and lints staged files.
- **CI.** Go tests run through gotestsum with one automatic rerun, and the `test-report` job classifies results as regression, pass with flakes, or pass. A cross-platform build workflow compiles and vets Windows and arm64 targets and the `openvino` tag.
- **Profiling and debugging.** [`doc/PROFILING.md`](doc/PROFILING.md), [`doc/DEBUG-COLLECTION.md`](doc/DEBUG-COLLECTION.md).

---

## Further Reading

- [`doc/wiki/`](doc/wiki/): user-facing documentation, including `detection-pipeline.md`, `configuration-reference.md`, `security.md` and `building.md`.
- [`internal/api/v2/README.md`](internal/api/v2/README.md): API endpoint catalog.
- [`internal/datastore/AGENTS.md`](internal/datastore/AGENTS.md): datastore rules and the migration direction.
- [`internal/classifier/README.md`](internal/classifier/README.md): the orchestrator, model manager and how to add a model.
- [`internal/analysis/jobqueue/README.md`](internal/analysis/jobqueue/README.md), [`internal/analysis/species/README.md`](internal/analysis/species/README.md), [`internal/notification/README.md`](internal/notification/README.md), [`internal/mqtt/README.md`](internal/mqtt/README.md), [`internal/birdweather/README.md`](internal/birdweather/README.md), [`internal/backup/README.md`](internal/backup/README.md): per-package behaviour.
- [`internal/events/ARCHITECTURE.md`](internal/events/ARCHITECTURE.md): the async event system in depth.

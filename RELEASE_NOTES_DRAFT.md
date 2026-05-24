This release introduces an audio liveness watchdog that automatically recovers from silent audio capture failures, a system health diagnostics page with 31 checks across 8 categories, and multiselect with bulk actions for the detections list. New Help & Support pages guide users through bug reporting with support dump generation. ONNX Runtime availability is now checked across the model gallery, install pipeline, and health system so users get clear feedback when the runtime is missing. A large batch of thread safety, TOCTOU race, and panic safety fixes improves stability across the audio pipeline and API layer.

## New Features

### Audio Liveness Watchdog with Tiered Recovery
A new per-source watchdog detects when audio capture silently dies (e.g., USB audio hardware failure) and orchestrates automatic recovery through a tiered state machine: single-source restart, full app restart, then terminal failure with notification at every step. Previously, a silent audio thread death could go undetected for hours with no error logged and no alert fired. The watchdog exposes per-source health state via `GET /api/v2/health/audio` and its check interval and thresholds are configurable through new settings (#3106, #3112).

### System Health Diagnostics
A new System Health page under Help runs 31 diagnostic checks across 8 categories (system, audio, analysis, streams, database, network, config, logs) in parallel with per-check timeouts. Results are grouped by category with color-coded status pills. Reports can be copied to clipboard or exported as JSON. Checks that are not yet wired to live data show a "Skipped" status rather than misleading results. The diagnostics backend caches reports by UUID for retrieval (#3132, #3133, #3135, #3137, #3185, #3186, #3187).

### Detection List Multiselect and Bulk Actions
The detections list gains a selection mode with per-row checkboxes, shift-click range selection, and a header checkbox for page-level select/deselect. A Gmail-style "select all N matching" banner appears when the entire page is selected. Supported bulk actions include delete, mark correct, mark false positive, lock, and unlock, with a confirmation modal and toast feedback. New batch API endpoints under `/api/v2/detections/batch/` handle the backend operations (#3119).

### Help & Support Pages with Guided Bug Reporting
A new Help & Support page at `/ui/help` provides cards for Report Bug, Ask a Question, and Quick Links. The guided Report Bug flow at `/ui/help/report-bug` walks users through providing system information, describing the issue, generating a support dump, and opening a GitHub issue. The sidebar gains a collapsible Help section and the header settings menu adds Report Bug and Ask a Question links (#3130).

### ONNX Runtime Availability Gating
Perch, BattyBirdNET, and geomodel features are now gated behind an ONNX Runtime availability check with five defensive layers: the model gallery grays out incompatible models with a warning banner, the install API rejects ONNX-dependent installs, the orchestrator emits a high-priority bell notification on load failure, already-installed models that lost ORT show a red warning, and a new `ort_availability` health check reports version and library path. Users no longer see cryptic init errors when ORT is missing (#3153, #3155).

### Species Heatmap Grid API (Preview)
A new `GET /api/v2/range/heatmap` endpoint computes species probability grids across a map viewport for all 48 BirdNET weeks, returning a compact binary payload (BNHM format). The endpoint uses batch geomodel inference to compute thousands of grid points in a single ONNX session call, with an in-memory LRU cache and generation-counter invalidation. A dedicated multi-threaded ONNX session and IoBinding tensor reuse eliminate per-batch allocation overhead. This is the backend foundation for the upcoming Migration Explorer feature; no frontend visualization ships in this release (#3105, #3111).

## Security

- **Container entrypoint command injection** - replaced unsafe `eval echo` with safe parameter expansion in both Docker and Podman entrypoints to prevent command injection via crafted model paths. Added `.onnx` and `.csv` to model file permission setup. Brought Podman entrypoint to feature parity with Docker: rootless mode detection, pre-flight checks, gosu privilege dropping (#3159).
- **Install script sed injection** - added `sed_escape_replacement()` helper that escapes `\`, `|`, and `&` before interpolation into sed commands, preventing injection via crafted RTSP URLs, coordinates, or password hashes. Added input validation for lat/lon, audio format, locale, and port range. Updated config generation to target the new multi-source format (#3160).

## Bug Fixes

### Audio Pipeline
- **Panic recovery in audio dispatch and shutdown** - drain and stop routes now wrap Close() calls in individual recover blocks, preventing a misbehaving consumer from crashing the caller goroutine. Added serialization mutex for restart, reconfigure, and watchdog escalation to prevent conflicting router states (#3110).
- **Quiet hours not enforcing for RTSP streams** - the scheduler was never wired to the stream manager (missing `SetStreamManager` call), and used raw URLs instead of the runtime-generated hashed source IDs (`rtsp_<hex>`). Streams were silently never stopped or started (#3127 by @iamrans0m00).
- **Default audio channels not applied consistently** - some stream start paths skipped the defaultChannels fallback, causing channel count mismatches (#3143).
- **Default bit depth fallback missing** - added defaultBitDepth fallback and fixed a buffer leak in ReconfigureSource (#3146).
- **Model assignment changes not detected during hot-reload** - the analysis engine now detects when model-to-source assignments change and logs the new mapping (#3183).

### Alerting
- **Notifications always sent to both bell and push** - the alert dispatcher ignored the action's target field and dispatched to all channels. Bell-only rules now skip push, push-only rules skip persistent storage, and per-target deduplication prevents duplicate dispatches (#3136).

### Thread Safety & Resource Leaks
- **Logger module not registered for classifier** - orchestrator and nighttime scheduler logs were silently lost because the "birdnet" module was not in logger defaults. Also fixes unbounded overrun tracker map growth when sources are removed (#3104).
- **Model reload panic could leak mutex** - wrapped entry.mu in closure with defer. Fixed uninstall ordering so config updates before range filter reload (#3114).
- **Float32Pool leak on inference panic** - deferred pool return so the buffer is reclaimed even if inference panics (#3115).
- **Context.Canceled Sentry noise** - filtered context cancellation errors and non-finite duration values from Sentry reporting (#3128).
- **Controller.engine not thread-safe** - converted to atomic.Pointer to eliminate data races (#3141).
- **Race in handleSettingsChanges goroutine** - eliminated concurrent access to shared state during settings change handling (#3145).
- **controlChan panic on shutdown** - guarded channel sends to prevent send-on-closed-channel panics during graceful shutdown (#3149).
- **TOCTOU races in API handlers** - snapshot Processor and BirdImageCache at request entry to prevent mid-request pointer swaps. Deduplicated database stats queries with consistent locking (#3154, #3156).

### Dashboard & Frontend
- **CSRF token blocking logout** - added `/api/v2/auth/logout` to CSRF skip list so logout works when the token expires on long-lived pages like live stream (#3117).
- **Notification "Illegal constructor" error** - use ServiceWorkerRegistration.showNotification() when a Service Worker is active, falling back to the Notification constructor otherwise (#3117).
- **Clipboard error in popup terminal** - added catch for unhandled NotAllowedError when the document loses focus (#3117).
- **Empty statusText in dashboard errors** - added fallback for browsers that return empty status text (#3125).
- **Fullscreen crash on Safari** - added typeof guard with webkitRequestFullscreen fallback (#3129).
- **Pre-renderer shutdown race** - removed channel close that could panic on concurrent Submit() (#3129).
- **Hemisphere detection at equator** - replaced `latitude != 0` proxy with the existing LocationConfigured boolean flag so equator locations work correctly (#3129).
- **crypto.randomUUID on non-HTTPS** - centralized with try/catch fallback for insecure contexts (#3152).
- **StatusPill readability** - improved contrast and sizing for better readability across themes (#3182).

### Query & Sorting
- **Hourly and species queries ignoring sort order** - routed these query types through advanced search so user-selected sort columns take effect (#3139).
- **Hourly default sort and cross-type filters dropped** - made query routing aware of hourly-specific defaults and filters that span query types (#3142).
- **Silent filter dropping in unified query path** - consolidated all query routing into a single path to prevent conditions where filters were silently ignored (#3147).

### Database
- **FLOOR() incompatible with SQLite** - replaced with CAST(AS INTEGER) for cross-database compatibility (#3107).

### Classifier
- **Rarity scores showing "Very Rare 0%" for non-English locales** - species comparison used full labels with locale-specific common names, which never matched geomodel labels that use English names. Fixed all three comparison points to match on scientific name only (#3170).

## Internationalization

- **Czech locale added** (#3124 by @TeTeHacko).

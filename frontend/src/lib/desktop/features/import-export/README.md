# import-export feature

Provides the Import/Export page under the System menu. The page shows a
source list (BirdNET-Pi import plus planned sources) and an import activity
card that reflects the current or last import job, including imports left
running in the background after the wizard is closed.

## Import activity card

`ImportActivityCard.svelte` polls `GET /api/v2/import/status` on mount and
whenever its `refreshSignal` prop changes (the page bumps it when the wizard
closes). While a job is running it subscribes to the same SSE progress
stream as the wizard; duplicate read-only streams are intentional so the
card keeps updating behind the open wizard modal.

## BirdNET-Pi import wizard

Multi-step wizard that imports detections from a BirdNET-Pi SQLite database
into BirdNET-Go.

### Endpoints used

- `GET /api/v2/system/external-media` - source discovery
- `POST /api/v2/import/birdnet-pi` - start import
- `GET /api/v2/import/jobs/:jobId/progress` - SSE progress stream
- `POST /api/v2/import/jobs/:jobId/cancel` - cancel running import
- `GET /api/v2/import/status` - check if import is already running

### Deferred follow-ups

- Native free-path import (currently shows informational panel)
- birds.db upload (file upload from browser)
- Pre-commit count/preview endpoint
- Real non-English translations (English fallback used for now)

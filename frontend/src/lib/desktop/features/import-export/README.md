# import-export feature

Provides the Import/Export page under the System menu.

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

# Dashboard API Endpoints

## Daily Species Summary

**Endpoint:** `GET /api/v2/analytics/species/daily`

**Query Parameters:**

- `date` (required): Date in YYYY-MM-DD format
- `min_confidence` (optional): Minimum confidence percentage (0-100), default: 0
- `limit` (optional): Maximum number of species to return

**Response:**

```json
[
  {
    "scientific_name": "Crex crex",
    "common_name": "ruisrääkkä",
    "species_code": "corcra",
    "count": 19,
    "hourly_counts": [0, 0, 0, 0, 0, 0, 0, 0, 0, 19, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
    "high_confidence": true,
    "first_heard": "09:09:54",
    "latest_heard": "09:09:54",
    "thumbnail_url": "https://upload.wikimedia.org/..."
  }
]
```

**Usage in Svelte:**

```typescript
const response = await fetch(`/api/v2/analytics/species/daily?date=${selectedDate}`);
const dailySummary = await response.json();
```

## Recent Detections

**Endpoint:** `GET /api/v2/detections/recent`

**Query Parameters:**

- `limit` (optional): Number of recent detections to return, default: 10

**Response:**

```json
[
  {
    "id": 15573,
    "date": "2025-07-13",
    "time": "09:09:54",
    "source": "rtsp://localhost:8554/mystream",
    "beginTime": "2025-07-13T09:09:49+03:00",
    "endTime": "2025-07-13T09:10:06+03:00",
    "speciesCode": "corcra",
    "scientificName": "Crex crex",
    "commonName": "ruisrääkkä",
    "confidence": 1,
    "verified": "unverified",
    "locked": false,
    "comments": []
  }
]
```

**Usage in Svelte:**

```typescript
const response = await fetch('/api/v2/detections/recent?limit=10');
const recentDetections = await response.json();
```

## Additional Endpoints

### Get Single Detection

**Endpoint:** `GET /api/v2/detections/:id`

### Get All Detections (Paginated)

**Endpoint:** `GET /api/v2/detections`

**Query Parameters:**

- `queryType`: "hourly", "species", "search", or "all"
- `date`: Date filter (YYYY-MM-DD)
- `hour`: Hour filter (0-23)
- `species`: Species common name filter
- `search`: Search query
- `numResults`: Results per page (default: 100, max: 1000)
- `offset`: Pagination offset

**Response:**

```json
{
  "data": [...],
  "total": 1234,
  "limit": 100,
  "offset": 0,
  "current_page": 1,
  "total_pages": 13
}
```

### Species Summary (with date range)

**Endpoint:** `GET /api/v2/analytics/species/summary`

**Query Parameters:**

- `start_date`: Start date (YYYY-MM-DD)
- `end_date`: End date (YYYY-MM-DD)
- `min_confidence`: Minimum confidence percentage (0-100)
- `limit`: Maximum number of species

### New Species Detections

**Endpoint:** `GET /api/v2/analytics/species/detections/new`

**Query Parameters:**

- `start_date`: Start date for comparison
- `end_date`: End date for comparison
- `baseline_days`: Number of days to use as baseline (default: 30)

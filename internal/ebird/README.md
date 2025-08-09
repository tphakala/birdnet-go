# eBird API Integration

This package provides integration with the eBird API v2 for retrieving bird taxonomy and species information.

## Configuration

To enable eBird integration, add the following to your BirdNET-Go configuration:

```yaml
realtime:
  ebird:
    enabled: true
    apikey: "your-ebird-api-key"
    cachettl: 24 # Cache duration in hours (default: 24)
    locale: "en" # Language locale (default: "en")
```

## Getting an eBird API Key

1. Create an eBird account at https://ebird.org
2. Visit https://ebird.org/api/keygen to request an API key
3. Add the API key to your configuration

## Features

- **Taxonomy Data**: Retrieves complete taxonomic hierarchy for bird species
- **Family Tree**: Builds hierarchical classification (Kingdom → Phylum → Class → Order → Family → Genus → Species)
- **Subspecies**: Identifies subspecies and forms when available
- **Caching**: Built-in memory cache to minimize API requests
- **Rate Limiting**: Automatic rate limiting to respect API limits (10 requests/second max)

## API Endpoints

The eBird integration enhances the following endpoints:

### GET /api/v2/species

When eBird is enabled, the species endpoint includes taxonomy information:

```json
{
  "scientific_name": "Turdus migratorius",
  "common_name": "American Robin",
  "rarity": {
    "status": "very_common",
    "score": 0.95,
    "location_based": true,
    "latitude": 45.5152,
    "longitude": -122.6784,
    "date": "2024-01-27",
    "threshold_applied": 0.01
  },
  "taxonomy": {
    "kingdom": "Animalia",
    "phylum": "Chordata",
    "class": "Aves",
    "order": "Passeriformes",
    "family": "Turdidae",
    "family_common": "Thrushes",
    "genus": "Turdus",
    "species": "Turdus migratorius",
    "species_common": "American Robin",
    "subspecies": ["Turdus migratorius migratorius"],
    "updated_at": "2024-01-27T10:30:00Z"
  }
}
```

## Cache Management

The eBird client caches API responses to improve performance and reduce API usage:

- Default cache TTL: 24 hours (configurable)
- Cache is automatically cleaned up
- Taxonomy data rarely changes, so longer cache times are recommended

## Error Handling

The integration is designed to fail gracefully:

- If eBird API is unavailable, endpoints continue to work without taxonomy data
- Failed requests are logged but don't break the main functionality
- Rate limit errors are handled automatically with retry logic

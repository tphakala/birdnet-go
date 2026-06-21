# Species Guide

The **Species Guide** enriches detected species with a plain-language description,
taxonomy (genus/family), a similar-species comparison panel, and your own
free-form notes. Descriptions come from Wikipedia; taxonomy enrichment can
optionally come from eBird.

The feature is **disabled by default** and contacts no external service until you
enable it. When enabled, it sends only the **scientific name** of a detected
species and a language code to the configured providers — no coordinates, audio,
or personal data. See [Privacy & Data Collection](telemetry-privacy.md) for the
full disclosure.

## Quick start

Add the following to the `realtime.dashboard` section of your `config.yaml` (or
toggle it from **Settings → User Interface** in the web UI):

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true # turn the feature on (default: false)
```

That is the only required setting. With just this, guides are fetched from
Wikipedia in your dashboard language, cached locally, and shown on the species
detail and detection detail views.

Settings take effect immediately — **no restart is required** (hot-reload).

## Configuration reference

All keys live under `realtime.dashboard.speciesguide`:

| Key                  | Type   | Default     | Description                                                                                            |
| -------------------- | ------ | ----------- | ------------------------------------------------------------------------------------------------------ |
| `enabled`            | bool   | `false`     | Master switch for the whole feature.                                                                   |
| `provider`           | string | `wikipedia` | `wikipedia` (descriptions only) or `auto` (Wikipedia descriptions + eBird taxonomy when a key is set). |
| `fallbackpolicy`     | string | `all`       | `all` merges every available provider to fill gaps; `none` uses only the primary provider.             |
| `prefetchenabled`    | bool   | `true`      | Pre-fetch a guide in the background the first time a new species is detected.                          |
| `warmtopn`           | int    | `50`        | On startup, warm the cache for your top-N most-detected species (`0` disables warming).                |
| `shownotes`          | bool   | `true`      | Show the per-species notes section.                                                                    |
| `showenrichments`    | bool   | `true`      | Show enrichment badges (expectedness, current season, external links).                                 |
| `showsimilarspecies` | bool   | `true`      | Show the similar-species comparison panel.                                                             |

> The three `show*` flags default to **on** when omitted. Set them to `false`
> only to hide a section you don't want.

### Example — full configuration

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      provider: auto # use eBird taxonomy in addition to Wikipedia
      fallbackpolicy: all
      prefetchenabled: true
      warmtopn: 50
      shownotes: true
      showenrichments: true
      showsimilarspecies: true
```

## Enabling eBird taxonomy enrichment (optional)

Wikipedia alone provides descriptions. To also fill in genus/family taxonomy and
localized common names from eBird, you need a **free eBird API key** and must set
`provider: auto`.

1. Request a key at <https://ebird.org/api/keygen> (requires a free eBird account).
2. Configure the eBird integration (separate from the species guide block):

   ```yaml
   realtime:
     ebird:
       enabled: true
       apikey: "YOUR_EBIRD_API_KEY"
       locale: en # locale for eBird common names
     dashboard:
       speciesguide:
         enabled: true
         provider: auto # eBird is only consulted in "auto" mode
   ```

If `provider` is `auto` but no eBird key is configured, the guide silently falls
back to Wikipedia-only — eBird enrichment is simply skipped, not an error.

## Notes

When `shownotes` is on, you can keep free-form notes per species (e.g. field
marks, local sightings). **Reading** notes is public; **creating, editing, and
deleting** notes requires authentication, so configure
[authentication](cloudflare_tunnel_guide.md#enabling-authentication) if your
instance is exposed.

## Data sources & licensing

- **Wikipedia** — article text is licensed **CC BY-SA 4.0**; the guide displays
  the source link and license alongside each description.
- **eBird** — taxonomy data, used only when you configure an API key.

Guides are cached in your local database so repeat views and similar-species
lookups don't re-contact the providers. The cache ages out automatically
(short-lived for "not found" results, longer for real guides), so it does not
grow without bound.

## Troubleshooting

| Symptom                                | Likely cause / fix                                                                                                   |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Guide section never appears            | `enabled` is still `false`, or the species detail panel is showing a species with no Wikipedia article.              |
| "No guide found for species"           | Wikipedia has no matching article for that scientific name; this is expected for some species and is cached briefly. |
| Taxonomy (genus/family) missing        | You are in `wikipedia` mode, or `auto` mode without a valid eBird API key.                                           |
| Notes can be read but not added/edited | Note writes require authentication — sign in (or enable auth) first.                                                 |
| "Too many requests" on the guide panel | The guide endpoints are rate-limited; wait a moment. Behind a reverse proxy, configure trusted-proxy/IP extraction.  |

## Privacy summary

When enabled, the Species Guide makes outbound HTTPS requests to Wikipedia (and
eBird, if configured) containing only a scientific name and a language code. No
coordinates, audio, or personal data are transmitted. The feature is opt-in and
sends nothing while `enabled: false`.

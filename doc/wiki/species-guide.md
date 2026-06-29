# Species Guide

The **Species Guide** enriches detected species with taxonomy (genus/family), a
localized common name, external reference links, a similar-species comparison
panel, and your own free-form notes — plus an optional plain-language description.

Taxonomy, common names, and external links come from the **OpenFauna** dataset
that ships **embedded in the binary**, so the guide works **fully offline** with
no API key. The one piece that requires the internet — the **Wikipedia article
description** — is **opt-in** and off by default.

The feature is **disabled by default**. While disabled, or while enabled without
Wikipedia descriptions, it contacts **no external service**. When you opt into
Wikipedia descriptions, it sends only the **scientific name** of a detected
species and a language code to Wikipedia — no coordinates, audio, or personal
data. See [Privacy & Data Collection](telemetry-privacy.md) for the full
disclosure.

## Quick start

Add the following to the `realtime.dashboard` section of your `config.yaml` (or
toggle it from **Settings → User Interface** in the web UI):

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true # turn the feature on (default: false)
```

That is the only required setting. With just this, guides are built **offline**
from the embedded OpenFauna dataset (taxonomy, localized common names, and
external links such as Wikipedia and iNaturalist), cached locally, and shown on
the species detail and detection detail views. Links resolve to your dashboard
language.

Settings take effect immediately — **no restart is required** (hot-reload).

## Configuration reference

All keys live under `realtime.dashboard.speciesguide`:

| Key                        | Type | Default | Description                                                                                                                                                                                               |
| -------------------------- | ---- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enabled`                  | bool | `false` | Master switch for the whole feature.                                                                                                                                                                      |
| `enablewikipedia`          | bool | `false` | Also fetch article **descriptions** from Wikipedia (online). Off by default; everything else stays offline.                                                                                               |
| `enablesupplementarylinks` | bool | `false` | Add **supplementary links** — Xeno-canto recordings plus a computed Wikipedia link for species missing from the offline dataset. Off by default; computed at render time with **no** background fetching. |
| `prefetchenabled`          | bool | `true`  | Pre-fetch a guide in the background the first time a new species is detected.                                                                                                                             |
| `warmtopn`                 | int  | `50`    | On startup, warm the cache for your top-N most-detected species (`0` disables warming).                                                                                                                   |
| `shownotes`                | bool | `true`  | Show the per-species notes section.                                                                                                                                                                       |
| `showenrichments`          | bool | `true`  | Show enrichment badges (expectedness, current season, external links).                                                                                                                                    |
| `showsimilarspecies`       | bool | `true`  | Show the similar-species comparison panel.                                                                                                                                                                |

> The three `show*` flags default to **on** when omitted. Set them to `false`
> only to hide a section you don't want.

### Example — full configuration

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      enablewikipedia: true # opt in to online Wikipedia descriptions (default: false)
      enablesupplementarylinks: false # opt in to Xeno-canto + Wikipedia gap-fill links (default: false)
      prefetchenabled: true
      warmtopn: 50
      shownotes: true
      showenrichments: true
      showsimilarspecies: true
```

## Enabling Wikipedia descriptions (optional)

OpenFauna does not carry article prose, only a link to the Wikipedia article. To
show the full description text **inside** the guide, opt in:

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      enablewikipedia: true
```

With this on, the guide fetches the article for the detected species from the
Wikipedia edition matching your dashboard language, caches it locally, and shows
it with its source link and license. With it off, the guide still shows the
Wikipedia **link** (from OpenFauna) — it just doesn't fetch the article text.

## Supplementary links (optional)

By default the guide shows only the external links that exist for a species in
the embedded OpenFauna dataset (e.g. Wikipedia, iNaturalist, GBIF). Turn on
`enablesupplementarylinks` to additionally show:

- a **Xeno-canto** link to community recordings of the species, and
- a computed **Wikipedia** link for species that are _not_ in the offline
  dataset (a gap-fill, so even uncovered species get at least one reference).

```yaml
realtime:
  dashboard:
    speciesguide:
      enabled: true
      enablesupplementarylinks: true
```

These links are **computed at render time** from the scientific name and your
dashboard language — they are plain outbound links you click, so enabling them
performs **no** background network requests.

## Notes

When `shownotes` is on, you can keep free-form notes per species (e.g. field
marks, local sightings). **Reading** notes is public; **creating, editing, and
deleting** notes requires authentication, so configure
[authentication](cloudflare_tunnel_guide.md#enabling-authentication) if your
instance is exposed.

## Data sources & licensing

- **OpenFauna** (embedded, offline) — taxonomy, localized common names, and the
  external reference links (Wikipedia, iNaturalist, GBIF). Compiled from the GBIF
  backbone taxonomy, Wikipedia, and the iNaturalist Open Data taxonomy.
- **Wikipedia** (online, only when `enablewikipedia` is on) — article
  descriptions, licensed **CC BY-SA 4.0**; the guide displays the source link
  and license alongside each description.
- **Xeno-canto** and a computed **Wikipedia** gap-fill link (only when
  `enablesupplementarylinks` is on) — outbound links resolved at render time; no
  data is fetched in the background.

Guides are cached in your local database so repeat views and similar-species
lookups don't re-contact Wikipedia. The cache ages out automatically
(short-lived for "not found" results, longer for real guides), so it does not
grow without bound.

## Troubleshooting

| Symptom                                  | Likely cause / fix                                                                                                  |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| Guide section never appears              | `enabled` is still `false`.                                                                                         |
| No description text, only taxonomy/links | `enablewikipedia` is `false` (offline mode), or the species has no Wikipedia article in your dashboard language.    |
| Taxonomy (genus/family) missing          | The species is not present in the embedded OpenFauna dataset.                                                       |
| Notes can be read but not added/edited   | Note writes require authentication — sign in (or enable auth) first.                                                |
| "Too many requests" on the guide panel   | The guide endpoints are rate-limited; wait a moment. Behind a reverse proxy, configure trusted-proxy/IP extraction. |

## Privacy summary

Taxonomy, common names, and links are served entirely from embedded data and
make **no** outbound requests. Only when `enablewikipedia` is on does the guide
make outbound HTTPS requests to Wikipedia, containing only a scientific name and
a language code — no coordinates, audio, or personal data. The feature is opt-in
and sends nothing while `enabled: false` (or while `enablewikipedia: false`).

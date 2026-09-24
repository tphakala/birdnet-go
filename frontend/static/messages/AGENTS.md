# i18n Translation Files

Translation files for the BirdNET-Go frontend. **Read `README.md` in this
directory before changing anything**: it documents the key structure and the
shared namespaces that prevent duplicate strings.

- `en.json`: English, the source of truth. Always change it first.
- 15 more locales: `cs`, `da`, `de`, `es`, `fi`, `fr`, `hu`, `it`, `lv`, `nb`,
  `nl`, `pl`, `pt`, `sk`, `sv`.

## Every Change Updates Every Locale

1. Add or change the key in `en.json`
2. From `frontend/`, run `npm run i18n:sync`. It propagates the key structure
   to every locale, filling missing keys with the English value.
3. Translate those English fallbacks in each locale file
4. Run `npm run generate:i18n-types` and commit the regenerated
   `src/lib/i18n/types.generated.ts`

The pre-commit hook runs `npm run i18n:sync:check` and
`npm run generate:i18n-types:check`, and CI enforces both. A commit with
out-of-sync locales or stale generated types fails.

## Key Principles

- **Reuse before adding.** Many common strings already exist; use the
  `common.*` namespace for reusable UI text instead of duplicating it.
- **Follow existing naming**: dot-separated, camelCase segments, grouped by
  feature.
- **Separate keys for separate meanings.** When one English word has different
  meanings in different places, give each its own key.
- **Parameters** use `{name}` placeholders; keep them identical across locales.
- **Accessibility strings** (`aria-label` text) are translated like any other.

## Software Terminology

Translate in the context of a software application for identifying bird
sounds, never the everyday or physical meaning of a word:

| Term      | Correct context                          | Wrong context            |
| --------- | ---------------------------------------- | ------------------------ |
| Dashboard | Application dashboard (main UI overview) | Car dashboard            |
| Settings  | Application settings/preferences         | Physical device settings |
| Log       | Event log, logging output                | Wooden log               |
| Stream    | Audio/video stream, data stream          | River stream             |
| Filter    | Data filter, search filter               | Coffee filter            |
| Cache     | Data cache, browser cache                | Hidden storage           |
| Terminal  | Command-line terminal                    | Airport terminal         |
| Port      | Network port                             | Harbor port              |

Brand and product names (BirdNET, BirdWeather, eBird, Pirate Weather, MQTT, and
similar) are not translated.

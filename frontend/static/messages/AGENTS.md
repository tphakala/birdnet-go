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

What enforces this:

- **Pre-commit hook**: runs `npm run i18n:sync:check` when any locale file is
  staged, and `npm run generate:i18n-types:check` when `en.json` or the type
  generator changes.
- **CI**: checks the generated types, fails on missing keys and on newly added
  English fallbacks that were never translated (`--fail-on-untranslated`), and
  fails when code uses a key that `en.json` does not define. It reports
  orphaned keys but does not fail on them.

Run `npm run i18n:validate:full` before pushing; it runs the same checks
locally, including the orphaned-key check.

## Key Principles

- **Reuse before adding.** Many common strings already exist; use the
  `common.*` namespace for reusable UI text instead of duplicating it.
- **Follow existing naming**: dot-separated, camelCase segments, grouped by
  feature (`settings.audio.soundCards.gainLabel`). A segment that mirrors a
  backend identifier (an alert event, metric or operator, a status value, a
  check ID) keeps that identifier's spelling, even snake_case, except that dots
  in the identifier become underscores (see `toKeySegment` in
  `src/lib/utils/alertSchema.ts`). Do not "fix" those keys.
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

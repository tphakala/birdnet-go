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

To rename a key, move its translated value in each locale file first:
`i18n:sync` deletes every key that is not in `en.json` and fills the new key
with the English value.

Changing the English text of an existing key is different: `i18n:sync` leaves
the other locales alone, and unless the parameters (`{name}`) change, no check
notices that their translations are now stale. Update that key's translation in
every locale yourself. If the parameters do change, CI reports a parameter
mismatch in every locale until they are updated, and the types must be
regenerated.

What enforces this:

- **Pre-commit hook**: runs `npm run i18n:sync:check` when any locale file is
  staged (it also fails on orphaned keys), and
  `npm run generate:i18n-types:check` when `en.json`, the type generator, its
  ICU helper (`icuMessage.ts`) or `types.generated.ts` changes.
- **CI**: checks the generated types, fails on missing keys and on newly added
  English fallbacks that were never translated (`--fail-on-untranslated`), and
  fails when code uses a key that `en.json` does not define. It also fails on
  parameter (`{name}`) mismatches, empty values and invalid ICU syntax in every
  translated locale. Values are read the way `t()` reads them at runtime: HTML
  tags and apostrophes are plain text (so write `'{name}'`, never ICU's
  `''{name}''`, which renders both apostrophes), parameters inside tags are
  compared too, and Go template field references (`{{.Name}}`) count as plain
  words. ICU arguments `t()` cannot render (`select`, `selectordinal`,
  `number`, `date`, `time`) fail validation; use `{name}` and `plural` only.
  Tag structure (unclosed or mismatched tags) is not checked (see
  `src/lib/i18n/icuMessage.ts`). `en.json` itself is not
  checked, so review the English text yourself (ICU syntax and empty values).
  CI reports orphaned keys but does not fail on them.

Before pushing, run `npm run i18n:validate:ci` (the translation validator with
CI's exact flags) and `npm run i18n:validate:full` (sync, types, usage and
untranslated checks, including orphaned keys).

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

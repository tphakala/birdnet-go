# i18n Translation Validation Tools

Automated validation tools for BirdNET-Go's i18n translation files.

## Overview

Two complementary validators ensure translation quality:

1. **Translation File Validator** - Validates translation files for completeness and correctness
2. **Usage Validator** - Ensures all translation keys used in code actually exist

## Quick Start

```bash
# Validate all translation files
npm run i18n:validate

# Check if keys used in code exist in translations
npm run i18n:check-usage

# Translation validator with CI's exact flags
npm run i18n:validate:ci

# Sync, generated types, usage and untranslated checks
npm run i18n:validate:full

# Find unused translation keys
npm run i18n:find-unused
```

## Translation File Validator

### Features

✅ **Completeness Checks**

- Missing keys (compared to en.json)
- Extra/outdated keys
- Empty string values
- Untranslated strings (identical to English)

✅ **Correctness Checks**

- Valid JSON syntax
- ICU MessageFormat syntax validation
- Parameter consistency ({param} names match)
- Plural forms validation

### Usage

```bash
# Basic validation
npm run i18n:validate

# Strict mode (shows all details)
npm run i18n:validate:strict

# CI mode (CI's exact flags: fails below 100% coverage, on warnings,
# and on newly added untranslated English fallbacks)
npm run i18n:validate:ci

# Generate JSON report for LLMs
npm run i18n:validate:json

# Generate Markdown report
npm run i18n:report:md
```

### Example Output

```text
🌍 Validating translation files...

📚 Reference (en.json): 1193 keys

╔══════════════════════════════════════════════════════════╗
║         Translation Validation Results                  ║
╚══════════════════════════════════════════════════════════╝

❌ DE: 1094 keys (91.70% coverage)
  ⚠️  Missing: 104 keys
  ❌ Invalid ICU syntax: 2
  ❌ Parameter mismatches: 1

✅ FR: 1101 keys (92.29% coverage)
  ⚠️  Missing: 101 keys
```

### JSON Output for LLMs

Use `--json` flag for structured output that LLMs can parse:

```bash
npm run i18n:validate:json
```

```json
{
  "success": false,
  "timestamp": "2025-10-28T11:33:41.136Z",
  "summary": {
    "totalLocales": 5,
    "referenceKeyCount": 1193,
    "passedLocales": 0,
    "totalErrors": 22,
    "totalWarnings": 943
  },
  "errors": [
    {
      "type": "invalid_icu",
      "locale": "de",
      "key": "settings.notifications.templates.titlePlaceholder",
      "error": "MALFORMED_ARGUMENT",
      "severity": "error",
      "message": "Invalid ICU MessageFormat syntax",
      "file": "static/messages/de.json",
      "fixable": true,
      "suggestedFix": "Fix ICU syntax error: MALFORMED_ARGUMENT"
    }
  ],
  "warnings": [...],
  "locales": [...]
}
```

## Usage Validator

### Features

✅ **Usage Validation**

- Scans all `.svelte` and `.ts` files for `t('key')` calls
- Finds missing translations (keys used but not in en.json)
- Finds unused translations (keys in en.json never used in code)
- Tracks usage locations with file:line references

### Usage

```bash
# Check for missing translations
npm run i18n:check-usage

# Find unused translation keys (dead code)
npm run i18n:find-unused
```

### Example Output

```text
🔍 Scanning codebase for translation key usage...

📚 Loaded 1193 keys from en.json
   Found 884 unique keys in 50 files

╔══════════════════════════════════════════════════════════╗
║         Translation Usage Validation                    ║
╚══════════════════════════════════════════════════════════╝

📊 Statistics:
   Unique translation keys used: 884
   Total t() calls: 995
   Files scanned: 50
   Translation keys defined: 1193

❌ Missing Translations (22 keys)
   Keys used in code but not found in en.json:

   • common.actions.download
     └─ src/lib/desktop/views/DetectionDetail.svelte:446

   • common.buttons.clear
     └─ src/lib/desktop/components/forms/DateRangePicker.svelte:285
```

## npm Scripts

| Script                 | Description                                                                             |
| ---------------------- | --------------------------------------------------------------------------------------- |
| `i18n:validate`        | Basic translation file validation                                                       |
| `i18n:validate:strict` | Detailed validation output                                                              |
| `i18n:validate:ci`     | CI mode (min 100% coverage, fail on warnings, fail on new untranslated values)          |
| `i18n:validate:json`   | JSON output for LLM parsing                                                             |
| `i18n:report`          | Generate JSON report                                                                    |
| `i18n:report:md`       | Generate Markdown report                                                                |
| `i18n:check-usage`     | Find missing translations for used keys                                                 |
| `i18n:find-unused`     | Find unused translation keys                                                            |
| `i18n:validate:full`   | Sync, generated types, usage and untranslated checks (not the CI translation validator) |

## GitHub Actions Integration

Validation runs automatically on:

- Pull requests touching translation files or Svelte/TS files
- Pushes to main branch touching translation files

### What It Does

1. **Validates translation files** for completeness and correctness
2. **Checks usage** to ensure all keys used in code exist
3. **Generates reports** in GitHub Actions summary
4. **Comments on PRs** with validation results
5. **Fails the build** if critical errors found

### Example PR Comment

```markdown
## 🌍 i18n Validation Results

### Summary

- **Status:** ❌ Failed
- **Total Errors:** 22
- **Total Warnings:** 943
- **Passed Locales:** 0/5

### ❌ Errors (22)

#### INVALID_ICU

- **de**: `settings.notifications.templates.titlePlaceholder`
  - Invalid ICU MessageFormat syntax
  - 💡 Suggested fix: Fix ICU syntax error: MALFORMED_ARGUMENT

📊 [View detailed report](...)
```

## Validation Rules

### Translation Keys

Translation keys must:

- Be in dot notation: `common.buttons.save`
- Exist in English (`en.json`) first
- Have matching parameter names across all languages
- Use valid ICU MessageFormat syntax for plurals

### ICU MessageFormat Examples

```json
{
  "simple": "Hello World",
  "withParam": "Hello {name}",
  "plural": "{count, plural, =0 {No items} one {# item} other {# items}}",
  "select": "{gender, select, male {He} female {She} other {They}}"
}
```

### Common Errors

#### Empty Value

```json
{
  "common.save": "" // ❌ Error: empty value
}
```

#### Parameter Mismatch

```json
// en.json
{
  "greeting": "Hello {name}"
}

// de.json
{
  "greeting": "Hallo {username}"  // ❌ Error: parameter mismatch
}
```

#### Invalid ICU Syntax

```json
{
  "count": "{count, plural, {No items}" // ❌ Error: malformed ICU syntax
}
```

#### Missing Translation

```typescript
// Component.svelte
{
  t('new.feature.title');
} // ❌ Error: key not in en.json
```

## Fixing Issues

### 1. Missing Keys

Add the missing key to all translation files, starting with `en.json`:

```json
// static/messages/en.json
{
  "new.feature.title": "New Feature"
}

// static/messages/de.json
{
  "new.feature.title": "Neue Funktion"
}
```

### 2. Invalid ICU Syntax

Fix the ICU MessageFormat syntax:

```json
// ❌ Wrong
{
  "count": "{count, plural, {No items}"
}

// ✅ Correct
{
  "count": "{count, plural, =0 {No items} one {# item} other {# items}}"
}
```

### 3. Parameter Mismatches

Ensure parameter names match across all languages:

```json
// ✅ All use {name}
{
  "en": "Hello {name}",
  "de": "Hallo {name}",
  "fr": "Bonjour {name}"
}
```

### 4. Unused Keys

Remove unused keys or verify they're not used dynamically:

```bash
# Find unused keys
npm run i18n:find-unused

# Review and remove if truly unused
```

## Integration with Development Workflow

### Before Committing

Run both; neither covers the other:

```bash
# Translation validator with CI's exact flags
npm run i18n:validate:ci

# Sync, generated types, usage and untranslated checks (including orphaned keys)
npm run i18n:validate:full
```

### Adding New Translations

1. Add key to `en.json` first
2. Use in code: `{t('new.key')}`
3. Run `npm run i18n:check-usage` to verify
4. Add translations to other languages
5. Run `npm run i18n:validate` to verify all languages

### Pre-Commit Hook (Optional)

Add to `.husky/pre-commit`:

```bash
cd frontend && npm run i18n:validate:ci
```

## Configuration

### Adjust Coverage Threshold

Edit `package.json`:

```json
{
  "scripts": {
    "i18n:validate:ci": "npx tsx src/lib/i18n/validateTranslations.ts --min-coverage 100 --fail-on-warnings --strict --fail-on-untranslated"
  }
}
```

### Skip Specific Files

Edit `validateUsage.ts` to adjust file filtering:

```typescript
if (
  file.includes('.test.') || // Skip test files
  file.includes('.spec.') || // Skip spec files
  file.includes('node_modules') // Skip dependencies
) {
  continue;
}
```

## Troubleshooting

### "No translation keys found in codebase"

- Ensure you're running from the `frontend/` directory
- Check that `src/` directory exists with `.svelte` and `.ts` files

### "False positives in usage validator"

- The validator filters out test files and most false positives
- Adjust the regex in `validateUsage.ts` if needed

### "Validation takes too long"

- Validation should complete in < 1 second for translation files
- Usage scanning should complete in < 5 seconds
- Check for very large files or excessive `t()` usage

## Architecture

### Files

```
frontend/
├── src/lib/i18n/
│   ├── validateTranslations.ts    # Translation file validator
│   ├── validateUsage.ts           # Usage validator
│   └── config.ts                  # Locale configuration
├── rules/
│   └── ast-grep-i18n.yml          # ast-grep rules (future use)
└── static/messages/
    ├── en.json                     # English (reference)
    ├── de.json                     # German
    ├── fr.json                     # French
    ├── es.json                     # Spanish
    ├── fi.json                     # Finnish
    └── pt.json                     # Portuguese
```

### How It Works

1. **Translation File Validator**:
   - Loads `en.json` as reference
   - Compares each locale against reference
   - Validates ICU MessageFormat syntax
   - Checks parameter consistency
   - Reports missing/extra/invalid keys

2. **Usage Validator**:
   - Scans all `.svelte` and `.ts` files using grep
   - Extracts `t('key')` calls with regex
   - Filters out test files and false positives
   - Cross-references with `en.json`
   - Reports missing translations and unused keys

3. **GitHub Actions**:
   - Runs on PR and push events
   - Generates JSON and Markdown reports
   - Posts PR comments with results
   - Fails build if critical errors found

## Performance

Each validator finishes in a second or two, including `tsx` startup.

## Contributing

When adding new validation rules:

1. Update `validateTranslations.ts` or `validateUsage.ts`
2. Add tests if possible
3. Update this README with examples
4. Test locally before committing

## License

Same as BirdNET-Go project license.

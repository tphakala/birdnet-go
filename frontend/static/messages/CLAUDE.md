# Claude Instructions for i18n Translation Management

## Overview

This directory contains the internationalization (i18n) message files for BirdNET-Go frontend. When working with translations, please read the comprehensive documentation in `README.md` first.

## Important Instructions

1. **Read the README.md file** in this directory before making any changes to translation files
2. **Follow the established structure** documented in README.md to avoid duplication
3. **Check existing keys** before creating new ones - many common UI strings already exist
4. **Use the common namespace** for reusable UI elements across components

## Quick Reference

- Documentation: `./README.md`
- English (base): `./en.json`
- Other languages: `./[language-code].json`

## Key Principles

1. **DRY**: Don't duplicate translations - reuse from `common.*` namespace
2. **Consistency**: Follow existing naming conventions
3. **Context**: When same word has different meanings, create specific keys
4. **Parameters**: Use `{parameter}` for dynamic content
5. **Accessibility**: Include aria-label translations

## Critical Rules

### All Languages Must Be Updated

**When adding or modifying translations, ALL translation files must be updated:**

- `en.json` (English - base language)
- `de.json` (German)
- `es.json` (Spanish)
- `fi.json` (Finnish)
- `fr.json` (French)
- `nl.json` (Dutch)
- `pl.json` (Polish)
- `pt.json` (Portuguese)

Never add a key to only one language file. If you add a translation to `en.json`, you must add the corresponding translations to all other language files.

### Software Terminology Context

All translations must align with **software and application terminology**, not general or alternative meanings:

| Term      | Correct Context                          | Incorrect Context        |
| --------- | ---------------------------------------- | ------------------------ |
| Dashboard | Application dashboard (main UI overview) | Car dashboard            |
| Settings  | Application settings/preferences         | Physical device settings |
| Log       | Event log, logging output                | Wooden log               |
| Stream    | Audio/video stream, data stream          | River stream             |
| Filter    | Data filter, search filter               | Coffee filter            |
| Cache     | Data cache, browser cache                | Hidden storage           |
| Terminal  | Command-line terminal                    | Airport terminal         |
| Port      | Network port                             | Harbor port              |

When translating, always consider the software context of BirdNET-Go as a bird sound identification application.

Please ensure you understand the translation structure before making modifications.

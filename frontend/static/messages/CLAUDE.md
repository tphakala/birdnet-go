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

Please ensure you understand the translation structure before making modifications.

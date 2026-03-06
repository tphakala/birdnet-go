#!/usr/bin/env bash
# Check for bare daisyUI-style color classes in Svelte files.
# These must use explicit CSS variable syntax: bg-[var(--color-base-200)] not bg-base-200
#
# Why: Bare classes like bg-base-200 look like daisyUI utilities and confuse LLMs.
# They also don't reliably generate CSS for Tailwind variant combinations
# (before:, checked:, hover:, etc.) because our color tokens are not in @theme.

set -euo pipefail

SEARCH_DIR="${1:-src/lib/desktop}"

# Pattern matches bare daisyUI-style color utility classes.
# Uses word boundary + specific prefixes to avoid matching compound classes like btn-outline-primary.
# Excludes: bg-black, bg-white, bg-transparent, text-base (font-size), CSS var() refs
COLORS='base-100|base-200|base-300|base-content|primary|primary-content|secondary|secondary-content|accent|accent-content|error|error-content|warning|warning-content|success|success-content|info|info-content|neutral|neutral-content'

# Match only direct Tailwind utility patterns (with optional variant prefix and opacity suffix)
# e.g., bg-primary, text-error/60, hover:bg-base-200, checked:bg-primary
# But NOT: btn-outline-primary, btn-primary (compound component classes)
PATTERN="(^|[\" ])((hover|focus|active|checked|disabled|before|after|group-hover|peer-checked|focus-visible|focus-within):)?(bg|text|border|ring|outline|fill|stroke|caret|accent|divide)-($COLORS)(/[0-9]+)?([\" ]|$)"

# Exclude lines that are CSS var() references (e.g., style:color="var(--text-secondary)")
MATCHES=$(grep -rnE "$PATTERN" "$SEARCH_DIR" --include="*.svelte" | grep -v 'var(--' || true)

if [ -n "$MATCHES" ]; then
  echo "ERROR: Found bare daisyUI-style color classes. Use explicit CSS variable syntax instead."
  echo ""
  echo "Examples:"
  echo "  bg-base-200      -> bg-[var(--color-base-200)]"
  echo "  text-base-content -> text-[var(--color-base-content)]"
  echo "  text-error        -> text-[var(--color-error)]"
  echo "  border-primary    -> border-[var(--color-primary)]"
  echo ""
  echo "Found in:"
  echo "$MATCHES"
  exit 1
else
  echo "No bare daisyUI-style color classes found."
fi

# --- Check 2: Legacy daisyUI CSS variable tokens in <style> blocks and CSS files ---
# These used oklch(var(--p)) or hsl(var(--b2)) syntax from daisyUI's token system.
# All must use var(--color-*) or color-mix(in srgb, var(--color-*) N%, transparent).

LEGACY_PATTERN='oklch\(var\(--|hsl\(var\(--'
LEGACY_MATCHES=$(grep -rnE "$LEGACY_PATTERN" "$SEARCH_DIR" --include="*.svelte" --include="*.css" || true)

if [ -n "$LEGACY_MATCHES" ]; then
  echo "ERROR: Found legacy daisyUI CSS variable tokens. Use new token syntax instead."
  echo ""
  echo "Examples:"
  echo "  oklch(var(--p))          -> var(--color-primary)"
  echo "  oklch(var(--bc) / 0.7)   -> color-mix(in srgb, var(--color-base-content) 70%, transparent)"
  echo "  hsl(var(--b2))           -> var(--color-base-200)"
  echo ""
  echo "Found in:"
  echo "$LEGACY_MATCHES"
  exit 1
else
  echo "No legacy daisyUI CSS variable tokens found."
fi

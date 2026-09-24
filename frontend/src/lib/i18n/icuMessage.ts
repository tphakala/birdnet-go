/**
 * ICU message helpers shared by the i18n build scripts (validateTranslations.ts
 * and generateTypes.ts).
 *
 * The runtime translator (`t()` in store.svelte.ts) interpolates `{name}` and
 * ICU plural blocks itself. It treats HTML tags as literal text, which the UI
 * then renders as HTML, and it has no ICU apostrophe quoting: `'` is always a
 * literal character. The helpers below make the formatjs parser read a value
 * the same way before parsing it:
 *
 * - `ignoreTag: true` keeps tags literal, so a tag with attributes
 *   (`<a href="...">`, INVALID_TAG in the default mode) is accepted and a
 *   parameter anywhere in the string, including inside a tag attribute such as
 *   `<a href="{url}">`, is an ordinary argument. Tag structure (unclosed or
 *   mismatched tags) is therefore not checked.
 * - Every apostrophe is doubled, so `l'{name}` yields the parameter `name`
 *   instead of an ICU-quoted literal.
 * - Go template field references (`{{.CommonName}}`) are replaced by a plain
 *   word, so the rest of the value is still checked.
 *
 * Only `{name}` and `plural` are resolved by the runtime; select, number, date
 * and time arguments are accepted here but render unresolved.
 */

import { parse as parseICU } from '@formatjs/icu-messageformat-parser';

type ICUElements = ReturnType<typeof parseICU>;

/** Parser options that match how the runtime treats HTML tags (as literal text). */
const ICU_PARSE_OPTIONS = { ignoreTag: true } as const;

/**
 * Matches Go template field references such as `{{.CommonName}}`. Alert
 * template placeholders show these to users; they are not ICU syntax and the
 * ICU parser rejects them (MALFORMED_ARGUMENT). Requiring the leading `.`
 * keeps an ICU plural branch that is just a parameter (`other {{name}}`) from
 * matching. Other template actions (`{{if .X}}`, `{{range}}`) do not match and
 * fail validation.
 */
const GO_TEMPLATE_PATTERN = /\{\{-?\s*\.[^{}]*\}\}/g;

/** Plain word substituted for each Go template field reference before parsing. */
const GO_TEMPLATE_STAND_IN = 'GoTemplateField';

/**
 * Rewrites a translation value so the ICU parser reads it the way the runtime
 * does: Go template field references become a plain word and every apostrophe
 * becomes a literal (ICU `''`).
 */
function toRuntimeICU(value: string): string {
  return value.replace(GO_TEMPLATE_PATTERN, GO_TEMPLATE_STAND_IN).replaceAll("'", "''");
}

/** Fallback for strings the ICU parser rejects: simple `{name}` placeholders. */
const SIMPLE_PARAM_PATTERN = /\{(\w+)\}/g;

/** First and last ICU element types that carry a parameter name in `value`. */
const FIRST_ARGUMENT_TYPE = 1; // argument
const LAST_ARGUMENT_TYPE = 6; // plural (2-5: number, date, time, select)

/**
 * Parses a translation value as ICU MessageFormat the way the runtime reads it
 * (see toRuntimeICU) and returns the parser's error message, or null when the
 * value is valid.
 */
export function findICUSyntaxError(value: string): string | null {
  try {
    parseICU(toRuntimeICU(value), ICU_PARSE_OPTIONS);
    return null;
  } catch (error) {
    return error instanceof Error ? error.message : String(error);
  }
}

/**
 * Walks an ICU AST collecting parameter names, recursing into plural/select
 * option branches. The AST has no tag nodes, because values are parsed with
 * ignoreTag. Types 1-6 are the parameter-bearing nodes (argument, number, date, time,
 * select, plural); literal (0), pound (7) and tag (8) carry no parameter name.
 */
function collectParams(elements: ICUElements, params: Set<string>): void {
  for (const element of elements) {
    const node = element as unknown as Record<string, unknown>;

    if (
      typeof node.type === 'number' &&
      node.type >= FIRST_ARGUMENT_TYPE &&
      node.type <= LAST_ARGUMENT_TYPE &&
      typeof node.value === 'string'
    ) {
      params.add(node.value);
    }

    if (typeof node.options === 'object' && node.options !== null) {
      for (const option of Object.values(node.options as Record<string, unknown>)) {
        if (option && typeof option === 'object' && 'value' in option) {
          const optionValue = (option as Record<string, unknown>).value;
          if (Array.isArray(optionValue)) {
            collectParams(optionValue as ICUElements, params);
          }
        }
      }
    }
  }
}

/**
 * Extracts parameter names from a translation value in order of first
 * appearance, including parameters inside HTML tags and plural/select
 * branches. Falls back to simple `{name}` matching for values the ICU parser
 * rejects.
 */
export function extractICUParameters(text: string): string[] {
  const params = new Set<string>();

  try {
    collectParams(parseICU(toRuntimeICU(text), ICU_PARSE_OPTIONS), params);
  } catch {
    for (const match of text.matchAll(SIMPLE_PARAM_PATTERN)) {
      params.add(match[1]);
    }
  }

  return [...params];
}

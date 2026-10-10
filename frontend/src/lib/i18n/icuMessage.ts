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
 * Only `{name}` and cardinal `plural` are resolved by the runtime, so
 * findICUSyntaxError reports select, selectordinal, number, date and time
 * arguments and plural offsets, which would otherwise render wrong or
 * unresolved.
 */

import {
  parse as parseICU,
  isArgumentElement,
  isDateElement,
  isNumberElement,
  isPluralElement,
  isSelectElement,
  isTimeElement,
  type MessageFormatElement,
} from '@formatjs/icu-messageformat-parser';

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

/**
 * Returns the ICU argument kind the runtime t() cannot render (select,
 * selectordinal, plural with an offset, number, date, time), or null when it
 * can render the element.
 */
function unsupportedArgumentKind(element: MessageFormatElement): string | null {
  if (isSelectElement(element)) return 'select';
  if (isPluralElement(element) && element.pluralType === 'ordinal') return 'selectordinal';
  if (isPluralElement(element) && element.offset !== 0) return 'plural offset';
  if (isNumberElement(element)) return 'number';
  if (isDateElement(element)) return 'date';
  if (isTimeElement(element)) return 'time';
  return null;
}

/** Finds the first argument in an ICU AST that the runtime cannot render. */
function findUnsupportedArgument(elements: MessageFormatElement[]): string | null {
  for (const element of elements) {
    const kind = unsupportedArgumentKind(element);
    if (kind !== null && 'value' in element) {
      return `unsupported ICU ${kind} argument {${element.value}}: t() resolves only {name} and plural`;
    }
    if (isPluralElement(element) || isSelectElement(element)) {
      for (const option of Object.values(element.options)) {
        const nested = findUnsupportedArgument(option.value);
        if (nested !== null) return nested;
      }
    }
  }
  return null;
}

/**
 * Parses a translation value as ICU MessageFormat the way the runtime reads it
 * (see toRuntimeICU) and returns the parser's error message, a message naming
 * an argument the runtime cannot render, or null when the value is valid.
 */
export function findICUSyntaxError(value: string): string | null {
  try {
    return findUnsupportedArgument(parseICU(toRuntimeICU(value), ICU_PARSE_OPTIONS));
  } catch (error) {
    return error instanceof Error ? error.message : String(error);
  }
}

/**
 * Walks an ICU AST collecting parameter names, recursing into plural/select
 * option branches. The AST has no tag nodes, because values are parsed with
 * ignoreTag. Argument, number, date, time, select and plural elements name a
 * parameter; literal and pound elements do not.
 */
function collectParams(elements: MessageFormatElement[], params: Set<string>): void {
  for (const element of elements) {
    if (
      isArgumentElement(element) ||
      isNumberElement(element) ||
      isDateElement(element) ||
      isTimeElement(element) ||
      isSelectElement(element) ||
      isPluralElement(element)
    ) {
      params.add(element.value);
    }

    if (isPluralElement(element) || isSelectElement(element)) {
      for (const option of Object.values(element.options)) {
        collectParams(option.value, params);
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

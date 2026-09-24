/**
 * ICU message helpers shared by the i18n build scripts (validateTranslations.ts
 * and generateTypes.ts).
 *
 * The runtime translator (`t()` in store.svelte.ts) interpolates `{name}` and
 * ICU plural blocks itself and treats HTML tags as literal text, which the UI
 * then renders as HTML. The formatjs parser, by default, reads tags as ICU rich
 * text elements: it rejects any tag with attributes (`<a href="...">` is
 * INVALID_TAG) and hides parameters nested inside a tag's children. Parsing
 * with `ignoreTag: true` matches the runtime: tags stay literal, so a
 * parameter anywhere in the string, including inside a tag attribute such as
 * `<a href="{url}">`, is an ordinary argument.
 */

import { parse as parseICU } from '@formatjs/icu-messageformat-parser';

type ICUElements = ReturnType<typeof parseICU>;

/** Parser options that match how the runtime treats HTML tags (as literal text). */
export const ICU_PARSE_OPTIONS = { ignoreTag: true } as const;

/**
 * Matches Go template actions such as `{{.CommonName}}`. Alert template
 * placeholders show these to users; they are not ICU syntax and the ICU
 * parser rejects them (MALFORMED_ARGUMENT). Requiring the leading `.` keeps
 * an ICU plural branch that is just a parameter (`other {{name}}`) from
 * matching.
 */
const GO_TEMPLATE_PATTERN = /\{\{-?\s*\.[^{}]*\}\}/;

/** Fallback for strings the ICU parser rejects: simple `{name}` placeholders. */
const SIMPLE_PARAM_PATTERN = /\{(\w+)\}/g;

/** First and last ICU element types that carry a parameter name in `value`. */
const FIRST_ARGUMENT_TYPE = 1; // argument
const LAST_ARGUMENT_TYPE = 6; // plural (2-5: number, date, time, select)

/**
 * Reports whether a translation value contains Go template syntax, which is
 * not ICU MessageFormat and must not be ICU-validated.
 */
export function containsGoTemplate(value: string): boolean {
  return GO_TEMPLATE_PATTERN.test(value);
}

/**
 * Parses a translation value as ICU MessageFormat with runtime-matching
 * options and returns the parser's error message, or null when the value is
 * valid. Values containing Go template syntax are skipped (null).
 */
export function findICUSyntaxError(value: string): string | null {
  if (containsGoTemplate(value)) return null;
  try {
    parseICU(value, ICU_PARSE_OPTIONS);
    return null;
  } catch (error) {
    return error instanceof Error ? error.message : String(error);
  }
}

/**
 * Walks an ICU AST collecting parameter names, recursing into plural/select
 * option branches and (when a caller parsed without ignoreTag) tag children.
 * Types 1-6 are the parameter-bearing nodes (argument, number, date, time,
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

    if (Array.isArray(node.children)) {
      collectParams(node.children as ICUElements, params);
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
 * rejects (such as Go template placeholders).
 */
export function extractICUParameters(text: string): string[] {
  const params = new Set<string>();

  try {
    collectParams(parseICU(text, ICU_PARSE_OPTIONS), params);
  } catch {
    for (const match of text.matchAll(SIMPLE_PARAM_PATTERN)) {
      params.add(match[1]);
    }
  }

  return [...params];
}

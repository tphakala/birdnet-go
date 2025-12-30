/**
 * ICU MessageFormat plural parser
 * Extracted for testability from store.svelte.ts
 */

/**
 * Parse ICU MessageFormat plural syntax in a message string
 * @param message - The message containing plural syntax
 * @param params - Parameters including the count value
 * @param locale - The locale for plural rules
 * @returns The message with plurals resolved
 */
export function parsePlural(
  message: string,
  params: Record<string, unknown>,
  locale: string
): string {
  // Fixed regex that handles two levels of nested braces
  // Pattern: (?:[^{}]|\{(?:[^{}]|\{[^{}]*\})*\})* matches:
  //   - [^{}] any character that's not a brace
  //   - OR \{...\} braces containing either non-brace chars or one more level of braces
  // This handles: {count, plural, one {text with {placeholder}} other {more {nested}}}
  const result = message.replace(
    // eslint-disable-next-line security/detect-unsafe-regex
    /\{(\w+),\s*plural,((?:[^{}]|\{(?:[^{}]|\{[^{}]*\})*\})*)\}/g,
    (match, paramName: string, pluralPattern: string) => {
      // eslint-disable-next-line security/detect-object-injection
      const count = params[paramName];
      if (typeof count !== 'number') return match;

      // Parse plural rules with nested brace support (two levels)
      // Use matchAll for cleaner parsing
      const ruleRegex =
        // eslint-disable-next-line security/detect-unsafe-regex
        /(?:=(\d+)|(zero|one|two|few|many|other))\s*\{((?:[^{}]|\{(?:[^{}]|\{[^{}]*\})*\})*)\}/g;
      const rules = [...pluralPattern.matchAll(ruleRegex)];
      if (rules.length === 0) return match;

      // Get the correct plural category
      const pluralRules = new Intl.PluralRules(locale);
      const category = pluralRules.select(count);

      // Collect matches by type for proper precedence
      let categoryMatchText: string | undefined;
      let otherMatchText: string | undefined;

      for (const ruleMatch of rules) {
        const [, exactMatch, pluralCategory, text] = ruleMatch;

        // Exact match (=N) has highest precedence - return immediately
        if (exactMatch && Number(exactMatch) === count) {
          return text.replace(/#/g, count.toString());
        }

        // Store category match and 'other' fallback for later
        if (pluralCategory === category) {
          categoryMatchText = text;
        } else if (pluralCategory === 'other') {
          otherMatchText = text;
        }
      }

      // Apply precedence: specific category > 'other' fallback
      const resultText = categoryMatchText ?? otherMatchText;
      if (resultText) {
        return resultText.replace(/#/g, count.toString());
      }

      return match;
    }
  );

  return result;
}

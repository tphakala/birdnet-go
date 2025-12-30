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
  // Fixed regex that handles one level of nested braces
  // Pattern: (?:[^{}]|\{[^{}]*\})* matches either:
  //   - [^{}] any character that's not a brace
  //   - OR \{[^{}]*\} a complete pair of braces with non-brace content
  const result = message.replace(
    // eslint-disable-next-line security/detect-unsafe-regex
    /\{(\w+),\s*plural,((?:[^{}]|\{[^{}]*\})*)\}/g,
    (match, paramName: string, pluralPattern: string) => {
      // eslint-disable-next-line security/detect-object-injection
      const count = params[paramName];
      if (typeof count !== 'number') return match;

      // Parse plural rules
      const rules = pluralPattern.match(/(?:=(\d+)|zero|one|two|few|many|other)\s*\{([^}]+)\}/g);
      if (!rules) return match;

      // Get the correct plural category
      const pluralRules = new Intl.PluralRules(locale);
      const category = pluralRules.select(count);

      for (const rule of rules) {
        const ruleMatch = rule.match(/(?:=(\d+)|(zero|one|two|few|many|other))\s*\{([^}]+)\}/);
        if (!ruleMatch) continue;

        const [, exactMatch, pluralCategory, text] = ruleMatch;

        // Check exact match first (e.g., =0)
        if (exactMatch && Number(exactMatch) === count) {
          return text.replace(/#/g, count.toString());
        }

        // Check plural category
        if (pluralCategory === category) {
          return text.replace(/#/g, count.toString());
        }

        // Fallback to 'other' category
        if (pluralCategory === 'other') {
          return text.replace(/#/g, count.toString());
        }
      }

      return match;
    }
  );

  return result;
}

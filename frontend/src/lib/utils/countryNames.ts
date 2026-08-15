// Localization of ISO 3166-1 alpha-2 country codes to display names via the
// platform Intl.DisplayNames, so region metadata can ship bare codes and each
// client renders them in the user's language without a shipped name table.

/**
 * Localize a list of ISO 3166-1 alpha-2 country codes to display names in the
 * given locale. Intl.DisplayNames is not a secure-context API, so it is safe on
 * the plain-HTTP LANs BirdNET-Go commonly runs on; it is still feature-detected
 * and every lookup is guarded, so an unsupported runtime, an unsupported locale,
 * or an unknown/malformed code degrades to the raw code rather than throwing.
 *
 * The input order is preserved. A nullish list yields an empty array.
 */
export function localizedCountryNames(
  codes: string[] | null | undefined,
  locale: string
): string[] {
  const list = codes ?? [];
  if (typeof Intl === 'undefined' || typeof Intl.DisplayNames !== 'function') {
    return [...list];
  }

  let display: Intl.DisplayNames;
  try {
    display = new Intl.DisplayNames([locale], { type: 'region' });
  } catch {
    // An unsupported locale throws RangeError; fall back to the raw codes.
    return [...list];
  }

  return list.map(code => {
    try {
      // Intl.DisplayNames returns the code itself for an unknown-but-well-formed
      // region, and throws RangeError for a malformed one; both fall back to the
      // code.
      return display.of(code) ?? code;
    } catch {
      return code;
    }
  });
}

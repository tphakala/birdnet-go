/**
 * Class strings of PlaceSearch whose colors are checked against both themes in
 * `src/test/color-contrast.test.ts`.
 *
 * Text uses theme tokens that do not depend on the color scheme and no opacity, so contrast
 * holds on the surface and on the highlighted option. The highlight background and outline are
 * the shared ones from SelectDropdown.styles.ts.
 *
 * Class names stay literal strings so Tailwind can find them when it scans the source.
 */

/** Option text: the name in base-content. */
export const PLACE_OPTION_CLASS = 'text-[var(--color-base-content)]';

/** Secondary text of an option (type, city, state, country). */
export const PLACE_OPTION_DETAIL_CLASS = 'text-[var(--text-muted)]';

/** The line saying where searches go. */
export const PLACE_DISCLOSURE_CLASS = 'text-[var(--text-muted)]';

/** The icon of the link to Photon. */
export const PLACE_DISCLOSURE_ICON_CLASS = 'text-[var(--text-muted)]';

/** Status line under the field (searching, nothing found). */
export const PLACE_MESSAGE_CLASS = 'text-[var(--text-muted)]';

/** Failure message and its icon. */
export const PLACE_ERROR_CLASS = 'text-[var(--text-error)]';

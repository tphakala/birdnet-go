/**
 * logoVariant.ts
 *
 * Shared mapping from color scheme IDs to LogoBadge gradient variants.
 * Used by DesktopSidebar and ColorSchemePicker to derive the correct
 * logo variant based on the active scheme and logo style.
 */
import type { SchemeId } from './scheme';

/** Logo gradient variant names matching LogoBadge's variant prop */
export type LogoVariant = 'ocean' | 'forest' | 'amber' | 'violet' | 'rose' | 'scheme' | 'solid';

/** Maps each scheme ID to its dedicated vibrant gradient variant */
export const SCHEME_GRADIENT_MAP: Readonly<Record<SchemeId, LogoVariant>> = {
  blue: 'ocean',
  forest: 'forest',
  amber: 'amber',
  violet: 'violet',
  rose: 'rose',
  custom: 'scheme',
};

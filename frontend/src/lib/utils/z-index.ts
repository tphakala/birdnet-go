/**
 * Z-Index Scale Documentation
 *
 * This file defines the standard z-index scale used throughout the application
 * to ensure consistent layering and avoid z-index conflicts.
 *
 * IMPORTANT: Always use these predefined values instead of arbitrary z-index values.
 */

/**
 * Z-Index Scale Constants
 *
 * The scale follows a systematic approach with gaps to allow for future additions:
 * - Base elements: 0-99
 * - Overlays and dropdowns: 100-999
 * - Modals and dialogs: 1000-1999
 * - Notifications and toasts: 2000-2999
 * - Critical/system alerts: 9000+
 */
export const Z_INDEX = {
  // Base level elements
  BASE: 0,
  STICKY_HEADER: 10,
  STICKY_FOOTER: 10,

  // Overlays and dropdowns
  DROPDOWN: 100,
  AUTOCOMPLETE: 150,
  SELECT_MENU: 150,

  // Modals and popovers
  MODAL_BACKDROP: 900,
  MODAL: 950,

  // Portal elements (render to document.body)
  PORTAL_DROPDOWN: 1000, // SpeciesInput dropdown
  PORTAL_TOOLTIP: 1001, // Tooltip overlays

  // Notifications
  NOTIFICATION_DROPDOWN: 1010, // NotificationBell dropdown
  TOAST: 2000,

  // Critical system elements
  SYSTEM_ALERT: 9000,
  DEBUGGER: 9999,
} as const;

/**
 * Usage Examples:
 *
 * In TypeScript/JavaScript:
 * ```ts
 * import { Z_INDEX } from '$lib/utils/z-index';
 * element.style.zIndex = Z_INDEX.MODAL.toString();
 * ```
 *
 * In Tailwind CSS classes:
 * ```svelte
 * <div class="z-100">  <!-- Use Z_INDEX.DROPDOWN value -->
 * <div class="z-1000"> <!-- Use Z_INDEX.PORTAL_DROPDOWN value -->
 * <div class="z-1010"> <!-- Use Z_INDEX.NOTIFICATION_DROPDOWN value -->
 * ```
 *
 * In inline styles:
 * ```svelte
 * <div style="z-index: {Z_INDEX.MODAL}">
 * ```
 */

/**
 * Guidelines:
 *
 * 1. Always use constants from this file instead of arbitrary values
 * 2. Leave gaps between categories for future additions
 * 3. Document any new z-index values added to this scale
 * 4. Consider using portal pattern for elements that need to escape stacking contexts
 * 5. Avoid using !important with z-index values
 * 6. Test z-index changes with multiple overlapping elements
 */

export type ZIndexValue = (typeof Z_INDEX)[keyof typeof Z_INDEX];

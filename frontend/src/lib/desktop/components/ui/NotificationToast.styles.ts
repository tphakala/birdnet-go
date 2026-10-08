/**
 * Class strings for NotificationToast.
 *
 * Each toast type pairs a status fill with that status's own content token, so the text
 * contrast is the contrast of the token pair, which src/test/color-contrast.test.ts checks
 * at rest and on hover for both themes. Action buttons add no background of their own: a
 * translucent overlay would change the contrast of the toast's text, which differs per type
 * and theme.
 *
 * Class names stay literal strings so Tailwind can find them when it scans the source.
 */
import type { ToastType } from '$lib/stores/toast';

/** Fill and text classes of each toast type. */
export const TOAST_TYPE_CLASSES: Record<ToastType, string> = {
  info: 'bg-[var(--color-info)] text-[var(--color-info-content)]',
  success: 'bg-[var(--color-success)] text-[var(--color-success-content)]',
  warning: 'bg-[var(--color-warning)] text-[var(--color-warning-content)]',
  error: 'bg-[var(--color-error)] text-[var(--color-error-content)]',
};

/** Action button: outlined in the toast's text colour, with no background. */
export const TOAST_ACTION_CLASS =
  'inline-flex items-center justify-center px-2 py-1 text-xs font-medium rounded border border-current transition-colors hover:underline';

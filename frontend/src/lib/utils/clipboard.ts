/** How long a copy button shows its copied state before it resets, in milliseconds */
export const COPY_FEEDBACK_TIMEOUT_MS = 2000;

/**
 * Copy text to clipboard with execCommand fallback for non-HTTPS contexts.
 * Falls back to textarea if the Clipboard API is unavailable or rejects.
 * Pass targetDoc when copying from a popup window so the fallback textarea
 * is appended to the correct document.
 */
export async function copyToClipboard(text: string, targetDoc?: Document): Promise<boolean> {
  const nav = targetDoc?.defaultView?.navigator ?? navigator;
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- clipboard API is undefined on plain HTTP
  if (nav.clipboard?.writeText) {
    try {
      await nav.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied or other failure; fall through to textarea fallback
    }
  }
  return fallbackCopy(text, targetDoc);
}

function fallbackCopy(text: string, targetDoc?: Document): boolean {
  const doc = targetDoc ?? document;
  // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- body is null when a popup document is destroyed
  if (!doc.body) return false;
  // select() moves focus to the textarea; the button the user pressed gets it back below
  const previouslyFocused = doc.activeElement;
  const textarea = doc.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  doc.body.appendChild(textarea);
  try {
    textarea.select();
    return doc.execCommand('copy');
  } catch {
    return false;
  } finally {
    textarea.remove();
    restoreFocus(doc, previouslyFocused);
  }
}

function restoreFocus(doc: Document, element: Element | null): void {
  // A popup document's elements belong to the popup window's classes, not this window's
  const view = doc.defaultView ?? window;
  if (!(element instanceof view.HTMLElement || element instanceof view.SVGElement)) return;
  if (!element.isConnected || element === doc.body || element === doc.documentElement) return;
  element.focus({ preventScroll: true });
}
